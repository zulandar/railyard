package telegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/zulandar/railyard/internal/agentloop"
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
		ctx:     loopCtx,
		cancel:  cancel,
		loop:    loop,
		events:  events,
		oneShot: prompt != "",
		recvCh:  make(chan string, 64),
		doneCh:  make(chan struct{}),
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
	// Runner: execute the loop, then close the events channel so the pump below
	// terminates. The run error becomes the process exit error.
	go func() {
		_, err := p.loop.Run(p.ctx, input)
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

// renderLoopEvent maps a loop event to a relay output line. Assistant text is
// relayed as-is; tool calls surface as a progress line. Other events (usage,
// tool-call end, final — final duplicates the last assistant text) are dropped.
func renderLoopEvent(ev agentloop.Event) string {
	switch ev.Type {
	case agentloop.EventAssistantText:
		return ev.Text
	case agentloop.EventToolCallStart:
		return formatToolProgress(ev.ToolName, ev.ToolArgs)
	default:
		return ""
	}
}

// formatToolProgress renders a concise "🔧" progress line for a tool call,
// surfacing the bash command or file path when present.
func formatToolProgress(name, args string) string {
	detail := args
	var m map[string]any
	if json.Unmarshal([]byte(args), &m) == nil {
		switch {
		case asString(m["command"]) != "":
			detail = asString(m["command"])
		case asString(m["path"]) != "":
			detail = asString(m["path"])
		}
	}
	detail = agentloop.Truncate(detail, 200)
	if detail == "" {
		return "🔧 " + name
	}
	return fmt.Sprintf("🔧 %s: %s", name, detail)
}

func asString(v any) string {
	s, _ := v.(string)
	return s
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
