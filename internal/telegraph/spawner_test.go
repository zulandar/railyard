package telegraph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeMockBinary creates a shell script in dir that acts as a mock claude binary.
func writeMockBinary(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0755); err != nil {
		t.Fatalf("write mock binary: %v", err)
	}
	return path
}

func TestClaudeSpawner_SpawnAndRecv(t *testing.T) {
	dir := t.TempDir()
	binary := writeMockBinary(t, dir, "claude", `echo "line one"
echo "line two"
echo "line three"`)

	spawner := &ClaudeSpawner{
		ClaudeBinary: binary,
		WorkDir:      dir,
	}

	proc, err := spawner.Spawn(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Close()

	var lines []string
	for line := range proc.Recv() {
		lines = append(lines, line)
	}

	<-proc.Done()

	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %v", len(lines), lines)
	}
	if lines[0] != "line one" {
		t.Errorf("line[0] = %q, want %q", lines[0], "line one")
	}
	if lines[1] != "line two" {
		t.Errorf("line[1] = %q, want %q", lines[1], "line two")
	}
	if lines[2] != "line three" {
		t.Errorf("line[2] = %q, want %q", lines[2], "line three")
	}
}

func TestClaudeSpawner_SendClosesStdin(t *testing.T) {
	dir := t.TempDir()
	// Script reads from stdin and echoes it back.
	binary := writeMockBinary(t, dir, "claude", `cat`)

	spawner := &ClaudeSpawner{
		ClaudeBinary: binary,
		WorkDir:      dir,
	}

	proc, err := spawner.Spawn(context.Background(), "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Close()

	if err := proc.Send("hello from test"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Second send should fail.
	if err := proc.Send("second"); err == nil {
		t.Error("expected error on second Send, got nil")
	}

	var lines []string
	for line := range proc.Recv() {
		lines = append(lines, line)
	}

	<-proc.Done()

	if len(lines) != 1 || lines[0] != "hello from test" {
		t.Errorf("lines = %v, want [\"hello from test\"]", lines)
	}
}

func TestClaudeSpawner_SendOnClosedProcess(t *testing.T) {
	dir := t.TempDir()
	binary := writeMockBinary(t, dir, "claude", `cat`)

	spawner := &ClaudeSpawner{
		ClaudeBinary: binary,
		WorkDir:      dir,
	}

	proc, err := spawner.Spawn(context.Background(), "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	proc.Close()
	<-proc.Done()

	if err := proc.Send("after close"); err == nil {
		t.Error("expected error on Send after Close, got nil")
	}
}

func TestClaudeSpawner_ContextCancel(t *testing.T) {
	dir := t.TempDir()
	binary := writeMockBinary(t, dir, "claude", `sleep 60`)

	spawner := &ClaudeSpawner{
		ClaudeBinary: binary,
		WorkDir:      dir,
	}

	ctx, cancel := context.WithCancel(context.Background())
	proc, err := spawner.Spawn(ctx, "go")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Cancel after a short delay.
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	select {
	case <-proc.Done():
		// Process exited due to context cancel — success.
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for process to exit after context cancel")
	}
}

func TestClaudeSpawner_LongOutput(t *testing.T) {
	dir := t.TempDir()

	// Generate a script that outputs 100 lines.
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("echo \"line ")
		sb.WriteString(strings.Repeat("x", 50))
		sb.WriteString("\"\n")
	}
	binary := writeMockBinary(t, dir, "claude", sb.String())

	spawner := &ClaudeSpawner{
		ClaudeBinary: binary,
		WorkDir:      dir,
	}

	proc, err := spawner.Spawn(context.Background(), "go")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Close()

	var lines []string
	for line := range proc.Recv() {
		lines = append(lines, line)
	}

	<-proc.Done()

	if len(lines) != 100 {
		t.Errorf("got %d lines, want 100", len(lines))
	}
}

func TestClaudeSpawner_SystemPromptFlag(t *testing.T) {
	dir := t.TempDir()
	// Print args so we can verify flags are passed.
	binary := writeMockBinary(t, dir, "claude", `for arg in "$@"; do echo "$arg"; done`)

	spawner := &ClaudeSpawner{
		ClaudeBinary: binary,
		SystemPrompt: "you are a dispatch agent",
		WorkDir:      dir,
	}

	proc, err := spawner.Spawn(context.Background(), "do work")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Close()

	var lines []string
	for line := range proc.Recv() {
		lines = append(lines, line)
	}
	<-proc.Done()

	all := strings.Join(lines, "\n")
	if !strings.Contains(all, "--append-system-prompt") {
		t.Error("expected --append-system-prompt in args")
	}
	if !strings.Contains(all, "you are a dispatch agent") {
		t.Error("expected system prompt value in args")
	}
	if !strings.Contains(all, "-p") {
		t.Error("expected -p flag in args")
	}
	if !strings.Contains(all, "do work") {
		t.Error("expected prompt value in args")
	}
}

func TestClaudeSpawner_MissingBinary(t *testing.T) {
	spawner := &ClaudeSpawner{
		ClaudeBinary: "/nonexistent/path/to/claude-binary-xyz",
	}
	_, err := spawner.Spawn(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error when claude binary does not exist")
	}
	if !strings.Contains(err.Error(), "start claude") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "start claude")
	}
}

