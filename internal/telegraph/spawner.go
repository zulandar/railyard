package telegraph

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ClaudeSpawner implements ProcessSpawner by launching claude CLI subprocesses.
// Each spawned process is one-shot: stdin is written once then closed, Claude
// processes the input and writes output on stdout, then exits.
type ClaudeSpawner struct {
	SystemPrompt string // appended via --append-system-prompt
	WorkDir      string // working directory for the subprocess
	ClaudeBinary string // path to claude binary; defaults to "claude"
}

// Spawn starts a claude subprocess. If prompt is non-empty, it is passed via
// -p (one-shot mode, no stdin pipe). If prompt is empty, stdin is piped and
// the caller must use Send() to provide input.
func (s *ClaudeSpawner) Spawn(ctx context.Context, prompt string) (Process, error) {
	binary := s.ClaudeBinary
	if binary == "" {
		binary = "claude"
	}

	args := []string{
		"--dangerously-skip-permissions",
		"--output-format", "text",
	}
	if s.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", s.SystemPrompt)
	}

	var stdinPipe io.WriteCloser

	if prompt != "" {
		// One-shot mode: pass prompt via -p, no stdin pipe needed.
		args = append(args, "-p", prompt)
	}

	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, binary, args...)

	if s.WorkDir != "" {
		cmd.Dir = s.WorkDir
	}

	// Use a process group so SIGTERM kills the entire tree (shell + children).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second

	// Set up stdout pipe for reading output.
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("telegraph: stdout pipe: %w", err)
	}

	// Set up stdin pipe only when no prompt is provided.
	if prompt == "" {
		stdinPipe, err = cmd.StdinPipe()
		if err != nil {
			cancel()
			return nil, fmt.Errorf("telegraph: stdin pipe: %w", err)
		}
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("telegraph: start claude: %w", err)
	}

	recvCh := make(chan string, 64)
	doneCh := make(chan struct{})

	proc := &claudeProcess{
		cmd:       cmd,
		cancel:    cancel,
		stdinPipe: stdinPipe,
		recvCh:    recvCh,
		doneCh:    doneCh,
	}

	// Read stdout lines, then wait for process exit.
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB buffer
		for scanner.Scan() {
			recvCh <- scanner.Text()
		}
		close(recvCh)
		cmd.Wait()
		close(doneCh)
	}()

	return proc, nil
}

// claudeProcess implements the Process interface for a running claude subprocess.
type claudeProcess struct {
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	stdinPipe io.WriteCloser // nil when spawned with -p

	mu     sync.Mutex
	sent   bool // true after Send() has been called
	closed bool
	recvCh chan string
	doneCh chan struct{}
}

// Send writes a message to the subprocess stdin and closes it (EOF signal).
// Can only be called once; subsequent calls return an error.
func (p *claudeProcess) Send(msg string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return fmt.Errorf("telegraph: process closed")
	}
	if p.sent {
		return fmt.Errorf("telegraph: message already sent")
	}
	if p.stdinPipe == nil {
		return fmt.Errorf("telegraph: no stdin pipe (process spawned with prompt)")
	}

	p.sent = true
	if _, err := io.WriteString(p.stdinPipe, msg); err != nil {
		return fmt.Errorf("telegraph: write stdin: %w", err)
	}
	// Close stdin to signal EOF — Claude processes on EOF.
	if err := p.stdinPipe.Close(); err != nil {
		return fmt.Errorf("telegraph: close stdin: %w", err)
	}
	return nil
}

// Recv returns a channel that delivers subprocess stdout lines.
func (p *claudeProcess) Recv() <-chan string {
	return p.recvCh
}

// Done returns a channel that closes when the process exits.
func (p *claudeProcess) Done() <-chan struct{} {
	return p.doneCh
}

// Close terminates the subprocess via context cancellation (SIGTERM).
func (p *claudeProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true
	p.cancel()
	return nil
}
