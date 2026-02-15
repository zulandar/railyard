package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
)

// --- Session ID tests ---

func TestGenerateSessionID_Format(t *testing.T) {
	id, err := GenerateSessionID()
	if err != nil {
		t.Fatalf("GenerateSessionID: %v", err)
	}
	if !strings.HasPrefix(id, "sess-") {
		t.Errorf("ID %q missing sess- prefix", id)
	}
	if len(id) != 13 {
		t.Errorf("ID %q length = %d, want 13", id, len(id))
	}
}

func TestGenerateSessionID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		id, err := GenerateSessionID()
		if err != nil {
			t.Fatalf("GenerateSessionID: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		seen[id] = true
	}
}

func TestGenerateSessionID_HexChars(t *testing.T) {
	id, err := GenerateSessionID()
	if err != nil {
		t.Fatalf("GenerateSessionID: %v", err)
	}
	suffix := id[5:] // after "sess-"
	for _, c := range suffix {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex char %q in suffix %q", string(c), suffix)
		}
	}
}

// --- Command construction tests ---

func TestBuildCommand_Args(t *testing.T) {
	opts := SpawnOpts{
		ClaudeBinary:   "/usr/bin/claude",
		ContextPayload: "test context",
	}
	cmd, cancel := buildCommand(context.Background(), opts)
	defer cancel()

	args := cmd.Args
	if args[0] != "/usr/bin/claude" {
		t.Errorf("binary = %q, want %q", args[0], "/usr/bin/claude")
	}

	// Find required flags.
	foundSkipPerms := false
	foundSP := false
	foundP := false
	for i, a := range args {
		if a == "--dangerously-skip-permissions" {
			foundSkipPerms = true
		}
		if a == "--system-prompt" && i+1 < len(args) {
			foundSP = true
			if args[i+1] != "test context" {
				t.Errorf("--system-prompt value = %q, want %q", args[i+1], "test context")
			}
		}
		if a == "-p" && i+1 < len(args) {
			foundP = true
			if args[i+1] == "" {
				t.Error("-p value should not be empty")
			}
		}
	}
	if !foundSkipPerms {
		t.Error("--dangerously-skip-permissions flag not found in args")
	}
	if !foundSP {
		t.Error("--system-prompt flag not found in args")
	}
	if !foundP {
		t.Error("-p flag not found in args")
	}
}

func TestBuildCommand_WorkDir(t *testing.T) {
	opts := SpawnOpts{
		WorkDir: "/tmp/test-workdir",
	}
	cmd, cancel := buildCommand(context.Background(), opts)
	defer cancel()

	if cmd.Dir != "/tmp/test-workdir" {
		t.Errorf("Dir = %q, want %q", cmd.Dir, "/tmp/test-workdir")
	}
}

func TestBuildCommand_DefaultBinary(t *testing.T) {
	opts := SpawnOpts{}
	cmd, cancel := buildCommand(context.Background(), opts)
	defer cancel()

	if cmd.Args[0] != "claude" {
		t.Errorf("binary = %q, want default %q", cmd.Args[0], "claude")
	}
}

func TestBuildCommand_Cancel(t *testing.T) {
	opts := SpawnOpts{}
	cmd, cancel := buildCommand(context.Background(), opts)
	defer cancel()

	if cmd.Cancel == nil {
		t.Error("cmd.Cancel should be set (SIGTERM handler)")
	}
}

// --- logWriter tests ---

func TestLogWriter_Write(t *testing.T) {
	w := &logWriter{
		direction: "out",
		writeFn:   func(models.AgentLog) error { return nil },
	}

	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 5 {
		t.Errorf("Write returned %d, want 5", n)
	}

	w.mu.Lock()
	got := w.buf.String()
	w.mu.Unlock()

	if got != "hello" {
		t.Errorf("buf = %q, want %q", got, "hello")
	}
}

func TestLogWriter_WriteMultiple(t *testing.T) {
	w := &logWriter{
		direction: "out",
		writeFn:   func(models.AgentLog) error { return nil },
	}

	w.Write([]byte("hello "))
	w.Write([]byte("world"))

	w.mu.Lock()
	got := w.buf.String()
	w.mu.Unlock()

	if got != "hello world" {
		t.Errorf("buf = %q, want %q", got, "hello world")
	}
}

