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

// --- GetThread validation tests ---

func TestGetThread_InvalidID(t *testing.T) {
	_, err := GetThread(nil, 0)
	if err == nil {
		t.Fatal("expected error for threadID=0")
	}
	if got := err.Error(); got != "messaging: threadID is required" {
		t.Errorf("error = %q", got)
	}
}

// --- Reply validation tests ---

func TestReply_MissingFrom(t *testing.T) {
	_, err := Reply(nil, 1, "", "body")
	if err == nil {
		t.Fatal("expected error for missing from")
	}
	if got := err.Error(); got != "messaging: from is required" {
		t.Errorf("error = %q", got)
	}
}

func TestReply_InvalidParent(t *testing.T) {
	_, err := Reply(nil, 0, "eng-abc", "body")
	if err == nil {
		t.Fatal("expected error for parentMsgID=0")
	}
	if got := err.Error(); got != "messaging: parentMsgID is required" {
		t.Errorf("error = %q", got)
	}
}

// --- AcknowledgeBroadcast validation tests ---

func TestAcknowledgeBroadcast_MissingAgentID(t *testing.T) {
	err := AcknowledgeBroadcast(nil, 1, "")
	if err == nil {
		t.Fatal("expected error for missing agentID")
	}
	if got := err.Error(); got != "messaging: agentID is required" {
		t.Errorf("error = %q", got)
	}
}

// --- SendOpts ThreadID ---

func TestSendOpts_ThreadID(t *testing.T) {
	tid := uint(42)
	opts := SendOpts{ThreadID: &tid}
	if opts.ThreadID == nil {
		t.Fatal("expected ThreadID to be set")
	}
	if *opts.ThreadID != 42 {
		t.Errorf("ThreadID = %d, want 42", *opts.ThreadID)
	}
}
