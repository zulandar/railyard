package telegraph

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubTitleGenerator is a minimal TitleGenerator for unit tests.
// It returns the configured title and/or error on every call.
type stubTitleGenerator struct {
	title string
	err   error
}

func (s *stubTitleGenerator) GenerateTitle(_ context.Context, _ string) (string, error) {
	return s.title, s.err
}

// --- generateThreadTitle tests ---

func TestGenerateThreadTitle_HappyPath(t *testing.T) {
	gen := &stubTitleGenerator{title: "Add Login Endpoint"}
	got := generateThreadTitle(context.Background(), gen, "can you add a login endpoint to the API?")
	if got != "Add Login Endpoint" {
		t.Errorf("generateThreadTitle = %q, want %q", got, "Add Login Endpoint")
	}
}

func TestGenerateThreadTitle_ErrorFallsBackToBody(t *testing.T) {
	gen := &stubTitleGenerator{err: errors.New("api unavailable")}
	body := "close out the epic for the auth module"
	got := generateThreadTitle(context.Background(), gen, body)
	if got != body {
		t.Errorf("generateThreadTitle = %q, want %q (body fallback)", got, body)
	}
}

func TestGenerateThreadTitle_EmptyStringFallsBackToBody(t *testing.T) {
	gen := &stubTitleGenerator{title: ""}
	body := "deploy the staging environment"
	got := generateThreadTitle(context.Background(), gen, body)
	if got != body {
		t.Errorf("generateThreadTitle = %q, want %q (empty title fallback)", got, body)
	}
}

func TestGenerateThreadTitle_WhitespaceOnlyTitleFallsBack(t *testing.T) {
	gen := &stubTitleGenerator{title: "   "}
	body := "run the migrations"
	got := generateThreadTitle(context.Background(), gen, body)
	if got != body {
		t.Errorf("generateThreadTitle = %q, want %q (whitespace title fallback)", got, body)
	}
}

func TestGenerateThreadTitle_NilGenFallsBackToBody(t *testing.T) {
	body := "what is the status of the backend cars?"
	got := generateThreadTitle(context.Background(), nil, body)
	if got != body {
		t.Errorf("generateThreadTitle = %q, want %q (nil gen fallback)", got, body)
	}
}

func TestGenerateThreadTitle_LongBodyTruncatesAt60Chars(t *testing.T) {
	gen := &stubTitleGenerator{err: errors.New("fail")}
	body := "this is a very long message that exceeds sixty characters in total length and should be truncated"
	got := generateThreadTitle(context.Background(), gen, body)

	// Result must not exceed 60 runes.
	runes := []rune(got)
	if len(runes) > 60 {
		t.Errorf("generateThreadTitle length = %d runes, want ≤ 60; got %q", len(runes), got)
	}
	// Result must be a prefix of the body.
	if !strings.HasPrefix(body, got) {
		t.Errorf("truncated title %q is not a prefix of body", got)
	}
}

func TestGenerateThreadTitle_EmptyBodyReturnsDispatch(t *testing.T) {
	got := generateThreadTitle(context.Background(), nil, "")
	if got != "Dispatch" {
		t.Errorf("generateThreadTitle with empty body = %q, want %q", got, "Dispatch")
	}
}

func TestGenerateThreadTitle_Exactly60CharsNotTruncated(t *testing.T) {
	body := strings.Repeat("a", 60)
	got := generateThreadTitle(context.Background(), nil, body)
	if got != body {
		t.Errorf("generateThreadTitle 60-char body = %q, want unchanged", got)
	}
}

func TestGenerateThreadTitle_61CharsIsTruncated(t *testing.T) {
	body := strings.Repeat("b", 61)
	got := generateThreadTitle(context.Background(), nil, body)
	if len([]rune(got)) != 60 {
		t.Errorf("generateThreadTitle 61-char body: got %d runes, want 60", len([]rune(got)))
	}
}

// --- AITitleGenerator tests ---

// mockTitleAI implements TitleAI for testing AITitleGenerator.
type mockTitleAI struct {
	response string
	err      error
	prompt   string // last prompt received
}

func (m *mockTitleAI) RunPrompt(_ context.Context, prompt string) (string, error) {
	m.prompt = prompt
	return m.response, m.err
}

// Fix #7: AITitleGenerator should use any AI provider, not just Claude.
func TestAITitleGenerator_UsesAIProvider(t *testing.T) {
	ai := &mockTitleAI{response: "Deploy Staging Env"}
	gen := NewAITitleGenerator(ai)

	title, err := gen.GenerateTitle(context.Background(), "please deploy the staging environment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "Deploy Staging Env" {
		t.Errorf("title = %q, want %q", title, "Deploy Staging Env")
	}
	// Verify prompt was sent to the AI.
	if ai.prompt == "" {
		t.Error("expected AI to receive a prompt")
	}
}

func TestAITitleGenerator_PropagatesError(t *testing.T) {
	ai := &mockTitleAI{err: errors.New("provider unavailable")}
	gen := NewAITitleGenerator(ai)

	_, err := gen.GenerateTitle(context.Background(), "test body")
	if err == nil {
		t.Fatal("expected error when AI returns error")
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Errorf("error = %q, want to contain 'provider unavailable'", err.Error())
	}
}

func TestAITitleGenerator_TrimsWhitespace(t *testing.T) {
	ai := &mockTitleAI{response: "  Fix Login Bug  \n"}
	gen := NewAITitleGenerator(ai)

	title, err := gen.GenerateTitle(context.Background(), "fix the login bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "Fix Login Bug" {
		t.Errorf("title = %q, want %q", title, "Fix Login Bug")
	}
}

func TestAITitleGenerator_NilAIReturnsError(t *testing.T) {
	gen := NewAITitleGenerator(nil)

	_, err := gen.GenerateTitle(context.Background(), "test body")
	if err == nil {
		t.Fatal("expected error when AI is nil")
	}
}

// --- fallbackTitle tests ---

func TestFallbackTitle_EmptyReturnsDispatch(t *testing.T) {
	if got := fallbackTitle(""); got != "Dispatch" {
		t.Errorf("fallbackTitle(\"\") = %q, want \"Dispatch\"", got)
	}
}

func TestFallbackTitle_ShortBodyUnchanged(t *testing.T) {
	body := "hello world"
	if got := fallbackTitle(body); got != body {
		t.Errorf("fallbackTitle short body = %q, want %q", got, body)
	}
}

func TestFallbackTitle_LongBodyTruncated(t *testing.T) {
	body := strings.Repeat("x", 100)
	got := fallbackTitle(body)
	if len([]rune(got)) != 60 {
		t.Errorf("fallbackTitle 100-char body = %d runes, want 60", len([]rune(got)))
	}
}
