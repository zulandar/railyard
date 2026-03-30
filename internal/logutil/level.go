package logutil

import (
	"io"
	"log/slog"
	"strings"
)

// ParseLevel returns a slog.Level from env and flag values.
// Flag takes precedence over env. Invalid/empty values default to INFO.
func ParseLevel(envVal, flagVal string) slog.Level {
	raw := flagVal
	if raw == "" {
		raw = envVal
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// NewLogger creates a *slog.Logger with ConsoleHandler.
func NewLogger(stdout, stderr io.Writer, level slog.Level) *slog.Logger {
	return slog.New(NewConsoleHandler(stdout, stderr, level))
}
