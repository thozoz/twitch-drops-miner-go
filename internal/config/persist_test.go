package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveConfigPath_ExplicitFlagWins(t *testing.T) {
	t.Setenv("TDM_CONFIG", "/env/path/config.json")

	got, err := ResolveConfigPath("/flag/path/config.json")
	require.NoError(t, err)
	assert.Equal(t, "/flag/path/config.json", got)
}

func TestResolveConfigPath_FallsBackToEnv(t *testing.T) {
	t.Setenv("TDM_CONFIG", "/env/path/config.json")

	got, err := ResolveConfigPath("")
	require.NoError(t, err)
	assert.Equal(t, "/env/path/config.json", got)
}

func TestResolveConfigPath_FallsBackToXDGDefault(t *testing.T) {
	t.Setenv("TDM_CONFIG", "")

	got, err := ResolveConfigPath("")
	require.NoError(t, err)

	want, err := ConfigFilePath()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestLoad_MissingEnvConfigFails(t *testing.T) {
	// An operator who points TDM_CONFIG at a file that isn't there must be told,
	// not silently given defaults — that silence was the reported bug.
	t.Setenv("TDM_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.json"))

	_, err := Load("")
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestLoad_ReadsConfigNamedByEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"priority":["Rust","Fortnite"],"log_level":"debug"}`), 0600))

	t.Setenv("TDM_CONFIG", path)

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, []string{"Rust", "Fortnite"}, cfg.Priority)
	assert.Equal(t, "debug", cfg.LogLevel)
}

func TestSavePriority_PreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// A hand-written config carrying a key this build knows nothing about.
	original := `{"priority":["Old"],"log_level":"debug","future_setting":{"nested":true}}`
	require.NoError(t, os.WriteFile(path, []byte(original), 0600))

	require.NoError(t, SavePriority(path, []string{"Rust", "Fortnite"}))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))

	assert.Equal(t, []any{"Rust", "Fortnite"}, doc["priority"], "priority should be replaced")
	assert.Equal(t, "debug", doc["log_level"], "unrelated known key must survive")
	assert.Equal(t, map[string]any{"nested": true}, doc["future_setting"], "unknown key must survive")
}

func TestSavePriority_DoesNotMaterialiseDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// User set only priority. Saving must not spray log_level/log_format/exclude
	// into their file.
	require.NoError(t, os.WriteFile(path, []byte(`{"priority":["Old"]}`), 0600))
	require.NoError(t, SavePriority(path, []string{"New"}))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))

	assert.Equal(t, []string{"priority"}, keysOf(doc))
}

func TestSavePriority_CreatesFileWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")

	require.NoError(t, SavePriority(path, []string{"Rust"}))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"Rust"}, cfg.Priority)
}

func TestSavePriority_EmptyListWritesEmptyArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, SavePriority(path, nil))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"priority": []`)
}

func TestSavePriority_RefusesToClobberInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	broken := []byte(`{"priority": ["Rust",`)
	require.NoError(t, os.WriteFile(path, broken, 0600))

	err := SavePriority(path, []string{"Fortnite"})
	require.Error(t, err)

	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, broken, after, "a config we cannot parse must be left untouched")
}

func TestSavePriority_RequiresPath(t *testing.T) {
	require.Error(t, SavePriority("", []string{"Rust"}))
}

func TestSavePriority_RoundTripsThroughLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, SavePriority(path, []string{"Rust", "Fortnite"}))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"Rust", "Fortnite"}, cfg.Priority)
}

func TestSaveKey_WritesValueAndRoundTripsThroughLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, SaveKey(path, "enable_badges_emotes", true))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.True(t, cfg.EnableBadgesEmotes)
}

func TestSaveKey_PreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := `{"priority":["Old"],"log_level":"debug","future_setting":{"nested":true}}`
	require.NoError(t, os.WriteFile(path, []byte(original), 0600))

	require.NoError(t, SaveKey(path, "enable_badges_emotes", true))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))

	assert.Equal(t, true, doc["enable_badges_emotes"], "target key should be set")
	assert.Equal(t, []any{"Old"}, doc["priority"], "unrelated known key must survive")
	assert.Equal(t, "debug", doc["log_level"], "unrelated known key must survive")
	assert.Equal(t, map[string]any{"nested": true}, doc["future_setting"], "unknown key must survive")
}

func TestSaveKey_RefusesToClobberInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	broken := []byte(`{"priority": ["Rust",`)
	require.NoError(t, os.WriteFile(path, broken, 0600))

	err := SaveKey(path, "enable_badges_emotes", true)
	require.Error(t, err)

	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, broken, after, "a config we cannot parse must be left untouched")
}

func TestSaveKey_RequiresPath(t *testing.T) {
	require.Error(t, SaveKey("", "enable_badges_emotes", true))
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
