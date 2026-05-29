package dispatch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/zulandar/railyard/internal/agentloop"
)

// interactiveLoopConfig configures the stdin-driven native dispatch driver.
type interactiveLoopConfig struct {
	Client        agentloop.Completer
	Model         string
	SystemPrompt  string
	WorkDir       string // dispatch worktree; bash/read_file are scoped to it
	MaxIterations int    // 0 uses the agentloop default
	In            io.Reader
	Out           io.Writer
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
		Tools:         agentloop.DispatchTools(cfg.WorkDir),
		MaxIterations: cfg.MaxIterations,
		Events:        events,
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
		runInteractiveTurn(ctx, loop, line, events, cfg.Out)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("dispatch: read input: %w", err)
	}
	return nil
}

// runInteractiveTurn runs one loop turn, streaming its events to out until the
// turn completes. The loop runs in a goroutine so events can be drained live.
func runInteractiveTurn(ctx context.Context, loop *agentloop.Loop, input string, events <-chan agentloop.Event, out io.Writer) {
	done := make(chan error, 1)
	go func() {
		_, err := loop.Run(ctx, input)
		done <- err
	}()

	for {
		select {
		case ev := <-events:
			printLoopEvent(out, ev)
		case err := <-done:
			// Drain any events buffered before the run returned.
			for {
				select {
				case ev := <-events:
					printLoopEvent(out, ev)
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
		fmt.Fprintln(out, formatToolProgress(ev.ToolName, ev.ToolArgs))
	case agentloop.EventToolCallEnd:
		if ev.ToolError != "" {
			fmt.Fprintf(out, "⚠️ %s failed: %s\n", ev.ToolName, agentloop.Truncate(ev.ToolError, 200))
		}
	}
}

// formatToolProgress renders a concise "🔧" progress line, surfacing the bash
// command or file path when present.
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
