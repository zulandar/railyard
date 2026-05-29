package telegraph

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/zulandar/railyard/internal/agentloop"
)

// Rate-limit retry tuning for the chat-dispatch native loop. The agentloop
// client marks HTTP 429 non-retryable on purpose: the engine car-building path
// relies on its own OUTER pause-and-retry wrapper (pkg/cli/engine.go's
// spawnAndMonitorWithRetryRunner). The chat-dispatch path has no such wrapper,
// so a bounded retry lives here instead — keeping retry external to
// agentloop.Loop, matching the engine convention. (railyard-08t)
const (
	// dispatchRateLimitMaxRetries bounds how many times a dispatch turn is
	// retried after an upstream 429 before the turn fails and the relay warns.
	dispatchRateLimitMaxRetries = 2
	// dispatchRateLimitBaseWait is the backoff base used when a 429 carries no
	// Retry-After hint: 5s, 10s, ...
	dispatchRateLimitBaseWait = 5 * time.Second
	// dispatchRateLimitMaxWait caps a single pause so a bogus or hostile
	// Retry-After can't stall a chat turn near the 15m process timeout.
	dispatchRateLimitMaxWait = 60 * time.Second
)

// OpenRouterSpawner implements ProcessSpawner using the Railyard-owned native
// agent loop (internal/agentloop) instead of the claude CLI. It is the
// native-loop counterpart to ClaudeSpawner: given a system prompt, worktree,
// model, and OpenAI-compatible client, each Spawn drives an agentloop.Loop with
// the dispatch tool profile (bash + read_file) and adapts its events to the
// Process I/O contract that SessionManager already consumes — so all existing
// relay / persistence / chunking / empty-output handling is reused unchanged.
type OpenRouterSpawner struct {
	// SystemPrompt is the dispatch system prompt (kept minimal — the same
	// RenderPrompt output the claude path uses). It is NOT wrapped in a harness
	// mega-prompt; clean prompts are why weak models follow tools.
	SystemPrompt string
	// WorkDir is the dispatch worktree; bash and read_file are scoped to it.
	WorkDir string
	// Client is the OpenAI-compatible client (typically *agentloop.Client).
	Client agentloop.Completer
	// Model is the agent model name (e.g. openrouter/owl-alpha).
	Model string
	// MaxIterations bounds the loop; 0 uses the agentloop default.
	MaxIterations int
	// RateLimitMaxRetries bounds 429 pause-and-retry attempts for one dispatch
	// turn; 0 uses dispatchRateLimitMaxRetries.
	RateLimitMaxRetries int
	// sleepFn waits the given duration honoring ctx cancellation. nil uses a
	// real timer; tests inject a no-op to avoid waiting on real backoff.
	sleepFn func(ctx context.Context, d time.Duration) error
}

// maxRetries returns the configured 429 retry bound, or the default.
func (s *OpenRouterSpawner) maxRetries() int {
	if s.RateLimitMaxRetries > 0 {
		return s.RateLimitMaxRetries
	}
	return dispatchRateLimitMaxRetries
}

// Spawn starts a native-loop process. If prompt is non-empty it is the one-shot
// input (the loop runs immediately); if empty, the caller supplies the input via
// a single Send() — mirroring ClaudeSpawner's piped-stdin semantics.
func (s *OpenRouterSpawner) Spawn(ctx context.Context, prompt string) (Process, error) {
	if s.Client == nil {
		return nil, fmt.Errorf("telegraph: openrouter spawn: client not configured")
	}

	loopCtx, cancel := context.WithCancel(ctx)
	events := make(chan agentloop.Event, 64)
	loop := agentloop.NewLoop(s.Client, agentloop.LoopConfig{
		Model:         s.Model,
		SystemPrompt:  s.SystemPrompt,
		Tools:         agentloop.DispatchTools(s.WorkDir),
		MaxIterations: s.MaxIterations,
		Events:        events,
	})

	p := &loopProcess{
		ctx:        loopCtx,
		cancel:     cancel,
		loop:       loop,
		events:     events,
		oneShot:    prompt != "",
		recvCh:     make(chan string, 64),
		doneCh:     make(chan struct{}),
		maxRetries: s.maxRetries(),
		sleepFn:    s.sleepFn,
	}
	if p.oneShot {
		p.start(prompt)
	}
	return p, nil
}

// loopProcess adapts a running agentloop.Loop to the Process interface. It runs
// the loop in one goroutine while a driver goroutine pumps loop events onto the
// recv channel as output lines, closing recv and done when the loop finishes.
type loopProcess struct {
	ctx    context.Context
	cancel context.CancelFunc
	loop   *agentloop.Loop
	events chan agentloop.Event

	oneShot bool
	recvCh  chan string
	doneCh  chan struct{}

	// maxRetries bounds 429 pause-and-retry attempts; sleepFn (nil = real timer)
	// performs the backoff pause honoring ctx.
	maxRetries int
	sleepFn    func(ctx context.Context, d time.Duration) error

	startOnce sync.Once

	mu      sync.Mutex
	sent    bool
	closed  bool
	exitErr error
	stderr  string
}

// start launches the driver exactly once.
func (p *loopProcess) start(input string) {
	p.startOnce.Do(func() { go p.drive(input) })
}

