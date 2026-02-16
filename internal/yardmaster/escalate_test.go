package yardmaster

import (
	"strings"
	"testing"
)

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
