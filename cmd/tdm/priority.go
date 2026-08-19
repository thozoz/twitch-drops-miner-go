package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"tdm/internal/config"
	"tdm/internal/ipc"
)

var priorityCmd = &cobra.Command{
	Use:   "priority",
	Short: "Manage priority games",
}

var priorityListCmd = &cobra.Command{
	Use:   "list",
	Short: "List prioritized games",
	RunE: func(cmd *cobra.Command, args []string) error {
		return executePriorityCall(cmd, ipc.PriorityParams{
			Action: ipc.PriorityList,
		})
	},
}

var priorityAddCmd = &cobra.Command{
	Use:   "add <game...>",
	Short: "Add games to priority list",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return executePriorityCall(cmd, ipc.PriorityParams{
			Action: ipc.PriorityAdd,
			Games:  args,
		})
	},
}

var prioritySetCmd = &cobra.Command{
	Use:   "set <game...>",
	Short: "Set priority list",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return executePriorityCall(cmd, ipc.PriorityParams{
			Action: ipc.PrioritySet,
			Games:  args,
		})
	},
}

func executePriorityCall(cmd *cobra.Command, params ipc.PriorityParams) error {
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

	var result ipc.PriorityResult
	if err := ipc.Call(ctx, conn, ipc.MethodPriority, params, &result); err != nil {
		return &CommandError{Code: ExitError, Err: err}
	}

	fmt.Fprintln(cmd.OutOrStdout(), formatPriority(result.Priority))
	return nil
}

func formatPriority(games []string) string {
	if len(games) == 0 {
		return "(none)"
	}
	return strings.Join(games, "\n")
}

func init() {
	priorityCmd.AddCommand(priorityListCmd)
	priorityCmd.AddCommand(priorityAddCmd)
	priorityCmd.AddCommand(prioritySetCmd)
	rootCmd.AddCommand(priorityCmd)
}
