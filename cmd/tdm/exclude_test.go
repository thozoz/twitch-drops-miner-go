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

func TestFormatGameList(t *testing.T) {
	assert.Equal(t, "(none)", formatGameList(nil))
	assert.Equal(t, "(none)", formatGameList([]string{}))
	assert.Equal(t, "Kakele Online - MMORPG", formatGameList([]string{"Kakele Online - MMORPG"}))
	assert.Equal(t, "ROBLOX\nSpecial Events", formatGameList([]string{"ROBLOX", "Special Events"}))
}

func TestAddGames(t *testing.T) {
	t.Run("appends new entries in order", func(t *testing.T) {
		assert.Equal(t, []string{"A", "B", "C"}, addGames([]string{"A"}, []string{"B", "C"}))
	})

	t.Run("skips duplicates", func(t *testing.T) {
		assert.Equal(t, []string{"A", "B"}, addGames([]string{"A", "B"}, []string{"A"}))
	})

	t.Run("matching is case-sensitive", func(t *testing.T) {
		// SelectCampaign compares Game.Name case-sensitively, so the CLI must not
		// collapse "ROBLOX" and "Roblox" into one entry — they exclude different
		// campaigns.
		assert.Equal(t, []string{"ROBLOX", "Roblox"}, addGames([]string{"ROBLOX"}, []string{"Roblox"}))
	})

	t.Run("does not alias the input backing array", func(t *testing.T) {
		in := []string{"A"}
		out := addGames(in, []string{"B"})
		out[0] = "mutated"
		assert.Equal(t, []string{"A"}, in)
	})
}

func TestRemoveGamesFromList(t *testing.T) {
	t.Run("removes and preserves remaining order", func(t *testing.T) {
		assert.Equal(t, []string{"A", "C"}, removeGamesFromList([]string{"A", "B", "C"}, []string{"B"}))
	})

	t.Run("removing an absent game is a no-op", func(t *testing.T) {
		assert.Equal(t, []string{"A"}, removeGamesFromList([]string{"A"}, []string{"Z"}))
	})

	t.Run("removes several at once", func(t *testing.T) {
		assert.Equal(t, []string{"B"}, removeGamesFromList([]string{"A", "B", "C"}, []string{"A", "C"}))
	})

	t.Run("removing everything yields an empty, non-nil list", func(t *testing.T) {
		out := removeGamesFromList([]string{"A"}, []string{"A"})
		assert.Empty(t, out)
		assert.NotNil(t, out)
	})

	t.Run("empty drop list returns a copy", func(t *testing.T) {
		in := []string{"A"}
		out := removeGamesFromList(in, nil)
		out[0] = "mutated"
		assert.Equal(t, []string{"A"}, in)
	})
}

func TestExcludeCmd_OfflineFallback(t *testing.T) {
	// With no daemon listening, the exclude commands work against config.json
	// instead of refusing outright — the whole point of issue #7 is not having to
	// hand-edit JSON.
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
		out := run(t, "exclude", "list")
		assert.Contains(t, out, "(none)")
	})

	t.Run("add writes to config and says it applies next start", func(t *testing.T) {
		out := run(t, "exclude", "add", "Kakele Online - MMORPG", "Special Events")
		assert.Contains(t, out, "Kakele Online - MMORPG")
		assert.Contains(t, out, "tdm is not running")
		assert.Contains(t, out, "takes effect on next start",
			"the operator must not mistake this for a live change")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"Kakele Online - MMORPG", "Special Events"}, cfg.Exclude)
	})

	t.Run("add is idempotent", func(t *testing.T) {
		run(t, "exclude", "add", "Special Events")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"Kakele Online - MMORPG", "Special Events"}, cfg.Exclude,
			"an already-present game must not be duplicated")
	})

	t.Run("remove drops just the named game", func(t *testing.T) {
		out := run(t, "exclude", "remove", "Special Events")
		assert.NotContains(t, out, "Special Events")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"Kakele Online - MMORPG"}, cfg.Exclude)
	})

	t.Run("removing an absent game succeeds and changes nothing", func(t *testing.T) {
		run(t, "exclude", "remove", "Never Added")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"Kakele Online - MMORPG"}, cfg.Exclude)
	})

	t.Run("set replaces the whole list", func(t *testing.T) {
		run(t, "exclude", "set", "ROBLOX")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"ROBLOX"}, cfg.Exclude)
	})

	t.Run("list reads back what was written", func(t *testing.T) {
		out := run(t, "exclude", "list")
		assert.Contains(t, out, "ROBLOX")
	})
}

