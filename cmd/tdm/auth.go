package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

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
	Short: "Authenticate with Twitch using OAuth Device Code Flow",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := logging.FromContext(ctx)

		cmd.Println("OAuth Device Code Flow is currently disabled by Twitch (invalid client).")
		cmd.Println("Please authenticate using your browser's 'auth-token' cookie instead:")
		cmd.Println("  tdm auth set-token <token>")
		cmd.Println("See README.md for instructions on finding your auth-token cookie.")
		logger.Warn("device login is currently disabled by Twitch; use 'tdm auth set-token'")
		return &CommandError{
			Code: ExitAuthRequired,
			Err:  errors.New("device login temporarily disabled by Twitch; use 'tdm auth set-token'"),
		}
	},
}

var authSetTokenCmd = &cobra.Command{
	Use:   "set-token [token]",
	Short: "Set Twitch authentication token directly (e.g. from browser cookie)",
	Long: `Set Twitch authentication credentials directly using an OAuth access token,
such as the 'auth-token' cookie from your browser session on twitch.tv.

If no token argument is provided, you will be prompted to enter it.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := logging.FromContext(ctx)

		var token string
		if len(args) > 0 {
			token = args[0]
		} else {
			fmt.Print("Enter Twitch auth-token: ")
			reader := bufio.NewReader(os.Stdin)
			input, err := reader.ReadString('\n')
			if err != nil {
				return &CommandError{Code: ExitError, Err: fmt.Errorf("failed to read token from stdin: %w", err)}
			}
			token = input
		}

		token = strings.TrimSpace(token)
		if token == "" {
			return &CommandError{Code: ExitError, Err: errors.New("token cannot be empty")}
		}

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

		if err := session.SetToken(ctx, token); err != nil {
			logger.Error("failed to set token", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		data := session.Data()
		fmt.Printf("Successfully authenticated as %s (user id %d)\n", data.Login, data.UserID)
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
	authCmd.AddCommand(authSetTokenCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}
