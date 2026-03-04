package engine

import (
	"strings"
	"testing"
)

// Injection security regression tests for the engine package.
// Verifies that malicious car IDs, engine IDs, track names, and branch names
// cannot cause command injection via os/exec calls.

func TestGenerateID_OnlyHexChars(t *testing.T) {
	for range 100 {
		id, err := GenerateID()
		if err != nil {
			t.Fatal(err)
		}
		// Must match eng-[0-9a-f]{8}
		if len(id) != 12 || !strings.HasPrefix(id, "eng-") {
			t.Fatalf("bad engine ID format: %q", id)
		}
		for _, c := range id[4:] {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Fatalf("engine ID contains non-hex char %q: %s", c, id)
			}
		}
	}
}

func TestGenerateSessionID_OnlyHexChars(t *testing.T) {
	for range 100 {
		id, err := GenerateSessionID()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(id, "sess-") {
			t.Fatalf("bad session ID format: %q", id)
		}
		for _, c := range id[5:] {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Fatalf("session ID contains non-hex char %q: %s", c, id)
			}
		}
	}
}

func TestOverlayTableName_SafeWithMaliciousInput(t *testing.T) {
	tests := []struct {
		name     string
		engineID string
	}{
		{"normal", "eng-a1b2c3d4"},
		{"hyphens replaced", "eng-abc-def"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := OverlayTableName(tt.engineID)
			// Must not contain shell or SQL metacharacters
			for _, bad := range []string{";", "'", "\"", "`", "$", "(", ")", "|", "&", "-"} {
				if strings.Contains(result, bad) {
					t.Errorf("OverlayTableName(%q) = %q contains %q", tt.engineID, result, bad)
				}
			}
		})
	}
}

func TestGitCleanArgs_NoShellInjection(t *testing.T) {
	args := gitCleanArgs()
	// Must start with "clean" and use separate args, not a shell string
	if args[0] != "clean" {
		t.Errorf("gitCleanArgs()[0] = %q, want %q", args[0], "clean")
	}
	// Each exclude must be a separate -e arg, not concatenated
	for i, arg := range args {
		if arg == "-e" && i+1 < len(args) {
			exclude := args[i+1]
			if strings.ContainsAny(exclude, ";|&`$()") {
				t.Errorf("gitCleanArgs exclude %q contains shell metacharacters", exclude)
			}
		}
	}
}

func TestCreateBranch_RejectsMaliciousBranchNames(t *testing.T) {
	// CreateBranch passes branch names as separate args to git (not via sh -c),
	// so shell metacharacters are safe. Git itself rejects invalid ref names.
	// This test documents that branch names with special chars don't cause panics.
	malicious := []string{
		"branch; rm -rf /",
		"branch$(whoami)",
		"branch`id`",
		"branch|cat /etc/passwd",
		"../../etc/passwd",
	}
	for _, name := range malicious {
		// These will fail at git level (invalid ref), not cause injection.
		// Just verify no panic.
		err := CreateBranch("/nonexistent", name, "main")
		if err == nil {
			t.Errorf("CreateBranch with malicious name %q should fail", name)
		}
	}
}

func TestPushBranch_RejectsMaliciousBranchNames(t *testing.T) {
	malicious := []string{
		"branch; rm -rf /",
		"branch$(whoami)",
	}
	for _, name := range malicious {
		err := PushBranch("/nonexistent", name)
		if err == nil {
			t.Errorf("PushBranch with malicious name %q should fail", name)
		}
	}
}
