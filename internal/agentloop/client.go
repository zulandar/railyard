// Package agentloop implements a provider-agnostic agentic loop that drives
// OpenAI-compatible chat APIs (OpenRouter first; any openai_compat endpoint
// second). It owns the HTTP surface (Client), the tool abstraction (Tool), and
// the loop that maintains conversation history and dispatches tool calls
// (Loop). It deliberately depends on no other Railyard package so any consumer
// (telegraph, dispatch, bull, engines) can wire it without import cycles.
package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Message is one entry in the OpenAI-compatible chat history.
type Message struct {
	Role    string // "system", "user", "assistant", or "tool"
	Content string
	// ToolCalls is set on assistant messages that request tool invocations.
	ToolCalls []ToolCall
	// ToolCallID links a role=="tool" result message to the assistant
	// ToolCall.ID it answers.
	ToolCallID string
	// Name is the tool name, set on role=="tool" result messages.
	Name string
}

// ToolCall is a model-requested tool invocation.
type ToolCall struct {
	ID   string
	Name string
	// Arguments is the inner JSON object the model passed (already unquoted
	// from the wire-level string), ready to hand to Tool.Execute.
	Arguments json.RawMessage
}

// ToolDef describes a tool to the model using function-calling schema.
type ToolDef struct {
	Name        string
	Description string
	// Parameters is a JSON Schema object describing the tool's arguments.
	Parameters json.RawMessage
}

// Request is a single chat completion request.
type Request struct {
	Model    string
	Messages []Message
	Tools    []ToolDef
	// ToolChoice is optional ("", "auto", "none", "required").
	ToolChoice string
}

// Usage is token accounting reported by a completion response.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Response is a parsed chat completion.
type Response struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
}

// Credentials carries the resolved endpoint and auth for a Client.
type Credentials struct {
	// BaseURL is the API root WITHOUT a trailing /chat/completions
	// (e.g. https://openrouter.ai/api/v1).
	BaseURL string
	APIKey  string
	// Headers are extra request headers (e.g. OpenRouter HTTP-Referer/X-Title).
	Headers map[string]string
}

// Client is the only HTTP surface in the package.
type Client struct {
	creds          Credentials
	httpClient     *http.Client
	maxRetries     int
	retryBaseDelay time.Duration
}

// Option customizes a Client.
type Option func(*Client)

// WithHTTPClient overrides the HTTP client (useful for tests / custom timeouts).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }

// WithMaxRetries sets how many times a 5xx response is retried (default 3).
func WithMaxRetries(n int) Option { return func(c *Client) { c.maxRetries = n } }

// WithRetryBaseDelay sets the base backoff delay between 5xx retries
// (default 500ms; tests set 0 to avoid sleeping).
func WithRetryBaseDelay(d time.Duration) Option {
	return func(c *Client) { c.retryBaseDelay = d }
}

// NewClient builds a Client from already-resolved credentials.
func NewClient(creds Credentials, opts ...Option) *Client {
	c := &Client{
		creds:          creds,
		httpClient:     &http.Client{Timeout: 5 * time.Minute},
		maxRetries:     3,
		retryBaseDelay: 500 * time.Millisecond,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// RateLimitError is returned for HTTP 429 responses.
type RateLimitError struct {
	RetryAfter time.Duration
	Message    string
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limited (retry after %s): %s", e.RetryAfter, e.Message)
	}
	return fmt.Sprintf("rate limited: %s", e.Message)
}

// CreditError is returned for HTTP 402 (insufficient credits/quota).
type CreditError struct {
	Message string
}

func (e *CreditError) Error() string {
	return fmt.Sprintf("insufficient credits: %s", e.Message)
}

// APIError is returned for other non-2xx responses (and exhausted 5xx retries).
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api error (status %d): %s", e.StatusCode, e.Message)
}

// --- wire types ---

