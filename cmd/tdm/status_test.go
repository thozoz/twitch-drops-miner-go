package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
)

type mockStatusHandler struct {
	statusResult   ipc.StatusResult
	priorityResult ipc.PriorityResult
	getLogsResult  ipc.GetLogsResult
	streamLines    []string
}

func (m *mockStatusHandler) Status(ctx context.Context) (ipc.StatusResult, error) {
	return m.statusResult, nil
}

func (m *mockStatusHandler) Priority(ctx context.Context, p ipc.PriorityParams) (ipc.PriorityResult, error) {
	return m.priorityResult, nil
}

func (m *mockStatusHandler) Shutdown(ctx context.Context, p ipc.ShutdownParams) (ipc.ShutdownResult, error) {
	return ipc.ShutdownResult{Status: "shutting_down"}, nil
}

func (m *mockStatusHandler) GetLogs(ctx context.Context, p ipc.GetLogsParams) (ipc.GetLogsResult, error) {
	return m.getLogsResult, nil
}

func (m *mockStatusHandler) StreamLogs(ctx context.Context, conn *jsonrpc2.Conn, p ipc.GetLogsParams) error {
	for _, line := range m.streamLines {
		_ = conn.Notify(ctx, ipc.NotifyLogEntry, ipc.LogEntryNotification{
			Timestamp: time.Now(),
			Line:      line,
		})
	}
	return nil
}

func TestFormatStatus(t *testing.T) {
	fixed := ipc.StatusResult{
		Status:          "watching",
		PID:             4242,
		UptimeSeconds:   120,
		ActiveGame:      "Rust",
		ActiveCampaign:  "Rust Drops Season 1",
		ActiveDrop:      "Rust Hoodie",
		CurrentMinutes:  30,
		RequiredMinutes: 60,
		ProgressPercent: 50.0,
		WatchingChannel: "streamer123",
		ETASeconds:      1800,
		ErrorCount:      2,
		LastSyncTime:    time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
	}

	t.Run("human readable format", func(t *testing.T) {
		out, err := formatStatus(fixed, false)
		require.NoError(t, err)

		assert.Contains(t, out, "Status: watching")
		assert.Contains(t, out, "Campaign: Rust Drops Season 1 (Rust)")
		assert.Contains(t, out, "Channel: streamer123")
		assert.Contains(t, out, "Drop: Rust Hoodie (30/60 min, 50.0%)")
		assert.Contains(t, out, "ETA: 30m0s")
		assert.Contains(t, out, "Errors: 2")
		assert.Contains(t, out, "Uptime: 2m0s")
	})

	t.Run("json format round-trip", func(t *testing.T) {
		out, err := formatStatus(fixed, true)
		require.NoError(t, err)
		assert.True(t, json.Valid([]byte(out)))

		var decoded ipc.StatusResult
		err = json.Unmarshal([]byte(out), &decoded)
		require.NoError(t, err)
		assert.Equal(t, fixed, decoded)
	})
}

func TestStatusCmd_DialFailure(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("XDG_RUNTIME_DIR", tempDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"status"})

	code := Execute()
	assert.Equal(t, ExitError, code)
	assert.Contains(t, buf.String(), "tdm is not running")
}

func TestStatusCmd_Success(t *testing.T) {
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

	handler := &mockStatusHandler{
		statusResult: ipc.StatusResult{
			Status:          "mining",
			PID:             100,
			UptimeSeconds:   60,
			ActiveGame:      "Overwatch 2",
			ActiveCampaign:  "OW2 Drops",
			ActiveDrop:      "Kiriko Skin",
			CurrentMinutes:  15,
			RequiredMinutes: 60,
			ProgressPercent: 25.0,
			WatchingChannel: "super",
			ETASeconds:      2700,
			ErrorCount:      0,
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

	t.Run("human format execution", func(t *testing.T) {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs([]string{"status"})

		code := Execute()
		assert.Equal(t, ExitOK, code)
		assert.Contains(t, buf.String(), "Status: mining")
		assert.Contains(t, buf.String(), "Campaign: OW2 Drops (Overwatch 2)")
		assert.Contains(t, buf.String(), "Channel: super")
	})

	t.Run("json format execution", func(t *testing.T) {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs([]string{"status", "--json"})

		code := Execute()
		assert.Equal(t, ExitOK, code)

		var decoded ipc.StatusResult
		err = json.Unmarshal(buf.Bytes(), &decoded)
		require.NoError(t, err)
		assert.Equal(t, "mining", decoded.Status)
		assert.Equal(t, "Overwatch 2", decoded.ActiveGame)
	})
}
