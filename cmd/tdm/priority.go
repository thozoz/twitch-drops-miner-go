package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
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

var priorityRemoveCmd = &cobra.Command{
	Use:   "remove <game...>",
	Short: "Remove games from priority list",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return executePriorityCall(cmd, ipc.PriorityParams{
			Action: ipc.PriorityRemove,
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
		return executePriorityOffline(cmd, params)
	}

	conn, err := ipc.Dial(ctx, addr, 3*time.Second, nil)
	if err != nil {
		// No daemon listening. Rather than refusing outright, fall back to the
		// config file so the list can be inspected or staged before first launch
		// without hand-editing JSON.
		return executePriorityOffline(cmd, params)
	}
	defer conn.Close()

	var result ipc.PriorityResult
	if err := ipc.Call(ctx, conn, ipc.MethodPriority, params, &result); err != nil {
		return &CommandError{Code: ExitError, Err: err}
	}

	fmt.Fprintln(cmd.OutOrStdout(), formatPriority(result.Priority))
	return nil
}

// executePriorityOffline services a priority command straight from config.json
// when no daemon is reachable. Mutations take effect the next time the daemon
// starts, which the caller is told explicitly so the lack of a running daemon is
// never mistaken for the change having been applied live.
func executePriorityOffline(cmd *cobra.Command, params ipc.PriorityParams) error {
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

	updated := append([]string(nil), cfg.Priority...)

	switch params.Action {
	case ipc.PriorityList:
		fmt.Fprintln(cmd.OutOrStdout(), formatPriority(updated))
		return nil

	case ipc.PriorityAdd:
		updated = addGames(updated, params.Games)

	case ipc.PriorityRemove:
		updated = removeGamesFromList(updated, params.Games)

	case ipc.PrioritySet:
		updated = append([]string(nil), params.Games...)
	}

	if err := config.SavePriority(path, updated); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "tdm is not running, and the config file could not be written")
		return &CommandError{Code: ExitError, Err: err}
	}

	fmt.Fprintln(cmd.OutOrStdout(), formatPriority(updated))
	fmt.Fprintf(cmd.ErrOrStderr(), "tdm is not running; saved to %s, takes effect on next start\n", path)
	return nil
}

// formatGameList renders a game list for terminal output, one entry per line.
// Shared by the priority and exclude commands so both print identically.
func formatGameList(games []string) string {
	if len(games) == 0 {
		return "(none)"
	}
	return strings.Join(games, "\n")
}

// formatPriority is retained as the priority command's spelling of
// formatGameList; both lists render the same way.
func formatPriority(games []string) string {
	return formatGameList(games)
}

// addGames appends every entry of add that is not already in list, preserving
// insertion order and skipping duplicates. Matching is case-sensitive, the same
// comparison inventory.SelectCampaign performs on Game.Name.
func addGames(list, add []string) []string {
	out := append([]string(nil), list...)
	for _, g := range add {
		found := false
		for _, existing := range out {
			if existing == g {
				found = true
				break
			}
		}
		if !found {
			out = append(out, g)
		}
	}
	return out
}

// removeGamesFromList returns list with every entry in drop removed, preserving
// the order of what remains. Removing an absent game is a no-op, not an error.
// This is the offline-path twin of the daemon's removeGames.
func removeGamesFromList(list, drop []string) []string {
	if len(drop) == 0 {
		return append([]string(nil), list...)
	}

	dropSet := make(map[string]struct{}, len(drop))
	for _, g := range drop {
		dropSet[g] = struct{}{}
	}

	out := make([]string, 0, len(list))
	for _, g := range list {
		if _, found := dropSet[g]; found {
			continue
		}
		out = append(out, g)
	}
	return out
}

func init() {
	priorityCmd.AddCommand(priorityListCmd)
	priorityCmd.AddCommand(priorityAddCmd)
	priorityCmd.AddCommand(priorityRemoveCmd)
	priorityCmd.AddCommand(prioritySetCmd)
	rootCmd.AddCommand(priorityCmd)
}
