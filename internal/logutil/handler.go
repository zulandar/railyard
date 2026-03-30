package logutil

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"
)

// ConsoleHandler implements slog.Handler with human-friendly terminal output.
// INFO/DEBUG write to stdout; WARN/ERROR write to stderr.
// Format: [RFC3339] message key=value ...
// WARN/ERROR lines include the level prefix; INFO/DEBUG omit it.
type ConsoleHandler struct {
	stdout io.Writer
	stderr io.Writer
	level  slog.Leveler
	attrs  []slog.Attr
	mu     *sync.Mutex
}

// NewConsoleHandler returns a handler that writes human-friendly log lines.
func NewConsoleHandler(stdout, stderr io.Writer, level slog.Leveler) *ConsoleHandler {
	return &ConsoleHandler{
		stdout: stdout,
		stderr: stderr,
		level:  level,
		mu:     &sync.Mutex{},
	}
}

func (h *ConsoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *ConsoleHandler) Handle(_ context.Context, r slog.Record) error {
	w := h.stdout
	if r.Level >= slog.LevelWarn {
		w = h.stderr
	}

	buf := make([]byte, 0, 256)

	// Timestamp.
	buf = append(buf, '[')
	buf = append(buf, r.Time.Format(time.RFC3339)...)
	buf = append(buf, ']', ' ')

	// Level prefix for WARN/ERROR only.
	if r.Level >= slog.LevelWarn {
		buf = append(buf, r.Level.String()...)
		buf = append(buf, ' ')
	}

	// Message.
	buf = append(buf, r.Message...)

	// Pre-set attrs from With().
	for _, a := range h.attrs {
		buf = consoleAppendAttr(buf, a)
	}

	// Record attrs.
	r.Attrs(func(a slog.Attr) bool {
		buf = consoleAppendAttr(buf, a)
		return true
	})

	buf = append(buf, '\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := w.Write(buf)
	return err
}

func (h *ConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ConsoleHandler{
		stdout: h.stdout,
		stderr: h.stderr,
		level:  h.level,
		attrs:  append(slices.Clone(h.attrs), attrs...),
		mu:     h.mu,
	}
}

// WithGroup is a no-op; group prefixes are not rendered in console output.
func (h *ConsoleHandler) WithGroup(_ string) slog.Handler {
	return h
}

func consoleAppendAttr(buf []byte, a slog.Attr) []byte {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return buf
	}
	buf = append(buf, ' ')
	buf = append(buf, a.Key...)
	buf = append(buf, '=')

	val := a.Value
	switch val.Kind() {
	case slog.KindString:
		s := val.String()
		if consoleNeedsQuote(s) {
			buf = strconv.AppendQuote(buf, s)
		} else {
			buf = append(buf, s...)
		}
	case slog.KindInt64:
		buf = strconv.AppendInt(buf, val.Int64(), 10)
	case slog.KindFloat64:
		buf = strconv.AppendFloat(buf, val.Float64(), 'f', -1, 64)
	case slog.KindBool:
		buf = strconv.AppendBool(buf, val.Bool())
	case slog.KindDuration:
		buf = append(buf, val.Duration().String()...)
	case slog.KindTime:
		buf = append(buf, val.Time().Format(time.RFC3339)...)
	default:
		s := val.String()
		if consoleNeedsQuote(s) {
			buf = strconv.AppendQuote(buf, s)
		} else {
			buf = append(buf, s...)
		}
	}
	return buf
}

func consoleNeedsQuote(s string) bool {
	if s == "" {
		return true
	}
	for _, c := range s {
		if c == ' ' || c == '\t' || c == '"' || c == '=' || c < 0x20 {
			return true
		}
	}
	return false
}
