package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeCompleter returns a scripted sequence of responses (or errors), one per
// Complete call, and records the requests it received.
type fakeCompleter struct {
	responses   []Response
	err         error // if set, returned on every call
	calls       int
	gotRequests []Request
}

func (f *fakeCompleter) Complete(_ context.Context, req Request) (Response, error) {
	f.gotRequests = append(f.gotRequests, req)
	i := f.calls
	f.calls++
	if f.err != nil {
		return Response{}, f.err
	}
	if i >= len(f.responses) {
		return Response{}, fmt.Errorf("fakeCompleter: no scripted response for call %d", i)
	}
	return f.responses[i], nil
}

// fakeTool is a Tool whose behavior is supplied by a closure.
type fakeTool struct {
	name string
	fn   func(ctx context.Context, args json.RawMessage) (string, error)
}

func (t fakeTool) Definition() ToolDef {
	return ToolDef{Name: t.name, Description: "fake tool", Parameters: json.RawMessage(`{"type":"object"}`)}
}

func (t fakeTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return t.fn(ctx, args)
}

func toolCallResp(id, name, args string) Response {
	return Response{
		FinishReason: "tool_calls",
		ToolCalls:    []ToolCall{{ID: id, Name: name, Arguments: json.RawMessage(args)}},
	}
}

func TestLoop_StopsOnFinalText(t *testing.T) {
	fc := &fakeCompleter{responses: []Response{{Content: "hello there", FinishReason: "stop"}}}
	loop := NewLoop(fc, LoopConfig{Model: "m", SystemPrompt: "be brief"})

	res, err := loop.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalText != "hello there" {
		t.Errorf("FinalText = %q, want %q", res.FinalText, "hello there")
	}
	if res.StopReason != StopFinished {
		t.Errorf("StopReason = %q, want %q", res.StopReason, StopFinished)
	}
	if fc.calls != 1 {
		t.Errorf("completer calls = %d, want 1", fc.calls)
	}
	// First request must carry the system prompt then the user message.
	msgs := fc.gotRequests[0].Messages
	if len(msgs) != 2 || msgs[0].Role != "system" || msgs[0].Content != "be brief" {
		t.Fatalf("messages[0] = %+v, want system/be brief", msgs)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "hi" {
		t.Errorf("messages[1] = %+v, want user/hi", msgs[1])
	}
	// Tool definitions must be advertised in the request.
}

func TestLoop_ExecutesToolCallThenStops(t *testing.T) {
	fc := &fakeCompleter{responses: []Response{
		toolCallResp("call_1", "bash", `{"command":"ls"}`),
		{Content: "I listed the files", FinishReason: "stop"},
	}}
	var gotArgs string
	bash := fakeTool{name: "bash", fn: func(_ context.Context, args json.RawMessage) (string, error) {
		gotArgs = string(args)
		return "file1\nfile2", nil
	}}
	loop := NewLoop(fc, LoopConfig{Model: "m", SystemPrompt: "sys", Tools: []Tool{bash}})

	res, err := loop.Run(context.Background(), "list files")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalText != "I listed the files" {
		t.Errorf("FinalText = %q", res.FinalText)
	}
	if gotArgs != `{"command":"ls"}` {
		t.Errorf("tool received args %q, want %q", gotArgs, `{"command":"ls"}`)
	}
	// The request advertises the tool.
	if len(fc.gotRequests[0].Tools) != 1 || fc.gotRequests[0].Tools[0].Name != "bash" {
		t.Errorf("request tools = %+v, want [bash]", fc.gotRequests[0].Tools)
	}
	// Second request must include assistant(tool_calls) then tool(result).
	msgs := fc.gotRequests[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || last.ToolCallID != "call_1" || last.Content != "file1\nfile2" {
		t.Errorf("last message = %+v, want tool/call_1/result", last)
	}
	asst := msgs[len(msgs)-2]
	if asst.Role != "assistant" || len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "call_1" {
		t.Errorf("assistant message = %+v, want tool_calls call_1", asst)
	}
}

func TestLoop_HonorsMaxIterations(t *testing.T) {
	// Model never stops calling tools.
	fc := &fakeCompleter{responses: []Response{
		toolCallResp("c1", "noop", `{}`),
		toolCallResp("c2", "noop", `{}`),
		toolCallResp("c3", "noop", `{}`),
		toolCallResp("c4", "noop", `{}`),
	}}
	var execCount int
	noop := fakeTool{name: "noop", fn: func(_ context.Context, _ json.RawMessage) (string, error) {
		execCount++
		return "ok", nil
	}}
	loop := NewLoop(fc, LoopConfig{Model: "m", Tools: []Tool{noop}, MaxIterations: 3})

	res, err := loop.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.StopReason != StopMaxIterations {
		t.Errorf("StopReason = %q, want %q", res.StopReason, StopMaxIterations)
	}
	if fc.calls != 3 {
		t.Errorf("completer calls = %d, want 3 (maxIterations)", fc.calls)
	}
	if execCount != 3 {
		t.Errorf("tool executions = %d, want 3", execCount)
	}
}

func TestLoop_DefaultsMaxIterations(t *testing.T) {
	loop := NewLoop(&fakeCompleter{}, LoopConfig{Model: "m"})
	if loop.maxIterations != 30 {
		t.Errorf("default maxIterations = %d, want 30", loop.maxIterations)
	}
}

func TestLoop_ToolErrorReturnedToModel(t *testing.T) {
	fc := &fakeCompleter{responses: []Response{
		toolCallResp("c1", "bash", `{"command":"boom"}`),
		{Content: "recovered", FinishReason: "stop"},
	}}
	bash := fakeTool{name: "bash", fn: func(_ context.Context, _ json.RawMessage) (string, error) {
		return "", errors.New("command exited 1")
	}}
	loop := NewLoop(fc, LoopConfig{Model: "m", Tools: []Tool{bash}})

	res, err := loop.Run(context.Background(), "do it")
	if err != nil {
		t.Fatalf("Run should not abort on tool error: %v", err)
	}
	if res.FinalText != "recovered" {
		t.Errorf("FinalText = %q, want recovered", res.FinalText)
	}
	// The tool error must be fed back to the model as the tool result.
	toolMsg := fc.gotRequests[1].Messages[len(fc.gotRequests[1].Messages)-1]
	if toolMsg.Role != "tool" || !strings.Contains(toolMsg.Content, "command exited 1") {
		t.Errorf("tool result = %+v, want error text fed back", toolMsg)
	}
}

func TestLoop_UnknownToolReturnedToModel(t *testing.T) {
	fc := &fakeCompleter{responses: []Response{
		toolCallResp("c1", "does_not_exist", `{}`),
		{Content: "ok", FinishReason: "stop"},
	}}
	loop := NewLoop(fc, LoopConfig{Model: "m", Tools: []Tool{
		fakeTool{name: "bash", fn: func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil }},
	}})

	res, err := loop.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalText != "ok" {
		t.Errorf("FinalText = %q", res.FinalText)
	}
	toolMsg := fc.gotRequests[1].Messages[len(fc.gotRequests[1].Messages)-1]
	if toolMsg.Role != "tool" || !strings.Contains(strings.ToLower(toolMsg.Content), "unknown tool") {
		t.Errorf("tool result = %+v, want unknown-tool error", toolMsg)
	}
}

func TestLoop_AggregatesUsage(t *testing.T) {
	fc := &fakeCompleter{responses: []Response{
		{FinishReason: "tool_calls", ToolCalls: []ToolCall{{ID: "c1", Name: "noop", Arguments: json.RawMessage(`{}`)}},
			Usage: Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14}},
		{Content: "done", FinishReason: "stop", Usage: Usage{PromptTokens: 20, CompletionTokens: 6, TotalTokens: 26}},
	}}
	noop := fakeTool{name: "noop", fn: func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil }}
	loop := NewLoop(fc, LoopConfig{Model: "m", Tools: []Tool{noop}})

	res, err := loop.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Usage.TotalTokens != 40 || res.Usage.PromptTokens != 30 || res.Usage.CompletionTokens != 10 {
		t.Errorf("aggregated Usage = %+v, want {30 10 40}", res.Usage)
	}
}

