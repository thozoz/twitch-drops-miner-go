package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/thozoz/twitch-drops-miner-go/internal/auth"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/logging"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

var inventorySelectJSON bool

var inventorySelectCmd = &cobra.Command{
	Use:   "select",
	Short: "Select the highest-priority active drop campaign and resolve a live channel",
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

		cfg := config.FromContext(ctx)
		var priority, exclude []string
		enableBadgesEmotes := false
		if cfg != nil {
			priority = cfg.Priority
			exclude = cfg.Exclude
			enableBadgesEmotes = cfg.EnableBadgesEmotes
		}

		eligible, skipped := inventory.SplitEligible(campaigns, enableBadgesEmotes)
		for _, sk := range skipped {
			logger.Debug("campaign skipped",
				"game", sk.Game.Name,
				"campaign", sk.Name,
				"reason", string(sk.Reason),
				"detail", sk.Reason.Detail(),
				"link_url", sk.LinkURL,
			)
		}

		selected := inventory.SelectCampaign(eligible, priority, exclude, time.Now(), enableBadgesEmotes)
		if selected == nil {
			if inventorySelectJSON {
				fmt.Println("{}")
			} else {
				fmt.Println("no eligible campaign to mine")
			}
			return nil
		}

		resolved, err := inventory.ResolveChannel(ctx, client, *selected)
		if err != nil {
			logger.Error("failed to resolve channel for selected campaign", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		if inventorySelectJSON {
			output := struct {
				SelectedCampaign *inventory.DropsCampaign `json:"selected_campaign"`
				ResolvedChannel  *model.Channel          `json:"resolved_channel"`
			}{
				SelectedCampaign: selected,
				ResolvedChannel:  resolved,
			}
			data, err := json.MarshalIndent(output, "", "  ")
			if err != nil {
				logger.Error("failed to marshal select output to JSON", "error", err)
				return &CommandError{Code: ExitError, Err: err}
			}
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Selected campaign: %s (%s)\n", selected.Name, selected.Game.Name)
		if resolved == nil {
			fmt.Printf("No live channel found for %s — will resolve once one goes live\n", selected.Game.Name)
		} else {
			aclTag := ""
			if resolved.ACLBased {
				aclTag = " [ACL]"
			}
			fmt.Printf("Watching channel: %s (%d viewers)%s\n", resolved.Name(), resolved.Viewers, aclTag)
		}

		return nil
	},
}

func init() {
	inventorySelectCmd.Flags().BoolVar(&inventorySelectJSON, "json", false, "Output selection in JSON format")
	inventoryCmd.AddCommand(inventorySelectCmd)
}
