package config

import (
	"context"
	"os"
	"strings"

	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type contextKey struct{}

var configContextKey = contextKey{}

// WithConfig returns a context with the given Config stored.
func WithConfig(ctx context.Context, cfg *Config) context.Context {
	return context.WithValue(ctx, configContextKey, cfg)
}

// FromContext retrieves the Config stored in context.
func FromContext(ctx context.Context) *Config {
	if cfg, ok := ctx.Value(configContextKey).(*Config); ok {
		return cfg
	}
	return nil
}

// Load loads configuration with precedence:
// Defaults -> Config File (explicit or default location if present) -> TDM_* Env Vars
func Load(explicitPath string) (*Config, error) {
	k := koanf.New(".")

	// 1. Defaults
	defaultMap := map[string]interface{}{
		"log_level":  "info",
		"log_format": "text",
		"log_file":   "",
		"priority":   []string{},
		"exclude":    []string{},
	}
	if err := k.Load(confmap.Provider(defaultMap, "."), nil); err != nil {
		return nil, err
	}

	// 2. Config File — --config flag, else TDM_CONFIG, else the default XDG path.
	targetPath, err := ResolveConfigPath(explicitPath)
	if err != nil {
		return nil, err
	}

	// A path the operator named explicitly must exist: silently falling back to
	// defaults is exactly the failure mode that made TDM_CONFIG look broken.
	// A missing file at the default location is normal and simply means
	// "no config yet".
	explicitlyRequested := explicitPath != "" || os.Getenv("TDM_CONFIG") != ""

	if targetPath != "" {
		if _, statErr := os.Stat(targetPath); statErr == nil {
			if err := k.Load(file.Provider(targetPath), json.Parser()); err != nil {
				return nil, err
			}
		} else if explicitlyRequested {
			return nil, statErr
		}
	}

	// 3. TDM_* Environment Variables
	if err := k.Load(env.Provider(".", env.Opt{
		Prefix: "TDM_",
		TransformFunc: func(k, v string) (string, any) {
			key := strings.ToLower(strings.TrimPrefix(k, "TDM_"))
			// TDM_CONFIG selects which file to read (handled above); it is not
			// itself a config value. Drop it so it cannot shadow a real key.
			if key == "config" {
				return "", nil
			}
			return key, v
		},
	}), nil); err != nil {
		return nil, err
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
