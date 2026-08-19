package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/spf13/cobra"
	"tdm/internal/config"
	"tdm/internal/ipc"
	"tdm/internal/logging"
)

var stopTimeout int

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a running tdm mining daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := logging.FromContext(ctx)

		addr, err := config.SocketPath()
		if err != nil {
			logger.Error("failed to resolve socket path", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		running, _ := ipc.ProbeRunning(addr, 500*time.Millisecond)
		if !running {
			fmt.Fprintln(cmd.OutOrStdout(), "tdm is not running")
			return nil
		}

		var conn *jsonrpc2.Conn
		dialDeadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(dialDeadline) {
			conn, err = ipc.Dial(ctx, addr, 1*time.Second, nil)
			if err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil {
			fmt.Fprintln(cmd.OutOrStdout(), "tdm is not running")
			return nil
		}

		var result ipc.ShutdownResult
		_ = ipc.Call(ctx, conn, ipc.MethodShutdown, ipc.ShutdownParams{TimeoutSeconds: stopTimeout}, &result)
		_ = conn.Close()

		pollDeadline := time.Now().Add(time.Duration(stopTimeout+5) * time.Second)
		for time.Now().Before(pollDeadline) {
			time.Sleep(100 * time.Millisecond)
			isStillRunning, _ := ipc.ProbeRunning(addr, 100*time.Millisecond)
			if !isStillRunning {
				fmt.Fprintln(cmd.OutOrStdout(), "tdm daemon stopped")
				return nil
			}
		}

		return &CommandError{Code: ExitError, Err: errors.New("daemon did not stop within timeout")}
	},
}

func init() {
	stopCmd.Flags().IntVar(&stopTimeout, "timeout", 15, "Timeout in seconds to wait for daemon shutdown")
	rootCmd.AddCommand(stopCmd)
}
