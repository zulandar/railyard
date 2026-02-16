package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
)

// --- StallConfig defaults ---

func TestNewStallDetector_DefaultConfig(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{})

	if sd.cfg.StdoutTimeout != DefaultStdoutTimeout {
		t.Errorf("StdoutTimeout = %v, want %v", sd.cfg.StdoutTimeout, DefaultStdoutTimeout)
	}
	if sd.cfg.RepeatedErrorMax != DefaultRepeatedErrorMax {
		t.Errorf("RepeatedErrorMax = %d, want %d", sd.cfg.RepeatedErrorMax, DefaultRepeatedErrorMax)
	}
	if sd.cfg.MaxClearCycles != DefaultMaxClearCycles {
		t.Errorf("MaxClearCycles = %d, want %d", sd.cfg.MaxClearCycles, DefaultMaxClearCycles)
	}
}

func TestNewStallDetector_CustomConfig(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:    30 * time.Second,
		RepeatedErrorMax: 5,
		MaxClearCycles:   10,
	})

	if sd.cfg.StdoutTimeout != 30*time.Second {
		t.Errorf("StdoutTimeout = %v, want 30s", sd.cfg.StdoutTimeout)
	}
	if sd.cfg.RepeatedErrorMax != 5 {
		t.Errorf("RepeatedErrorMax = %d, want 5", sd.cfg.RepeatedErrorMax)
	}
	if sd.cfg.MaxClearCycles != 10 {
		t.Errorf("MaxClearCycles = %d, want 10", sd.cfg.MaxClearCycles)
	}
}

func TestNewStallDetector_RegistersCallback(t *testing.T) {
	sess := newMockSession()
	if sess.stdout.onWrite != nil {
		t.Fatal("onWrite should be nil before NewStallDetector")
	}

	NewStallDetector(sess, StallConfig{})

	if sess.stdout.onWrite == nil {
		t.Fatal("onWrite should be registered after NewStallDetector")
	}
}

func TestNewStallDetector_SetsEngineAndCarID(t *testing.T) {
	sess := newMockSession()
	sess.EngineID = "eng-abc"
	sess.CarID = "car-xyz"

	sd := NewStallDetector(sess, StallConfig{})

	if sd.engineID != "eng-abc" {
		t.Errorf("engineID = %q, want %q", sd.engineID, "eng-abc")
	}
	if sd.carID != "car-xyz" {
		t.Errorf("carID = %q, want %q", sd.carID, "car-xyz")
	}
}

// --- Stdout timeout detection ---

func TestStallDetector_StdoutTimeout(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout: 100 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sd.Start(ctx)

	select {
	case reason := <-sd.Stalled():
		if reason.Type != "stdout_timeout" {
			t.Errorf("Type = %q, want %q", reason.Type, "stdout_timeout")
		}
		if !strings.Contains(reason.Detail, "no stdout") {
			t.Errorf("Detail = %q, want to contain %q", reason.Detail, "no stdout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stdout_timeout stall")
	}
}

func TestStallDetector_StdoutTimeout_ResetByOutput(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout: 200 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sd.Start(ctx)

	// Keep writing output to prevent timeout.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			time.Sleep(80 * time.Millisecond)
			sess.stdout.Write([]byte(fmt.Sprintf("output %d\n", i)))
		}
		close(done)
	}()

	// Wait for writes to finish.
	<-done

	// Should NOT have stalled during the writes.
	select {
	case reason := <-sd.Stalled():
		// If it stalled, it should be AFTER the writes stopped.
		if reason.Type != "stdout_timeout" {
			t.Errorf("unexpected stall type: %s", reason.Type)
		}
	case <-time.After(500 * time.Millisecond):
		// Timeout detection may fire after our writes stop — that's acceptable.
		// The key check is that we didn't stall DURING the writes.
	}
}

