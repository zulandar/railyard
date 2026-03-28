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
