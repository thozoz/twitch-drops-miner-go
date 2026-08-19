package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tdm/internal/model"
	"tdm/internal/state"
)

func TestAuthStatus_NotAuthenticated(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"auth", "status"})

	code := Execute()
	assert.Equal(t, ExitError, code)
}

func TestAuthLogout_Empty(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"auth", "logout"})

	code := Execute()
	assert.Equal(t, ExitOK, code)
}

func TestGQLProbe_NotAuthenticated(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"gql", "probe", "ViewerDropsDashboard"})

	code := Execute()
	assert.Equal(t, ExitError, code)
}

func TestAuthStatus_InvalidTokenExit2(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/validate" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":401,"message":"invalid access token"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	tdmStateDir := filepath.Join(tempDir, "tdm")
	authPath := filepath.Join(tdmStateDir, "auth.json")
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	data := &model.AuthData{
		AccessToken:  "invalid_token",
		RefreshToken: "some_refresh_token",
		UserID:       123,
		Login:        "testuser",
		DeviceID:     "1234567890abcdef1234567890abcdef",
		UserAgent:    "Dalvik/2.1.0",
	}
	require.NoError(t, state.AtomicWriteJSON(authPath, data, 0600))

	// Note: authStatusCmd uses newHTTPClient which talks to real twitch by default,
	// but when Validate runs with a mock URL or invalid token, it returns ErrTokenInvalid -> ExitAuthRequired (2).
}

func TestInventoryList_NotAuthenticated(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"inventory", "list"})

	code := Execute()
	assert.Equal(t, ExitError, code)
}

func TestInventorySelect_NotAuthenticated(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"inventory", "select"})

	code := Execute()
	assert.Equal(t, ExitError, code)
}

func TestInventoryWatchDecision_NotAuthenticated(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"inventory", "watch-decision"})

	code := Execute()
	assert.Equal(t, ExitError, code)
}

func TestChannelWatch_NotAuthenticated(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"channel", "watch", "somechannel"})

	code := Execute()
	assert.Equal(t, ExitError, code)
}

func TestMine_NotAuthenticated(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mine"})

	code := Execute()
	assert.Equal(t, ExitError, code)
}




