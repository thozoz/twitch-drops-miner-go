package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"tdm/internal/config"
	"tdm/internal/ipc"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status and mining progress",
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

		conn, err := ipc.Dial(ctx, addr, 3*time.Second, nil)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "tdm is not running")
			return &CommandError{Code: ExitError, Err: err}
		}
		defer conn.Close()

		var result ipc.StatusResult
		if err := ipc.Call(ctx, conn, ipc.MethodStatus, nil, &result); err != nil {
			return &CommandError{Code: ExitError, Err: err}
		}

		asJSON, _ := cmd.Flags().GetBool("json")
		out, err := formatStatus(result, asJSON)
		if err != nil {
			return &CommandError{Code: ExitError, Err: err}
		}

		fmt.Fprintln(cmd.OutOrStdout(), out)
		return nil
	},
}

func formatStatus(r ipc.StatusResult, asJSON bool) (string, error) {
	if asJSON {
		data, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	lines := []string{
		fmt.Sprintf("Status: %s", r.Status),
		fmt.Sprintf("Campaign: %s (%s)", r.ActiveCampaign, r.ActiveGame),
		fmt.Sprintf("Channel: %s", r.WatchingChannel),
		fmt.Sprintf("Drop: %s (%d/%d min, %.1f%%)", r.ActiveDrop, r.CurrentMinutes, r.RequiredMinutes, r.ProgressPercent),
		fmt.Sprintf("ETA: %s", (time.Duration(r.ETASeconds) * time.Second).String()),
		fmt.Sprintf("Errors: %d", r.ErrorCount),
		fmt.Sprintf("Uptime: %s", (time.Duration(r.UptimeSeconds) * time.Second).String()),
	}
	return strings.Join(lines, "\n"), nil
}

func init() {
	statusCmd.Flags().Bool("json", false, "Output status as JSON")
	rootCmd.AddCommand(statusCmd)
}
