package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/agentloop"
)

// scriptedCompleter is a fake agentloop.Completer returning a fixed sequence of
// responses (one per Complete call) and recording the requests it received.
type scriptedCompleter struct {
	responses []agentloop.Response
	errs      []error

	mu       sync.Mutex
	calls    int
	requests []agentloop.Request
}

func (c *scriptedCompleter) Complete(_ context.Context, req agentloop.Request) (agentloop.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	i := c.calls
	c.calls++
	if i < len(c.errs) && c.errs[i] != nil {
		return agentloop.Response{}, c.errs[i]
	}
	if i >= len(c.responses) {
		return agentloop.Response{}, fmt.Errorf("scriptedCompleter: no response for call %d", i)
	}
	return c.responses[i], nil
}

func (c *scriptedCompleter) requestAt(i int) agentloop.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests[i]
}

func (c *scriptedCompleter) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func stopResp(content string) agentloop.Response {
	return agentloop.Response{Content: content, FinishReason: "stop"}
}

func bashCallResp(id, command string) agentloop.Response {
	args, _ := json.Marshal(map[string]string{"command": command})
	return agentloop.Response{
		FinishReason: "tool_calls",
		ToolCalls:    []agentloop.ToolCall{{ID: id, Name: "bash", Arguments: args}},
	}
}

func messageContents(msgs []agentloop.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Role)
		b.WriteString(":")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func TestInteractiveLoop_MultiTurnPreservesHistory(t *testing.T) {
	c := &scriptedCompleter{responses: []agentloop.Response{
		stopResp("Plan: 1) add login form 2) wire auth"),
		stopResp("Created car-001 for the login work"),
	}}
	var out strings.Builder
	in := strings.NewReader("plan a login feature\ncreate the car\nexit\n")

	err := runInteractiveLoop(context.Background(), interactiveLoopConfig{
		Client:       c,
		Model:        "openrouter/owl-alpha",
		SystemPrompt: "you are the dispatch planner",
		WorkDir:      t.TempDir(),
		In:           in,
		Out:          &out,
	})
	if err != nil {
		t.Fatalf("runInteractiveLoop: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Plan: 1) add login form") {
		t.Errorf("output missing first-turn answer:\n%s", got)
	}
	if !strings.Contains(got, "Created car-001") {
		t.Errorf("output missing second-turn answer:\n%s", got)
	}

	// Two user turns => two model calls; "exit" must not trigger a third.
	if c.callCount() != 2 {
		t.Errorf("model calls = %d, want 2", c.callCount())
	}

	// The second turn's request must carry the full prior conversation.
	second := messageContents(c.requestAt(1).Messages)
	for _, want := range []string{
		"system:you are the dispatch planner",
		"user:plan a login feature",
		"assistant:Plan: 1) add login form 2) wire auth",
		"user:create the car",
	} {
		if !strings.Contains(second, want) {
			t.Errorf("second-turn request missing %q; got:\n%s", want, second)
		}
	}
}

func TestInteractiveLoop_StreamsToolProgress(t *testing.T) {
	c := &scriptedCompleter{responses: []agentloop.Response{
		bashCallResp("c1", "echo hi"),
		stopResp("the answer is hi"),
	}}
	var out strings.Builder
	in := strings.NewReader("run echo\nexit\n")

	err := runInteractiveLoop(context.Background(), interactiveLoopConfig{
		Client:  c,
		Model:   "m",
		WorkDir: t.TempDir(),
		In:      in,
		Out:     &out,
	})
	if err != nil {
		t.Fatalf("runInteractiveLoop: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "🔧") || !strings.Contains(got, "echo hi") {
		t.Errorf("output missing tool progress line:\n%s", got)
	}
	if !strings.Contains(got, "the answer is hi") {
		t.Errorf("output missing final answer:\n%s", got)
	}
}

func TestInteractiveLoop_EOFTerminatesCleanly(t *testing.T) {
	c := &scriptedCompleter{responses: []agentloop.Response{stopResp("done")}}
	var out strings.Builder
	in := strings.NewReader("hello\n") // no "exit"; ends on EOF

	err := runInteractiveLoop(context.Background(), interactiveLoopConfig{
		Client: c, Model: "m", WorkDir: t.TempDir(), In: in, Out: &out,
	})
	if err != nil {
		t.Fatalf("runInteractiveLoop: %v", err)
	}
	if c.callCount() != 1 {
		t.Errorf("model calls = %d, want 1", c.callCount())
	}
}

func TestInteractiveLoop_SkipsBlankLines(t *testing.T) {
	c := &scriptedCompleter{responses: []agentloop.Response{stopResp("ok")}}
	var out strings.Builder
	in := strings.NewReader("\n   \nhello\nexit\n")

	if err := runInteractiveLoop(context.Background(), interactiveLoopConfig{
		Client: c, Model: "m", WorkDir: t.TempDir(), In: in, Out: &out,
	}); err != nil {
		t.Fatalf("runInteractiveLoop: %v", err)
	}
	if c.callCount() != 1 {
		t.Errorf("model calls = %d, want 1 (blank lines skipped)", c.callCount())
	}
}

func TestInteractiveLoop_RetriesAfterRateLimit(t *testing.T) {
	// A transient 429 on a turn must be retried in place (not abandoned): the
	// turn recovers and prints the answer, with a retry notice and no error line.
	c := &scriptedCompleter{
		responses: []agentloop.Response{{}, stopResp("recovered answer")},
		errs:      []error{&agentloop.RateLimitError{Message: "slow down"}, nil},
	}
	var out strings.Builder
	in := strings.NewReader("first\nexit\n")

	if err := runInteractiveLoop(context.Background(), interactiveLoopConfig{
		Client: c, Model: "m", WorkDir: t.TempDir(), In: in, Out: &out,
		sleep: func(context.Context, time.Duration) error { return nil },
	}); err != nil {
		t.Fatalf("runInteractiveLoop: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "recovered answer") {
		t.Errorf("turn should recover after a transient 429:\n%s", got)
	}
	if !strings.Contains(strings.ToLower(got), "rate limited") {
		t.Errorf("expected a retry notice:\n%s", got)
	}
	if strings.Contains(got, "error:") {
		t.Errorf("a recovered turn must not print an error line:\n%s", got)
	}
	if c.callCount() != 2 {
		t.Errorf("model calls = %d, want 2 (one 429 + one success)", c.callCount())
	}
}

func TestInteractiveLoop_NonRateLimitErrorPrintedAndContinues(t *testing.T) {
	// A non-429 error is NOT retried: it prints an error and the REPL continues
	// to the next turn (preserving the pre-existing fail-soft behavior).
	c := &scriptedCompleter{
		responses: []agentloop.Response{{}, stopResp("second answer")},
		errs:      []error{&agentloop.APIError{StatusCode: 400, Message: "bad request"}, nil},
	}
	var out strings.Builder
	in := strings.NewReader("first\nsecond\nexit\n")

	if err := runInteractiveLoop(context.Background(), interactiveLoopConfig{
		Client: c, Model: "m", WorkDir: t.TempDir(), In: in, Out: &out,
		sleep: func(context.Context, time.Duration) error { return nil },
	}); err != nil {
		t.Fatalf("runInteractiveLoop: %v", err)
	}
	got := out.String()
	if !strings.Contains(strings.ToLower(got), "error") {
		t.Errorf("output should report the non-retryable turn error:\n%s", got)
	}
	if !strings.Contains(got, "second answer") {
		t.Errorf("loop should continue to the next turn after an error:\n%s", got)
	}
	if c.callCount() != 2 {
		t.Errorf("model calls = %d, want 2", c.callCount())
	}
}
