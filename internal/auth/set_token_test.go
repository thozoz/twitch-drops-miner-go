package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/state"
)

func TestSession_SetToken_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/validate" {
			assert.Equal(t, "OAuth valid_web_token", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"client_id":  WebClientID,
				"login":      "streamer123",
				"scopes":     []string{"user_read"},
				"user_id":    "987654",
				"expires_in": 3600,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	authPath := filepath.Join(tempDir, "auth.json")

	client := resty.New().SetBaseURL(server.URL)
	session, err := LoadOrEmpty(authPath, client)
	require.NoError(t, err)
	assert.False(t, session.Authenticated())

	err = session.SetToken(context.Background(), "valid_web_token")
	require.NoError(t, err)

	assert.True(t, session.Authenticated())
	assert.Equal(t, "valid_web_token", session.AccessToken())
	assert.Equal(t, WebClientID, session.ClientID())
	assert.Equal(t, WebUserAgent, session.UserAgent())
	assert.NotEmpty(t, session.DeviceID())

	var diskData model.AuthData
	require.NoError(t, state.ReadJSON(authPath, &diskData))
	assert.Equal(t, "valid_web_token", diskData.AccessToken.Reveal())
	assert.Equal(t, WebClientID, diskData.AuthClientID)
	assert.Equal(t, "streamer123", diskData.Login)
	assert.Equal(t, 987654, diskData.UserID)
	assert.Equal(t, WebUserAgent, diskData.AuthUserAgent)
}

func TestSession_SetToken_Sanitization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/validate" {
			assert.Equal(t, "OAuth sanitized_token", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"client_id":  WebClientID,
				"login":      "streamer123",
				"user_id":    "987654",
				"expires_in": 3600,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	tests := []struct {
		name  string
		input string
	}{
		{"with OAuth prefix", "OAuth sanitized_token"},
		{"with oauth prefix lowercase", "oauth:sanitized_token"},
		{"with quotes and spaces", "  \"sanitized_token\"  "},
		{"from cookie string", "auth-token=sanitized_token; Path=/; Domain=.twitch.tv"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			authPath := filepath.Join(tempDir, "auth.json")
			client := resty.New().SetBaseURL(server.URL)
			session, err := LoadOrEmpty(authPath, client)
			require.NoError(t, err)

			err = session.SetToken(context.Background(), tt.input)
			require.NoError(t, err)
			assert.Equal(t, "sanitized_token", session.AccessToken())
		})
	}
}

func TestSession_SetToken_InvalidToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":401,"message":"invalid access token"}`))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	authPath := filepath.Join(tempDir, "auth.json")

	client := resty.New().SetBaseURL(server.URL)
	session, err := LoadOrEmpty(authPath, client)
	require.NoError(t, err)

	err = session.SetToken(context.Background(), "invalid_token")
	require.Error(t, err)
	assert.False(t, session.Authenticated())
	assert.NoFileExists(t, authPath)
}

func TestSession_SetToken_EmptyToken(t *testing.T) {
	session, err := LoadOrEmpty("/non/existent/path/auth.json", nil)
	require.NoError(t, err)

	err = session.SetToken(context.Background(), "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}
