package yardmaster

import (
	"strings"
	"testing"
)

// --- Switch validation tests ---

func TestSwitch_NilDB(t *testing.T) {
	_, err := Switch(nil, "be-001", SwitchOpts{RepoDir: "/tmp"})
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestSwitch_EmptyBeadID(t *testing.T) {
	_, err := Switch(nil, "", SwitchOpts{RepoDir: "/tmp"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSwitch_EmptyRepoDir(t *testing.T) {
	_, err := Switch(nil, "be-001", SwitchOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSwitchOpts_ZeroValue(t *testing.T) {
	opts := SwitchOpts{}
	if opts.RepoDir != "" || opts.DryRun {
		t.Error("zero-value SwitchOpts should have empty fields")
	}
}

func TestSwitchResult_ZeroValue(t *testing.T) {
	r := SwitchResult{}
	if r.BeadID != "" || r.Branch != "" || r.TestsPassed || r.Merged {
		t.Error("zero-value SwitchResult should have empty/false fields")
	}
}

// --- UnblockDeps validation tests ---

func TestUnblockDeps_NilDB(t *testing.T) {
	_, err := UnblockDeps(nil, "be-001")
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestUnblockDeps_EmptyBeadID(t *testing.T) {
	_, err := UnblockDeps(nil, "")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- CreateReindexJob validation tests ---

func TestCreateReindexJob_NilDB(t *testing.T) {
	err := CreateReindexJob(nil, "backend", "abc123")
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestCreateReindexJob_EmptyTrack(t *testing.T) {
	err := CreateReindexJob(nil, "", "abc123")
	if err == nil {
		t.Fatal("expected error")
	}
}
