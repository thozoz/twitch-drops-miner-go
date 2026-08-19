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
	"golang.org/x/sync/errgroup"
	"github.com/thozoz/twitch-drops-miner-go/internal/auth"
	"github.com/thozoz/twitch-drops-miner-go/internal/channel"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/daemon"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
	"github.com/thozoz/twitch-drops-miner-go/internal/logging"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/pubsub"
	"github.com/thozoz/twitch-drops-miner-go/internal/session"
)

var daemonMode bool

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the mining supervisor continuously in the foreground",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := logging.FromContext(ctx)

		addr, err := config.SocketPath()
		if err != nil {
			logger.Error("failed to resolve socket path", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		ln, err := ipc.Bind(addr)
		if err != nil {
			if errors.Is(err, ipc.ErrAlreadyRunning) {
				fmt.Fprintln(os.Stderr, "tdm is already running")
				return &CommandError{Code: ExitError, Err: err}
			}
			logger.Error("failed to bind IPC listener", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}
		defer func() {
			_ = ipc.Unbind(ln, addr)
		}()

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

		gqlClient := gql.NewClient(registry, authSession, authSession, httpClient)
		userID := authSession.Data().UserID

		ring := daemon.NewRingBuffer(1000)
		cfg := config.FromContext(ctx)
		runLogger := logging.NewWithExtraWriter(cfg, ring)

		sigCtx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		runCtx, runCancel := context.WithCancel(sigCtx)
		defer runCancel()

		statePath, _ := config.StateFilePath()

		var supervisor *daemon.Supervisor

		runWatch := func(wCtx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
			watcher := channel.NewWatcher(gqlClient, httpClient, authSession, userID)
			pubsubClient := pubsub.NewClient(authSession)
			var sessionOpts []session.SessionOption
			if statePath != "" {
				sessionOpts = append(sessionOpts, session.WithStatePath(statePath))
			}
			sessionOpts = append(sessionOpts, session.WithOnProgress(func(d *inventory.TimedDrop) {
				if supervisor != nil {
					supervisor.UpdateDropProgress(d)
				}
			}))
			watchSession := session.NewWatchSession(gqlClient, watcher, pubsubClient, userID, runLogger, sessionOpts...)
			err := watchSession.Run(wCtx, campaign, ch)
			drop := watchSession.ActiveDrop()
			return drop, err
		}

		var priority, exclude []string
		if cfg != nil {
			priority = cfg.Priority
			exclude = cfg.Exclude
		}

		supervisor = daemon.NewProductionSupervisor(
			gqlClient,
			userID,
			runLogger,
			priority,
			exclude,
			daemon.WithWatchRunner(runWatch),
		)

		handler := daemon.NewHandler(supervisor, runCancel, ring)

		if !daemonMode {
			fmt.Println("tdm mining daemon running in foreground (Ctrl+C to stop)")
		}

		g, gctx := errgroup.WithContext(runCtx)
		g.Go(func() error {
			return ipc.Serve(gctx, ln, handler)
		})
		g.Go(func() error {
			return supervisor.Run(gctx)
		})

		waitDone := make(chan error, 1)
		go func() {
			waitDone <- g.Wait()
		}()

		select {
		case err := <-waitDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				runLogger.Error("daemon terminated with error", "error", err)
				return &CommandError{Code: ExitError, Err: err}
			}
			return nil
		case <-runCtx.Done():
			select {
			case err := <-waitDone:
				if err != nil && !errors.Is(err, context.Canceled) {
					runLogger.Error("daemon terminated with error", "error", err)
					return &CommandError{Code: ExitError, Err: err}
				}
				return nil
			case <-time.After(10 * time.Second):
				runLogger.Warn("graceful shutdown timed out, forcing exit")
				os.Exit(1)
				return nil
			}
		}
	},
}

func init() {
	runCmd.Flags().BoolVar(&daemonMode, "daemon-mode", false, "Run in background daemon mode (suppresses interactive banner)")
	rootCmd.AddCommand(runCmd)
}
