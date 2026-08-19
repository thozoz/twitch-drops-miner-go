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

type mockLogsHandler struct {
	getLogsResult ipc.GetLogsResult
	streamLines   []string
}

func (m *mockLogsHandler) Status(ctx context.Context) (ipc.StatusResult, error) {
	return ipc.StatusResult{}, nil
}

func (m *mockLogsHandler) Priority(ctx context.Context, p ipc.PriorityParams) (ipc.PriorityResult, error) {
	return ipc.PriorityResult{}, nil
}

func (m *mockLogsHandler) Shutdown(ctx context.Context, p ipc.ShutdownParams) (ipc.ShutdownResult, error) {
	return ipc.ShutdownResult{Status: "shutting_down"}, nil
}

func (m *mockLogsHandler) GetLogs(ctx context.Context, p ipc.GetLogsParams) (ipc.GetLogsResult, error) {
	return m.getLogsResult, nil
}

func (m *mockLogsHandler) StreamLogs(ctx context.Context, conn *jsonrpc2.Conn, p ipc.GetLogsParams) error {
	for _, line := range m.streamLines {
		_ = conn.Notify(ctx, ipc.NotifyLogEntry, ipc.LogEntryNotification{
			Timestamp: time.Now(),
			Line:      line,
		})
	}
	return nil
}

func TestFormatLogLines(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		assert.Equal(t, "", formatLogLines([]string{}))
		assert.Equal(t, "", formatLogLines(nil))
	})

	t.Run("single line", func(t *testing.T) {
		assert.Equal(t, "single line message", formatLogLines([]string{"single line message"}))
	})

	t.Run("multiple lines", func(t *testing.T) {
		lines := []string{"line 1", "line 2", "line 3"}
		assert.Equal(t, "line 1\nline 2\nline 3", formatLogLines(lines))
	})
}

func TestLogsCmd_DialFailure(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("XDG_RUNTIME_DIR", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"logs"})

	code := Execute()
	assert.Equal(t, ExitError, code)
	assert.Contains(t, buf.String(), "tdm is not running")
}

func TestLogsCmd_SnapshotRoundTrip(t *testing.T) {
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

	fixtureLines := []string{
		"2026-08-19T10:00:00Z INFO starting tdm daemon",
		"2026-08-19T10:00:01Z INFO syncing campaigns",
		"2026-08-19T10:00:02Z INFO watching channel teststreamer",
	}

	handler := &mockLogsHandler{
		getLogsResult: ipc.GetLogsResult{
			Lines: fixtureLines,
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

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"logs", "-n", "3"})

	code := Execute()
	assert.Equal(t, ExitOK, code)

	output := buf.String()
	for _, expectedLine := range fixtureLines {
		assert.Contains(t, output, expectedLine)
	}
}

func TestLogsCmd_FollowMode(t *testing.T) {
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

	snapshotLines := []string{"[snapshot] line 1"}
	streamLines := []string{"[stream] line 2", "[stream] line 3"}

	handler := &mockLogsHandler{
		getLogsResult: ipc.GetLogsResult{Lines: snapshotLines},
		streamLines:   streamLines,
	}

	ctx, cancel := context.WithCancel(context.Background())
	serverDone2 := make(chan error, 1)
	go func() {
		serverDone2 <- ipc.Serve(ctx, ln, handler)
	}()
	defer func() {
		cancel()
		<-serverDone2
	}()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)

	cmdCtx, cmdCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cmdCancel()
	rootCmd.SetArgs([]string{"logs", "-f", "-n", "1"})

	code := ExecuteContext(cmdCtx)
	assert.Equal(t, ExitOK, code)

	output := buf.String()
	assert.Contains(t, output, "[snapshot] line 1")
	assert.Contains(t, output, "[stream] line 2")
	assert.Contains(t, output, "[stream] line 3")
}
