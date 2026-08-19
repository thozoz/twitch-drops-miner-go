package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"tdm/internal/config"
	"tdm/internal/logging"
)

const (
	ExitOK           = 0
	ExitError        = 1
	ExitAuthRequired = 2
)

// CommandError wraps an error with an associated exit code.
type CommandError struct {
	Code int
	Err  error
}

func (e *CommandError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("command failed with exit code %d", e.Code)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

var (
	configFile string
	logLevel   string
	logFormat  string
	logFile    string
)

var rootCmd = &cobra.Command{
	Use:           "tdm",
	Short:         "tdm is a headless-first Twitch Drops Miner",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(configFile)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %w", err)
		}

		if cmd.Flags().Changed("log-level") {
			cfg.LogLevel = logLevel
		}
		if cmd.Flags().Changed("log-format") {
			cfg.LogFormat = logFormat
		}
		if cmd.Flags().Changed("log-file") {
			cfg.LogFile = logFile
		}

		logger := logging.New(cfg)

		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		ctx = config.WithConfig(ctx, cfg)
		ctx = logging.WithLogger(ctx, logger)
		cmd.SetContext(ctx)

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "Path to configuration file")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "Log format (text, json)")
	rootCmd.PersistentFlags().StringVar(&logFile, "log-file", "", "Path to log file (empty = stderr)")
}

func newHTTPClient() *resty.Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	client := resty.NewWithClient(&http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	})
	return client
}

func resetCommandFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, c := range cmd.Commands() {
		resetCommandFlags(c)
	}
}

func resetCommandContexts(cmd *cobra.Command, ctx context.Context) {
	cmd.SetContext(ctx)
	for _, c := range cmd.Commands() {
		resetCommandContexts(c, ctx)
	}
}

// ExecuteContext runs the root command with the given context and returns an exit code.
func ExecuteContext(ctx context.Context) int {
	resetCommandFlags(rootCmd)
	resetCommandContexts(rootCmd, ctx)
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		var cmdErr *CommandError
		if errors.As(err, &cmdErr) {
			return cmdErr.Code
		}
		return ExitError
	}
	return ExitOK
}

// Execute runs the root command and returns an exit code.
func Execute() int {
	return ExecuteContext(context.Background())
}

