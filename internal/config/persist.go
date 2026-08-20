package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/thozoz/twitch-drops-miner-go/internal/state"
)

// configFilePerm matches the permissions used for the other files tdm writes
// under the user's XDG directories. config.json holds no credentials today, but
// it lives beside auth.json and is owned by a single user, so it is not made
// group- or world-readable.
const configFilePerm os.FileMode = 0600

// ResolveConfigPath returns the configuration file path to use, applying the
// precedence: explicit path (the --config flag) > TDM_CONFIG > the default XDG
// location. It is the single place this order is defined so the CLI and the
// daemon cannot drift apart.
//
// The returned path is not guaranteed to exist — callers that require the file
// decide how to treat a missing one.
func ResolveConfigPath(explicitPath string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}
	if env := os.Getenv("TDM_CONFIG"); env != "" {
		return env, nil
	}
	return ConfigFilePath()
}

// SavePriority writes the given priority list into the config file at path,
// preserving every other key already in the file — including keys this version
// of tdm does not know about. A user who hand-wrote a minimal config.json gets
// their file back with only "priority" changed, rather than rewritten with
// every default materialised.
//
// The write goes through state.AtomicWriteJSON, so a crash mid-write cannot
// leave a truncated config behind.
func SavePriority(path string, priority []string) error {
	if path == "" {
		return errors.New("config: cannot save priority without a config file path")
	}

	doc := map[string]any{}

	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &doc); err != nil {
				// Refuse to clobber a file we cannot parse — overwriting it would
				// silently discard whatever the operator actually had there.
				return fmt.Errorf("config: %s is not valid JSON, refusing to overwrite: %w", path, err)
			}
		}
	case errors.Is(err, os.ErrNotExist):
		// First write — AtomicWriteJSON creates the parent directory.
	default:
		return fmt.Errorf("config: failed to read %s: %w", path, err)
	}

	// Marshal an empty list as [] rather than null so the file stays readable.
	if priority == nil {
		priority = []string{}
	}
	doc["priority"] = priority

	return state.AtomicWriteJSON(path, doc, configFilePerm)
}
