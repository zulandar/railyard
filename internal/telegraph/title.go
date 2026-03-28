package telegraph

import (
	"context"
	"fmt"
	"strings"
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

// TitleAI abstracts an AI provider that can run a text prompt and return a
// response. This is the same shape as bull.TriageAI but defined here to
// avoid a cross-package dependency.
type TitleAI interface {
	RunPrompt(ctx context.Context, prompt string) (string, error)
}

const titlePrompt = "Generate a concise 5-10 word thread title for the following message. " +
	"Reply with only the title, no punctuation, no quotes.\n\nMessage: %s"

// AITitleGenerator uses any AI provider (via TitleAI) to generate a concise
// thread title. It replaces the previous Claude-specific implementation,
// allowing any configured agent provider to perform title generation.
type AITitleGenerator struct {
	ai TitleAI
}

// NewAITitleGenerator creates an AITitleGenerator backed by the given AI provider.
func NewAITitleGenerator(ai TitleAI) *AITitleGenerator {
	return &AITitleGenerator{ai: ai}
}

// GenerateTitle sends a title-generation prompt to the AI provider and returns
// a short descriptive title.
func (g *AITitleGenerator) GenerateTitle(ctx context.Context, body string) (string, error) {
	if g.ai == nil {
		return "", fmt.Errorf("telegraph: title: no AI provider configured")
	}
	resp, err := g.ai.RunPrompt(ctx, fmt.Sprintf(titlePrompt, body))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp), nil
}
