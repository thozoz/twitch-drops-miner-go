package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/spf13/cobra"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
)

var dropExcludeCmd = &cobra.Command{
	Use:   "drop-exclude",
	Short: "Manage excluded drop-name/benefit keywords",
	Long: "Manage the list of keywords tdm will never earn a drop for.\n\n" +
		"Matching is case-insensitive substring matching against a drop's name and\n" +
		"its benefit names. A matching, unclaimed drop is treated as permanently\n" +
		"unearnable, and any drop that requires it as a precondition is pruned along\n" +
		"with it, unless the excluded drop was already claimed before the keyword\n" +
		"was added.",
}

var dropExcludeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List excluded drop-name/benefit keywords",
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeDropExcludeCall(cmd, ipc.DropExcludeParams{
			Action: ipc.DropExcludeList,
		})
	},
}

var dropExcludeAddCmd = &cobra.Command{
	Use:   "add <keyword...>",
	Short: "Add keywords to the drop exclude list",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeDropExcludeCall(cmd, ipc.DropExcludeParams{
			Action:   ipc.DropExcludeAdd,
			Keywords: args,
		})
	},
}

var dropExcludeRemoveCmd = &cobra.Command{
	Use:   "remove <keyword...>",
	Short: "Remove keywords from the drop exclude list",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeDropExcludeCall(cmd, ipc.DropExcludeParams{
			Action:   ipc.DropExcludeRemove,
			Keywords: args,
		})
	},
}

var dropExcludeSetCmd = &cobra.Command{
	Use:   "set <keyword...>",
	Short: "Replace the drop exclude list",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeDropExcludeCall(cmd, ipc.DropExcludeParams{
			Action:   ipc.DropExcludeSet,
			Keywords: args,
		})
	},
}

func executeDropExcludeCall(cmd *cobra.Command, params ipc.DropExcludeParams) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	addr, err := config.SocketPath()
	if err != nil {
		return executeDropExcludeOffline(cmd, params)
	}

	conn, err := ipc.Dial(ctx, addr, 3*time.Second, nil)
	if err != nil {
		// No daemon listening. Same reasoning as the exclude/priority commands:
		// fall back to the config file so the list can be inspected or staged
		// before first launch without hand-editing JSON.
		return executeDropExcludeOffline(cmd, params)
	}
	defer conn.Close()

	var result ipc.DropExcludeResult
	if err := ipc.Call(ctx, conn, ipc.MethodDropExclude, params, &result); err != nil {
		var jErr *jsonrpc2.Error
		if errors.As(err, &jErr) && jErr.Code == jsonrpc2.CodeMethodNotFound {
			// Running daemon predates daemon.DropExclude. Fall back to offline
			// config file with a note explaining the daemon version mismatch.
			return executeDropExcludeOfflineWithReason(cmd, params, "running daemon does not support live drop-exclude updates")
		}
		return &CommandError{Code: ExitError, Err: err}
	}

	fmt.Fprintln(cmd.OutOrStdout(), formatGameList(result.DropExclude))
	return nil
}

// executeDropExcludeOffline services a drop-exclude command straight from
// config.json when no daemon is reachable. Mutations take effect the next
// time the daemon starts, which the caller is told explicitly so the lack of
// a running daemon is never mistaken for the change having been applied live.
func executeDropExcludeOffline(cmd *cobra.Command, params ipc.DropExcludeParams) error {
	return executeDropExcludeOfflineWithReason(cmd, params, "tdm is not running")
}

func executeDropExcludeOfflineWithReason(cmd *cobra.Command, params ipc.DropExcludeParams, reason string) error {
	path, err := config.ResolveConfigPath(configFile)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s, and the config file path could not be resolved\n", reason)
		return &CommandError{Code: ExitError, Err: err}
	}

	cfg, err := config.Load(configFile)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s, and the config file could not be read\n", reason)
		return &CommandError{Code: ExitError, Err: err}
	}

	updated := append([]string(nil), cfg.DropExclude...)

	switch params.Action {
	case ipc.DropExcludeList:
		fmt.Fprintln(cmd.OutOrStdout(), formatGameList(updated))
		if reason != "tdm is not running" {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s; reading from %s\n", reason, path)
		}
		return nil

	case ipc.DropExcludeAdd:
		updated = addGames(updated, params.Keywords)

	case ipc.DropExcludeRemove:
		updated = removeGamesFromList(updated, params.Keywords)

	case ipc.DropExcludeSet:
		updated = append([]string(nil), params.Keywords...)
	}

	if err := config.SaveDropExclude(path, updated); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s, and the config file could not be written\n", reason)
		return &CommandError{Code: ExitError, Err: err}
	}

	fmt.Fprintln(cmd.OutOrStdout(), formatGameList(updated))
	fmt.Fprintf(cmd.ErrOrStderr(), "%s; saved to %s, takes effect on next start\n", reason, path)
	return nil
}

func init() {
	dropExcludeCmd.AddCommand(dropExcludeListCmd)
	dropExcludeCmd.AddCommand(dropExcludeAddCmd)
	dropExcludeCmd.AddCommand(dropExcludeRemoveCmd)
	dropExcludeCmd.AddCommand(dropExcludeSetCmd)
	rootCmd.AddCommand(dropExcludeCmd)
}
