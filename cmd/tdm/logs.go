package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "View or follow daemon logs",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		addr, err := config.SocketPath()
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "tdm is not running")
			return &CommandError{Code: ExitError, Err: err}
		}

		follow, _ := cmd.Flags().GetBool("follow")
		lines, _ := cmd.Flags().GetInt("lines")

		// Snapshot dial to retrieve recent history
		conn, err := ipc.Dial(ctx, addr, 3*time.Second, nil)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "tdm is not running")
			return &CommandError{Code: ExitError, Err: err}
		}

		var snapshot ipc.GetLogsResult
		if err := ipc.Call(ctx, conn, ipc.MethodGetLogs, ipc.GetLogsParams{Limit: lines}, &snapshot); err != nil {
			_ = conn.Close()
			return &CommandError{Code: ExitError, Err: err}
		}
		_ = conn.Close()

		if out := formatLogLines(snapshot.Lines); out != "" {
			fmt.Fprintln(cmd.OutOrStdout(), out)
		}

		if !follow {
			return nil
		}

		// Log-rotation via lumberjack renames/truncates the underlying file, so naively
		// tailing the file loses lines exactly at rotation boundaries. Streaming through
		// the daemon's in-memory ring buffer over the existing JSON-RPC connection sidesteps
		// that failure mode entirely, at the cost of only seeing logs written after the
		// daemon's ring buffer was populated (acceptable — GetLogs's initial snapshot already
		// covers recent history).
		followCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()

		onNotify := func(method string, params json.RawMessage) {
			if method == ipc.NotifyLogEntry {
				var notif ipc.LogEntryNotification
				if err := json.Unmarshal(params, &notif); err == nil {
					fmt.Fprintln(cmd.OutOrStdout(), notif.Line)
				}
			}
		}

		streamConn, err := ipc.Dial(followCtx, addr, 3*time.Second, onNotify)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "tdm is not running")
			return &CommandError{Code: ExitError, Err: err}
		}
		defer streamConn.Close()

		var ack map[string]any
		if err := ipc.Call(followCtx, streamConn, ipc.MethodStreamLogs, ipc.GetLogsParams{Limit: lines, Follow: true}, &ack); err != nil {
			return &CommandError{Code: ExitError, Err: err}
		}

		<-followCtx.Done()
		return nil
	},
}

func formatLogLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func init() {
	logsCmd.Flags().BoolP("follow", "f", false, "Follow log output")
	logsCmd.Flags().IntP("lines", "n", 50, "Number of lines to show")
	rootCmd.AddCommand(logsCmd)
}
