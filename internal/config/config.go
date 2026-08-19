package config

// Config holds the application configuration loaded from defaults, files, env, and flags.
type Config struct {
	LogLevel  string `koanf:"log_level" json:"log_level"`
	LogFormat string `koanf:"log_format" json:"log_format"`
	LogFile   string `koanf:"log_file" json:"log_file"`
}
