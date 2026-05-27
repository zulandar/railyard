package telegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/models"
)

// scriptedCompleter is a fake agentloop.Completer returning a fixed sequence of
// responses, one per Complete call.
type scriptedCompleter struct {
	responses []agentloop.Response
	err       error

	mu    sync.Mutex
	calls int
}

func (c *scriptedCompleter) Complete(_ context.Context, _ agentloop.Request) (agentloop.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return agentloop.Response{}, c.err
	}
	i := c.calls
	c.calls++
	if i >= len(c.responses) {
		return agentloop.Response{}, fmt.Errorf("scriptedCompleter: no response for call %d", i)
	}
	return c.responses[i], nil
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

// drain collects all Recv lines until the channel closes, with a safety timeout.
func drain(t *testing.T, proc Process) []string {
	t.Helper()
	var lines []string
	recv := proc.Recv()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case line, ok := <-recv:
			if !ok {
				return lines
			}
			lines = append(lines, line)
		case <-timeout:
			t.Fatal("timed out draining Recv")
		}
	}
}

func TestOpenRouterSpawner_OneShotProducesAnswer(t *testing.T) {
	c := &scriptedCompleter{responses: []agentloop.Response{stopResp("Open cars: 3")}}
	spawner := &OpenRouterSpawner{
		SystemPrompt: "you are dispatch",
		WorkDir:      t.TempDir(),
		Client:       c,
		Model:        "openrouter/owl-alpha",
	}

	proc, err := spawner.Spawn(context.Background(), "what is the status?")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Close()

	lines := drain(t, proc)
	<-proc.Done()

	if !strings.Contains(strings.Join(lines, "\n"), "Open cars: 3") {
		t.Errorf("relayed lines = %v, want to contain the answer", lines)
	}
	if proc.ExitErr() != nil {
		t.Errorf("ExitErr() = %v, want nil", proc.ExitErr())
	}
}

func TestOpenRouterSpawner_SendProvidesInput(t *testing.T) {
	c := &scriptedCompleter{responses: []agentloop.Response{stopResp("hello back")}}
	spawner := &OpenRouterSpawner{WorkDir: t.TempDir(), Client: c, Model: "m"}

	proc, err := spawner.Spawn(context.Background(), "") // empty -> input via Send
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Close()

	if err := proc.Send("hi there"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// A second Send must error (one-shot semantics, mirroring claudeProcess).
	if err := proc.Send("again"); err == nil {
		t.Error("expected error on second Send, got nil")
	}

	lines := drain(t, proc)
	<-proc.Done()
	if !strings.Contains(strings.Join(lines, "\n"), "hello back") {
		t.Errorf("lines = %v, want the answer", lines)
	}
}

func TestOpenRouterSpawner_ToolCallProgressLine(t *testing.T) {
	// The model calls bash (a harmless echo), then answers. The relay must see
	// a 🔧 progress line for the tool call AND the final answer.
	c := &scriptedCompleter{responses: []agentloop.Response{
		bashCallResp("c1", "echo hi"),
		stopResp("the answer is hi"),
	}}
	spawner := &OpenRouterSpawner{WorkDir: t.TempDir(), Client: c, Model: "m"}

	proc, err := spawner.Spawn(context.Background(), "run echo")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Close()

	lines := drain(t, proc)
	<-proc.Done()

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "🔧") || !strings.Contains(joined, "bash") || !strings.Contains(joined, "echo hi") {
		t.Errorf("relayed lines = %q, want a 🔧 progress line naming bash/echo hi", joined)
	}
	if !strings.Contains(joined, "the answer is hi") {
		t.Errorf("relayed lines = %q, want the final answer", joined)
	}
}

func TestOpenRouterSpawner_ClientErrorSetsExitErr(t *testing.T) {
	// An upstream error must surface via ExitErr (so the relay sends the
	// empty/error-output warning rather than ghosting), not panic the loop.
	c := &scriptedCompleter{err: &agentloop.RateLimitError{RetryAfter: 5 * time.Second, Message: "slow down"}}
	spawner := &OpenRouterSpawner{WorkDir: t.TempDir(), Client: c, Model: "m"}

	proc, err := spawner.Spawn(context.Background(), "go")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Close()

	_ = drain(t, proc)
	<-proc.Done()

	if proc.ExitErr() == nil {
		t.Error("ExitErr() = nil, want non-nil after upstream error")
	}
}

