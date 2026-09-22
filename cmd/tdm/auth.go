package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"

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

		onCode := func(verificationURI, userCode string) {
			fmt.Printf("Go to %s and enter code: %s\n", verificationURI, userCode)
		}

		if err := session.Login(ctx, onCode); err != nil {
			logger.Error("login failed", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		data := session.Data()
		fmt.Printf("Logged in as %s (user id %d)\n", data.Login, data.UserID)
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

var authSetTokenCmd = &cobra.Command{
	Use:   "set-token [legacy-android-token]",
	Short: "Import an existing Android-issued Twitch token",
	Long: `Import an existing Android-issued Twitch OAuth token.

This command cannot create a new token. Twitch Web/browser auth-token cookies
are rejected because they do not pass headless GraphQL integrity checks. Omit
the argument to read the token from standard input.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var token string
		if len(args) == 1 {
			token = args[0]
		} else {
			cmd.Print("Enter existing Android Twitch token: ")
			input, err := readTokenLine(cmd.InOrStdin())
			if err != nil {
				return &CommandError{Code: ExitError, Err: fmt.Errorf("read token from stdin: %w", err)}
			}
			token = input
		}

		authPath, err := config.AuthFilePath()
		if err != nil {
			return &CommandError{Code: ExitError, Err: err}
		}
		session, err := auth.LoadOrEmpty(authPath, newHTTPClient())
		if err != nil {
			return &CommandError{Code: ExitError, Err: err}
		}
		if err := session.SetToken(cmd.Context(), token); err != nil {
			return &CommandError{Code: ExitError, Err: err}
		}

		data := session.Data()
		cmd.Printf("Imported token for %s (user id %d)\n", data.Login, data.UserID)
		return nil
	},
}

func readTokenLine(r io.Reader) (string, error) {
	input, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return input, nil
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
