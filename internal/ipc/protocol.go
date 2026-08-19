package ipc

import (
	"context"
	"time"

	"github.com/sourcegraph/jsonrpc2"
)

// JSON-RPC 2.0 method name constants.
const (
	MethodStatus     = "daemon.Status"
	MethodPriority   = "daemon.Priority"
	MethodShutdown   = "daemon.Shutdown"
	MethodGetLogs    = "daemon.GetLogs"
	MethodStreamLogs = "daemon.StreamLogs"

	NotifyLogEntry = "log.entry"
)

// StatusResult contains snapshot details about the daemon's current operational state.
type StatusResult struct {
	Status          string    `json:"status"`
	PID             int       `json:"pid"`
	UptimeSeconds   int64     `json:"uptime_seconds"`
	ActiveGame      string    `json:"active_game"`
	ActiveCampaign  string    `json:"active_campaign"`
	ActiveDrop      string    `json:"active_drop"`
	CurrentMinutes  int       `json:"current_minutes"`
	RequiredMinutes int       `json:"required_minutes"`
	ProgressPercent float64   `json:"progress_percent"`
	WatchingChannel string    `json:"watching_channel"`
	ETASeconds      int64     `json:"eta_seconds"`
	ErrorCount      int       `json:"error_count"`
	LastSyncTime    time.Time `json:"last_sync_time"`
}

// PriorityAction defines the mutation or query action for priority games.
type PriorityAction string

const (
	PriorityList PriorityAction = "list"
	PriorityAdd  PriorityAction = "add"
	PrioritySet  PriorityAction = "set"
)

// PriorityParams contains parameters for daemon.Priority.
type PriorityParams struct {
	Action PriorityAction `json:"action"`
	Games  []string       `json:"games"`
}

// PriorityResult contains the effective priority game list after an operation.
type PriorityResult struct {
	Priority []string `json:"priority"`
}

// ShutdownParams contains parameters for daemon.Shutdown.
type ShutdownParams struct {
	TimeoutSeconds int `json:"timeout_seconds"`
}

// ShutdownResult contains the status of the shutdown request.
type ShutdownResult struct {
	Status string `json:"status"`
}

// GetLogsParams contains parameters for daemon.GetLogs and daemon.StreamLogs.
type GetLogsParams struct {
	Limit  int  `json:"limit"`
	Follow bool `json:"follow"`
}

// GetLogsResult contains a slice of recent log lines.
type GetLogsResult struct {
	Lines []string `json:"lines"`
}

// LogEntryNotification contains data sent with log.entry server notifications.
type LogEntryNotification struct {
	Timestamp time.Time `json:"timestamp"`
	Line      string    `json:"line"`
}

// Handler defines the contract for handling JSON-RPC requests on the daemon.
type Handler interface {
	Status(ctx context.Context) (StatusResult, error)
	Priority(ctx context.Context, p PriorityParams) (PriorityResult, error)
	Shutdown(ctx context.Context, p ShutdownParams) (ShutdownResult, error)
	GetLogs(ctx context.Context, p GetLogsParams) (GetLogsResult, error)
	StreamLogs(ctx context.Context, conn *jsonrpc2.Conn, p GetLogsParams) error
}
