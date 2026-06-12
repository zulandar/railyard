package yardmaster

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/config"
)

// fakeCompleter is a canned-response agentloop.Completer that records the
// request it received.
type fakeCompleter struct {
	resp agentloop.Response
	err  error
	got  agentloop.Request
}

func (f *fakeCompleter) Complete(_ context.Context, req agentloop.Request) (agentloop.Response, error) {
	f.got = req
	return f.resp, f.err
}

func TestEscalateToAgent_NativeLoop(t *testing.T) {
	fake := &fakeCompleter{resp: agentloop.Response{Content: "GUIDANCE:check the logs"}}
	orig := resolveBackend
	resolveBackend = func(cfg *config.Config) (agentloop.Completer, bool, error) {
		return fake, true, nil
	}
	t.Cleanup(func() { resolveBackend = orig })

	result, err := EscalateToAgent(context.Background(), EscalateOpts{
		Config:   &config.Config{AuthMethod: "openrouter"},
		Reason:   "stuck",
		Details:  "tests keep failing",
		Model:    "test-model",
		CarID:    "car-1",
		EngineID: "eng-1",
	})
	if err != nil {
		t.Fatalf("EscalateToAgent: %v", err)
	}
	if result.Action != EscalateGuidance {
		t.Errorf("action = %q, want %q", result.Action, EscalateGuidance)
	}
	if result.Message != "check the logs" {
		t.Errorf("message = %q, want %q", result.Message, "check the logs")
	}
	if fake.got.Model != "test-model" {
		t.Errorf("model = %q, want %q", fake.got.Model, "test-model")
	}
	if len(fake.got.Messages) != 1 || !strings.Contains(fake.got.Messages[0].Content, "tests keep failing") {
		t.Errorf("prompt should contain the escalation details, got %+v", fake.got.Messages)
	}
}

func TestEscalateToAgent_NativeResolveError(t *testing.T) {
	orig := resolveBackend
	resolveBackend = func(cfg *config.Config) (agentloop.Completer, bool, error) {
		return nil, true, errors.New("OPENROUTER_API_KEY not set")
	}
	t.Cleanup(func() { resolveBackend = orig })

	_, err := EscalateToAgent(context.Background(), EscalateOpts{
		Config: &config.Config{AuthMethod: "openrouter"},
		Reason: "stuck",
	})
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Errorf("expected resolve error to surface, got %v", err)
	}
}

func TestEscalateToAgent_NativeCompleteError(t *testing.T) {
	fake := &fakeCompleter{err: errors.New("upstream 503")}
	orig := resolveBackend
	resolveBackend = func(cfg *config.Config) (agentloop.Completer, bool, error) {
		return fake, true, nil
	}
	t.Cleanup(func() { resolveBackend = orig })

	_, err := EscalateToAgent(context.Background(), EscalateOpts{
		Config: &config.Config{AuthMethod: "openrouter"},
		Reason: "stuck",
	})
	if err == nil || !strings.Contains(err.Error(), "upstream 503") {
		t.Errorf("expected completion error to surface, got %v", err)
	}
}

func TestEscalateToAgent_NonNativeUsesCLIProvider(t *testing.T) {
	orig := resolveBackend
	resolveBackend = func(cfg *config.Config) (agentloop.Completer, bool, error) {
		return nil, false, nil // non-native method: fall back to CLI provider
	}
	t.Cleanup(func() { resolveBackend = orig })

	// An unknown provider name proves the CLI path was taken: it fails at
	// provider resolution, before spawning anything.
	_, err := EscalateToAgent(context.Background(), EscalateOpts{
		Config:       &config.Config{AuthMethod: "api_key"},
		ProviderName: "no-such-provider",
		Reason:       "stuck",
	})
	if err == nil || !strings.Contains(err.Error(), "resolve provider") {
		t.Errorf("expected CLI provider resolution error, got %v", err)
	}
}

