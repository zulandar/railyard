package messaging

import (
	"testing"
)

// --- Send validation tests ---

func TestSend_MissingFrom(t *testing.T) {
	_, err := Send(nil, "", "yardmaster", "test", "body", SendOpts{})
	if err == nil {
		t.Fatal("expected error for missing from")
	}
	if got := err.Error(); got != "messaging: from is required" {
		t.Errorf("error = %q", got)
	}
}

func TestSend_MissingTo(t *testing.T) {
	_, err := Send(nil, "eng-abc", "", "test", "body", SendOpts{})
	if err == nil {
		t.Fatal("expected error for missing to")
	}
	if got := err.Error(); got != "messaging: to is required" {
		t.Errorf("error = %q", got)
	}
}

func TestSend_MissingSubject(t *testing.T) {
	_, err := Send(nil, "eng-abc", "yardmaster", "", "body", SendOpts{})
	if err == nil {
		t.Fatal("expected error for missing subject")
	}
	if got := err.Error(); got != "messaging: subject is required" {
		t.Errorf("error = %q", got)
	}
}

// --- Inbox validation tests ---

func TestInbox_MissingAgentID(t *testing.T) {
	_, err := Inbox(nil, "")
	if err == nil {
		t.Fatal("expected error for missing agentID")
	}
	if got := err.Error(); got != "messaging: agentID is required" {
		t.Errorf("error = %q", got)
	}
}

// --- SendOpts defaults ---

func TestSendOpts_DefaultPriority(t *testing.T) {
	opts := SendOpts{}
	if opts.Priority != "" {
		t.Errorf("default Priority = %q, want empty (filled in Send)", opts.Priority)
	}
}
