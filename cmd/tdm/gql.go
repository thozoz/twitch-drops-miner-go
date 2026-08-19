package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"tdm/internal/auth"
	"tdm/internal/config"
	"tdm/internal/gql"
	"tdm/internal/logging"
)

var gqlVarFlags []string

var gqlCmd = &cobra.Command{
	Use:   "gql",
	Short: "Twitch GraphQL diagnostics and operations",
}

var gqlProbeCmd = &cobra.Command{
	Use:   "probe <operation>",
	Short: "Execute a diagnostic GraphQL persisted query",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		operationName := args[0]
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

		vars := make(map[string]any)
		for _, pair := range gqlVarFlags {
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 2 {
				vars[parts[0]] = parts[1]
			} else {
				vars[parts[0]] = ""
			}
		}

		client := gql.NewClient(registry, session, session, httpClient)
		resp, err := client.Do(ctx, operationName, vars)
		if err != nil {
			if errors.Is(err, auth.ErrReauthRequired) {
				logger.Error("credentials invalid or refresh failed: run 'tdm auth login'")
				return &CommandError{Code: ExitAuthRequired, Err: err}
			}
			logger.Error("GQL request failed", "operation", operationName, "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		var pretty bytes.Buffer
		if err := json.Indent(&pretty, resp, "", "  "); err != nil {
			fmt.Println(string(resp))
		} else {
			fmt.Println(pretty.String())
		}

		return nil
	},
}

func init() {
	gqlProbeCmd.Flags().StringSliceVar(&gqlVarFlags, "var", nil, "Query variable in key=value format (repeatable)")
	gqlCmd.AddCommand(gqlProbeCmd)
	rootCmd.AddCommand(gqlCmd)
}
