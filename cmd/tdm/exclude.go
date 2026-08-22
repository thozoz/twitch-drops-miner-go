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

var excludeCmd = &cobra.Command{
	Use:   "exclude",
	Short: "Manage excluded (blacklisted) games",
	Long: "Manage the list of games tdm will never mine.\n\n" +
		"Matching is case-sensitive on the game's exact Twitch category name, the same\n" +
		"comparison the campaign selector performs — \"ROBLOX\" and \"Roblox\" are not the same entry.",
}

var excludeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List excluded games",
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeExcludeCall(cmd, ipc.ExcludeParams{
			Action: ipc.ExcludeList,
		})
	},
}

var excludeAddCmd = &cobra.Command{
	Use:   "add <game...>",
	Short: "Add games to the exclude list",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeExcludeCall(cmd, ipc.ExcludeParams{
			Action: ipc.ExcludeAdd,
			Games:  args,
		})
	},
}

var excludeRemoveCmd = &cobra.Command{
	Use:   "remove <game...>",
	Short: "Remove games from the exclude list",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeExcludeCall(cmd, ipc.ExcludeParams{
			Action: ipc.ExcludeRemove,
			Games:  args,
		})
	},
}

var excludeSetCmd = &cobra.Command{
	Use:   "set <game...>",
	Short: "Replace the exclude list",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeExcludeCall(cmd, ipc.ExcludeParams{
			Action: ipc.ExcludeSet,
			Games:  args,
		})
	},
}

func executeExcludeCall(cmd *cobra.Command, params ipc.ExcludeParams) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	addr, err := config.SocketPath()
	if err != nil {
		return executeExcludeOffline(cmd, params)
	}

	conn, err := ipc.Dial(ctx, addr, 3*time.Second, nil)
	if err != nil {
		// No daemon listening. Same reasoning as the priority command: fall back
		// to the config file so the list can be inspected or staged before first
		// launch without hand-editing JSON.
		return executeExcludeOffline(cmd, params)
	}
	defer conn.Close()

	var result ipc.ExcludeResult
	if err := ipc.Call(ctx, conn, ipc.MethodExclude, params, &result); err != nil {
		var jErr *jsonrpc2.Error
		if errors.As(err, &jErr) && jErr.Code == jsonrpc2.CodeMethodNotFound {
			// Running daemon predates daemon.Exclude method. Fall back to offline config file.
			return executeExcludeOffline(cmd, params)
		}
		return &CommandError{Code: ExitError, Err: err}
	}

	fmt.Fprintln(cmd.OutOrStdout(), formatGameList(result.Exclude))
	return nil
}

// executeExcludeOffline services an exclude command straight from config.json
// when no daemon is reachable. Mutations take effect the next time the daemon
// starts, which the caller is told explicitly so the lack of a running daemon is
// never mistaken for the change having been applied live.
func executeExcludeOffline(cmd *cobra.Command, params ipc.ExcludeParams) error {
	path, err := config.ResolveConfigPath(configFile)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "tdm is not running, and the config file path could not be resolved")
		return &CommandError{Code: ExitError, Err: err}
	}

	cfg, err := config.Load(configFile)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "tdm is not running, and the config file could not be read")
		return &CommandError{Code: ExitError, Err: err}
	}

	updated := append([]string(nil), cfg.Exclude...)

	switch params.Action {
	case ipc.ExcludeList:
		fmt.Fprintln(cmd.OutOrStdout(), formatGameList(updated))
		return nil

	case ipc.ExcludeAdd:
		updated = addGames(updated, params.Games)

	case ipc.ExcludeRemove:
		updated = removeGamesFromList(updated, params.Games)

	case ipc.ExcludeSet:
		updated = append([]string(nil), params.Games...)
	}

	if err := config.SaveExclude(path, updated); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "tdm is not running, and the config file could not be written")
		return &CommandError{Code: ExitError, Err: err}
	}

	fmt.Fprintln(cmd.OutOrStdout(), formatGameList(updated))
	fmt.Fprintf(cmd.ErrOrStderr(), "tdm is not running; saved to %s, takes effect on next start\n", path)
	return nil
}

func init() {
	excludeCmd.AddCommand(excludeListCmd)
	excludeCmd.AddCommand(excludeAddCmd)
	excludeCmd.AddCommand(excludeRemoveCmd)
	excludeCmd.AddCommand(excludeSetCmd)
	rootCmd.AddCommand(excludeCmd)
}
