package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
)

type mockPriorityHandler struct {
	mu                sync.Mutex
	lastParams        ipc.PriorityParams
	priorityResult    ipc.PriorityResult
	lastExcludeParams ipc.ExcludeParams
	excludeResult     ipc.ExcludeResult
}

func (m *mockPriorityHandler) Status(ctx context.Context) (ipc.StatusResult, error) {
	return ipc.StatusResult{}, nil
}

func (m *mockPriorityHandler) Priority(ctx context.Context, p ipc.PriorityParams) (ipc.PriorityResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastParams = p
	return m.priorityResult, nil
}

func (m *mockPriorityHandler) Exclude(ctx context.Context, p ipc.ExcludeParams) (ipc.ExcludeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastExcludeParams = p
	return m.excludeResult, nil
}

func (m *mockPriorityHandler) Shutdown(ctx context.Context, p ipc.ShutdownParams) (ipc.ShutdownResult, error) {
	return ipc.ShutdownResult{Status: "shutting_down"}, nil
}

func (m *mockPriorityHandler) GetLogs(ctx context.Context, p ipc.GetLogsParams) (ipc.GetLogsResult, error) {
	return ipc.GetLogsResult{}, nil
}

func (m *mockPriorityHandler) StreamLogs(ctx context.Context, conn *jsonrpc2.Conn, p ipc.GetLogsParams) error {
	return nil
}

func TestFormatPriority(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		assert.Equal(t, "(none)", formatPriority([]string{}))
		assert.Equal(t, "(none)", formatPriority(nil))
	})

	t.Run("single game", func(t *testing.T) {
		assert.Equal(t, "Rust", formatPriority([]string{"Rust"}))
	})

	t.Run("multiple games", func(t *testing.T) {
		assert.Equal(t, "Rust\nOverwatch 2\nValorant", formatPriority([]string{"Rust", "Overwatch 2", "Valorant"}))
	})
}

func TestPriorityCmd_OfflineFallback(t *testing.T) {
	// With no daemon listening, the priority commands work against config.json
	// instead of refusing outright, so a list can be staged before first launch.
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
		out := run(t, "priority", "list")
		assert.Contains(t, out, "(none)")
	})

	t.Run("add writes to config and says it applies next start", func(t *testing.T) {
		out := run(t, "priority", "add", "Rust")
		assert.Contains(t, out, "Rust")
		assert.Contains(t, out, "tdm is not running")
		assert.Contains(t, out, "takes effect on next start",
			"the operator must not mistake this for a live change")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"Rust"}, cfg.Priority)
	})

	t.Run("add is idempotent and appends", func(t *testing.T) {
		run(t, "priority", "add", "Rust", "Valorant")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"Rust", "Valorant"}, cfg.Priority,
			"an already-present game must not be duplicated")
	})

	t.Run("set replaces the whole list", func(t *testing.T) {
		run(t, "priority", "set", "Overwatch 2")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"Overwatch 2"}, cfg.Priority)
	})

	t.Run("list reads back what was written", func(t *testing.T) {
		out := run(t, "priority", "list")
		assert.Contains(t, out, "Overwatch 2")
	})
}

func TestPriorityCmd_OfflinePreservesUnknownConfigKeys(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("XDG_RUNTIME_DIR", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	cfgPath, err := config.ConfigFilePath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"log_level":"debug","priority":["Old"]}`), 0o600))

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"priority", "set", "Rust"})
	require.Equal(t, ExitOK, Execute(), buf.String())

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"Rust"}, cfg.Priority)
	assert.Equal(t, "debug", cfg.LogLevel, "an unrelated setting must survive the write")
}

func TestPriorityCmd_RoundTrip(t *testing.T) {
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
		priorityResult: ipc.PriorityResult{
			Priority: []string{"Rust", "Overwatch 2"},
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

	t.Run("priority list", func(t *testing.T) {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs([]string{"priority", "list"})

		code := Execute()
		assert.Equal(t, ExitOK, code)
		assert.Contains(t, buf.String(), "Rust\nOverwatch 2")
		assert.Equal(t, ipc.PriorityList, handler.lastParams.Action)
	})

	t.Run("priority add", func(t *testing.T) {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs([]string{"priority", "add", "Valorant"})

		code := Execute()
		assert.Equal(t, ExitOK, code)
		assert.Equal(t, ipc.PriorityAdd, handler.lastParams.Action)
		assert.Equal(t, []string{"Valorant"}, handler.lastParams.Games)
	})

	t.Run("priority set", func(t *testing.T) {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs([]string{"priority", "set", "GameA", "GameB"})

		code := Execute()
		assert.Equal(t, ExitOK, code)
		assert.Equal(t, ipc.PrioritySet, handler.lastParams.Action)
		assert.Equal(t, []string{"GameA", "GameB"}, handler.lastParams.Games)
	})
}
