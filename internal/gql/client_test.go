package gql

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubIdentity struct {
	clientID    string
	deviceID    string
	sessionID   string
	userAgent   string
	accessToken string
}

func (s *stubIdentity) ClientID() string    { return s.clientID }
func (s *stubIdentity) DeviceID() string    { return s.deviceID }
func (s *stubIdentity) SessionID() string   { return s.sessionID }
func (s *stubIdentity) UserAgent() string   { return s.userAgent }
func (s *stubIdentity) AccessToken() string { return s.accessToken }

type stubRefresher struct {
	mu            sync.Mutex
	calls         int
	refreshErr    error
	onRefreshFunc func()
}

func (r *stubRefresher) RefreshOnUnauthorized(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.onRefreshFunc != nil {
		r.onRefreshFunc()
	}
	return r.refreshErr
}

func (r *stubRefresher) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// ------------------- RateLimiter Tests -------------------

func TestRateLimiter_ConcurrencyAndWindow(t *testing.T) {
	ctx := context.Background()
	rl := NewRateLimiter(5, 50*time.Millisecond)
	defer rl.Close()

	// 5 concurrent acquires succeed immediately
	for i := 0; i < 5; i++ {
		err := rl.Acquire(ctx)
		require.NoError(t, err)
	}

	concurrent, total, cap := rl.Status()
	assert.Equal(t, 5, concurrent)
	assert.Equal(t, 5, total)
	assert.Equal(t, 5, cap)

	// 6th acquire blocks
	acquire6Done := make(chan error, 1)
	go func() {
		acquire6Done <- rl.Acquire(ctx)
	}()

	select {
	case <-acquire6Done:
		t.Fatal("6th acquire should have blocked")
	case <-time.After(20 * time.Millisecond):
		// Expected to block
	}

	// Releasing 1 slot still does not allow 6th acquire yet if total == capacity and window hasn't elapsed
	rl.Release()
	concurrent, _, _ = rl.Status()
	assert.Equal(t, 4, concurrent)

	// Wait for window to reset (50ms)
	select {
	case err := <-acquire6Done:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("6th acquire timed out after window reset")
	}

	// Clean up released slots
	rl.Release()
	for i := 0; i < 4; i++ {
		rl.Release()
	}
}

func TestRateLimiter_ContextCancellation(t *testing.T) {
	rl := NewRateLimiter(1, time.Second)
	defer rl.Close()

	ctx, cancel := context.WithCancel(context.Background())
	err := rl.Acquire(ctx)
	require.NoError(t, err)

	acquireDone := make(chan error, 1)
	go func() {
		acquireDone <- rl.Acquire(ctx)
	}()

	select {
	case <-acquireDone:
		t.Fatal("acquire should have blocked")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-acquireDone:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("acquire did not unblock on context cancellation")
	}

	rl.Release()
}

