package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/thozoz/twitch-drops-miner-go/internal/auth"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/logging"
	"github.com/thozoz/twitch-drops-miner-go/internal/pubsub"
)

var pubsubChannelID string

var pubsubCmd = &cobra.Command{
	Use:   "pubsub",
	Short: "Diagnostics and operations for Twitch PubSub WebSocket",
}

var pubsubListenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Listen to live Twitch PubSub edge events",
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

		client := pubsub.NewClient(session)
		userID := session.Data().UserID
		client.AddTopics(pubsub.UserDropsTopic(userID), pubsub.UserNotificationsTopic(userID))

		if pubsubChannelID != "" {
			client.AddTopics(
				pubsub.ChannelStreamStateTopic(pubsubChannelID),
				pubsub.ChannelStreamUpdateTopic(pubsubChannelID),
			)
		}

		sigCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer cancel()

		go func() {
			if err := client.Run(sigCtx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("pubsub client error", "error", err)
			}
		}()

		fmt.Printf("Connected to PubSub. Subscribed to %d topics. Listening for events (Ctrl+C to quit)...\n", len(client.Topics()))

		for {
			select {
			case <-sigCtx.Done():
				return nil
			case ev, ok := <-client.Events():
				if !ok {
					return nil
				}
				fmt.Printf("%s [%s] %s\n", ev.Topic, ev.Type, string(ev.Payload))
			}
		}
	},
}

func init() {
	pubsubListenCmd.Flags().StringVar(&pubsubChannelID, "channel-id", "", "Optional channel ID to subscribe to stream state and update topics")
	pubsubCmd.AddCommand(pubsubListenCmd)
	rootCmd.AddCommand(pubsubCmd)
}
