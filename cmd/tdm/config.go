package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
)

var forceInit bool

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage tdm configuration",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the effective merged configuration as JSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.FromContext(cmd.Context())
		if cfg == nil {
			var err error
			cfg, err = config.Load("")
			if err != nil {
				return err
			}
		}
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a default configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		targetPath, err := config.ConfigFilePath()
		if err != nil {
			return err
		}

		if _, err := os.Stat(targetPath); err == nil {
			if !forceInit {
				return fmt.Errorf("config file already exists at %s (use --force to overwrite)", targetPath)
			}
		}

		dir := filepath.Dir(targetPath)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}

		cfg := config.FromContext(cmd.Context())
		if cfg == nil {
			cfg = &config.Config{
				LogLevel:  "info",
				LogFormat: "text",
				LogFile:   "",
			}
		}

		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')

		if err := os.WriteFile(targetPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write config file: %w", err)
		}

		fmt.Printf("Wrote configuration to %s\n", targetPath)
		return nil
	},
}

// configSetting describes one key settable via `tdm config set`. Adding a new
// settable key means adding one entry here, not threading a new case through
// a switch statement.
type configSetting struct {
	description string
	// parse converts the raw CLI argument into the value written to
	// config.json. It must reject anything it cannot confidently interpret as
	// this setting's type rather than writing something that silently means
	// nothing.
	parse func(raw string) (any, error)
}

var configSettings = map[string]configSetting{
	"enable_badges_emotes": {
		description: "Include badge/emote-reward campaigns in mining candidates (default: false)",
		parse:       parseConfigBool,
	},
}

func parseConfigBool(raw string) (any, error) {
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("expected a boolean (true/false/1/0), got %q", raw)
	}
	return v, nil
}

func settableConfigKeys() []string {
	keys := make([]string, 0, len(configSettings))
	for k := range configSettings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a single configuration key and persist it to the config file",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key, raw := args[0], args[1]

		setting, ok := configSettings[key]
		if !ok {
			fmt.Fprintf(cmd.ErrOrStderr(), "unknown config key %q\n\nSettable keys:\n", key)
			for _, k := range settableConfigKeys() {
				fmt.Fprintf(cmd.ErrOrStderr(), "  %s - %s\n", k, configSettings[k].description)
			}
			return &CommandError{Code: ExitError, Err: fmt.Errorf("unknown config key %q", key)}
		}

		value, err := setting.parse(raw)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "invalid value for %s: %v\n", key, err)
			return &CommandError{Code: ExitError, Err: err}
		}

		path, err := config.ResolveConfigPath(configFile)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "failed to resolve config file path")
			return &CommandError{Code: ExitError, Err: err}
		}

		if err := config.SaveKey(path, key, value); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "failed to write %s: %v\n", path, err)
			return &CommandError{Code: ExitError, Err: err}
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%s = %v\n", key, value)
		fmt.Fprintf(cmd.OutOrStdout(), "Saved to %s. This setting is read at startup only -- restart tdm (tdm stop && tdm start) for it to take effect.\n", path)
		return nil
	},
}

func init() {
	configInitCmd.Flags().BoolVarP(&forceInit, "force", "f", false, "Overwrite existing configuration file")
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configSetCmd)
	rootCmd.AddCommand(configCmd)
}