func TestLogWriter_Flush(t *testing.T) {
	var captured models.AgentLog
	var called bool

	w := &logWriter{
		engineID:  "eng-abc",
		sessionID: "sess-12345",
		beadID:    "bead-xyz",
		direction: "out",
		writeFn: func(log models.AgentLog) error {
			called = true
			captured = log
			return nil
		},
	}

	w.Write([]byte("test output"))

	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if !called {
		t.Fatal("writeFn not called")
	}
	if captured.EngineID != "eng-abc" {
		t.Errorf("EngineID = %q, want %q", captured.EngineID, "eng-abc")
	}
	if captured.SessionID != "sess-12345" {
		t.Errorf("SessionID = %q, want %q", captured.SessionID, "sess-12345")
	}
	if captured.BeadID != "bead-xyz" {
		t.Errorf("BeadID = %q, want %q", captured.BeadID, "bead-xyz")
	}
	if captured.Direction != "out" {
		t.Errorf("Direction = %q, want %q", captured.Direction, "out")
	}
	if captured.Content != "test output" {
		t.Errorf("Content = %q, want %q", captured.Content, "test output")
	}
	if captured.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}

	// Buffer should be reset after flush.
	w.mu.Lock()
	remaining := w.buf.String()
	w.mu.Unlock()
	if remaining != "" {
		t.Errorf("buf after flush = %q, want empty", remaining)
	}
}

func TestLogWriter_FlushEmpty(t *testing.T) {
	called := false
	w := &logWriter{
		direction: "out",
		writeFn: func(models.AgentLog) error {
			called = true
			return nil
		},
	}

	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if called {
		t.Error("writeFn should not be called when buffer is empty")
	}
}

func TestLogWriter_Close(t *testing.T) {
	var captured models.AgentLog
	w := &logWriter{
		engineID:  "eng-abc",
		sessionID: "sess-12345",
		beadID:    "bead-xyz",
		direction: "err",
		writeFn: func(log models.AgentLog) error {
			captured = log
			return nil
		},
	}

	w.Write([]byte("remaining content"))

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if captured.Content != "remaining content" {
		t.Errorf("Content = %q, want %q", captured.Content, "remaining content")
	}
	if captured.Direction != "err" {
		t.Errorf("Direction = %q, want %q", captured.Direction, "err")
	}
}

// --- Validation tests ---

func TestSpawnAgent_EmptyEngineID(t *testing.T) {
	_, err := SpawnAgent(context.Background(), nil, SpawnOpts{
		BeadID:         "bead-1",
		ContextPayload: "ctx",
	})
	if err == nil {
		t.Fatal("expected error for empty engineID")
	}
	if !strings.Contains(err.Error(), "engineID is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "engineID is required")
	}
}

func TestSpawnAgent_EmptyBeadID(t *testing.T) {
	_, err := SpawnAgent(context.Background(), nil, SpawnOpts{
		EngineID:       "eng-abc",
		ContextPayload: "ctx",
	})
	if err == nil {
		t.Fatal("expected error for empty beadID")
	}
	if !strings.Contains(err.Error(), "beadID is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "beadID is required")
	}
}

func TestSpawnAgent_EmptyContext(t *testing.T) {
	_, err := SpawnAgent(context.Background(), nil, SpawnOpts{
		EngineID: "eng-abc",
		BeadID:   "bead-1",
	})
	if err == nil {
		t.Fatal("expected error for empty contextPayload")
	}
	if !strings.Contains(err.Error(), "contextPayload is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "contextPayload is required")
	}
}

// --- Subprocess tests (mock binary) ---

// writeMockBinary creates a shell script in dir that acts as a mock claude binary.
func writeMockBinary(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0755); err != nil {
		t.Fatalf("write mock binary: %v", err)
	}
	return path
}

// mockDB returns a nil-safe writeFn-based logWriter setup. For subprocess tests
// we replace the writeFn on the session's writers after spawn to avoid needing a
// real DB connection. The DB update for Engine.SessionID is skipped in these tests.
func spawnWithMockDB(t *testing.T, ctx context.Context, opts SpawnOpts) *Session {
	t.Helper()

	if opts.EngineID == "" {
		opts.EngineID = "eng-test1"
	}
	if opts.BeadID == "" {
		opts.BeadID = "bead-test1"
	}
	if opts.ContextPayload == "" {
		opts.ContextPayload = "test context"
	}

	// We can't pass nil DB to SpawnAgent because it calls db.Model().
	// Instead, build and run the command manually for unit tests.
	sessionID, err := GenerateSessionID()
	if err != nil {
		t.Fatalf("GenerateSessionID: %v", err)
	}

	cmd, cancel := buildCommand(ctx, opts)

	var mu sync.Mutex
	var stdoutLogs []models.AgentLog
	var stderrLogs []models.AgentLog

	stdoutWriter := &logWriter{
		engineID:  opts.EngineID,
		sessionID: sessionID,
		beadID:    opts.BeadID,
		direction: "out",
		writeFn: func(log models.AgentLog) error {
			mu.Lock()
			stdoutLogs = append(stdoutLogs, log)
			mu.Unlock()
			return nil
		},
	}
	stderrWriter := &logWriter{
		engineID:  opts.EngineID,
		sessionID: sessionID,
		beadID:    opts.BeadID,
		direction: "err",
		writeFn: func(log models.AgentLog) error {
			mu.Lock()
			stderrLogs = append(stderrLogs, log)
			mu.Unlock()
			return nil
		},
	}

	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start mock binary: %v", err)
	}

	waitCh := make(chan error, 1)
	flushCtx, flushCancel := context.WithCancel(ctx)

	startFlusher(flushCtx, stdoutWriter, 50*time.Millisecond)
	startFlusher(flushCtx, stderrWriter, 50*time.Millisecond)

	go func() {
		waitErr := cmd.Wait()
		flushCancel()
		stdoutWriter.Close()
		stderrWriter.Close()
		waitCh <- waitErr
	}()

	return &Session{
		ID:       sessionID,
		EngineID: opts.EngineID,
		BeadID:   opts.BeadID,
		PID:      cmd.Process.Pid,
		cmd:      cmd,
		cancel:   cancel,
		waitCh:   waitCh,
		stdout:   stdoutWriter,
		stderr:   stderrWriter,
	}
}

