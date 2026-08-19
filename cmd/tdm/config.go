package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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

func init() {
	configInitCmd.Flags().BoolVarP(&forceInit, "force", "f", false, "Overwrite existing configuration file")
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configInitCmd)
	rootCmd.AddCommand(configCmd)
}