func TestRateLimiter_CloseUnblocks(t *testing.T) {
	rl := NewRateLimiter(1, time.Second)

	ctx := context.Background()
	err := rl.Acquire(ctx)
	require.NoError(t, err)

	acquireDone := make(chan error, 1)
	go func() {
		acquireDone <- rl.Acquire(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	err = rl.Close()
	require.NoError(t, err)

	select {
	case err := <-acquireDone:
		assert.ErrorIs(t, err, ErrRateLimiterClosed)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("acquire did not unblock on Close")
	}
}

// ------------------- Backoff Tests -------------------

func TestBackoff_Saturation(t *testing.T) {
	b := NewExponentialBackoff(WithBackoffBase(2), WithBackoffMaximum(300))

	var lastDuration time.Duration
	for i := 0; i < 10; i++ {
		lastDuration = b.Next()
	}

	// On the 10th call (steps=9, 2^9=512 > 300), value saturates at exactly 300s
	assert.Equal(t, 300*time.Second, lastDuration)

	// Step count should not grow once saturated
	assert.Equal(t, 9, b.Steps())

	// Reset resets step count
	b.Reset()
	assert.Equal(t, 0, b.Steps())
}

// ------------------- Client Tests -------------------

func TestClient_Do_UnknownOperation(t *testing.T) {
	reg, _, err := LoadRegistry("")
	require.NoError(t, err)

	c := NewClient(reg, nil, nil, nil)
	_, err = c.Do(context.Background(), "NonExistentOperation", nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownOperation)
}

func TestClient_Do_HeaderInjection(t *testing.T) {
	reg, _, err := LoadRegistry("")
	require.NoError(t, err)

	ident := &stubIdentity{
		clientID:    "test-client-id",
		deviceID:    "test-device-id-1234567890abcdef",
		sessionID:   "test-session-id",
		userAgent:   "TestAgent/1.0",
		accessToken: "test-access-token-xyz",
	}

	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"test":true}}`))
	}))
	defer server.Close()

	httpClient := resty.New().SetHostURL(server.URL)
	c := NewClient(reg, ident, nil, httpClient)

	res, err := c.Do(context.Background(), "ViewerDropsDashboard", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, res)

	assert.Equal(t, "test-client-id", capturedHeaders.Get("Client-Id"))
	assert.Equal(t, "test-device-id-1234567890abcdef", capturedHeaders.Get("X-Device-Id"))
	assert.Equal(t, "test-session-id", capturedHeaders.Get("Client-Session-Id"))
	assert.Equal(t, "TestAgent/1.0", capturedHeaders.Get("User-Agent"))
	assert.Equal(t, "OAuth test-access-token-xyz", capturedHeaders.Get("Authorization"))
	assert.Equal(t, "*/*", capturedHeaders.Get("Accept"))
	assert.Equal(t, "gzip", capturedHeaders.Get("Accept-Encoding"))
	assert.Equal(t, "https://www.twitch.tv", capturedHeaders.Get("Origin"))
	assert.Equal(t, "https://www.twitch.tv", capturedHeaders.Get("Referer"))
}

func TestClient_Do_SingleRetryPersistedQueryNotFound(t *testing.T) {
	reg, _, err := LoadRegistry("")
	require.NoError(t, err)

	notFoundFixture, err := os.ReadFile("../../testdata/fixtures/gql_persisted_query_not_found.json")
	require.NoError(t, err)
	dashboardFixture, err := os.ReadFile("../../testdata/fixtures/gql_viewerdropsdashboard.json")
	require.NoError(t, err)

	t.Run("succeeds on second attempt after single retry", func(t *testing.T) {
		var requestCount int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := atomic.AddInt32(&requestCount, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if count == 1 {
				_, _ = w.Write(notFoundFixture)
			} else {
				_, _ = w.Write(dashboardFixture)
			}
		}))
		defer server.Close()

		httpClient := resty.New().SetHostURL(server.URL)
		c := NewClient(reg, nil, nil, httpClient, WithMinRetryDelay(1*time.Millisecond))

		res, err := c.Do(context.Background(), "ViewerDropsDashboard", nil)
		require.NoError(t, err)
		assert.NotEmpty(t, res)
		assert.Equal(t, int32(2), atomic.LoadInt32(&requestCount))
	})

	t.Run("fails without infinite loop if PersistedQueryNotFound repeats", func(t *testing.T) {
		var requestCount int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&requestCount, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(notFoundFixture)
		}))
		defer server.Close()

		httpClient := resty.New().SetHostURL(server.URL)
		c := NewClient(reg, nil, nil, httpClient, WithMinRetryDelay(1*time.Millisecond))

		_, err := c.Do(context.Background(), "ViewerDropsDashboard", nil)
		assert.Error(t, err)
		assert.Equal(t, int32(2), atomic.LoadInt32(&requestCount))
	})
}

func TestClient_Do_UnauthorizedRefresh(t *testing.T) {
	reg, _, err := LoadRegistry("")
	require.NoError(t, err)

	dashboardFixture, err := os.ReadFile("../../testdata/fixtures/gql_viewerdropsdashboard.json")
	require.NoError(t, err)

	t.Run("refreshes token once on 401 and succeeds on retry", func(t *testing.T) {
		var requestCount int32
		ident := &stubIdentity{
			accessToken: "old-token",
		}
		refresher := &stubRefresher{
			onRefreshFunc: func() {
				ident.accessToken = "new-refreshed-token"
			},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := atomic.AddInt32(&requestCount, 1)
			if count == 1 {
				assert.Equal(t, "OAuth old-token", r.Header.Get("Authorization"))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			assert.Equal(t, "OAuth new-refreshed-token", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(dashboardFixture)
		}))
		defer server.Close()

		httpClient := resty.New().SetHostURL(server.URL)
		c := NewClient(reg, ident, refresher, httpClient)

		res, err := c.Do(context.Background(), "ViewerDropsDashboard", nil)
		require.NoError(t, err)
		assert.NotEmpty(t, res)
		assert.Equal(t, int32(2), atomic.LoadInt32(&requestCount))
		assert.Equal(t, 1, refresher.CallCount())
	})

	t.Run("fails when refresh returns error", func(t *testing.T) {
		refresher := &stubRefresher{
			refreshErr: errors.New("refresh failed"),
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		httpClient := resty.New().SetHostURL(server.URL)
		c := NewClient(reg, nil, refresher, httpClient)

		_, err := c.Do(context.Background(), "ViewerDropsDashboard", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "token refresh failed")
		assert.Equal(t, 1, refresher.CallCount())
	})
}

func TestClient_DoBatch_Chunking(t *testing.T) {
	reg, _, err := LoadRegistry("")
	require.NoError(t, err)

	var requestCount int32
	var chunkSizes []int
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)

		var reqs []RequestPayload
		err := json.NewDecoder(r.Body).Decode(&reqs)
		require.NoError(t, err)

		mu.Lock()
		chunkSizes = append(chunkSizes, len(reqs))
		mu.Unlock()

		respPayloads := make([]ResponseEnvelope, len(reqs))
		for i, req := range reqs {
			data, _ := json.Marshal(map[string]any{
				"index": req.Variables["index"],
			})
			respPayloads[i] = ResponseEnvelope{
				Data: data,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(respPayloads)
	}))
	defer server.Close()

	httpClient := resty.New().SetHostURL(server.URL)
	c := NewClient(reg, nil, nil, httpClient)

	// Create 45 operations
	ops := make([]BatchOp, 45)
	for i := 0; i < 45; i++ {
		ops[i] = BatchOp{
			Name: "ViewerDropsDashboard",
			Variables: map[string]any{
				"index": i,
			},
		}
	}

	results, err := c.DoBatch(context.Background(), ops)
	require.NoError(t, err)
	assert.Len(t, results, 45)

	// 45 operations split at 20/request => 3 requests (20, 20, 5)
	assert.Equal(t, int32(3), atomic.LoadInt32(&requestCount))
	assert.ElementsMatch(t, []int{20, 20, 5}, chunkSizes)

	// Verify results order
	for i, raw := range results {
		var item struct {
			Index int `json:"index"`
		}
		err := json.Unmarshal(raw, &item)
		require.NoError(t, err)
		assert.Equal(t, i, item.Index)
	}
}
