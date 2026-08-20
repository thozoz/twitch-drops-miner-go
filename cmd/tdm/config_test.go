package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
)

func TestConfigSetCmd(t *testing.T) {
	run := func(t *testing.T, args ...string) (string, int) {
		t.Helper()
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs(args)

		code := Execute()
		return buf.String(), code
	}

	t.Run("sets enable_badges_emotes", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", tempDir)
		t.Setenv("XDG_CONFIG_HOME", tempDir)
		t.Setenv("XDG_RUNTIME_DIR", tempDir)
		xdg.Reload()
		t.Cleanup(func() { xdg.Reload() })

		cfgPath, err := config.ConfigFilePath()
		require.NoError(t, err)

		out, code := run(t, "config", "set", "enable_badges_emotes", "true")
		require.Equal(t, ExitOK, code, out)
		assert.Contains(t, out, "enable_badges_emotes")
		assert.Contains(t, out, "true")
		assert.Contains(t, out, "restart tdm")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.True(t, cfg.EnableBadgesEmotes)
	})

	t.Run("rejects unknown key", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", tempDir)
		t.Setenv("XDG_CONFIG_HOME", tempDir)
		t.Setenv("XDG_RUNTIME_DIR", tempDir)
		xdg.Reload()
		t.Cleanup(func() { xdg.Reload() })

		out, code := run(t, "config", "set", "enable_badges_emote", "true")
		assert.NotEqual(t, ExitOK, code)
		assert.Contains(t, out, "unknown config key")
		assert.Contains(t, out, "enable_badges_emotes")
	})

	t.Run("rejects unparseable value without writing a file", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", tempDir)
		t.Setenv("XDG_CONFIG_HOME", tempDir)
		t.Setenv("XDG_RUNTIME_DIR", tempDir)
		xdg.Reload()
		t.Cleanup(func() { xdg.Reload() })

		cfgPath, err := config.ConfigFilePath()
		require.NoError(t, err)

		out, code := run(t, "config", "set", "enable_badges_emotes", "maybe")
		assert.NotEqual(t, ExitOK, code)
		assert.Contains(t, out, "invalid value")

		_, statErr := os.Stat(cfgPath)
		assert.True(t, os.IsNotExist(statErr), "config file must not be created on an invalid value")
	})

	t.Run("preserves unrelated existing keys", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", tempDir)
		t.Setenv("XDG_CONFIG_HOME", tempDir)
		t.Setenv("XDG_RUNTIME_DIR", tempDir)
		xdg.Reload()
		t.Cleanup(func() { xdg.Reload() })

		cfgPath, err := config.ConfigFilePath()
		require.NoError(t, err)
		require.NoError(t, config.SaveKey(cfgPath, "log_level", "debug"))
		require.NoError(t, config.SavePriority(cfgPath, []string{"Rust"}))

		out, code := run(t, "config", "set", "enable_badges_emotes", "false")
		require.Equal(t, ExitOK, code, out)

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.False(t, cfg.EnableBadgesEmotes)
		assert.Equal(t, "debug", cfg.LogLevel)
		assert.Equal(t, []string{"Rust"}, cfg.Priority)
	})
}
