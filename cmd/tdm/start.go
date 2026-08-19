package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"tdm/internal/config"
	"tdm/internal/daemon"
	"tdm/internal/ipc"
	"tdm/internal/logging"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the mining supervisor as a background daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := logging.FromContext(ctx)

		addr, err := config.SocketPath()
		if err != nil {
			logger.Error("failed to resolve socket path", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		// Fast parent-side check for already running daemon (DMN-08)
		running, _ := ipc.ProbeRunning(addr, 300*time.Millisecond)
		if running {
			fmt.Fprintln(os.Stderr, "tdm is already running")
			return &CommandError{Code: ExitError, Err: errors.New("tdm is already running")}
		}

		selfPath, err := os.Executable()
		if err != nil {
			logger.Error("failed to resolve self executable path", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		cfg := config.FromContext(ctx)
		effectiveLogPath := ""
		if cfg != nil && cfg.LogFile != "" {
			effectiveLogPath = cfg.LogFile
		} else {
			stateDir, stateErr := config.StateDir()
			if stateErr == nil {
				_ = os.MkdirAll(stateDir, 0755)
				effectiveLogPath = filepath.Join(stateDir, "miner.log")
			}
		}

		childArgs := []string{"run", "--daemon-mode"}
		if effectiveLogPath != "" {
			childArgs = append(childArgs, "--log-file", effectiveLogPath)
		}

		if cmd.Flags().Changed("config") {
			childArgs = append(childArgs, "--config", configFile)
		}
		if cmd.Flags().Changed("log-level") {
			childArgs = append(childArgs, "--log-level", logLevel)
		}
		if cmd.Flags().Changed("log-format") {
			childArgs = append(childArgs, "--log-format", logFormat)
		}

		childCmd := exec.Command(selfPath, childArgs...)
		childCmd.Stdin = nil
		childCmd.Stdout = nil
		childCmd.Stderr = nil

		daemon.ConfigureDetached(childCmd)

		if err := childCmd.Start(); err != nil {
			logger.Error("failed to start daemon process", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		// Health check polling: up to 5s budget
		deadline := time.Now().Add(5 * time.Second)
		var healthy bool
		for time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			running, _ := ipc.ProbeRunning(addr, 300*time.Millisecond)
			if running {
				healthy = true
				break
			}
		}

		if !healthy {
			fmt.Fprintln(os.Stderr, "daemon did not become healthy within 5s")
			return &CommandError{Code: ExitError, Err: errors.New("daemon did not become healthy within 5s")}
		}

		fmt.Printf("tdm daemon started (PID %d)\n", childCmd.Process.Pid)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
