package telegraph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// TitleGenerator generates a short descriptive title for a new thread from
// the user's opening message. The interface is kept minimal so it is
// trivially stubbable in tests — CI never needs a real API key.
type TitleGenerator interface {
	GenerateTitle(ctx context.Context, body string) (string, error)
}

// generateThreadTitle returns a short title for a new dispatch thread
// derived from body. It asks gen for a 5-10 word summary; if gen is nil,
// returns an error, or returns an empty string, the fallback is the first
// 60 characters of body (trimmed). The result is never empty.
func generateThreadTitle(ctx context.Context, gen TitleGenerator, body string) string {
	if gen != nil {
		title, err := gen.GenerateTitle(ctx, body)
		if err == nil && strings.TrimSpace(title) != "" {
			return strings.TrimSpace(title)
		}
	}
	return fallbackTitle(body)
}

// fallbackTitle returns the first 60 runes of body (trimmed). Falls back to
// "Dispatch" when body is empty so the thread name is never a blank string.
func fallbackTitle(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return "Dispatch"
	}
	const maxRunes = 60
	if utf8.RuneCountInString(body) <= maxRunes {
		return body
	}
	// Truncate at the 60th rune boundary.
	i := 0
	for j := range body {
		if i == maxRunes {
			return strings.TrimSpace(body[:j])
		}
		i++
	}
	return strings.TrimSpace(body)
}

// ClaudeTitleGenerator calls the Anthropic Messages API to generate a
// concise 5-10 word title from a user message. It reads ANTHROPIC_API_KEY
// from the environment (or the APIKey field) and times out after 5 seconds.
type ClaudeTitleGenerator struct {
	APIKey string
	client *http.Client
}

// NewClaudeTitleGenerator creates a ClaudeTitleGenerator. If apiKey is empty,
// ANTHROPIC_API_KEY is read from the environment.
func NewClaudeTitleGenerator(apiKey string) *ClaudeTitleGenerator {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	return &ClaudeTitleGenerator{
		APIKey: apiKey,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

const titlePrompt = "Generate a concise 5-10 word thread title for the following message. " +
	"Reply with only the title, no punctuation, no quotes.\n\nMessage: %s"

// GenerateTitle calls the Claude API and returns a short descriptive title.
func (g *ClaudeTitleGenerator) GenerateTitle(ctx context.Context, body string) (string, error) {
	if g.APIKey == "" {
		return "", fmt.Errorf("telegraph: title: no ANTHROPIC_API_KEY configured")
	}

	reqBody, err := json.Marshal(anthropicRequest{
		Model:     "claude-haiku-4-5",
		MaxTokens: 30,
		Messages: []anthropicMessage{
			{Role: "user", Content: fmt.Sprintf(titlePrompt, body)},
		},
	})
	if err != nil {
		return "", fmt.Errorf("telegraph: title: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("telegraph: title: create request: %w", err)
	}
	req.Header.Set("x-api-key", g.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("telegraph: title: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("telegraph: title: api error %d: %s", resp.StatusCode, b)
	}

	var apiResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", fmt.Errorf("telegraph: title: decode response: %w", err)
	}
	if len(apiResp.Content) == 0 {
		return "", fmt.Errorf("telegraph: title: empty response from API")
	}
	return strings.TrimSpace(apiResp.Content[0].Text), nil
}