func TestOpenRouterSpawner_SendAfterCloseErrors(t *testing.T) {
	c := &scriptedCompleter{responses: []agentloop.Response{stopResp("x")}}
	spawner := &OpenRouterSpawner{WorkDir: t.TempDir(), Client: c, Model: "m"}

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

func TestOpenRouterSpawner_CloseWithoutSendUnblocks(t *testing.T) {
	// Spawned with empty prompt but never Send()-ed: Close() must still close
	// Recv and Done so the relay/monitor goroutines unblock.
	c := &scriptedCompleter{responses: []agentloop.Response{stopResp("x")}}
	spawner := &OpenRouterSpawner{WorkDir: t.TempDir(), Client: c, Model: "m"}

	proc, err := spawner.Spawn(context.Background(), "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	proc.Close()

	select {
	case <-proc.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done() did not close after Close() without Send")
	}
	// Recv must be closed too.
	select {
	case _, ok := <-proc.Recv():
		if ok {
			t.Error("Recv() delivered a line after Close without Send")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Recv() not closed after Close without Send")
	}
}

func TestOpenRouterSpawner_RequiresClient(t *testing.T) {
	spawner := &OpenRouterSpawner{WorkDir: t.TempDir(), Model: "m"} // no Client
	if _, err := spawner.Spawn(context.Background(), "go"); err == nil {
		t.Fatal("expected error when Client is not configured")
	}
}

func TestLazySpawner_SelectsNativeLoopByFlag(t *testing.T) {
	// With UseNativeLoop set, LazySpawner must drive the native agentloop (whose
	// scripted answer we observe) rather than shelling out to claude.
	c := &scriptedCompleter{responses: []agentloop.Response{stopResp("native answer")}}
	spawner := &LazySpawner{
		RenderPrompt:   func() (string, error) { return "dispatch prompt", nil },
		EnsureWorktree: func() (string, error) { return t.TempDir(), nil },
		SyncWorktree:   func(string) error { return nil },
		UseNativeLoop:  true,
		Client:         c,
		Model:          "openrouter/owl-alpha",
	}

	proc, err := spawner.Spawn(context.Background(), "what is the status?")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Close()

	lines := drain(t, proc)
	<-proc.Done()
	if !strings.Contains(strings.Join(lines, "\n"), "native answer") {
		t.Errorf("lines = %v, want native-loop answer (proves native selection)", lines)
	}
}

func TestLazySpawner_NativeSkipsMCPConfig(t *testing.T) {
	// The native loop uses bash to run ry; it has no MCP client, so the
	// claude-specific .mcp.json write must be skipped.
	c := &scriptedCompleter{responses: []agentloop.Response{stopResp("ok")}}
	mcpCalled := false
	spawner := &LazySpawner{
		RenderPrompt:   func() (string, error) { return "p", nil },
		EnsureWorktree: func() (string, error) { return t.TempDir(), nil },
		WriteMCPConfig: func(string) error { mcpCalled = true; return nil },
		UseNativeLoop:  true,
		Client:         c,
		Model:          "m",
	}

	proc, err := spawner.Spawn(context.Background(), "go")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Close()
	_ = drain(t, proc)
	<-proc.Done()

	if mcpCalled {
		t.Error("WriteMCPConfig must not be called on the native-loop path")
	}
}

func TestLazySpawner_NativeRequiresClient(t *testing.T) {
	spawner := &LazySpawner{
		RenderPrompt:   func() (string, error) { return "p", nil },
		EnsureWorktree: func() (string, error) { return t.TempDir(), nil },
		UseNativeLoop:  true,
		// Client intentionally nil.
	}
	if _, err := spawner.Spawn(context.Background(), "go"); err == nil {
		t.Fatal("expected error when native loop selected without a Client")
	}
}

// TestOpenRouterSpawner_EndToEndRelaysSummary is the Phase 1 acceptance check:
// a status question drives the native loop (which runs a command, then
// summarizes), and SessionManager's existing relay/persistence posts the
// summary to the chat adapter and records it — all reused unchanged. This is
// the path that fixes the originally-reported "generic greeting" bug.
func TestOpenRouterSpawner_EndToEndRelaysSummary(t *testing.T) {
	db := openSessionTestDB(t)
	adapter := NewMockAdapter()
	adapter.Connect(context.Background())

	// The model runs a (harmless, hermetic) command standing in for `ry car
	// list`, then summarizes its output — exactly the behavior the bug lacked.
	c := &scriptedCompleter{responses: []agentloop.Response{
		bashCallResp("c1", "echo 'cars: 3 open'"),
		stopResp("There are 3 open cars."),
	}}
	spawner := &OpenRouterSpawner{
		SystemPrompt: "you are the dispatch agent",
		WorkDir:      t.TempDir(),
		Client:       c,
		Model:        "openrouter/owl-alpha",
	}

	sm, err := NewSessionManager(SessionManagerOpts{
		DB:                 db,
		Spawner:            spawner,
		Adapter:            adapter,
		RelayFlushInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	proc, err := spawner.Spawn(context.Background(), "what is the status?")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Reuse the production relay path verbatim.
	sm.relayOutput(context.Background(), "C01", "thread-1", 1, proc)

	var parts []string
	for _, msg := range adapter.AllSent() {
		parts = append(parts, msg.Text)
	}
	combined := strings.Join(parts, "\n")
	if !strings.Contains(combined, "There are 3 open cars.") {
		t.Errorf("adapter output = %q, want the summary", combined)
	}
	if !strings.Contains(combined, "🔧") {
		t.Errorf("adapter output = %q, want a tool-call progress line", combined)
	}

	// The assistant response must be persisted for resume/`ry logs`.
	var conv models.TelegraphConversation
	db.Where("role = ?", "assistant").Last(&conv)
	if !strings.Contains(conv.Content, "There are 3 open cars.") {
		t.Errorf("persisted assistant content = %q, want the summary", conv.Content)
	}
}
