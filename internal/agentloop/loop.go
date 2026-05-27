package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
)

// defaultMaxIterations bounds runaway tool/answer cycles (and token burn) when
// a model keeps calling tools without ever finishing.
const defaultMaxIterations = 30

// Tool is a plain-Go, transport-unaware capability the model can invoke.
type Tool interface {
	// Definition returns the function-calling schema advertised to the model.
	Definition() ToolDef
	// Execute runs the tool with the model-supplied JSON arguments and returns
	// the textual result fed back to the model. A returned error is surfaced to
	// the model as the tool result (not a hard abort); the loop continues.
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Completer is the subset of Client the Loop depends on. *Client satisfies it;
// tests substitute a fake.
type Completer interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// EventType classifies a Loop event.
type EventType string

const (
	// EventAssistantText is emitted when the model returns assistant text.
	EventAssistantText EventType = "assistant_text"
	// EventToolCallStart is emitted before a tool executes.
	EventToolCallStart EventType = "tool_call_start"
	// EventToolCallEnd is emitted after a tool executes (success or error).
	EventToolCallEnd EventType = "tool_call_end"
	// EventFinal is emitted once when the loop terminates, carrying final text.
	EventFinal EventType = "final"
	// EventUsage is emitted after each model response with that turn's usage.
	EventUsage EventType = "usage"
)

// Event is a streamed observation from a running Loop. Consumers read these to
// stream output and persist a transcript.
type Event struct {
	Type EventType
	// Text holds assistant text (EventAssistantText) or final text (EventFinal).
	Text string
	// ToolName is set on tool-call events.
	ToolName string
	// ToolArgs is the (possibly truncated) tool arguments on EventToolCallStart.
	ToolArgs string
	// ToolResult is the tool's textual output on EventToolCallEnd.
	ToolResult string
	// ToolError is non-empty when the tool returned an error on EventToolCallEnd.
	ToolError string
	// Usage carries per-turn token usage on EventUsage.
	Usage Usage
}

// StopReason explains why Run returned.
type StopReason string

const (
	// StopFinished means the model produced a final answer (no more tool calls).
	StopFinished StopReason = "finished"
	// StopMaxIterations means the iteration cap was hit before the model finished.
	StopMaxIterations StopReason = "max_iterations"
)

// Result is the outcome of a Run.
type Result struct {
	FinalText  string
	Usage      Usage // aggregated across this Run's model responses
	StopReason StopReason
	Iterations int
}

// LoopConfig configures a Loop. The system prompt is kept deliberately minimal
// (the consumer's RenderPrompt output) — prompt control is the proven reason
// weak models follow tools, so do NOT wrap it in a harness mega-prompt.
type LoopConfig struct {
	Model         string
	SystemPrompt  string
	SeedMessages  []Message
	Tools         []Tool
	MaxIterations int          // <=0 uses defaultMaxIterations
	ToolChoice    string       // optional; "" lets the provider default (auto)
	Events        chan<- Event // optional; nil disables event emission
}

// Loop is the agentic driver. It maintains the conversation history across
// turns, dispatches tool calls, and returns a final answer.
type Loop struct {
	client        Completer
	model         string
	tools         []Tool
	toolDefs      []ToolDef
	toolByName    map[string]Tool
	toolChoice    string
	maxIterations int
	events        chan<- Event

	// messages is the running conversation (system + seed + accumulated turns).
	messages []Message
}

// NewLoop builds a Loop, seeding the conversation with the system prompt
// (if any) followed by SeedMessages.
func NewLoop(client Completer, cfg LoopConfig) *Loop {
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = defaultMaxIterations
	}
	l := &Loop{
		client:        client,
		model:         cfg.Model,
		tools:         cfg.Tools,
		toolChoice:    cfg.ToolChoice,
		maxIterations: maxIter,
		events:        cfg.Events,
		toolByName:    make(map[string]Tool, len(cfg.Tools)),
	}
	for _, t := range cfg.Tools {
		def := t.Definition()
		l.toolDefs = append(l.toolDefs, def)
		l.toolByName[def.Name] = t
	}
	if cfg.SystemPrompt != "" {
		l.messages = append(l.messages, Message{Role: "system", Content: cfg.SystemPrompt})
	}
	l.messages = append(l.messages, cfg.SeedMessages...)
	return l
}

