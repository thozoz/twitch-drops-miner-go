package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"tdm/internal/auth"
	"tdm/internal/config"
	"tdm/internal/gql"
	"tdm/internal/inventory"
	"tdm/internal/logging"
	"tdm/internal/model"
)

var (
	watchDecisionCurrent      string
	watchDecisionOfflineSince string
	watchDecisionJSON         bool
)

var inventoryWatchDecisionCmd = &cobra.Command{
	Use:   "watch-decision",
	Short: "Evaluate channel switch decision against candidate channels",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := logging.FromContext(ctx)

		authPath, err := config.AuthFilePath()
		if err != nil {
			logger.Error("failed to resolve auth file path", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		httpClient := newHTTPClient()
		session, err := auth.LoadOrEmpty(authPath, httpClient)
		if err != nil {
			logger.Error("failed to load session", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		if !session.Authenticated() {
			fmt.Println("not authenticated, run 'tdm auth login'")
			return &CommandError{Code: ExitError, Err: errors.New("not authenticated")}
		}

		overridePath, _ := config.OperationsOverridePath()
		registry, replaced, err := gql.LoadRegistry(overridePath)
		if err != nil {
			logger.Error("failed to load GQL operations registry", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}
		if len(replaced) > 0 {
			logger.Warn("overriding GQL operations from config", "replaced", replaced)
		}

		client := gql.NewClient(registry, session, session, httpClient)
		fetcher := inventory.NewFetcher(client)

		campaigns, err := fetcher.FetchInventory(ctx, session.Data().UserID)
		if err != nil {
			if errors.Is(err, auth.ErrReauthRequired) {
				logger.Error("credentials invalid or expired: run 'tdm auth login'")
				return &CommandError{Code: ExitAuthRequired, Err: err}
			}
			logger.Error("failed to fetch inventory", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		eligible, unlinked := inventory.SplitEligible(campaigns)
		for _, u := range unlinked {
			logger.Warn("campaign unlinked, skipped: run the link URL to enable it",
				"game", u.Game.Name,
				"link_url", u.LinkURL,
			)
		}

		cfg := config.FromContext(ctx)
		var priority, exclude []string
		if cfg != nil {
			priority = cfg.Priority
			exclude = cfg.Exclude
		}

		selected := inventory.SelectCampaign(eligible, priority, exclude, time.Now())
		if selected == nil {
			if watchDecisionJSON {
				fmt.Println("{}")
			} else {
				fmt.Println("no eligible campaign to mine")
			}
			return nil
		}

		candidates, err := inventory.ResolveCandidates(ctx, client, *selected)
		if err != nil {
			logger.Error("failed to resolve candidates for selected campaign", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		wantedGames := []model.Game{selected.Game}

		var current *model.Channel
		if watchDecisionCurrent != "" {
			for _, cand := range candidates {
				if cand.Login == watchDecisionCurrent || cand.ID == watchDecisionCurrent {
					cCopy := cand
					current = &cCopy
					break
				}
			}
			if current == nil {
				current = &model.Channel{
					Login:       watchDecisionCurrent,
					DisplayName: watchDecisionCurrent,
					Online:      false,
				}
			}
		}

		var currentOfflineSince *time.Time
		if watchDecisionOfflineSince != "" {
			dur, err := time.ParseDuration(watchDecisionOfflineSince)
			if err != nil {
				logger.Error("invalid --offline-since duration", "error", err)
				return &CommandError{Code: ExitError, Err: fmt.Errorf("invalid duration for --offline-since: %w", err)}
			}
			t := time.Now().Add(-dur)
			currentOfflineSince = &t
			if current != nil {
				current.Online = false
			}
		}

		grace := inventory.NewOfflineGrace()
		decision := inventory.Decide(current, currentOfflineSince, candidates, wantedGames, grace, time.Now())

		if watchDecisionJSON {
			data, err := json.MarshalIndent(decision, "", "  ")
			if err != nil {
				logger.Error("failed to marshal watch decision to JSON", "error", err)
				return &CommandError{Code: ExitError, Err: err}
			}
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Decision: Switch=%t (Reason: %s)\n", decision.Switch, decision.Reason)
		if decision.Target != nil {
			fmt.Printf("Target channel: %s (%d viewers)\n", decision.Target.Name(), decision.Target.Viewers)
		}

		return nil
	},
}

func init() {
	inventoryWatchDecisionCmd.Flags().StringVar(&watchDecisionCurrent, "current", "", "Channel login currently being watched")
	inventoryWatchDecisionCmd.Flags().StringVar(&watchDecisionOfflineSince, "offline-since", "", "Duration since current channel went offline (e.g. 30s, 3m)")
	inventoryWatchDecisionCmd.Flags().BoolVar(&watchDecisionJSON, "json", false, "Output decision in JSON format")

	inventoryCmd.AddCommand(inventoryWatchDecisionCmd)
}
