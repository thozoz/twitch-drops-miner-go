package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"gopkg.in/natefinch/lumberjack.v2"
	"tdm/internal/config"
)

type contextKey struct{}

var (
	loggerContextKey = contextKey{}
	closerMu         sync.Mutex
	activeCloser     io.Closer
)

// WithLogger stores the logger in the context.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey, l)
}

// FromContext retrieves the logger from context or returns slog.Default().
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerContextKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// Close closes any active file log writer created by New.
func Close() error {
	closerMu.Lock()
	defer closerMu.Unlock()
	if activeCloser != nil {
		err := activeCloser.Close()
		activeCloser = nil
		return err
	}
	return nil
}

func parseLevel(levelStr string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(levelStr)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

// New creates a new structured logger configured from the given Config.
func New(cfg *config.Config) *slog.Logger {
	var logLevel slog.Level = slog.LevelInfo
	var unrecognizedLevel string

	if cfg != nil && cfg.LogLevel != "" {
		if lvl, ok := parseLevel(cfg.LogLevel); ok {
			logLevel = lvl
		} else {
			unrecognizedLevel = cfg.LogLevel
		}
	}

	opts := &slog.HandlerOptions{
		Level: logLevel,
	}

	var w io.Writer = os.Stderr
	if cfg != nil && cfg.LogFile != "" {
		lj := &lumberjack.Logger{
			Filename:   cfg.LogFile,
			MaxSize:    10, // megabytes
			MaxBackups: 3,
			MaxAge:     28, // days
		}
		w = lj
		closerMu.Lock()
		activeCloser = lj
		closerMu.Unlock()
	}

	var handler slog.Handler
	if cfg != nil && strings.ToLower(strings.TrimSpace(cfg.LogFormat)) == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	l := slog.New(handler)
	if unrecognizedLevel != "" {
		l.Warn("unrecognized log_level, defaulting to info", "provided", unrecognizedLevel)
	}
	return l
}

// NewWithExtraWriter creates a new structured logger configured from the given Config,
// additionally teeing output to the provided extra io.Writer if non-nil.
func NewWithExtraWriter(cfg *config.Config, extra io.Writer) *slog.Logger {
	var logLevel slog.Level = slog.LevelInfo
	var unrecognizedLevel string

	if cfg != nil && cfg.LogLevel != "" {
		if lvl, ok := parseLevel(cfg.LogLevel); ok {
			logLevel = lvl
		} else {
			unrecognizedLevel = cfg.LogLevel
		}
	}

	opts := &slog.HandlerOptions{
		Level: logLevel,
	}

	var w io.Writer = os.Stderr
	if cfg != nil && cfg.LogFile != "" {
		lj := &lumberjack.Logger{
			Filename:   cfg.LogFile,
			MaxSize:    10, // megabytes
			MaxBackups: 3,
			MaxAge:     28, // days
		}
		w = lj
		closerMu.Lock()
		activeCloser = lj
		closerMu.Unlock()
	}

	if extra != nil {
		w = io.MultiWriter(w, extra)
	}

	var handler slog.Handler
	if cfg != nil && strings.ToLower(strings.TrimSpace(cfg.LogFormat)) == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	l := slog.New(handler)
	if unrecognizedLevel != "" {
		l.Warn("unrecognized log_level, defaulting to info", "provided", unrecognizedLevel)
	}
	return l
}