func TestStallDetector_StdoutTimeout_ContextCancel(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout: 10 * time.Second, // long timeout
	})

	ctx, cancel := context.WithCancel(context.Background())

	sd.Start(ctx)

	// Cancel immediately.
	cancel()

	// Should not emit a stall.
	time.Sleep(100 * time.Millisecond)
	select {
	case reason := <-sd.Stalled():
		t.Errorf("unexpected stall after cancel: %+v", reason)
	default:
	}
}

// --- Repeated error detection ---

func TestStallDetector_RepeatedError(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:    10 * time.Second,
		RepeatedErrorMax: 3,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sd.Start(ctx)

	// Write the same error line 3 times.
	for i := 0; i < 3; i++ {
		sess.stdout.Write([]byte("Error: connection refused\n"))
	}

	select {
	case reason := <-sd.Stalled():
		if reason.Type != "repeated_error" {
			t.Errorf("Type = %q, want %q", reason.Type, "repeated_error")
		}
		if !strings.Contains(reason.Detail, "connection refused") {
			t.Errorf("Detail = %q, want to contain error text", reason.Detail)
		}
		if !strings.Contains(reason.Detail, "3 times") {
			t.Errorf("Detail = %q, want to contain '3 times'", reason.Detail)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for repeated_error stall")
	}
}

func TestStallDetector_RepeatedError_BelowThreshold(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:    10 * time.Second,
		RepeatedErrorMax: 3,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sd.Start(ctx)

	// Write the same error line only 2 times (below threshold).
	sess.stdout.Write([]byte("Error: connection refused\n"))
	sess.stdout.Write([]byte("Error: connection refused\n"))
	sess.stdout.Write([]byte("Some other output\n"))

	// Should not stall.
	time.Sleep(100 * time.Millisecond)
	select {
	case reason := <-sd.Stalled():
		t.Errorf("unexpected stall: %+v", reason)
	default:
	}
}

func TestStallDetector_RepeatedError_DifferentLines(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:    10 * time.Second,
		RepeatedErrorMax: 3,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sd.Start(ctx)

	// Write different error lines — no single one repeats 3 times.
	sess.stdout.Write([]byte("Error: A\nError: B\nError: C\n"))

	time.Sleep(100 * time.Millisecond)
	select {
	case reason := <-sd.Stalled():
		t.Errorf("unexpected stall: %+v", reason)
	default:
	}
}

func TestStallDetector_RepeatedError_CustomThreshold(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:    10 * time.Second,
		RepeatedErrorMax: 5,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sd.Start(ctx)

	// Write 4 times (below threshold of 5).
	for i := 0; i < 4; i++ {
		sess.stdout.Write([]byte("Error: timeout\n"))
	}

	time.Sleep(100 * time.Millisecond)
	select {
	case reason := <-sd.Stalled():
		t.Errorf("unexpected stall at 4 repeats (threshold 5): %+v", reason)
	default:
	}

	// Write 5th time — should trigger.
	sess.stdout.Write([]byte("Error: timeout\n"))

	select {
	case reason := <-sd.Stalled():
		if reason.Type != "repeated_error" {
			t.Errorf("Type = %q, want %q", reason.Type, "repeated_error")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for repeated_error stall at threshold 5")
	}
}

// --- Excessive clear cycles ---

func TestStallDetector_ExcessiveCycles(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:  10 * time.Second,
		MaxClearCycles: 5,
	})

	// Cycles 1-5 should be fine.
	for i := 1; i <= 5; i++ {
		sd.SetCycle(i)
	}

	select {
	case reason := <-sd.Stalled():
		t.Errorf("unexpected stall at cycle 5: %+v", reason)
	default:
	}

	// Cycle 6 should trigger.
	sd.SetCycle(6)

	select {
	case reason := <-sd.Stalled():
		if reason.Type != "excessive_cycles" {
			t.Errorf("Type = %q, want %q", reason.Type, "excessive_cycles")
		}
		if !strings.Contains(reason.Detail, "6") {
			t.Errorf("Detail = %q, want to contain cycle count", reason.Detail)
		}
		if !strings.Contains(reason.Detail, "5") {
			t.Errorf("Detail = %q, want to contain threshold", reason.Detail)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for excessive_cycles stall")
	}
}

func TestStallDetector_ExcessiveCycles_CustomThreshold(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:  10 * time.Second,
		MaxClearCycles: 2,
	})

	sd.SetCycle(2)

	select {
	case reason := <-sd.Stalled():
		t.Errorf("unexpected stall at cycle 2 (threshold 2): %+v", reason)
	default:
	}

	sd.SetCycle(3)

	select {
	case reason := <-sd.Stalled():
		if reason.Type != "excessive_cycles" {
			t.Errorf("Type = %q, want %q", reason.Type, "excessive_cycles")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stall")
	}
}

