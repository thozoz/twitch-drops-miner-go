package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tdm/internal/config"
	"tdm/internal/ipc"
)

type stubStopHandler struct {
	onShutdown func()
}

func (s *stubStopHandler) Status(ctx context.Context) (ipc.StatusResult, error) {
	return ipc.StatusResult{Status: "watching"}, nil
}

func (s *stubStopHandler) Priority(ctx context.Context, p ipc.PriorityParams) (ipc.PriorityResult, error) {
	return ipc.PriorityResult{}, nil
}

func (s *stubStopHandler) Shutdown(ctx context.Context, p ipc.ShutdownParams) (ipc.ShutdownResult, error) {
	if s.onShutdown != nil {
		go s.onShutdown()
	}
	return ipc.ShutdownResult{Status: "shutting_down"}, nil
}

func (s *stubStopHandler) GetLogs(ctx context.Context, p ipc.GetLogsParams) (ipc.GetLogsResult, error) {
	return ipc.GetLogsResult{}, nil
}

func (s *stubStopHandler) StreamLogs(ctx context.Context, conn *jsonrpc2.Conn, p ipc.GetLogsParams) error {
	return nil
}

func TestStop_WhenNotRunning(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("XDG_RUNTIME_DIR", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"stop"})

	code := Execute()
	assert.Equal(t, ExitOK, code)
	assert.Contains(t, buf.String(), "tdm is not running")
}

func TestStop_GracefulShutdown(t *testing.T) {
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
	defer func() {
		_ = ipc.Unbind(ln, addr)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := &stubStopHandler{
		onShutdown: func() {
			go func() {
				time.Sleep(10 * time.Millisecond)
				cancel()
			}()
		},
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- ipc.Serve(ctx, ln, handler)
	}()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"stop", "--timeout", "3"})

	code := Execute()
	assert.Equal(t, ExitOK, code)
	assert.Contains(t, buf.String(), "tdm daemon stopped")

	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not terminate")
	}
}
