package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/state"
)

func TestNormalizeTokenInput(t *testing.T) {
	tests := map[string]string{
		" token ":       "token",
		"OAuth token":   "token",
		"OAuth 'token'": "token",
		`OAuth "token"`: "token",
		"oauth:token":   "token",
		`"token"`:       "token",
		"auth-token=token; Path=/; Domain=.twitch.tv":   "token",
		`auth-token="token"; Path=/; Domain=.twitch.tv`: "token",
		`auth-token='token'; Path=/; Domain=.twitch.tv`: "token",
	}
	for input, want := range tests {
		assert.Equal(t, want, NormalizeTokenInput(input))
	}
}

func TestSessionSetTokenImportsAndroidToken(t *testing.T) {
	server := tokenValidationServer(AndroidClientID)
	defer server.Close()

	authPath := filepath.Join(t.TempDir(), "auth.json")
	session, err := LoadOrEmpty(authPath, resty.New().SetBaseURL(server.URL))
	require.NoError(t, err)
	require.NoError(t, session.SetToken(context.Background(), "OAuth android-token"))

	assert.Equal(t, "android-token", session.AccessToken())
	assert.Equal(t, AndroidClientID, session.ClientID())
	assert.Equal(t, AndroidUserAgents[0], session.UserAgent())

	var saved model.AuthData
	require.NoError(t, state.ReadJSON(authPath, &saved))
	assert.Equal(t, "android-token", saved.AccessToken.Reveal())
	assert.Equal(t, AndroidClientID, saved.AuthClientID)
	assert.Equal(t, "testuser", saved.Login)
}

func TestSessionSetTokenRejectsWebToken(t *testing.T) {
	const twitchWebClientID = "kimne78kx3ncx6brgo4mv6wki5h1ko"
	server := tokenValidationServer(twitchWebClientID)
	defer server.Close()

	authPath := filepath.Join(t.TempDir(), "auth.json")
	session, err := LoadOrEmpty(authPath, resty.New().SetBaseURL(server.URL))
	require.NoError(t, err)
	err = session.SetToken(context.Background(), "web-token")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedTokenClient)
	assert.NoFileExists(t, authPath)
	assert.False(t, session.Authenticated())
}

func TestSessionSetTokenRejectsEmptyInput(t *testing.T) {
	session, err := LoadOrEmpty(filepath.Join(t.TempDir(), "auth.json"), nil)
	require.NoError(t, err)
	assert.Error(t, session.SetToken(context.Background(), "  "))
}

func TestSessionSetTokenPreservesExistingSessionOnValidationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	authPath := filepath.Join(t.TempDir(), "auth.json")
	existing := &model.AuthData{AccessToken: "existing-token", AuthClientID: AndroidClientID}
	require.NoError(t, state.AtomicWriteJSON(authPath, existing, 0600))
	session, err := LoadOrEmpty(authPath, resty.New().SetBaseURL(server.URL))
	require.NoError(t, err)

	err = session.SetToken(context.Background(), "invalid-token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTokenInvalid))
	assert.Equal(t, "existing-token", session.AccessToken())
}

func tokenValidationServer(clientID string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/validate" || !strings.Contains(r.Header.Get("Authorization"), "token") {
			http.Error(w, "unexpected validation request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id": clientID,
			"login":     "testuser",
			"user_id":   "12345",
		})
	}))
}
