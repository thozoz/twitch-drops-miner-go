package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/thozoz/twitch-drops-miner-go/internal/auth"
	"github.com/thozoz/twitch-drops-miner-go/internal/channel"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/logging"
	"github.com/thozoz/twitch-drops-miner-go/internal/pubsub"
	"github.com/thozoz/twitch-drops-miner-go/internal/session"
)

var mineNoPubSub bool

var mineCmd = &cobra.Command{
	Use:   "mine",
	Short: "Select an active campaign, watch a live channel, track progress, and claim drops to completion",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := logging.FromContext(ctx)

		authPath, err := config.AuthFilePath()
		if err != nil {
			logger.Error("failed to resolve auth file path", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		httpClient := newHTTPClient()
		authSession, err := auth.LoadOrEmpty(authPath, httpClient)
		if err != nil {
			logger.Error("failed to load session", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		if !authSession.Authenticated() {
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

		client := gql.NewClient(registry, authSession, authSession, httpClient)
		fetcher := inventory.NewFetcher(client)

		campaigns, err := fetcher.FetchInventory(ctx, authSession.Data().UserID)
		if err != nil {
			if errors.Is(err, auth.ErrReauthRequired) {
				logger.Error("credentials invalid or expired: run 'tdm auth login'")
				return &CommandError{Code: ExitAuthRequired, Err: err}
			}
			logger.Error("failed to fetch inventory", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		// Startup sweep of unclaimed drops from prior runs (CLAIM-02)
		now := time.Now()
		sweptCount, sweepErrs := inventory.SweepUnclaimed(ctx, client, authSession.Data().UserID, campaigns, now, logger)
		if len(sweepErrs) > 0 {
			logger.Warn("encountered errors during startup sweep of unclaimed drops", "errors_count", len(sweepErrs))
		}
		if sweptCount > 0 {
			logger.Info("swept and claimed unclaimed drops from prior runs", "count", sweptCount)
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
			fmt.Println("no eligible campaign to mine")
			return nil
		}

		resolved, err := inventory.ResolveChannel(ctx, client, *selected)
		if err != nil {
			logger.Error("failed to resolve channel for selected campaign", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		if resolved == nil {
			fmt.Printf("no live channel found for %s\n", selected.Game.Name)
			return nil
		}

		aclTag := ""
		if resolved.ACLBased {
			aclTag = " [ACL]"
		}
		fmt.Printf("Selected campaign: %s (%s)\n", selected.Name, selected.Game.Name)
		fmt.Printf("Watching channel: %s (%d viewers)%s\n", resolved.Name(), resolved.Viewers, aclTag)

		channelWatcher := channel.NewWatcher(client, httpClient, authSession, authSession.Data().UserID)

		var pubsubClient *pubsub.Client
		if !mineNoPubSub {
			pubsubClient = pubsub.NewClient(authSession)
		}

		var sessionOpts []session.SessionOption
		statePath, err := config.StateFilePath()
		if err != nil {
			logger.Warn("failed to resolve state file path", "error", err)
		} else {
			sessionOpts = append(sessionOpts, session.WithStatePath(statePath))
		}

		watchSession := session.NewWatchSession(client, channelWatcher, pubsubClient, authSession.Data().UserID, logger, sessionOpts...)

		sigCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer cancel()

		runErr := watchSession.Run(sigCtx, *selected, *resolved)
		if runErr != nil {
			if errors.Is(runErr, session.ErrChannelOffline) {
				fmt.Println("channel went offline, exiting")
				return nil
			}
			if errors.Is(runErr, session.ErrNoEarnableDrop) {
				fmt.Println("no earnable drop on the selected campaign/channel, exiting")
				return nil
			}
			if errors.Is(runErr, context.Canceled) {
				return nil
			}
			logger.Error("mining session failed", "error", runErr)
			return &CommandError{Code: ExitError, Err: runErr}
		}

		fmt.Println("campaign complete, all drops claimed")
		return nil
	},
}

func init() {
	mineCmd.Flags().BoolVar(&mineNoPubSub, "no-pubsub", false, "Disable PubSub WebSocket and rely on GQL reconciliation alone")
	rootCmd.AddCommand(mineCmd)
}