// drive runs the loop and forwards its events to recv until completion.
func (p *loopProcess) drive(input string) {
	// Runner: execute the loop (with rate-limit pause-and-retry), then close the
	// events channel so the pump below terminates. The run error becomes the
	// process exit error.
	go func() {
		err := p.runWithRetry(input)
		p.mu.Lock()
		p.exitErr = err
		if err != nil {
			p.stderr = err.Error()
		}
		p.mu.Unlock()
		close(p.events)
	}()

	for ev := range p.events {
		line := renderLoopEvent(ev)
		if line == "" {
			continue
		}
		select {
		case p.recvCh <- line:
		case <-p.ctx.Done():
			// Consumer gone or cancelled: stop blocking but keep draining
			// events so the runner goroutine can finish and close the channel.
		}
	}
	close(p.recvCh)
	close(p.doneCh)
}

// runWithRetry runs the loop, pausing and retrying on upstream rate limits.
// Only *agentloop.RateLimitError triggers a retry — non-429 errors and success
// return immediately. On retry the loop is re-run with EMPTY input: Run appends
// the user message to history only on the first call, and returns before
// appending an assistant turn on a first-iteration 429, so the resend is
// identical with no duplicate user message. After the bound is exhausted the
// last rate-limit error is returned so the relay still warns the user instead
// of retrying forever. (railyard-08t)
func (p *loopProcess) runWithRetry(input string) error {
	var lastErr error
	for attempt := 0; ; attempt++ {
		in := input
		if attempt > 0 {
			in = ""
		}
		_, err := p.loop.Run(p.ctx, in)
		if err == nil {
			return nil
		}
		lastErr = err

		var rle *agentloop.RateLimitError
		if !errors.As(lastErr, &rle) || attempt >= p.maxRetries {
			return lastErr
		}

		wait := dispatchRetryWait(rle.RetryAfter, attempt)
		log.Printf("telegraph: dispatch rate limited (attempt %d/%d) — pausing %s before retry",
			attempt+1, p.maxRetries, wait)
		if err := p.sleep(wait); err != nil {
			// Context cancelled/expired during backoff: surface the rate-limit
			// error (more informative than the bare context error).
			return lastErr
		}
	}
}

// sleep waits d honoring loop-context cancellation. Tests inject sleepFn to
// avoid waiting on real backoff.
func (p *loopProcess) sleep(d time.Duration) error {
	if p.sleepFn != nil {
		return p.sleepFn(p.ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case <-t.C:
		return nil
	}
}

// dispatchRetryWait returns the pause before the next retry attempt (0-indexed).
// It honors the upstream Retry-After when present, otherwise uses exponential
// backoff from dispatchRateLimitBaseWait, capped at dispatchRateLimitMaxWait.
func dispatchRetryWait(retryAfter time.Duration, attempt int) time.Duration {
	wait := retryAfter
	if wait <= 0 {
		wait = dispatchRateLimitBaseWait << attempt // 5s, 10s, 20s, ...
	}
	if wait > dispatchRateLimitMaxWait {
		wait = dispatchRateLimitMaxWait
	}
	return wait
}

// renderLoopEvent maps a loop event to a relay output line. Assistant text is
// relayed as-is; tool calls surface as a progress line; tool failures surface so
// the relay user sees why the agent is stuck. Successful tool results stay
// summarized by the start line (full output would spam the relay), and usage /
// final events (final duplicates the last assistant text) are dropped.
func renderLoopEvent(ev agentloop.Event) string {
	switch ev.Type {
	case agentloop.EventAssistantText:
		return ev.Text
	case agentloop.EventToolCallStart:
		return agentloop.FormatToolProgress(ev.ToolName, ev.ToolArgs)
	case agentloop.EventToolCallEnd:
		if ev.ToolError != "" {
			return fmt.Sprintf("⚠️ %s failed: %s", ev.ToolName, agentloop.Truncate(ev.ToolError, 200))
		}
		return ""
	default:
		return ""
	}
}

// Send supplies the one-shot input when the process was spawned with an empty
// prompt, starting the loop. It may be called at most once.
func (p *loopProcess) Send(msg string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("telegraph: process closed")
	}
	if p.oneShot {
		p.mu.Unlock()
		return fmt.Errorf("telegraph: no input channel (process spawned with prompt)")
	}
	if p.sent {
		p.mu.Unlock()
		return fmt.Errorf("telegraph: message already sent")
	}
	p.sent = true
	p.mu.Unlock()

	p.start(msg)
	return nil
}

// Recv returns the channel delivering relayed output lines.
func (p *loopProcess) Recv() <-chan string { return p.recvCh }

// Done returns a channel that closes when the loop finishes.
func (p *loopProcess) Done() <-chan struct{} { return p.doneCh }

// ExitErr returns the loop's run error (nil on success). Valid after Done().
func (p *loopProcess) ExitErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}

// Stderr returns the run error text, if any. Valid after Done(). The native
// loop has no separate stderr stream; diagnostics come from the run error.
func (p *loopProcess) Stderr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr
}

// Close cancels the loop. If the driver never started (empty-prompt process
// that was never Send()-ed), it closes recv and done so relay/monitor unblock.
func (p *loopProcess) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	p.cancel()
	p.startOnce.Do(func() {
		close(p.recvCh)
		close(p.doneCh)
	})
	return nil
}
