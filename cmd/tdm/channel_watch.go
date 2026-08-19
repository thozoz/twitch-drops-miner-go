package main

import (
	"encoding/json"
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
	"github.com/thozoz/twitch-drops-miner-go/internal/logging"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

var channelWatchMinutes int

var channelCmd = &cobra.Command{
	Use:   "channel",
	Short: "Inspect and interact with Twitch channels",
}

var channelWatchCmd = &cobra.Command{
	Use:   "watch <login>",
	Short: "Watch a live channel and send Spade minute-watched beacons",
	Args:  cobra.ExactArgs(1),
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

		login := args[0]
		raw, err := client.Do(ctx, "VideoPlayerStreamInfoOverlayChannel", map[string]any{
			"channel": login,
		})
		if err != nil {
			if errors.Is(err, auth.ErrReauthRequired) {
				logger.Error("credentials invalid or expired: run 'tdm auth login'")
				return &CommandError{Code: ExitAuthRequired, Err: err}
			}
			logger.Error("failed to fetch channel stream info", "login", login, "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		var resp struct {
			User *struct {
				ID          string `json:"id"`
				Login       string `json:"login"`
				DisplayName string `json:"displayName"`
				Stream      *struct {
					ID           string `json:"id"`
					ViewersCount int    `json:"viewersCount"`
				} `json:"stream"`
				BroadcastSettings *struct {
					Title string `json:"title"`
					Game  *struct {
						ID          string `json:"id"`
						DisplayName string `json:"displayName"`
						Slug        string `json:"slug"`
					} `json:"game"`
				} `json:"broadcastSettings"`
			} `json:"user"`
		}

		if err := json.Unmarshal(raw, &resp); err != nil {
			logger.Error("failed to unmarshal stream info response", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		if resp.User == nil || resp.User.Stream == nil {
			fmt.Printf("channel %s is offline or does not exist\n", login)
			return &CommandError{Code: ExitError, Err: fmt.Errorf("channel %s is offline or does not exist", login)}
		}

		var game *model.Game
		if resp.User.BroadcastSettings != nil && resp.User.BroadcastSettings.Game != nil {
			g := resp.User.BroadcastSettings.Game
			gEntity := model.NewGame(g.ID, g.DisplayName, g.Slug)
			game = &gEntity
		}

		ch := model.Channel{
			ID:           resp.User.ID,
			Login:        resp.User.Login,
			DisplayName:  resp.User.DisplayName,
			Online:       true,
			Game:         game,
			Viewers:      resp.User.Stream.ViewersCount,
			DropsEnabled: true,
			BroadcastID:  resp.User.Stream.ID,
		}

		gameName := "unknown"
		if ch.Game != nil {
			gameName = ch.Game.Name
		}
		fmt.Printf("Watching channel: %s (%s) — %d viewers\n", ch.Name(), gameName, ch.Viewers)

		watcher := channel.NewWatcher(client, httpClient, session, session.Data().UserID)
		watcher.OnBeacon = func(seq int, success bool, err error) {
			if success {
				fmt.Printf("sent beacon %d (204)\n", seq)
			} else {
				if err != nil {
					fmt.Printf("beacon %d failed: %v\n", seq, err)
				} else {
					fmt.Printf("beacon %d failed\n", seq)
				}
			}
		}

		if err := watcher.Start(ctx, ch); err != nil {
			logger.Error("failed to start watching channel", "login", login, "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}
		defer watcher.Stop()

		sigCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer cancel()

		if channelWatchMinutes > 0 {
			select {
			case <-sigCtx.Done():
			case <-time.After(time.Duration(channelWatchMinutes) * time.Minute):
				fmt.Printf("Watch session completed (%d minutes)\n", channelWatchMinutes)
			}
		} else {
			<-sigCtx.Done()
		}

		return nil
	},
}

func init() {
	channelWatchCmd.Flags().IntVar(&channelWatchMinutes, "minutes", 0, "Duration in minutes to watch the channel (0 for continuous)")
	channelCmd.AddCommand(channelWatchCmd)
	rootCmd.AddCommand(channelCmd)
}
