package main

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tdm/internal/config"
	"tdm/internal/ipc"
)

type mockPriorityHandler struct {
	mu             sync.Mutex
	lastParams     ipc.PriorityParams
	priorityResult ipc.PriorityResult
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

func TestPriorityCmd_DialFailure(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("XDG_RUNTIME_DIR", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	subcommands := [][]string{
		{"priority", "list"},
		{"priority", "add", "Rust"},
		{"priority", "set", "Rust", "Overwatch 2"},
	}

	for _, args := range subcommands {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs(args)

		code := Execute()
		assert.Equal(t, ExitError, code, "expected ExitError for args: %v", args)
		assert.Contains(t, buf.String(), "tdm is not running")
	}
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
