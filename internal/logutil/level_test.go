package logutil

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name    string
		envVal  string
		flagVal string
		want    slog.Level
	}{
		{"empty defaults to info", "", "", slog.LevelInfo},
		{"env debug", "debug", "", slog.LevelDebug},
		{"env DEBUG uppercase", "DEBUG", "", slog.LevelDebug},
		{"env warn", "warn", "", slog.LevelWarn},
		{"env warning", "warning", "", slog.LevelWarn},
		{"env error", "error", "", slog.LevelError},
		{"env info", "info", "", slog.LevelInfo},
		{"flag overrides env", "error", "debug", slog.LevelDebug},
		{"invalid defaults to info", "garbage", "", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseLevel(tt.envVal, tt.flagVal)
			if got != tt.want {
				t.Errorf("ParseLevel(%q, %q) = %v, want %v", tt.envVal, tt.flagVal, got, tt.want)
			}
		})
	}
}

func TestNewLogger(t *testing.T) {
	var stdout, stderr bytes.Buffer
	logger := NewLogger(&stdout, &stderr, slog.LevelInfo)

	logger.Info("hello")

	if stdout.Len() == 0 {
		t.Error("expected output from NewLogger")
	}
}
