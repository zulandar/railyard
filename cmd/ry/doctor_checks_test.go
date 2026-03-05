package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/orchestration"
)

func writeTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "railyard.yaml")
	content := `owner: test-user
repo: git@github.com:test/repo.git
tracks:
  - name: backend
    language: go
    engine_slots: 3
`
	os.WriteFile(path, []byte(content), 0644)
	return path
}

// ---------------------------------------------------------------------------
// printCheckResult
// ---------------------------------------------------------------------------

func TestPrintCheckResult_Pass(t *testing.T) {
	var buf bytes.Buffer
	r := checkResult{name: "name", status: "PASS", detail: "detail"}
	printCheckResult(&buf, r)
	got := buf.String()
	if got != "[PASS] name: detail\n" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestPrintCheckResult_Fail(t *testing.T) {
	var buf bytes.Buffer
	r := checkResult{name: "name", status: "FAIL", detail: "detail"}
	printCheckResult(&buf, r)
	got := buf.String()
	if got != "[FAIL] name: detail\n" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestPrintCheckResult_Warn(t *testing.T) {
	var buf bytes.Buffer
	r := checkResult{name: "name", status: "WARN", detail: "detail"}
	printCheckResult(&buf, r)
	got := buf.String()
	if got != "[WARN] name: detail\n" {
		t.Fatalf("unexpected output: %q", got)
	}
}

// ---------------------------------------------------------------------------
// checkConfig
// ---------------------------------------------------------------------------

func TestCheckConfig_ValidFile(t *testing.T) {
	path := writeTestConfig(t)
	cfg, r := checkConfig(path)
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if r.status != "PASS" {
		t.Fatalf("expected PASS, got %s: %s", r.status, r.detail)
	}
}

func TestCheckConfig_MissingFile(t *testing.T) {
	cfg, r := checkConfig("/no/such/file/railyard.yaml")
	if cfg != nil {
		t.Fatal("expected nil config for missing file")
	}
	if r.status != "FAIL" {
		t.Fatalf("expected FAIL, got %s: %s", r.status, r.detail)
	}
}

// ---------------------------------------------------------------------------
// binaryLabel
// ---------------------------------------------------------------------------

func TestBinaryLabel(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"go", "Go"},
		{"dolt", "Dolt"},
		{"tmux", "tmux"},
		{"claude", "Claude CLI"},
		{"unknown", "unknown"},
	}
	for _, tc := range cases {
		got := binaryLabel(tc.input)
		if got != tc.want {
			t.Errorf("binaryLabel(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// checkTmuxSession
// ---------------------------------------------------------------------------

func TestCheckTmuxSession_NilTmux(t *testing.T) {
	orig := orchestration.DefaultTmux
	orchestration.DefaultTmux = nil
	defer func() { orchestration.DefaultTmux = orig }()

	cfg := &config.Config{}
	results := checkTmuxSession(cfg)
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	found := false
	for _, r := range results {
		if r.status == "WARN" && strings.Contains(r.detail, "not available") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected WARN about tmux not available, got %+v", results)
	}
}

func TestCheckTmuxSession_NilConfig(t *testing.T) {
	// DefaultTmux is non-nil (RealTmux{}), so the nil-config path runs.
	results := checkTmuxSession(nil)
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	found := false
	for _, r := range results {
		if r.status == "FAIL" && strings.Contains(r.detail, "no config") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected FAIL about no config, got %+v", results)
	}
}

// ---------------------------------------------------------------------------
// checkDoltServer / checkDatabase
// ---------------------------------------------------------------------------

func TestCheckDoltServer_InvalidPort(t *testing.T) {
	r := checkDoltServer("127.0.0.1", 1, "", "")
	if r.status != "FAIL" {
		t.Fatalf("expected FAIL for invalid port, got %s: %s", r.status, r.detail)
	}
}

func TestCheckDatabase_InvalidPort(t *testing.T) {
	r := checkDatabase("127.0.0.1", 1, "testdb", "", "")
	if r.status != "FAIL" {
		t.Fatalf("expected FAIL for invalid port, got %s: %s", r.status, r.detail)
	}
}

// ---------------------------------------------------------------------------
// connectFromConfig
// ---------------------------------------------------------------------------

func TestConnectFromConfig_MissingFile(t *testing.T) {
	_, _, err := connectFromConfig("/no/such/file/railyard.yaml")
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Fatalf("expected error containing 'load config', got: %v", err)
	}
}

func TestConnectFromConfig_InvalidDolt(t *testing.T) {
	path := writeTestConfig(t)
	_, _, err := connectFromConfig(path)
	if err == nil {
		t.Fatal("expected error when Dolt is not running")
	}
}
