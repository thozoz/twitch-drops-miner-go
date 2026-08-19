package state

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"tdm/internal/model"
)

func TestAtomicWriteJSON_RoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "auth.json")

	now := time.Now().Truncate(time.Millisecond).UTC()
	authIn := model.AuthData{
		AccessToken:  model.RedactedString("oauth-access-secret"),
		RefreshToken: model.RedactedString("oauth-refresh-secret"),
		UserID:       987654,
		Login:        "streamer123",
		DeviceID:     "device-hex-123456",
		UserAgent:    "CustomUserAgent/1.0",
		ObtainedAt:   now,
	}

	err := AtomicWriteJSON(targetPath, authIn, 0600)
	require.NoError(t, err)

	var authOut model.AuthData
	err = ReadJSON(targetPath, &authOut)
	require.NoError(t, err)

	assert.Equal(t, authIn.UserID, authOut.UserID)
	assert.Equal(t, authIn.Login, authOut.Login)
	assert.Equal(t, authIn.DeviceID, authOut.DeviceID)
	assert.Equal(t, authIn.UserAgent, authOut.UserAgent)
	assert.Equal(t, authIn.AccessToken.Reveal(), authOut.AccessToken.Reveal())
	assert.Equal(t, authIn.RefreshToken.Reveal(), authOut.RefreshToken.Reveal())
	assert.True(t, authIn.ObtainedAt.Equal(authOut.ObtainedAt))
}

func TestAtomicWriteJSON_CreatesParentDirectory(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "nested", "subdir", "deep", "state.json")

	data := map[string]string{"status": "ok"}
	err := AtomicWriteJSON(targetPath, data, 0600)
	require.NoError(t, err)

	var result map[string]string
	err = ReadJSON(targetPath, &result)
	require.NoError(t, err)
	assert.Equal(t, "ok", result["status"])
}

func TestAtomicWriteJSON_Permissions(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "secure.json")

	data := map[string]string{"key": "value"}
	err := AtomicWriteJSON(targetPath, data, 0600)
	require.NoError(t, err)

	stat, err := os.Stat(targetPath)
	require.NoError(t, err)

	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0600), stat.Mode().Perm())
	}
}

func TestAtomicWriteJSON_NoLeftoverTmpFiles(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "data.json")

	data := map[string]string{"foo": "bar"}
	err := AtomicWriteJSON(targetPath, data, 0600)
	require.NoError(t, err)

	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)

	for _, entry := range entries {
		assert.False(t, strings.HasSuffix(entry.Name(), ".tmp"), "found leftover tmp file: %s", entry.Name())
	}
	assert.Len(t, entries, 1)
	assert.Equal(t, "data.json", entries[0].Name())
}

func TestAtomicWriteJSON_CleanupOnEncodeFailure(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "bad.json")

	// Channels cannot be JSON-encoded, triggers encoder.Encode error
	badData := map[string]any{"bad": make(chan int)}
	err := AtomicWriteJSON(targetPath, badData, 0600)
	require.Error(t, err)

	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "temporary files should be cleaned up on encode failure")
}

func TestReadJSON_NonExistentFile(t *testing.T) {
	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "does_not_exist.json")

	var result map[string]any
	err := ReadJSON(nonExistentPath, &result)
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist), "expected os.ErrNotExist, got: %v", err)
}

func TestReadJSON_InvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "corrupt.json")

	err := os.WriteFile(targetPath, []byte("invalid json content {"), 0600)
	require.NoError(t, err)

	var result map[string]any
	err = ReadJSON(targetPath, &result)
	require.Error(t, err)
	assert.False(t, errors.Is(err, os.ErrNotExist))
}
