package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chromedp/cdproto/network"
	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/state"
)

func TestBrowserSessionFromHeaders(t *testing.T) {
	headers := network.Headers{
		"Authorization":     "OAuth browser-access-token",
		"Client-ID":         WebClientID,
		"Client-Integrity":  "v4.local.browser-integrity",
		"X-Device-Id":       "browser-device-id",
		"Client-Session-Id": "browser-session-id",
		"User-Agent":        "Browser UA",
	}

	session, ok := browserSessionFromHeaders(headers)
	require.True(t, ok)
	assert.Equal(t, "browser-access-token", session.AccessToken)
	assert.Equal(t, WebClientID, session.ClientID)
	assert.Equal(t, "v4.local.browser-integrity", session.ClientIntegrity)
	assert.Equal(t, "browser-device-id", session.DeviceID)
	assert.Equal(t, "browser-session-id", session.ClientSessionID)
	assert.Equal(t, "Browser UA", session.UserAgent)
}

func TestBrowserSessionFromHeadersRejectsIncompleteRequest(t *testing.T) {
	_, ok := browserSessionFromHeaders(network.Headers{
		"Authorization": "OAuth token",
		"Client-ID":     WebClientID,
		"User-Agent":    "Browser UA",
	})
	assert.False(t, ok)
}

func TestSessionLoginWithBrowserPersistsCompleteIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/oauth2/validate", r.URL.Path)
		require.Equal(t, "OAuth browser-access-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id": WebClientID,
			"login":     "browseruser",
			"user_id":   "987654",
		})
	}))
	defer server.Close()

	originalCapture := captureBrowserSession
	captureBrowserSession = func(_ context.Context, _ string, ready func()) (BrowserSession, error) {
		if ready != nil {
			ready()
		}
		return BrowserSession{
			AccessToken:     "browser-access-token",
			ClientID:        WebClientID,
			ClientIntegrity: "v4.local.browser-integrity",
			DeviceID:        "browser-device-id",
			ClientSessionID: "browser-session-id",
			UserAgent:       "Browser UA",
		}, nil
	}
	t.Cleanup(func() { captureBrowserSession = originalCapture })

	authPath := filepath.Join(t.TempDir(), "auth.json")
	session, err := LoadOrEmpty(authPath, resty.New().SetBaseURL(server.URL))
	require.NoError(t, err)
	readyCalled := false
	require.NoError(t, session.LoginWithBrowser(context.Background(), func() { readyCalled = true }))
	assert.True(t, readyCalled)
	assert.Equal(t, WebClientID, session.ClientID())
	assert.Equal(t, "browser-device-id", session.DeviceID())
	assert.Equal(t, "browser-session-id", session.SessionID())
	assert.Equal(t, "Browser UA", session.UserAgent())
	assert.Equal(t, "browser-access-token", session.AccessToken())
	assert.Equal(t, "v4.local.browser-integrity", session.IntegrityToken())

	var saved model.AuthData
	require.NoError(t, state.ReadJSON(authPath, &saved))
	assert.Equal(t, "browseruser", saved.Login)
	assert.Equal(t, 987654, saved.UserID)
	assert.False(t, saved.IntegrityCaptured.IsZero())

	profileDir := filepath.Join(filepath.Dir(authPath), "browser-profile")
	require.NoError(t, os.MkdirAll(profileDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(profileDir, "marker"), []byte("x"), 0600))
	require.NoError(t, session.Logout())
	assert.NoDirExists(t, profileDir)
}

func TestRefreshOnUnauthorizedUsesPersistentBrowserForWebSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id": WebClientID,
			"login":     "browseruser",
			"user_id":   "987654",
		})
	}))
	defer server.Close()

	originalCapture := captureBrowserSession
	captureBrowserSession = func(_ context.Context, _ string, _ func()) (BrowserSession, error) {
		return BrowserSession{
			AccessToken:     "rotated-browser-token",
			ClientID:        WebClientID,
			ClientIntegrity: "fresh-integrity",
			DeviceID:        "browser-device-id",
			ClientSessionID: "browser-session-id",
			UserAgent:       "Browser UA",
		}, nil
	}
	t.Cleanup(func() { captureBrowserSession = originalCapture })

	authPath := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, state.AtomicWriteJSON(authPath, &model.AuthData{
		AccessToken:     "expired-browser-token",
		AuthClientID:    WebClientID,
		DeviceID:        "old-device-id",
		AuthUserAgent:   "Old Browser UA",
		ClientIntegrity: "stale-integrity",
		ClientSessionID: "old-session-id",
	}, 0600))
	session, err := LoadOrEmpty(authPath, resty.New().SetBaseURL(server.URL))
	require.NoError(t, err)

	require.NoError(t, session.RefreshOnUnauthorized(context.Background()))
	assert.Equal(t, "rotated-browser-token", session.AccessToken())
	assert.Equal(t, "fresh-integrity", session.IntegrityToken())
}

func TestRefreshIntegrityCoalescesConcurrentBrowserCapture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id": WebClientID,
			"login":     "browseruser",
			"user_id":   "987654",
		})
	}))
	defer server.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	var captureCalls int32
	originalCapture := captureBrowserSession
	captureBrowserSession = func(_ context.Context, _ string, _ func()) (BrowserSession, error) {
		if atomic.AddInt32(&captureCalls, 1) == 1 {
			close(started)
			<-release
		}
		return BrowserSession{
			AccessToken:     "browser-access-token",
			ClientID:        WebClientID,
			ClientIntegrity: "fresh-integrity",
			DeviceID:        "browser-device-id",
			ClientSessionID: "browser-session-id",
			UserAgent:       "Browser UA",
		}, nil
	}
	t.Cleanup(func() { captureBrowserSession = originalCapture })

	authPath := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, state.AtomicWriteJSON(authPath, &model.AuthData{
		AccessToken:     "browser-access-token",
		AuthClientID:    WebClientID,
		ClientIntegrity: "stale-integrity",
	}, 0600))
	session, err := LoadOrEmpty(authPath, resty.New().SetBaseURL(server.URL))
	require.NoError(t, err)

	errs := make(chan error, 2)
	var callers sync.WaitGroup
	callers.Add(2)
	go func() {
		defer callers.Done()
		errs <- session.RefreshIntegrity(context.Background())
	}()
	<-started
	go func() {
		defer callers.Done()
		errs <- session.RefreshIntegrity(context.Background())
	}()
	close(release)
	callers.Wait()
	close(errs)

	for refreshErr := range errs {
		require.NoError(t, refreshErr)
	}
	assert.Equal(t, int32(1), captureCalls)
}
