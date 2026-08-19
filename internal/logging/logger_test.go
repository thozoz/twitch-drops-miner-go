package logging

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tdm/internal/config"
)

func TestNewLogger(t *testing.T) {
	// Text format default
	l := New(&config.Config{
		LogLevel:  "debug",
		LogFormat: "text",
	})
	require.NotNil(t, l)
	assert.True(t, l.Enabled(context.Background(), slog.LevelDebug))

	// JSON format
	lJSON := New(&config.Config{
		LogLevel:  "warn",
		LogFormat: "json",
	})
	require.NotNil(t, lJSON)
	assert.False(t, lJSON.Enabled(context.Background(), slog.LevelInfo))
	assert.True(t, lJSON.Enabled(context.Background(), slog.LevelWarn))

	// Unrecognized level falls back to info
	lUnknown := New(&config.Config{
		LogLevel: "invalid_level",
	})
	require.NotNil(t, lUnknown)
	assert.True(t, lUnknown.Enabled(context.Background(), slog.LevelInfo))
	assert.False(t, lUnknown.Enabled(context.Background(), slog.LevelDebug))

	// Nil config doesn't panic
	lNil := New(nil)
	require.NotNil(t, lNil)
	assert.True(t, lNil.Enabled(context.Background(), slog.LevelInfo))
}

func TestLoggerWithFile(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "test.log")

	l := New(&config.Config{
		LogLevel: "info",
		LogFile:  logFile,
	})
	require.NotNil(t, l)
	l.Info("test log message", "key", "val")
	t.Cleanup(func() {
		_ = Close()
	})
}

func TestLoggerContext(t *testing.T) {
	l := New(&config.Config{LogLevel: "debug"})
	ctx := WithLogger(context.Background(), l)
	assert.Equal(t, l, FromContext(ctx))

	assert.Equal(t, slog.Default(), FromContext(context.Background()))
}
