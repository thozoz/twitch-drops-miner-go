package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/adrg/xdg"
)

// ConfigDir returns the path to the tdm configuration directory ($XDG_CONFIG_HOME/tdm).
func ConfigDir() (string, error) {
	return filepath.Join(xdg.ConfigHome, "tdm"), nil
}

// StateDir returns the path to the tdm state directory ($XDG_STATE_HOME/tdm).
func StateDir() (string, error) {
	return filepath.Join(xdg.StateHome, "tdm"), nil
}

// ConfigFilePath returns the path to config.json ($XDG_CONFIG_HOME/tdm/config.json).
func ConfigFilePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// AuthFilePath returns the path to auth.json ($XDG_STATE_HOME/tdm/auth.json).
func AuthFilePath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "auth.json"), nil
}

// StateFilePath returns the path to state.json ($XDG_STATE_HOME/tdm/state.json).
func StateFilePath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}

// OperationsOverridePath returns the path to operations.json override ($XDG_CONFIG_HOME/tdm/operations.json).
func OperationsOverridePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "operations.json"), nil
}

// SocketPath returns the IPC socket/pipe address for the current operating system.
// On Windows it returns a named pipe path (\\.\pipe\tdm-<username>).
// On non-Windows it returns $XDG_RUNTIME_DIR/tdm.sock or /tmp/tdm-<uid>/tdm.sock fallback.
func SocketPath() (string, error) {
	if runtime.GOOS == "windows" {
		username := "default"
		if u, err := user.Current(); err == nil && u.Username != "" {
			sanitized := strings.Map(func(r rune) rune {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
					return r
				}
				return '-'
			}, u.Username)
			sanitized = strings.Trim(sanitized, "-")
			if sanitized != "" {
				username = sanitized
			}
		}
		return fmt.Sprintf(`\\.\pipe\tdm-%s`, username), nil
	}

	if xdg.RuntimeDir != "" {
		return filepath.Join(xdg.RuntimeDir, "tdm.sock"), nil
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("tdm-%d", os.Getuid()), "tdm.sock"), nil
}

