package dispatch

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/zulandar/railyard/internal/agentloop"
)

// interactiveLoopConfig configures the stdin-driven native dispatch driver.
type interactiveLoopConfig struct {
	Client        agentloop.Completer
	Model         string
	SystemPrompt  string
	WorkDir       string // dispatch worktree; bash/read_file are scoped to it
	MaxIterations int    // 0 uses the agentloop default
	// CodeSearch enables the semantic codesearch tool when non-nil (CocoIndex
	// configured); nil omits it. Dispatch searches all track main tables.
	CodeSearch *agentloop.CodeSearchParams
	In         io.Reader
	Out        io.Writer
	// sleep waits the given duration honoring ctx during rate-limit backoff;
	// nil uses a real timer. Tests inject a no-op to avoid waiting on backoff.
	sleep func(ctx context.Context, d time.Duration) error
}

// runInteractiveLoop drives an interactive dispatch session against the native
// agent loop: read a line from In, run one loop turn (history is preserved on
// the Loop across turns), stream the assistant text and tool-call progress to
// Out, and repeat until EOF or "exit"/"quit". It replaces the claude/codex
// interactive attach when auth_method selects the native loop.
func runInteractiveLoop(ctx context.Context, cfg interactiveLoopConfig) error {
	events := make(chan agentloop.Event, 64)
	loop := agentloop.NewLoop(cfg.Client, agentloop.LoopConfig{
		Model:         cfg.Model,
		SystemPrompt:  cfg.SystemPrompt,
		Tools:         agentloop.DispatchTools(cfg.WorkDir, cfg.CodeSearch),
		MaxIterations: cfg.MaxIterations,
		Events:        events,
		Role:          "dispatch",
	})

	fmt.Fprintf(cfg.Out, "Railyard dispatch (native loop, model=%s).\n", cfg.Model)
	fmt.Fprintf(cfg.Out, "Type a request; \"exit\" or Ctrl-D to quit.\n")

	scanner := bufio.NewScanner(cfg.In)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		fmt.Fprint(cfg.Out, "dispatch> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			break
		}
		runInteractiveTurn(ctx, loop, line, events, cfg.Out, cfg.sleep)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("dispatch: read input: %w", err)
	}
	return nil
}

// runInteractiveTurn runs one loop turn, streaming its events to out until the
// turn completes. The loop runs in a goroutine so events can be drained live.
// Upstream 429s are paused-and-retried via the shared agentloop helper (the
// same one telegraph dispatch uses), so a transient rate limit recovers in
// place instead of forcing the operator to retype. Retry notices are routed
// through a channel rather than written directly so all writes to out stay on
// this goroutine. (railyard-08t)
func runInteractiveTurn(ctx context.Context, loop *agentloop.Loop, input string, events <-chan agentloop.Event, out io.Writer, sleep func(context.Context, time.Duration) error) {
	done := make(chan error, 1)
	notices := make(chan string, 4)
	go func() {
		done <- agentloop.RunWithRateLimitRetry(ctx, agentloop.RateLimitRetryConfig{
			Sleep: sleep,
			OnRetry: func(attempt, maxRetries int, wait time.Duration) {
				select {
				case notices <- fmt.Sprintf("rate limited — retrying in %s (attempt %d/%d)…", wait, attempt+1, maxRetries):
				case <-ctx.Done():
				}
			},
		}, func(attempt int) error {
			// On retry, pass empty input: Run already appended the user message
			// to history on the first attempt, so a re-run must not duplicate it.
			in := input
			if attempt > 0 {
				in = ""
			}
			_, err := loop.Run(ctx, in)
			return err
		})
	}()

	for {
		select {
		case ev := <-events:
			printLoopEvent(out, ev)
		case n := <-notices:
			fmt.Fprintln(out, n)
		case err := <-done:
			// Drain any events/notices buffered before the run returned.
			for {
				select {
				case ev := <-events:
					printLoopEvent(out, ev)
				case n := <-notices:
					fmt.Fprintln(out, n)
				default:
					if err != nil {
						fmt.Fprintf(out, "error: %v\n", err)
					}
					return
				}
			}
		}
	}
}

// printLoopEvent renders a loop event to the interactive transcript. Assistant
// text is printed as-is; tool calls surface as a 🔧 progress line; tool failures
// are shown so the operator sees why the agent is stuck. Successful tool results
// stay summarized by the start line, and the final event (which duplicates the
// last assistant text) plus usage are dropped.
func printLoopEvent(out io.Writer, ev agentloop.Event) {
	switch ev.Type {
	case agentloop.EventAssistantText:
		fmt.Fprintln(out, ev.Text)
	case agentloop.EventToolCallStart:
		fmt.Fprintln(out, agentloop.FormatToolProgress(ev.ToolName, ev.ToolArgs))
	case agentloop.EventToolCallEnd:
		if ev.ToolError != "" {
			fmt.Fprintf(out, "⚠️ %s failed: %s\n", ev.ToolName, agentloop.Truncate(ev.ToolError, 200))
		}
	}
}
