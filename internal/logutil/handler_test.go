package logutil

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestConsoleHandler_Routing(t *testing.T) {
	tests := []struct {
		name       string
		logFunc    func(*slog.Logger)
		wantStdout bool
		wantStderr bool
		wantLevel  string
	}{
		{
			name:       "info to stdout",
			logFunc:    func(l *slog.Logger) { l.Info("test") },
			wantStdout: true,
		},
		{
			name:       "debug at debug level to stdout",
			logFunc:    func(l *slog.Logger) { l.Debug("test") },
			wantStdout: true,
		},
		{
			name:       "warn to stderr with prefix",
			logFunc:    func(l *slog.Logger) { l.Warn("test") },
			wantStderr: true,
			wantLevel:  "WARN",
		},
		{
			name:       "error to stderr with prefix",
			logFunc:    func(l *slog.Logger) { l.Error("test") },
			wantStderr: true,
			wantLevel:  "ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			h := NewConsoleHandler(&stdout, &stderr, slog.LevelDebug)
			logger := slog.New(h)

			tt.logFunc(logger)

			if tt.wantStdout && stdout.Len() == 0 {
				t.Error("expected output on stdout")
			}
			if !tt.wantStdout && stdout.Len() != 0 {
				t.Errorf("unexpected stdout: %s", stdout.String())
			}
			if tt.wantStderr && stderr.Len() == 0 {
				t.Error("expected output on stderr")
			}
			if !tt.wantStderr && stderr.Len() != 0 {
				t.Errorf("unexpected stderr: %s", stderr.String())
			}

			output := stdout.String() + stderr.String()
			if tt.wantLevel != "" && !strings.Contains(output, tt.wantLevel) {
				t.Errorf("expected %q prefix in: %s", tt.wantLevel, output)
			}
			if tt.wantLevel == "" {
				for _, lvl := range []string{"INFO", "DEBUG"} {
					if strings.Contains(output, lvl) {
						t.Errorf("unexpected %q prefix in: %s", lvl, output)
					}
				}
			}
		})
	}
}

func TestConsoleHandler_LevelFiltering(t *testing.T) {
	var stdout, stderr bytes.Buffer
	h := NewConsoleHandler(&stdout, &stderr, slog.LevelInfo)
	logger := slog.New(h)

	logger.Debug("should be filtered")

	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("debug should be filtered at info level, got stdout=%q stderr=%q",
			stdout.String(), stderr.String())
	}
}

func TestConsoleHandler_TimestampFormat(t *testing.T) {
	var stdout bytes.Buffer
	h := NewConsoleHandler(&stdout, &bytes.Buffer{}, slog.LevelInfo)
	logger := slog.New(h)

	logger.Info("test")

	line := stdout.String()
	if !strings.HasPrefix(line, "[") {
		t.Fatalf("expected [ prefix, got: %s", line)
	}
	closeBracket := strings.Index(line, "]")
	if closeBracket < 0 {
		t.Fatalf("expected ] in output, got: %s", line)
	}
	ts := line[1:closeBracket]
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Fatalf("expected RFC3339 timestamp, got %q: %v", ts, err)
	}
}

func TestConsoleHandler_Attributes(t *testing.T) {
	var stdout bytes.Buffer
	h := NewConsoleHandler(&stdout, &bytes.Buffer{}, slog.LevelInfo)
	logger := slog.New(h)

	logger.Info("claimed car", "car", "car-abc", "cycle", 1)

	line := stdout.String()
	if !strings.Contains(line, "car=car-abc") {
		t.Errorf("expected car=car-abc in: %s", line)
	}
	if !strings.Contains(line, "cycle=1") {
		t.Errorf("expected cycle=1 in: %s", line)
	}
}

func TestConsoleHandler_WithAttrs(t *testing.T) {
	var stdout bytes.Buffer
	h := NewConsoleHandler(&stdout, &bytes.Buffer{}, slog.LevelInfo)
	logger := slog.New(h).With("engine", "eng-123")

	logger.Info("started")

	line := stdout.String()
	if !strings.Contains(line, "engine=eng-123") {
		t.Errorf("expected engine=eng-123 in: %s", line)
	}
}

func TestConsoleHandler_QuotedStrings(t *testing.T) {
	var stdout bytes.Buffer
	h := NewConsoleHandler(&stdout, &bytes.Buffer{}, slog.LevelInfo)
	logger := slog.New(h)

	logger.Info("claimed", "title", "Add Token GORM model")

	line := stdout.String()
	if !strings.Contains(line, `title="Add Token GORM model"`) {
		t.Errorf("expected quoted title in: %s", line)
	}
}

func TestConsoleHandler_DebugVisible(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewConsoleHandler(&buf, &buf, slog.LevelDebug))
	logger.Debug("debug message", "key", "val")
	if !strings.Contains(buf.String(), "debug message") {
		t.Errorf("debug message not visible at Debug level, got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "key=val") {
		t.Errorf("debug attributes not visible, got: %q", buf.String())
	}
}

func TestConsoleHandler_DebugFilteredAtInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewConsoleHandler(&buf, &buf, slog.LevelInfo))
	logger.Debug("should not appear")
	if strings.Contains(buf.String(), "should not appear") {
		t.Errorf("debug message should be filtered at Info level, got: %q", buf.String())
	}
}