func TestExcludeCmd_OfflinePreservesUnrelatedConfigKeys(t *testing.T) {
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
		[]byte(`{"log_level":"debug","priority":["Rust"],"exclude":["Old"]}`), 0o600))

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"exclude", "set", "ROBLOX"})
	require.Equal(t, ExitOK, Execute(), buf.String())

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"ROBLOX"}, cfg.Exclude)
	assert.Equal(t, []string{"Rust"}, cfg.Priority, "the priority list must survive an exclude write")
	assert.Equal(t, "debug", cfg.LogLevel, "an unrelated setting must survive the write")
}

func TestExcludeCmd_RoundTrip(t *testing.T) {
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
		excludeResult: ipc.ExcludeResult{
			Exclude: []string{"ROBLOX", "Special Events"},
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

	t.Run("exclude list", func(t *testing.T) {
		out := call(t, "exclude", "list")
		assert.Contains(t, out, "ROBLOX\nSpecial Events")

		handler.mu.Lock()
		defer handler.mu.Unlock()
		assert.Equal(t, ipc.ExcludeList, handler.lastExcludeParams.Action)
	})

	t.Run("exclude add forwards the games", func(t *testing.T) {
		call(t, "exclude", "add", "Kakele Online - MMORPG")

		handler.mu.Lock()
		defer handler.mu.Unlock()
		assert.Equal(t, ipc.ExcludeAdd, handler.lastExcludeParams.Action)
		assert.Equal(t, []string{"Kakele Online - MMORPG"}, handler.lastExcludeParams.Games)
	})

	t.Run("exclude remove forwards the games", func(t *testing.T) {
		call(t, "exclude", "remove", "ROBLOX")

		handler.mu.Lock()
		defer handler.mu.Unlock()
		assert.Equal(t, ipc.ExcludeRemove, handler.lastExcludeParams.Action)
		assert.Equal(t, []string{"ROBLOX"}, handler.lastExcludeParams.Games)
	})

	t.Run("exclude set forwards the games", func(t *testing.T) {
		call(t, "exclude", "set", "ROBLOX", "Special Events")

		handler.mu.Lock()
		defer handler.mu.Unlock()
		assert.Equal(t, ipc.ExcludeSet, handler.lastExcludeParams.Action)
		assert.Equal(t, []string{"ROBLOX", "Special Events"}, handler.lastExcludeParams.Games)
	})
}

func TestPriorityCmd_RemoveOffline(t *testing.T) {
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
		[]byte(`{"priority":["Rust","Valorant","Overwatch 2"]}`), 0o600))

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"priority", "remove", "Valorant"})
	require.Equal(t, ExitOK, Execute(), buf.String())

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"Rust", "Overwatch 2"}, cfg.Priority,
		"removal must preserve the order of what remains")
}

func TestPriorityCmd_RemoveRoundTrip(t *testing.T) {
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
		priorityResult: ipc.PriorityResult{Priority: []string{"Rust"}},
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

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"priority", "remove", "Valorant"})
	require.Equal(t, ExitOK, Execute(), buf.String())

	handler.mu.Lock()
	defer handler.mu.Unlock()
	assert.Equal(t, ipc.PriorityRemove, handler.lastParams.Action)
	assert.Equal(t, []string{"Valorant"}, handler.lastParams.Games)
}

type legacyExcludeServerHandler struct{}

func (h *legacyExcludeServerHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
		Code:    jsonrpc2.CodeMethodNotFound,
		Message: "method not found: daemon.Exclude",
	})
}

func TestExcludeCmd_LegacyDaemonMethodNotFound_FallsBackToOffline(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("XDG_RUNTIME_DIR", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	cfgPath, err := config.ConfigFilePath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"exclude":["InitialGame"]}`), 0o600))

	addr, err := config.SocketPath()
	require.NoError(t, err)

	ln, err := ipc.Bind(addr)
	require.NoError(t, err)
	defer func() { _ = ipc.Unbind(ln, addr) }()

	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- ipc.ServeRaw(ctx, ln, &legacyExcludeServerHandler{})
	}()
	defer func() {
		cancel()
		<-serverDone
	}()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"exclude", "add", "FallbackGame"})
	require.Equal(t, ExitOK, Execute(), buf.String())

	out := buf.String()
	assert.Contains(t, out, "FallbackGame")
	assert.Contains(t, out, "running daemon does not support live exclude updates")
	assert.Contains(t, out, "takes effect on next start")

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"InitialGame", "FallbackGame"}, cfg.Exclude)

	// Also verify that 'exclude list' warns about the legacy daemon
	bufList := new(bytes.Buffer)
	rootCmd.SetOut(bufList)
	rootCmd.SetErr(bufList)
	rootCmd.SetArgs([]string{"exclude", "list"})
	require.Equal(t, ExitOK, Execute(), bufList.String())

	outList := bufList.String()
	assert.Contains(t, outList, "InitialGame")
	assert.Contains(t, outList, "FallbackGame")
	assert.Contains(t, outList, "running daemon does not support live exclude updates")
}