func TestEscalateToAgent_NilConfigUsesCLIProvider(t *testing.T) {
	// Callers that predate the Config field (nil Config) must keep the
	// CLI-provider behavior; resolveBackend must not be consulted.
	orig := resolveBackend
	resolveBackend = func(cfg *config.Config) (agentloop.Completer, bool, error) {
		t.Fatal("resolveBackend must not be called with nil Config")
		return nil, false, nil
	}
	t.Cleanup(func() { resolveBackend = orig })

	_, err := EscalateToAgent(context.Background(), EscalateOpts{
		ProviderName: "no-such-provider",
		Reason:       "stuck",
	})
	if err == nil || !strings.Contains(err.Error(), "resolve provider") {
		t.Errorf("expected CLI provider resolution error, got %v", err)
	}
}

func TestBuildEscalationPrompt_ContainsCarDetails(t *testing.T) {
	// Without a DB, car details are skipped but base prompt still works.
	prompt := buildEscalationPrompt(EscalateOpts{
		CarID:    "car-abc",
		EngineID: "eng-001",
		Reason:   "help",
		Details:  "stuck on test failure",
	})

	if !strings.Contains(prompt, "Yardmaster supervisor") {
		t.Error("prompt should contain Yardmaster supervisor header")
	}
	if !strings.Contains(prompt, "eng-001") {
		t.Error("prompt should contain engine ID")
	}
	if !strings.Contains(prompt, "help") {
		t.Error("prompt should contain reason")
	}
	if !strings.Contains(prompt, "stuck on test failure") {
		t.Error("prompt should contain details")
	}
	if !strings.Contains(prompt, "REASSIGN") {
		t.Error("prompt should list REASSIGN action")
	}
	if !strings.Contains(prompt, "GUIDANCE") {
		t.Error("prompt should list GUIDANCE action")
	}
	if !strings.Contains(prompt, "ESCALATE_HUMAN") {
		t.Error("prompt should list ESCALATE_HUMAN action")
	}
}

func TestBuildEscalationPrompt_NoDetails(t *testing.T) {
	prompt := buildEscalationPrompt(EscalateOpts{
		Reason: "stuck",
	})

	if !strings.Contains(prompt, "stuck") {
		t.Error("prompt should contain reason")
	}
	if !strings.Contains(prompt, "Available Actions") {
		t.Error("prompt should contain actions section")
	}
}

func TestParseEscalateResponse_Reassign(t *testing.T) {
	result := parseEscalateResponse("REASSIGN")
	if result.Action != EscalateReassign {
		t.Errorf("action = %q, want %q", result.Action, EscalateReassign)
	}
}

func TestParseEscalateResponse_Guidance(t *testing.T) {
	result := parseEscalateResponse("GUIDANCE:Try running the tests in isolation")
	if result.Action != EscalateGuidance {
		t.Errorf("action = %q, want %q", result.Action, EscalateGuidance)
	}
	if result.Message != "Try running the tests in isolation" {
		t.Errorf("message = %q, want %q", result.Message, "Try running the tests in isolation")
	}
}

func TestParseEscalateResponse_EscalateHuman(t *testing.T) {
	result := parseEscalateResponse("ESCALATE_HUMAN:Database migration needed")
	if result.Action != EscalateHuman {
		t.Errorf("action = %q, want %q", result.Action, EscalateHuman)
	}
	if result.Message != "Database migration needed" {
		t.Errorf("message = %q, want %q", result.Message, "Database migration needed")
	}
}

func TestParseEscalateResponse_Retry(t *testing.T) {
	result := parseEscalateResponse("RETRY")
	if result.Action != EscalateRetry {
		t.Errorf("action = %q, want %q", result.Action, EscalateRetry)
	}
}

func TestParseEscalateResponse_Skip(t *testing.T) {
	result := parseEscalateResponse("SKIP")
	if result.Action != EscalateSkip {
		t.Errorf("action = %q, want %q", result.Action, EscalateSkip)
	}
}