func TestLoop_PropagatesClientError(t *testing.T) {
	fc := &fakeCompleter{err: &RateLimitError{RetryAfter: 5, Message: "slow"}}
	loop := NewLoop(fc, LoopConfig{Model: "m"})

	_, err := loop.Run(context.Background(), "go")
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("error = %v (%T), want *RateLimitError to propagate", err, err)
	}
}

func TestLoop_MultiTurnPreservesHistory(t *testing.T) {
	fc := &fakeCompleter{responses: []Response{
		{Content: "first answer", FinishReason: "stop"},
		{Content: "second answer", FinishReason: "stop"},
	}}
	loop := NewLoop(fc, LoopConfig{Model: "m", SystemPrompt: "sys"})

	if _, err := loop.Run(context.Background(), "first question"); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	if _, err := loop.Run(context.Background(), "second question"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	// Second turn's request must carry the whole prior conversation.
	msgs := fc.gotRequests[1].Messages
	var roles []string
	for _, m := range msgs {
		roles = append(roles, m.Role+":"+m.Content)
	}
	want := []string{"system:sys", "user:first question", "assistant:first answer", "user:second question"}
	if strings.Join(roles, "|") != strings.Join(want, "|") {
		t.Errorf("turn-2 messages = %v, want %v", roles, want)
	}
}

func TestLoop_EmitsEvents(t *testing.T) {
	fc := &fakeCompleter{responses: []Response{
		toolCallResp("c1", "noop", `{"x":1}`),
		{Content: "all done", FinishReason: "stop"},
	}}
	noop := fakeTool{name: "noop", fn: func(_ context.Context, _ json.RawMessage) (string, error) { return "tool-output", nil }}
	events := make(chan Event, 64)
	loop := NewLoop(fc, LoopConfig{Model: "m", Tools: []Tool{noop}, Events: events})

	if _, err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	close(events)
	var types []EventType
	for ev := range events {
		types = append(types, ev.Type)
	}
	// Expect at least: a tool-call start, a tool-call end, and a final event.
	has := func(want EventType) bool {
		for _, ty := range types {
			if ty == want {
				return true
			}
		}
		return false
	}
	if !has(EventToolCallStart) || !has(EventToolCallEnd) || !has(EventFinal) {
		t.Errorf("event types = %v, want to include tool_call_start, tool_call_end, final", types)
	}
}
