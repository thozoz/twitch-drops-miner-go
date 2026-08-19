package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	// Set XDG_CONFIG_HOME to a temporary empty directory to ensure no config.json is picked up
	tempDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempDir)

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "text", cfg.LogFormat)
	assert.Equal(t, "", cfg.LogFile)
	assert.Empty(t, cfg.Priority)
	assert.Empty(t, cfg.Exclude)
}

func TestConfigFileLoading(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "custom-config.json")
	content := `{"log_level": "warn", "log_format": "json", "log_file": "/tmp/test.log", "priority": ["GameA"], "exclude": ["GameB"]}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0644))

	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "warn", cfg.LogLevel)
	assert.Equal(t, "json", cfg.LogFormat)
	assert.Equal(t, "/tmp/test.log", cfg.LogFile)
	assert.Equal(t, []string{"GameA"}, cfg.Priority)
	assert.Equal(t, []string{"GameB"}, cfg.Exclude)
}

func TestExplicitMissingConfigFileErrors(t *testing.T) {
	_, err := Load("/non/existent/path/config.json")
	assert.Error(t, err)
}

func TestEnvOverridePrecedence(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	content := `{"log_level": "warn", "log_format": "text"}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0644))

	t.Setenv("TDM_LOG_LEVEL", "debug")
	t.Setenv("TDM_LOG_FORMAT", "json")
	t.Setenv("TDM_LOG_FILE", "/var/log/tdm.log")

	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, "json", cfg.LogFormat)
	assert.Equal(t, "/var/log/tdm.log", cfg.LogFile)
}

func TestContextHelpers(t *testing.T) {
	cfg := &Config{LogLevel: "debug"}
	ctx := WithConfig(context.Background(), cfg)
	retrieved := FromContext(ctx)
	assert.Equal(t, cfg, retrieved)

	assert.Nil(t, FromContext(context.Background()))
}

func TestPaths(t *testing.T) {
	cDir, err := ConfigDir()
	require.NoError(t, err)
	assert.NotEmpty(t, cDir)

	sDir, err := StateDir()
	require.NoError(t, err)
	assert.NotEmpty(t, sDir)

	cFile, err := ConfigFilePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cDir, "config.json"), cFile)

	aFile, err := AuthFilePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(sDir, "auth.json"), aFile)

	oFile, err := OperationsOverridePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cDir, "operations.json"), oFile)
}
