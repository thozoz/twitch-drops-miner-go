package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/thozoz/twitch-drops-miner-go/internal/auth"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/logging"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage Twitch authentication credentials",
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with Twitch in a Chromium window",
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

		loginCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if err := session.LoginWithBrowser(loginCtx, func() {
			cmd.Println("Chromium opened. Sign in to Twitch in that window; TDM will continue automatically.")
		}); err != nil {
			logger.Error("login failed", "error", err)
			return &CommandError{Code: ExitAuthRequired, Err: err}
		}

		data := session.Data()
		cmd.Printf("Logged in as %s (user id %d)\n", data.Login, data.UserID)
		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check the current authentication status",
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

		userID, login, _, err := auth.Validate(ctx, httpClient, session.AccessToken())
		if err != nil {
			if errors.Is(err, auth.ErrTokenInvalid) {
				logger.Error("credentials invalid or expired: run 'tdm auth login'")
				return &CommandError{Code: ExitAuthRequired, Err: auth.ErrReauthRequired}
			}
			logger.Error("failed to validate token", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		fmt.Printf("Authenticated as %s (user id %d)\n", login, userID)
		return nil
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out and remove stored credentials",
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

		if err := session.Logout(); err != nil {
			logger.Error("logout failed", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		fmt.Println("Logged out successfully")
		return nil
	},
}

func init() {
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}
