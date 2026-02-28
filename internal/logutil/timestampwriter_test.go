package logutil

import (
	"bytes"
	"strings"
	"testing"
)

func TestTimestampWriter_SingleLine(t *testing.T) {
	var buf bytes.Buffer
	w := NewTimestampWriter(&buf)

	w.Write([]byte("hello world\n"))

	out := buf.String()
	if !strings.HasPrefix(out, "[") {
		t.Fatalf("expected timestamp prefix, got: %q", out)
	}
	if !strings.HasSuffix(out, "] hello world\n") {
		t.Fatalf("expected message suffix, got: %q", out)
	}
}

func TestTimestampWriter_MultiLine(t *testing.T) {
	var buf bytes.Buffer
	w := NewTimestampWriter(&buf)

	w.Write([]byte("line1\nline2\n"))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "[") {
			t.Fatalf("line %d missing timestamp prefix: %q", i, line)
		}
	}
}

func TestTimestampWriter_PartialWrites(t *testing.T) {
	var buf bytes.Buffer
	w := NewTimestampWriter(&buf)

	// Write "hello " then "world\n" in two calls.
	w.Write([]byte("hello "))
	w.Write([]byte("world\n"))

	out := buf.String()
	// Should have exactly one timestamp prefix for the single line.
	count := strings.Count(out, "] ")
	if count != 1 {
		t.Fatalf("expected 1 timestamp, got %d in: %q", count, out)
	}
	if !strings.HasSuffix(out, "] hello world\n") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestTimestampWriter_EmptyWrite(t *testing.T) {
	var buf bytes.Buffer
	w := NewTimestampWriter(&buf)

	n, err := w.Write([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes written, got %d", n)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected empty buffer, got: %q", buf.String())
	}
}

func TestTimestampWriter_ConsecutiveLines(t *testing.T) {
	var buf bytes.Buffer
	w := NewTimestampWriter(&buf)

	w.Write([]byte("first\n"))
	w.Write([]byte("second\n"))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "[") {
			t.Fatalf("line %d missing timestamp: %q", i, line)
		}
	}
}
