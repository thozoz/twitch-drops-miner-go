package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubHandler struct {
	statusResult   StatusResult
	priorityResult PriorityResult
	shutdownResult ShutdownResult
	getLogsResult  GetLogsResult
	streamLines    []string
}

func (s *stubHandler) Status(ctx context.Context) (StatusResult, error) {
	return s.statusResult, nil
}

func (s *stubHandler) Priority(ctx context.Context, p PriorityParams) (PriorityResult, error) {
	return s.priorityResult, nil
}

func (s *stubHandler) Shutdown(ctx context.Context, p ShutdownParams) (ShutdownResult, error) {
	return s.shutdownResult, nil
}

func (s *stubHandler) GetLogs(ctx context.Context, p GetLogsParams) (GetLogsResult, error) {
	return s.getLogsResult, nil
}

func (s *stubHandler) StreamLogs(ctx context.Context, conn *jsonrpc2.Conn, p GetLogsParams) error {
	for _, line := range s.streamLines {
		_ = conn.Notify(ctx, NotifyLogEntry, LogEntryNotification{
			Timestamp: time.Now(),
			Line:      line,
		})
	}
	return nil
}

func testSocketAddr(t *testing.T) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`\\.\pipe\tdm-test-srv-%d-%d`, time.Now().UnixNano(), rand.Intn(100000))
	}
	return filepath.Join(t.TempDir(), "srv.sock")
}

func TestServeAndCall_RoundTrip(t *testing.T) {
	addr := testSocketAddr(t)
	ln, err := Bind(addr)
	require.NoError(t, err)
	defer func() { _ = Unbind(ln, addr) }()

	fixedStatus := StatusResult{
		Status:          "watching",
		PID:             1234,
		UptimeSeconds:   120,
		ActiveGame:      "Rust",
		ActiveCampaign:  "Rust Drops",
		ActiveDrop:      "Rust Hoodie",
		CurrentMinutes:  30,
		RequiredMinutes: 60,
		ProgressPercent: 50.0,
		WatchingChannel: "streamer1",
		ETASeconds:      1800,
		ErrorCount:      0,
		LastSyncTime:    time.Now().Truncate(time.Second),
	}

	h := &stubHandler{statusResult: fixedStatus}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- Serve(ctx, ln, h)
	}()

	clientConn, err := Dial(ctx, addr, 2*time.Second, nil)
	require.NoError(t, err)
	defer clientConn.Close()

	var result StatusResult
	err = Call(ctx, clientConn, MethodStatus, nil, &result)
	require.NoError(t, err)

	assert.Equal(t, fixedStatus.Status, result.Status)
	assert.Equal(t, fixedStatus.PID, result.PID)
	assert.Equal(t, fixedStatus.ActiveGame, result.ActiveGame)
	assert.Equal(t, fixedStatus.ActiveCampaign, result.ActiveCampaign)
	assert.Equal(t, fixedStatus.ProgressPercent, result.ProgressPercent)
	assert.Equal(t, fixedStatus.WatchingChannel, result.WatchingChannel)

	cancel()
	require.NoError(t, <-serveErr)
}

func TestServeAndCall_MethodNotFound(t *testing.T) {
	addr := testSocketAddr(t)
	ln, err := Bind(addr)
	require.NoError(t, err)
	defer func() { _ = Unbind(ln, addr) }()

	h := &stubHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = Serve(ctx, ln, h)
	}()

	clientConn, err := Dial(ctx, addr, 2*time.Second, nil)
	require.NoError(t, err)
	defer clientConn.Close()

	var result map[string]any
	err = Call(ctx, clientConn, "daemon.NonExistentMethod", nil, &result)
	require.Error(t, err)

	var jErr *jsonrpc2.Error
	if assert.True(t, errors.As(err, &jErr)) {
		assert.Equal(t, int64(jsonrpc2.CodeMethodNotFound), jErr.Code)
	}

	cancel()
}

func TestServeAndCall_Notifications(t *testing.T) {
	addr := testSocketAddr(t)
	ln, err := Bind(addr)
	require.NoError(t, err)
	defer func() { _ = Unbind(ln, addr) }()

	lines := []string{"log line 1: started", "log line 2: watching channel"}
	h := &stubHandler{streamLines: lines}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = Serve(ctx, ln, h)
	}()

	received := make(chan string, 10)
	onNotify := func(method string, params json.RawMessage) {
		if method == NotifyLogEntry {
			var notif LogEntryNotification
			if err := json.Unmarshal(params, &notif); err == nil {
				received <- notif.Line
			}
		}
	}

	clientConn, err := Dial(ctx, addr, 2*time.Second, onNotify)
	require.NoError(t, err)
	defer clientConn.Close()

	var subAck map[string]string
	err = Call(ctx, clientConn, MethodStreamLogs, GetLogsParams{Limit: 10, Follow: true}, &subAck)
	require.NoError(t, err)
	assert.Equal(t, "subscribed", subAck["status"])

	// Assert lines received in order
	select {
	case line1 := <-received:
		assert.Equal(t, lines[0], line1)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notification line 1")
	}

	select {
	case line2 := <-received:
		assert.Equal(t, lines[1], line2)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notification line 2")
	}

	cancel()
}