func TestParseEscalateResponse_MultilineOutput(t *testing.T) {
	output := "Let me think about this...\nBased on the context:\nGUIDANCE:Check the database connection string"
	result := parseEscalateResponse(output)
	if result.Action != EscalateGuidance {
		t.Errorf("action = %q, want %q", result.Action, EscalateGuidance)
	}
	if result.Message != "Check the database connection string" {
		t.Errorf("message = %q", result.Message)
	}
}

func TestParseEscalateResponse_MalformedFallback(t *testing.T) {
	result := parseEscalateResponse("I'm not sure what to do here")
	if result.Action != EscalateSkip {
		t.Errorf("action = %q, want %q for malformed input", result.Action, EscalateSkip)
	}
	if result.Message != "unrecognized response" {
		t.Errorf("message = %q, want %q", result.Message, "unrecognized response")
	}
}

func TestParseEscalateResponse_EmptyInput(t *testing.T) {
	result := parseEscalateResponse("")
	if result.Action != EscalateSkip {
		t.Errorf("action = %q, want %q for empty input", result.Action, EscalateSkip)
	}
}

func TestParseEscalateResponse_WhitespaceAround(t *testing.T) {
	result := parseEscalateResponse("  REASSIGN  \n")
	if result.Action != EscalateReassign {
		t.Errorf("action = %q, want %q", result.Action, EscalateReassign)
	}
}

func TestEscalateActions_Values(t *testing.T) {
	tests := []struct {
		action EscalateAction
		want   string
	}{
		{EscalateReassign, "REASSIGN"},
		{EscalateGuidance, "GUIDANCE"},
		{EscalateHuman, "ESCALATE_HUMAN"},
		{EscalateRetry, "RETRY"},
		{EscalateSkip, "SKIP"},
	}

	for _, tt := range tests {
		if string(tt.action) != tt.want {
			t.Errorf("action %q != %q", tt.action, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// EscalationTracker tests
// ---------------------------------------------------------------------------

func TestEscalationTracker_FirstCallAllowed(t *testing.T) {
	et := NewEscalationTracker(10 * time.Minute)
	if !et.ShouldEscalate("car-1") {
		t.Error("first call to ShouldEscalate should return true")
	}
}

func TestEscalationTracker_CooldownBlocks(t *testing.T) {
	et := NewEscalationTracker(10 * time.Minute)
	if !et.ShouldEscalate("car-1") {
		t.Fatal("first call should return true")
	}
	if et.ShouldEscalate("car-1") {
		t.Error("second call within cooldown should return false")
	}
}

func TestEscalationTracker_CooldownExpires(t *testing.T) {
	et := NewEscalationTracker(10 * time.Millisecond)
	if !et.ShouldEscalate("car-1") {
		t.Fatal("first call should return true")
	}
	time.Sleep(20 * time.Millisecond)
	if !et.ShouldEscalate("car-1") {
		t.Error("call after cooldown expiry should return true")
	}
}

func TestEscalationTracker_DifferentCars(t *testing.T) {
	et := NewEscalationTracker(10 * time.Minute)
	if !et.ShouldEscalate("car-1") {
		t.Fatal("car-1 first call should return true")
	}
	if !et.ShouldEscalate("car-2") {
		t.Error("car-2 should be allowed even though car-1 is in cooldown")
	}
}

func TestEscalationTracker_Reset(t *testing.T) {
	et := NewEscalationTracker(10 * time.Minute)
	if !et.ShouldEscalate("car-1") {
		t.Fatal("first call should return true")
	}
	if et.ShouldEscalate("car-1") {
		t.Fatal("second call within cooldown should return false")
	}
	et.Reset("car-1")
	if !et.ShouldEscalate("car-1") {
		t.Error("call after Reset should return true")
	}
}

func TestEscalationTracker_ConcurrentAccess(t *testing.T) {
	et := NewEscalationTracker(10 * time.Millisecond)
	var wg sync.WaitGroup
	var allowed atomic.Int64

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if et.ShouldEscalate("car-race") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	// At least one goroutine should have been allowed; the exact count
	// depends on timing but there must be no race detector failures.
	if allowed.Load() < 1 {
		t.Errorf("expected at least 1 allowed escalation, got %d", allowed.Load())
	}
}
