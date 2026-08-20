package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/thozoz/twitch-drops-miner-go/internal/auth"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/logging"
)

var (
	inventoryListJSON     bool
	inventoryListInterval time.Duration
)

var inventoryCmd = &cobra.Command{
	Use:   "inventory",
	Short: "Inspect the operator's drop campaign inventory",
}

var inventoryListCmd = &cobra.Command{
	Use:   "list",
	Short: "List eligible and in-progress drop campaigns",
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

		runOnce := func(runCtx context.Context) error {
			campaigns, err := fetcher.FetchInventory(runCtx, session.Data().UserID)
			if err != nil {
				if errors.Is(err, auth.ErrReauthRequired) {
					logger.Error("credentials invalid or expired: run 'tdm auth login'")
					return &CommandError{Code: ExitAuthRequired, Err: err}
				}
				logger.Error("failed to fetch inventory", "error", err)
				return &CommandError{Code: ExitError, Err: err}
			}

			cfg := config.FromContext(runCtx)
			enableBadgesEmotes := false
			if cfg != nil {
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

			if inventoryListJSON {
				output := struct {
					Eligible []inventory.DropsCampaign  `json:"eligible"`
					Skipped  []inventory.SkippedCampaign `json:"skipped"`
				}{Eligible: eligible, Skipped: skipped}
				data, err := json.MarshalIndent(output, "", "  ")
				if err != nil {
					logger.Error("failed to marshal inventory to JSON", "error", err)
					return &CommandError{Code: ExitError, Err: err}
				}
				fmt.Println(string(data))
				return nil
			}

			if len(eligible) == 0 && len(skipped) == 0 {
				fmt.Println("No drop campaigns available.")
				return nil
			}

			for _, c := range eligible {
				fmt.Printf("%s - %s\n", c.Game.Name, c.Name)
				for _, d := range c.Drops {
					status := ""
					if d.IsClaimed {
						status = " [claimed]"
					}
					fmt.Printf("  • %s: %d/%d min%s\n", d.Name, d.CurrentMinutes, d.RequiredMinutes, status)
				}
			}

			for _, sk := range skipped {
				fmt.Printf("%s - %s [skipped: %s]\n", sk.Game.Name, sk.Name, sk.Reason.Detail())
			}

			return nil
		}

		if inventoryListInterval > 0 {
			sigCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer cancel()

			if err := runOnce(sigCtx); err != nil {
				return err
			}

			ticker := time.NewTicker(inventoryListInterval)
			defer ticker.Stop()

			for {
				select {
				case <-sigCtx.Done():
					return nil
				case <-ticker.C:
					for len(ticker.C) > 0 {
						<-ticker.C
					}
					if err := runOnce(sigCtx); err != nil {
						return err
					}
				}
			}
		}

		return runOnce(ctx)
	},
}

func init() {
	inventoryListCmd.Flags().BoolVar(&inventoryListJSON, "json", false, "Output results in JSON format")
	inventoryListCmd.Flags().DurationVar(&inventoryListInterval, "interval", 0, "Polling interval to refresh inventory continuously")

	inventoryCmd.AddCommand(inventoryListCmd)
	rootCmd.AddCommand(inventoryCmd)
}
