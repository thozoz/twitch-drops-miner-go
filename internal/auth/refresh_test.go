package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/state"
)

func TestRefreshOnUnauthorized_ConcurrentSingleFlight(t *testing.T) {
	var requestCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			atomic.AddInt64(&requestCount, 1)
			time.Sleep(50 * time.Millisecond) // Simulate network delay

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token":  "new_refreshed_access_token",
				"refresh_token": "new_refreshed_refresh_token",
				"token_type":    "bearer",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	authPath := filepath.Join(tempDir, "auth.json")

	initialData := &model.AuthData{
		AccessToken:  "initial_access_token",
		RefreshToken: "initial_refresh_token",
		UserID:       12345,
		Login:        "testuser",
		DeviceID:     "1234567890abcdef1234567890abcdef",
		UserAgent:    "Dalvik/2.1.0",
		ObtainedAt:   time.Now().Add(-1 * time.Hour),
	}
	require.NoError(t, state.AtomicWriteJSON(authPath, initialData, 0600))

	client := resty.New().SetBaseURL(server.URL)
	session, err := LoadOrEmpty(authPath, client)
	require.NoError(t, err)
	require.True(t, session.Authenticated())

	var wg sync.WaitGroup
	errs := make([]error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = session.RefreshOnUnauthorized(context.Background())
		}(i)
	}

	wg.Wait()

	for i, e := range errs {
		assert.NoError(t, e, "goroutine %d returned error", i)
	}

	assert.Equal(t, int64(1), atomic.LoadInt64(&requestCount), "exactly 1 HTTP request should be sent")
	assert.Equal(t, "new_refreshed_access_token", session.AccessToken())

	var diskData model.AuthData
	require.NoError(t, state.ReadJSON(authPath, &diskData))
	assert.Equal(t, "new_refreshed_access_token", diskData.AccessToken.Reveal())
	assert.Equal(t, "new_refreshed_refresh_token", diskData.RefreshToken.Reveal())
}

func TestRefreshOnUnauthorized_FailureLeavesDiskUntouched(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":400,"message":"invalid_client"}`))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	authPath := filepath.Join(tempDir, "auth.json")

	initialData := &model.AuthData{
		AccessToken:  "initial_access_token",
		RefreshToken: "initial_refresh_token",
		UserID:       12345,
		Login:        "testuser",
		DeviceID:     "1234567890abcdef1234567890abcdef",
		UserAgent:    "Dalvik/2.1.0",
		ObtainedAt:   time.Now().Add(-1 * time.Hour),
	}
	require.NoError(t, state.AtomicWriteJSON(authPath, initialData, 0600))
	diskBytesBefore, err := os.ReadFile(authPath)
	require.NoError(t, err)

	client := resty.New().SetBaseURL(server.URL)
	session, err := LoadOrEmpty(authPath, client)
	require.NoError(t, err)

	refreshErr := session.RefreshOnUnauthorized(context.Background())
	require.Error(t, refreshErr)
	assert.True(t, errors.Is(refreshErr, ErrReauthRequired))

	diskBytesAfter, err := os.ReadFile(authPath)
	require.NoError(t, err)
	assert.Equal(t, diskBytesBefore, diskBytesAfter, "disk file must remain byte-identical on refresh failure")
}

func TestSession_LoadOrEmpty_NonExistent(t *testing.T) {
	session, err := LoadOrEmpty("/non/existent/path/auth.json", nil)
	require.NoError(t, err)
	assert.False(t, session.Authenticated())
	assert.Empty(t, session.AccessToken())
}

func TestSession_IdentityMethods(t *testing.T) {
	session, err := LoadOrEmpty("/non/existent/path/auth.json", nil)
	require.NoError(t, err)

	assert.Equal(t, AndroidClientID, session.ClientID())

	sessionID1 := session.SessionID()
	sessionID2 := session.SessionID()
	assert.NotEmpty(t, sessionID1)
	assert.Equal(t, sessionID1, sessionID2, "SessionID must remain constant for the same Session")
}

func TestSession_Logout(t *testing.T) {
	tempDir := t.TempDir()
	authPath := filepath.Join(tempDir, "auth.json")

	initialData := &model.AuthData{
		AccessToken: "test_token",
	}
	require.NoError(t, state.AtomicWriteJSON(authPath, initialData, 0600))

	session, err := LoadOrEmpty(authPath, nil)
	require.NoError(t, err)
	require.True(t, session.Authenticated())

	require.NoError(t, session.Logout())
	assert.False(t, session.Authenticated())
	assert.NoFileExists(t, authPath)

	// Second logout on missing file should also succeed
	require.NoError(t, session.Logout())
}