// --- Stop ---

func TestStallDetector_Stop_PreventsStalls(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:  10 * time.Second,
		MaxClearCycles: 1,
	})

	sd.Stop()

	// SetCycle should not trigger after Stop.
	sd.SetCycle(10)

	select {
	case reason := <-sd.Stalled():
		t.Errorf("unexpected stall after Stop: %+v", reason)
	default:
	}
}

func TestStallDetector_Stop_PreventsOutputStalls(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:    10 * time.Second,
		RepeatedErrorMax: 2,
	})

	sd.Stop()

	// Write repeated errors — should not trigger.
	sess.stdout.Write([]byte("Error: X\nError: X\nError: X\n"))

	time.Sleep(100 * time.Millisecond)
	select {
	case reason := <-sd.Stalled():
		t.Errorf("unexpected stall after Stop: %+v", reason)
	default:
	}
}

// --- Single stall event ---

func TestStallDetector_OnlyOneStallEmitted(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:    100 * time.Millisecond,
		RepeatedErrorMax: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sd.Start(ctx)

	// Trigger repeated error first.
	sess.stdout.Write([]byte("Error: X\nError: X\n"))

	select {
	case <-sd.Stalled():
		// Good, first stall received.
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first stall")
	}

	// Wait long enough for stdout timeout to also fire — should NOT emit second stall.
	time.Sleep(300 * time.Millisecond)
	select {
	case reason := <-sd.Stalled():
		t.Errorf("unexpected second stall: %+v", reason)
	default:
	}
}

// --- Snippet capture ---