// Run appends userInput (when non-empty) as a user message, drives the model
// to a final answer (executing any requested tools), and returns the result.
// The conversation history persists on the Loop, so Run may be called again
// for a follow-up turn.
func (l *Loop) Run(ctx context.Context, userInput string) (Result, error) {
	if userInput != "" {
		l.messages = append(l.messages, Message{Role: "user", Content: userInput})
	}

	var agg Usage
	for iter := 1; iter <= l.maxIterations; iter++ {
		if err := ctx.Err(); err != nil {
			return Result{Usage: agg, StopReason: StopMaxIterations, Iterations: iter - 1}, err
		}

		resp, err := l.client.Complete(ctx, Request{
			Model:      l.model,
			Messages:   l.messages,
			Tools:      l.toolDefs,
			ToolChoice: l.toolChoice,
		})
		if err != nil {
			return Result{Usage: agg, Iterations: iter - 1}, err
		}

		agg.PromptTokens += resp.Usage.PromptTokens
		agg.CompletionTokens += resp.Usage.CompletionTokens
		agg.TotalTokens += resp.Usage.TotalTokens
		l.emit(ctx, Event{Type: EventUsage, Usage: resp.Usage})

		// Record the assistant turn (text and/or tool calls).
		l.messages = append(l.messages, Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})
		if resp.Content != "" {
			l.emit(ctx, Event{Type: EventAssistantText, Text: resp.Content})
		}

		// No tool calls => the model is done.
		if len(resp.ToolCalls) == 0 {
			l.emit(ctx, Event{Type: EventFinal, Text: resp.Content})
			return Result{FinalText: resp.Content, Usage: agg, StopReason: StopFinished, Iterations: iter}, nil
		}

		// Execute each requested tool and append its result message.
		for _, tc := range resp.ToolCalls {
			l.messages = append(l.messages, Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    l.execTool(ctx, tc),
			})
		}
	}

	// Iteration cap hit: return the last assistant text as a partial answer.
	final := lastAssistantText(l.messages)
	l.emit(ctx, Event{Type: EventFinal, Text: final})
	return Result{FinalText: final, Usage: agg, StopReason: StopMaxIterations, Iterations: l.maxIterations}, nil
}

// execTool runs one tool call, emitting start/end events, and returns the
// textual result to feed back to the model. Unknown tools and execution errors
// are converted into an error result (not a hard abort); the loop continues.
func (l *Loop) execTool(ctx context.Context, tc ToolCall) string {
	l.emit(ctx, Event{Type: EventToolCallStart, ToolName: tc.Name, ToolArgs: truncate(string(tc.Arguments), 512)})

	tool, ok := l.toolByName[tc.Name]
	if !ok {
		result := fmt.Sprintf("error: unknown tool %q", tc.Name)
		l.emit(ctx, Event{Type: EventToolCallEnd, ToolName: tc.Name, ToolError: result})
		return result
	}

	out, err := tool.Execute(ctx, tc.Arguments)
	if err != nil {
		l.emit(ctx, Event{Type: EventToolCallEnd, ToolName: tc.Name, ToolResult: out, ToolError: err.Error()})
		return fmt.Sprintf("error: %s", err.Error())
	}
	l.emit(ctx, Event{Type: EventToolCallEnd, ToolName: tc.Name, ToolResult: out})
	return out
}

// emit sends an event if a channel is configured, abandoning the send if the
// context is cancelled so a slow/absent consumer can't deadlock the loop.
func (l *Loop) emit(ctx context.Context, ev Event) {
	if l.events == nil {
		return
	}
	select {
	case l.events <- ev:
	case <-ctx.Done():
	}
}

func lastAssistantText(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && msgs[i].Content != "" {
			return msgs[i].Content
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
