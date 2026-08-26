package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
)

func TestDropExcludeCmd_OfflineFallback(t *testing.T) {
	// With no daemon listening, the drop-exclude commands work against
	// config.json instead of refusing outright, the same as exclude/priority.
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("XDG_RUNTIME_DIR", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	cfgPath, err := config.ConfigFilePath()
	require.NoError(t, err)

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs(args)

		code := Execute()
		require.Equal(t, ExitOK, code, "args: %v, output: %s", args, buf.String())
		return buf.String()
	}

	t.Run("list on a fresh install reports an empty list", func(t *testing.T) {
		out := run(t, "drop-exclude", "list")
		assert.Contains(t, out, "(none)")
	})

	t.Run("add writes to config and says it applies next start", func(t *testing.T) {
		out := run(t, "drop-exclude", "add", "cosmetic", "emote")
		assert.Contains(t, out, "cosmetic")
		assert.Contains(t, out, "tdm is not running")
		assert.Contains(t, out, "takes effect on next start",
			"the operator must not mistake this for a live change")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"cosmetic", "emote"}, cfg.DropExclude)
	})

	t.Run("add is idempotent", func(t *testing.T) {
		run(t, "drop-exclude", "add", "emote")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"cosmetic", "emote"}, cfg.DropExclude,
			"an already-present keyword must not be duplicated")
	})

	t.Run("remove drops just the named keyword", func(t *testing.T) {
		out := run(t, "drop-exclude", "remove", "emote")
		assert.NotContains(t, out, "emote")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"cosmetic"}, cfg.DropExclude)
	})

	t.Run("removing an absent keyword succeeds and changes nothing", func(t *testing.T) {
		run(t, "drop-exclude", "remove", "never-added")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"cosmetic"}, cfg.DropExclude)
	})

	t.Run("set replaces the whole list", func(t *testing.T) {
		run(t, "drop-exclude", "set", "badge")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"badge"}, cfg.DropExclude)
	})

	t.Run("list reads back what was written", func(t *testing.T) {
		out := run(t, "drop-exclude", "list")
		assert.Contains(t, out, "badge")
	})
}

func TestDropExcludeCmd_OfflinePreservesUnrelatedConfigKeys(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("XDG_RUNTIME_DIR", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	cfgPath, err := config.ConfigFilePath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte(`{"log_level":"debug","priority":["Rust"],"drop_exclude":["old"]}`), 0o600))

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"drop-exclude", "set", "cosmetic"})
	require.Equal(t, ExitOK, Execute(), buf.String())

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"cosmetic"}, cfg.DropExclude)
	assert.Equal(t, []string{"Rust"}, cfg.Priority, "the priority list must survive a drop-exclude write")
	assert.Equal(t, "debug", cfg.LogLevel, "an unrelated setting must survive the write")
}

func TestDropExcludeCmd_RoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("XDG_RUNTIME_DIR", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	addr, err := config.SocketPath()
	require.NoError(t, err)

	ln, err := ipc.Bind(addr)
	require.NoError(t, err)
	defer func() { _ = ipc.Unbind(ln, addr) }()

	handler := &mockPriorityHandler{
		dropExcludeResult: ipc.DropExcludeResult{
			DropExclude: []string{"cosmetic", "emote"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- ipc.Serve(ctx, ln, handler)
	}()
	defer func() {
		cancel()
		<-serverDone
	}()

	call := func(t *testing.T, args ...string) string {
		t.Helper()
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs(args)
		require.Equal(t, ExitOK, Execute(), buf.String())
		return buf.String()
	}

	t.Run("drop-exclude list", func(t *testing.T) {
		out := call(t, "drop-exclude", "list")
		assert.Contains(t, out, "cosmetic\nemote")

		handler.mu.Lock()
		defer handler.mu.Unlock()
		assert.Equal(t, ipc.DropExcludeList, handler.lastDropExcludeParams.Action)
	})

	t.Run("drop-exclude add forwards the keywords", func(t *testing.T) {
		call(t, "drop-exclude", "add", "badge")

		handler.mu.Lock()
		defer handler.mu.Unlock()
		assert.Equal(t, ipc.DropExcludeAdd, handler.lastDropExcludeParams.Action)
		assert.Equal(t, []string{"badge"}, handler.lastDropExcludeParams.Keywords)
	})

	t.Run("drop-exclude remove forwards the keywords", func(t *testing.T) {
		call(t, "drop-exclude", "remove", "cosmetic")

		handler.mu.Lock()
		defer handler.mu.Unlock()
		assert.Equal(t, ipc.DropExcludeRemove, handler.lastDropExcludeParams.Action)
		assert.Equal(t, []string{"cosmetic"}, handler.lastDropExcludeParams.Keywords)
	})

	t.Run("drop-exclude set forwards the keywords", func(t *testing.T) {
		call(t, "drop-exclude", "set", "cosmetic", "emote")

		handler.mu.Lock()
		defer handler.mu.Unlock()
		assert.Equal(t, ipc.DropExcludeSet, handler.lastDropExcludeParams.Action)
		assert.Equal(t, []string{"cosmetic", "emote"}, handler.lastDropExcludeParams.Keywords)
	})
}

type legacyDropExcludeServerHandler struct{}

func (h *legacyDropExcludeServerHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
		Code:    jsonrpc2.CodeMethodNotFound,
		Message: "method not found: daemon.DropExclude",
	})
}

func TestDropExcludeCmd_LegacyDaemonMethodNotFound_FallsBackToOffline(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("XDG_RUNTIME_DIR", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	cfgPath, err := config.ConfigFilePath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"drop_exclude":["initial"]}`), 0o600))

	addr, err := config.SocketPath()
	require.NoError(t, err)

	ln, err := ipc.Bind(addr)
	require.NoError(t, err)
	defer func() { _ = ipc.Unbind(ln, addr) }()

	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- ipc.ServeRaw(ctx, ln, &legacyDropExcludeServerHandler{})
	}()
	defer func() {
		cancel()
		<-serverDone
	}()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"drop-exclude", "add", "fallback"})
	require.Equal(t, ExitOK, Execute(), buf.String())

	out := buf.String()
	assert.Contains(t, out, "fallback")
	assert.Contains(t, out, "running daemon does not support live drop-exclude updates")
	assert.Contains(t, out, "takes effect on next start")

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"initial", "fallback"}, cfg.DropExclude)
}
