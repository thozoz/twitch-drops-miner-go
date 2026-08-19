package model

import (
	"log/slog"
)

// RedactedString is a string type that masks its value in logs and string formatting.
// The only way to retrieve the underlying secret is via Reveal().
type RedactedString string

// String returns "[REDACTED]" to satisfy fmt.Stringer and prevent accidental plaintext printing.
func (r RedactedString) String() string {
	return "[REDACTED]"
}

// LogValue returns a slog.Value of "[REDACTED]" to satisfy slog.LogValuer.
func (r RedactedString) LogValue() slog.Value {
	return slog.StringValue("[REDACTED]")
}

// Reveal returns the underlying raw string. This should ONLY be used by HTTP/auth clients
// sending credentials across network boundaries, never in logging or diagnostics code.
func (r RedactedString) Reveal() string {
	return string(r)
}