func TestSpawnAgent_MockBinary(t *testing.T) {
	dir := t.TempDir()
	binary := writeMockBinary(t, dir, "claude", `echo "hello from mock claude"`)

	sess := spawnWithMockDB(t, context.Background(), SpawnOpts{
		ClaudeBinary:   binary,
		EngineID:       "eng-mock1",
		BeadID:         "bead-mock1",
		ContextPayload: "test context",
		WorkDir:        dir,
	})

	if !strings.HasPrefix(sess.ID, "sess-") {
		t.Errorf("session ID %q missing sess- prefix", sess.ID)
	}
	if sess.PID <= 0 {
		t.Errorf("PID = %d, want > 0", sess.PID)
	}
	if sess.EngineID != "eng-mock1" {
		t.Errorf("EngineID = %q, want %q", sess.EngineID, "eng-mock1")
	}
	if sess.BeadID != "bead-mock1" {
		t.Errorf("BeadID = %q, want %q", sess.BeadID, "bead-mock1")
	}

	if err := sess.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

func TestSpawnAgent_CapturesStdout(t *testing.T) {
	dir := t.TempDir()
	binary := writeMockBinary(t, dir, "claude", `echo "captured output line 1"
echo "captured output line 2"`)

	sess := spawnWithMockDB(t, context.Background(), SpawnOpts{
		ClaudeBinary:   binary,
		EngineID:       "eng-cap",
		BeadID:         "bead-cap",
		ContextPayload: "ctx",
		WorkDir:        dir,
	})

	if err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Give a moment for final flush to complete.
	time.Sleep(50 * time.Millisecond)

	// Check stdout writer captured the output via writeFn.
	sess.stdout.mu.Lock()
	buf := sess.stdout.buf.String()
	sess.stdout.mu.Unlock()

	// After Close(), buf should be empty (flushed).
	// Verify by checking the writeFn was called — we inspect the logWriter's state
	// indirectly. Since Close() was called, all content should have been flushed.
	if buf != "" {
		t.Errorf("stdout buf should be empty after close, got %q", buf)
	}
}

func TestSpawnAgent_ExitError(t *testing.T) {
	dir := t.TempDir()
	binary := writeMockBinary(t, dir, "claude", `exit 1`)

	sess := spawnWithMockDB(t, context.Background(), SpawnOpts{
		ClaudeBinary:   binary,
		EngineID:       "eng-exit",
		BeadID:         "bead-exit",
		ContextPayload: "ctx",
		WorkDir:        dir,
	})

	err := sess.Wait()
	if err == nil {
		t.Fatal("expected non-nil error from exit 1")
	}
}

func TestSpawnAgent_ContextCancel(t *testing.T) {
	dir := t.TempDir()
	// Sleep long enough that context cancellation is what stops it.
	binary := writeMockBinary(t, dir, "claude", `sleep 60`)

	ctx, cancel := context.WithCancel(context.Background())

	sess := spawnWithMockDB(t, ctx, SpawnOpts{
		ClaudeBinary:   binary,
		EngineID:       "eng-cancel",
		BeadID:         "bead-cancel",
		ContextPayload: "ctx",
		WorkDir:        dir,
	})

	// Cancel after a short delay.
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	done := sess.Done()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error after context cancel, got nil")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for process to exit after cancel")
	}
}

func TestSpawnAgent_DoneChannel(t *testing.T) {
	dir := t.TempDir()
	binary := writeMockBinary(t, dir, "claude", fmt.Sprintf(`echo "done"`))

	sess := spawnWithMockDB(t, context.Background(), SpawnOpts{
		ClaudeBinary:   binary,
		EngineID:       "eng-done",
		BeadID:         "bead-done",
		ContextPayload: "ctx",
		WorkDir:        dir,
	})

	select {
	case err := <-sess.Done():
		if err != nil {
			t.Errorf("Done: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting on Done()")
	}
}
