package daemon

import (
	"context"
	"time"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
)

var _ ipc.Handler = (*Handler)(nil)

// Handler implements ipc.Handler by delegating to Supervisor and RingBuffer.
type Handler struct {
	sup    *Supervisor
	cancel context.CancelFunc
	ring   *RingBuffer
}

// NewHandler constructs an IPC handler wrapping the supervisor, shutdown cancel func, and log ring buffer.
func NewHandler(sup *Supervisor, cancel context.CancelFunc, ring *RingBuffer) *Handler {
	return &Handler{
		sup:    sup,
		cancel: cancel,
		ring:   ring,
	}
}

// Status returns a point-in-time snapshot of the daemon's operational state.
func (h *Handler) Status(ctx context.Context) (ipc.StatusResult, error) {
	return h.sup.Status(ctx)
}

// Priority queries or updates the priority games list.
func (h *Handler) Priority(ctx context.Context, p ipc.PriorityParams) (ipc.PriorityResult, error) {
	return h.sup.UpdatePriority(ctx, p)
}

// Exclude handles daemon.Exclude requests by delegating to the supervisor.
func (h *Handler) Exclude(ctx context.Context, p ipc.ExcludeParams) (ipc.ExcludeResult, error) {
	return h.sup.UpdateExclude(ctx, p)
}

// Shutdown initiates graceful termination by canceling the daemon context.
func (h *Handler) Shutdown(ctx context.Context, p ipc.ShutdownParams) (ipc.ShutdownResult, error) {
	if h.cancel != nil {
		h.cancel()
	}
	return ipc.ShutdownResult{
		Status: "shutting_down",
	}, nil
}

// GetLogs returns the most recent log lines from the in-memory ring buffer.
func (h *Handler) GetLogs(ctx context.Context, p ipc.GetLogsParams) (ipc.GetLogsResult, error) {
	return ipc.GetLogsResult{
		Lines: h.ring.Lines(p.Limit),
	}, nil
}

// StreamLogs subscribes to live log lines and forwards them as JSON-RPC notifications until canceled.
func (h *Handler) StreamLogs(ctx context.Context, conn *jsonrpc2.Conn, p ipc.GetLogsParams) error {
	ch, cancel := h.ring.Subscribe()
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-ch:
			if !ok {
				return nil
			}
			notif := ipc.LogEntryNotification{
				Timestamp: time.Now().UTC(),
				Line:      line,
			}
			if err := conn.Notify(ctx, ipc.NotifyLogEntry, notif); err != nil {
				return err
			}
		}
	}
}