func TestClaudeSpawner_CancelDoesNotHangWithUndrainedOutput(t *testing.T) {
	dir := t.TempDir()

	// Script outputs 200 lines (exceeds the 64-entry recvCh buffer) with a
	// small delay between each so the process is still running when we cancel.
	binary := writeMockBinary(t, dir, "claude", `
i=0
while [ $i -lt 200 ]; do
  echo "output line $i"
  i=$((i + 1))
done
# Keep the process alive so it's still running when we cancel.
sleep 60
`)

	spawner := &ClaudeSpawner{
		ClaudeBinary: binary,
		WorkDir:      dir,
	}

	ctx, cancel := context.WithCancel(context.Background())
	proc, err := spawner.Spawn(ctx, "test")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Read a few lines to confirm output is flowing, then stop draining.
	for i := 0; i < 5; i++ {
		select {
		case _, ok := <-proc.Recv():
			if !ok {
				t.Fatal("recvCh closed before we expected")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for initial output")
		}
	}

	// Cancel the context while the process is still producing output
	// and we are NOT draining recvCh. Before the fix, the scanner
	// goroutine would block on recvCh send, doneCh would never close,
	// and this test would time out.
	cancel()

	select {
	case <-proc.Done():
		// Success — process cleaned up despite undrained output.
	case <-time.After(15 * time.Second):
		t.Fatal("proc.Done() did not close within 15s — scanner goroutine likely hung on full recvCh")
	}
}

func TestLazySpawner_SpawnDelegates(t *testing.T) {
	dir := t.TempDir()
	binary := writeMockBinary(t, dir, "claude", `echo "lazy output"`)

	spawner := &LazySpawner{
		RenderPrompt: func() (string, error) {
			return "test system prompt", nil
		},
		EnsureWorktree: func() (string, error) {
			return dir, nil
		},
		SyncWorktree: func(worktreeDir string) error {
			return nil
		},
		WriteMCPConfig: func(worktreeDir string) error {
			return nil
		},
		ClaudeBinary: binary,
	}

	proc, err := spawner.Spawn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Close()

	var lines []string
	for line := range proc.Recv() {
		lines = append(lines, line)
	}
	<-proc.Done()

	if len(lines) != 1 || lines[0] != "lazy output" {
		t.Errorf("lines = %v, want [\"lazy output\"]", lines)
	}
}

func TestLazySpawner_RenderPromptError(t *testing.T) {
	spawner := &LazySpawner{
		RenderPrompt: func() (string, error) {
			return "", fmt.Errorf("config not found")
		},
		EnsureWorktree: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	_, err := spawner.Spawn(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error when RenderPrompt fails")
	}
	if !strings.Contains(err.Error(), "render prompt") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "render prompt")
	}
	if !strings.Contains(err.Error(), "config not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "config not found")
	}
}

func TestLazySpawner_WorktreeError(t *testing.T) {
	spawner := &LazySpawner{
		RenderPrompt: func() (string, error) {
			return "prompt", nil
		},
		EnsureWorktree: func() (string, error) {
			return "", fmt.Errorf("worktree locked by another process")
		},
	}

	_, err := spawner.Spawn(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error when EnsureWorktree fails")
	}
	if !strings.Contains(err.Error(), "ensure worktree") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "ensure worktree")
	}
}

func TestLazySpawner_RecoveryAfterFailure(t *testing.T) {
	dir := t.TempDir()
	binary := writeMockBinary(t, dir, "claude", `echo "recovered"`)

	callCount := 0
	spawner := &LazySpawner{
		RenderPrompt: func() (string, error) {
			callCount++
			if callCount == 1 {
				return "", fmt.Errorf("transient error")
			}
			return "system prompt", nil
		},
		EnsureWorktree: func() (string, error) {
			return dir, nil
		},
		SyncWorktree:   func(string) error { return nil },
		WriteMCPConfig: func(string) error { return nil },
		ClaudeBinary:   binary,
	}

	// First call fails.
	_, err := spawner.Spawn(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected first Spawn to fail")
	}

	// Second call succeeds (recovery).
	proc, err := spawner.Spawn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("expected second Spawn to succeed: %v", err)
	}
	defer proc.Close()

	var lines []string
	for line := range proc.Recv() {
		lines = append(lines, line)
	}
	<-proc.Done()

	if len(lines) != 1 || lines[0] != "recovered" {
		t.Errorf("lines = %v, want [\"recovered\"]", lines)
	}
}

func TestLazySpawner_SyncWorktreeErrorNonFatal(t *testing.T) {
	dir := t.TempDir()
	binary := writeMockBinary(t, dir, "claude", `echo "ok"`)

	spawner := &LazySpawner{
		RenderPrompt: func() (string, error) {
			return "prompt", nil
		},
		EnsureWorktree: func() (string, error) {
			return dir, nil
		},
		SyncWorktree: func(string) error {
			return fmt.Errorf("sync failed")
		},
		WriteMCPConfig: func(string) error { return nil },
		ClaudeBinary:   binary,
	}

	// Should succeed despite SyncWorktree error (non-fatal).
	proc, err := spawner.Spawn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Spawn should succeed despite sync error: %v", err)
	}
	defer proc.Close()
	<-proc.Done()
}

func TestLazySpawner_WriteMCPConfigErrorNonFatal(t *testing.T) {
	dir := t.TempDir()
	binary := writeMockBinary(t, dir, "claude", `echo "ok"`)

	spawner := &LazySpawner{
		RenderPrompt: func() (string, error) {
			return "prompt", nil
		},
		EnsureWorktree: func() (string, error) {
			return dir, nil
		},
		SyncWorktree: func(string) error { return nil },
		WriteMCPConfig: func(string) error {
			return fmt.Errorf("mcp write failed")
		},
		ClaudeBinary: binary,
	}

	// Should succeed despite WriteMCPConfig error (non-fatal).
	proc, err := spawner.Spawn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Spawn should succeed despite MCP config error: %v", err)
	}
	defer proc.Close()
	<-proc.Done()
}
