package config

import (
	"path/filepath"

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