type wireRequest struct {
	Model      string        `json:"model"`
	Messages   []wireMessage `json:"messages"`
	Tools      []wireTool    `json:"tools,omitempty"`
	ToolChoice string        `json:"tool_choice,omitempty"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolCallFunc `json:"function"`
}

type wireToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireResponse struct {
	Choices []struct {
		Message struct {
			Content   string         `json:"content"`
			ToolCalls []wireToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func toWireRequest(req Request) wireRequest {
	wr := wireRequest{Model: req.Model, ToolChoice: req.ToolChoice}
	for _, m := range req.Messages {
		wm := wireMessage{Role: m.Role, ToolCallID: m.ToolCallID, Name: m.Name}
		// Send content as JSON null only for an assistant message that carries
		// tool calls and no text; otherwise always include it (even empty).
		if m.Content != "" || len(m.ToolCalls) == 0 {
			content := m.Content
			wm.Content = &content
		}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: wireToolCallFunc{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			})
		}
		wr.Messages = append(wr.Messages, wm)
	}
	for _, t := range req.Tools {
		wr.Tools = append(wr.Tools, wireTool{
			Type:     "function",
			Function: wireFunction(t),
		})
	}
	return wr
}

// Complete sends a single chat completion request and returns the parsed
// response. 429 -> *RateLimitError, 402 -> *CreditError, other non-2xx ->
// *APIError. 5xx responses are retried with bounded backoff.
func (c *Client) Complete(ctx context.Context, req Request) (Response, error) {
	body, err := json.Marshal(toWireRequest(req))
	if err != nil {
		return Response{}, fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepBackoff(ctx, c.retryBaseDelay, attempt); err != nil {
				return Response{}, err
			}
		}
		resp, retryable, err := c.doOnce(ctx, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable {
			return Response{}, err
		}
	}
	return Response{}, lastErr
}

// doOnce performs a single HTTP attempt. retryable is true only for transport
// errors and 5xx responses.
func (c *Client) doOnce(ctx context.Context, body []byte) (resp Response, retryable bool, err error) {
	url := c.creds.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, false, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.creds.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.creds.APIKey)
	}
	for k, v := range c.creds.Headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Response{}, true, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, true, fmt.Errorf("read response body: %w", err)
	}

	switch {
	case httpResp.StatusCode == http.StatusTooManyRequests:
		return Response{}, false, &RateLimitError{
			RetryAfter: parseRetryAfter(httpResp.Header.Get("Retry-After")),
			Message:    errorMessage(raw),
		}
	case httpResp.StatusCode == http.StatusPaymentRequired:
		return Response{}, false, &CreditError{Message: errorMessage(raw)}
	case httpResp.StatusCode >= 500:
		return Response{}, true, &APIError{StatusCode: httpResp.StatusCode, Message: errorMessage(raw)}
	case httpResp.StatusCode >= 400:
		return Response{}, false, &APIError{StatusCode: httpResp.StatusCode, Message: errorMessage(raw)}
	}

	var wresp wireResponse
	if err := json.Unmarshal(raw, &wresp); err != nil {
		return Response{}, false, fmt.Errorf("decode response: %w (body: %s)", err, raw)
	}
	if wresp.Error != nil {
		// HTTP 2xx with an {"error":...} body is the transient stealth-provider
		// case (notably openrouter/owl-alpha). Treat as retryable so Complete's
		// existing backoff loop covers it — see railyard-0se.
		return Response{}, true, &APIError{StatusCode: httpResp.StatusCode, Message: wresp.Error.Message}
	}
	if len(wresp.Choices) == 0 {
		return Response{}, false, &APIError{StatusCode: httpResp.StatusCode, Message: "response contained no choices"}
	}

	choice := wresp.Choices[0]
	out := Response{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		Usage: Usage{
			PromptTokens:     wresp.Usage.PromptTokens,
			CompletionTokens: wresp.Usage.CompletionTokens,
			TotalTokens:      wresp.Usage.TotalTokens,
		},
	}
	for _, tc := range choice.Message.ToolCalls {
		var args json.RawMessage
		if tc.Function.Arguments != "" {
			args = json.RawMessage(tc.Function.Arguments)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	return out, false, nil
}

// sleepBackoff waits retryBaseDelay * 2^(attempt-1), honoring context cancel.
func sleepBackoff(ctx context.Context, base time.Duration, attempt int) error {
	if base <= 0 {
		return ctx.Err()
	}
	d := base * time.Duration(1<<(attempt-1))
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(h); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

// errorMessage extracts a human-readable message from an error response body,
// falling back to the raw body when it is not the expected {"error":{...}} shape.
func errorMessage(raw []byte) string {
	var body struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err == nil && body.Error != nil && body.Error.Message != "" {
		return body.Error.Message
	}
	if len(raw) == 0 {
		return "(empty body)"
	}
	return string(raw)
}