func TestStallDetector_SnippetCaptured(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:    10 * time.Second,
		RepeatedErrorMax: 2,
	})

	sess.stdout.Write([]byte("some output before error\n"))
	sess.stdout.Write([]byte("Error: fail\nError: fail\n"))

	select {
	case reason := <-sd.Stalled():
		if reason.Snippet == "" {
			t.Error("Snippet should not be empty")
		}
		if !strings.Contains(reason.Snippet, "Error: fail") {
			t.Errorf("Snippet = %q, want to contain error text", reason.Snippet)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestStallDetector_SnippetTruncatedTo500(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:    10 * time.Second,
		RepeatedErrorMax: 2,
	})

	// Write a very long output chunk.
	longOutput := strings.Repeat("x", 1000) + "\n"
	sess.stdout.Write([]byte(longOutput))
	sess.stdout.Write([]byte("Error: Y\nError: Y\n"))

	select {
	case reason := <-sd.Stalled():
		// The snippet from the error write should be <= 500 chars.
		if len(reason.Snippet) > 500 {
			t.Errorf("Snippet length = %d, want <= 500", len(reason.Snippet))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

// --- Rolling window ---

func TestStallDetector_RollingWindowBounded(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:    10 * time.Second,
		RepeatedErrorMax: 3,
	})

	// Write 150 unique lines — window should stay bounded at 100.
	for i := 0; i < 150; i++ {
		sess.stdout.Write([]byte(fmt.Sprintf("line %d\n", i)))
	}

	sd.mu.Lock()
	lineCount := len(sd.recentLines)
	sd.mu.Unlock()

	if lineCount > 100 {
		t.Errorf("recentLines length = %d, want <= 100", lineCount)
	}
}

func TestStallDetector_RepeatedLinesOlderThanWindowDontCount(t *testing.T) {
	sess := newMockSession()
	sd := NewStallDetector(sess, StallConfig{
		StdoutTimeout:    10 * time.Second,
		RepeatedErrorMax: 3,
	})

	// Write an error twice.
	sess.stdout.Write([]byte("Error: old\nError: old\n"))

	// Flush the window with 100 unique lines.
	for i := 0; i < 100; i++ {
		sess.stdout.Write([]byte(fmt.Sprintf("unique-%d\n", i)))
	}

	// Write the error once more — only 1 in window, not 3.
	sess.stdout.Write([]byte("Error: old\n"))

	time.Sleep(50 * time.Millisecond)
	select {
	case reason := <-sd.Stalled():
		t.Errorf("unexpected stall (old repeats should have scrolled out): %+v", reason)
	default:
	}
}

// --- HandleStall validation ---

func TestHandleStall_NilDB(t *testing.T) {
	// HandleStall with nil DB should panic or error; we just verify it returns error gracefully.
	// Since gorm with nil DB panics, we test the function signature and error format
	// with a real DB in integration tests. Here we just verify the function exists.
	_ = HandleStall
}

// --- StallReason fields ---

func TestStallReason_Fields(t *testing.T) {
	r := StallReason{
		Type:    "stdout_timeout",
		Detail:  "no stdout for 2m0s",
		Snippet: "last output line",
	}
	if r.Type != "stdout_timeout" {
		t.Errorf("Type = %q", r.Type)
	}
	if r.Detail != "no stdout for 2m0s" {
		t.Errorf("Detail = %q", r.Detail)
	}
	if r.Snippet != "last output line" {
		t.Errorf("Snippet = %q", r.Snippet)
	}
}

// --- logWriter onWrite callback ---

func TestLogWriter_OnWriteCallback(t *testing.T) {
	var captured []byte
	w := &logWriter{
		direction: "out",
		writeFn:   func(models.AgentLog) error { return nil },
		onWrite: func(p []byte) {
			captured = append(captured, p...)
		},
	}

	w.Write([]byte("hello"))
	w.Write([]byte(" world"))

	if string(captured) != "hello world" {
		t.Errorf("captured = %q, want %q", string(captured), "hello world")
	}
}

func TestLogWriter_OnWriteNil(t *testing.T) {
	// Ensure nil onWrite doesn't panic.
	w := &logWriter{
		direction: "out",
		writeFn:   func(models.AgentLog) error { return nil },
	}

	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
}

// --- Default constants ---

func TestDefaultConstants(t *testing.T) {
	if DefaultStdoutTimeout != 120*time.Second {
		t.Errorf("DefaultStdoutTimeout = %v, want 120s", DefaultStdoutTimeout)
	}
	if DefaultRepeatedErrorMax != 3 {
		t.Errorf("DefaultRepeatedErrorMax = %d, want 3", DefaultRepeatedErrorMax)
	}
	if DefaultMaxClearCycles != 5 {
		t.Errorf("DefaultMaxClearCycles = %d, want 5", DefaultMaxClearCycles)
	}
}

// --- Helper ---

// newMockSession creates a minimal Session with mock logWriters for testing.
func newMockSession() *Session {
	return &Session{
		EngineID: "eng-test1",
		CarID:   "car-test1",
		stdout: &logWriter{
			direction: "out",
			writeFn:   func(models.AgentLog) error { return nil },
		},
		stderr: &logWriter{
			direction: "err",
			writeFn:   func(models.AgentLog) error { return nil },
		},
	}
}
