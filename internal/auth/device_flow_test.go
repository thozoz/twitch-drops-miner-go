package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadFixture(t *testing.T, filename string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "fixtures", filename)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read fixture %s", filename)
	return data
}

func TestRunDeviceCodeFlow_Success(t *testing.T) {
	deviceCodeFixture := loadFixture(t, "auth_device_code.json")
	tokenFixture := loadFixture(t, "auth_token.json")

	var (
		deviceCalls    int32
		tokenCalls     int32
		sleepCallCount int32
		sleepDurations []time.Duration
		tokenObserved  bool
		firstSleepDone bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, AndroidClientID, r.Header.Get("Client-Id"))
		assert.Equal(t, "test-device-id", r.Header.Get("X-Device-Id"))
		assert.Equal(t, "CustomUA/1.0", r.Header.Get("User-Agent"))

		switch r.URL.Path {
		case "/oauth2/device":
			atomic.AddInt32(&deviceCalls, 1)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
			require.NoError(t, r.ParseForm())
			assert.Equal(t, AndroidClientID, r.FormValue("client_id"))
			assert.Equal(t, "", r.FormValue("scopes"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(deviceCodeFixture)

		case "/oauth2/token":
			callNum := atomic.AddInt32(&tokenCalls, 1)
			tokenObserved = true

			// Assert that sleep occurred BEFORE this first token request
			assert.True(t, firstSleepDone, "sleep must be called BEFORE the first token request")

			assert.Equal(t, http.MethodPost, r.Method)
			require.NoError(t, r.ParseForm())
			assert.Equal(t, AndroidClientID, r.FormValue("client_id"))
			assert.Equal(t, "urn:ietf:params:oauth:grant-type:device_code", r.FormValue("grant_type"))
			assert.Equal(t, "abcdefghijklmnopqrstuvwxyz1234567890ABCD", r.FormValue("device_code"))

			if callNum < 3 {
				// User hasn't authorized yet (400)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"status":400,"message":"authorization_pending"}`))
				return
			}

			// Authorized on 3rd attempt
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(tokenFixture)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var (
		onCodeCalls int
		gotURI      string
		gotCode     string
	)

	onCode := func(uri, code string) {
		onCodeCalls++
		gotURI = uri
		gotCode = code
		// When onCode is called, token endpoint must NOT have been called yet
		assert.False(t, tokenObserved, "token endpoint must not be called before onCode")
	}

	mockSleep := func(ctx context.Context, d time.Duration) error {
		atomic.AddInt32(&sleepCallCount, 1)
		sleepDurations = append(sleepDurations, d)
		firstSleepDone = true
		return nil
	}

	client := resty.New()
	accessToken, refreshToken, err := RunDeviceCodeFlow(
		context.Background(),
		client,
		"test-device-id",
		"CustomUA/1.0",
		onCode,
		WithBaseURL(server.URL),
		WithSleep(mockSleep),
	)

	require.NoError(t, err)
	assert.Equal(t, "abcdefghijklmnopqrstuvwxyz1234567890abcd", accessToken)
	assert.Equal(t, "1234567890abcdefghijklmnopqrstuvwxyz1234", refreshToken)

	// onCode should have fired exactly once despite 3 polling iterations
	assert.Equal(t, 1, onCodeCalls)
	assert.Equal(t, "https://www.twitch.tv/activate?device-code=ABCDEFGH", gotURI)
	assert.Equal(t, "ABCDEFGH", gotCode)

	// 3 token poll attempts -> 3 sleeps of 5s each
	assert.Equal(t, int32(1), atomic.LoadInt32(&deviceCalls))
	assert.Equal(t, int32(3), atomic.LoadInt32(&tokenCalls))
	assert.Equal(t, int32(3), atomic.LoadInt32(&sleepCallCount))
	for _, d := range sleepDurations {
		assert.Equal(t, 5*time.Second, d)
	}
}

func TestRunDeviceCodeFlow_CodeExpiry(t *testing.T) {
	deviceCodeFixture := loadFixture(t, "auth_device_code.json")
	tokenFixture := loadFixture(t, "auth_token.json")

	var (
		deviceCalls int32
		tokenCalls  int32
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/device":
			atomic.AddInt32(&deviceCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(deviceCodeFixture)

		case "/oauth2/token":
			dCalls := atomic.LoadInt32(&deviceCalls)
			atomic.AddInt32(&tokenCalls, 1)

			if dCalls == 1 {
				// First device code is not authorized
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"status":400,"message":"authorization_pending"}`))
				return
			}

			// Second device code succeeds
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(tokenFixture)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var onCodeCount int
	onCode := func(uri, code string) {
		onCodeCount++
	}

	currentTime := time.Now()
	mockNow := func() time.Time {
		return currentTime
	}

	mockSleep := func(ctx context.Context, d time.Duration) error {
		// Advance time by 2000s on first sleep, causing expires_in (1800s) to elapse
		if atomic.LoadInt32(&deviceCalls) == 1 {
			currentTime = currentTime.Add(2000 * time.Second)
		}
		return nil
	}

	client := resty.New()
	accessToken, refreshToken, err := RunDeviceCodeFlow(
		context.Background(),
		client,
		"test-device-id",
		"CustomUA/1.0",
		onCode,
		WithBaseURL(server.URL),
		WithSleep(mockSleep),
		WithNow(mockNow),
	)

	require.NoError(t, err)
	assert.NotEmpty(t, accessToken)
	assert.NotEmpty(t, refreshToken)

	// onCode should have fired twice: once for initial code, once for refreshed code
	assert.Equal(t, 2, onCodeCount)
	assert.Equal(t, int32(2), atomic.LoadInt32(&deviceCalls))
}

func TestRunDeviceCodeFlow_ContextCancelled(t *testing.T) {
	deviceCodeFixture := loadFixture(t, "auth_device_code.json")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/device" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(deviceCodeFixture)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately when sleep is invoked
	mockSleep := func(c context.Context, d time.Duration) error {
		cancel()
		return c.Err()
	}

	client := resty.New()
	_, _, err := RunDeviceCodeFlow(
		ctx,
		client,
		"test-device-id",
		"CustomUA/1.0",
		nil,
		WithBaseURL(server.URL),
		WithSleep(mockSleep),
	)

	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}
