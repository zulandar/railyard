package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/config"
	_ "github.com/zulandar/railyard/internal/engine/providers"
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
		{"mysql", "MySQL"},
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
// checkDBServer / checkDatabase
// ---------------------------------------------------------------------------

func TestCheckDBServer_InvalidPort(t *testing.T) {
	r := checkDBServer("127.0.0.1", 1, "", "")
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

func TestConnectFromConfig_InvalidDB(t *testing.T) {
	path := writeTestConfig(t)
	_, _, err := connectFromConfig(path)
	if err == nil {
		t.Fatal("expected error when database is not running")
	}
}

// ---------------------------------------------------------------------------
// checkProviderBinaries
// ---------------------------------------------------------------------------

func TestCheckProviderBinaries_DefaultClaude(t *testing.T) {
	cfg := &config.Config{
		AgentProvider: "claude",
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go", AgentProvider: "claude"},
		},
	}
	results := checkProviderBinaries(cfg)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// Claude may or may not be installed; just verify we get a result.
	if results[0].name != "Provider: claude" {
		t.Errorf("name = %q, want %q", results[0].name, "Provider: claude")
	}
}

func TestCheckProviderBinaries_MultipleProviders(t *testing.T) {
	cfg := &config.Config{
		AgentProvider: "claude",
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go", AgentProvider: "claude"},
			{Name: "frontend", Language: "typescript", AgentProvider: "codex"},
		},
	}
	results := checkProviderBinaries(cfg)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Should have one for claude and one for codex.
	names := map[string]bool{}
	for _, r := range results {
		names[r.name] = true
	}
	if !names["Provider: claude"] {
		t.Error("missing Provider: claude result")
	}
	if !names["Provider: codex"] {
		t.Error("missing Provider: codex result")
	}
}

func TestCheckProviderBinaries_Copilot(t *testing.T) {
	cfg := &config.Config{
		AgentProvider: "copilot",
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go", AgentProvider: "copilot"},
		},
	}
	results := checkProviderBinaries(cfg)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].name != "Provider: copilot" {
		t.Errorf("name = %q, want %q", results[0].name, "Provider: copilot")
	}
	// Provider should be registered (via init import), so status is PASS or
	// WARN (binary missing) — never an "unknown provider" WARN.
	if results[0].status == "WARN" && strings.Contains(results[0].detail, "not registered") {
		t.Error("copilot provider should be registered but got 'not registered'")
	}
}

func TestCheckProviderBinaries_CopilotWithOtherProviders(t *testing.T) {
	cfg := &config.Config{
		AgentProvider: "claude",
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go", AgentProvider: "claude"},
			{Name: "frontend", Language: "typescript", AgentProvider: "copilot"},
		},
	}
	results := checkProviderBinaries(cfg)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	names := map[string]bool{}
	for _, r := range results {
		names[r.name] = true
	}
	if !names["Provider: claude"] {
		t.Error("missing Provider: claude result")
	}
	if !names["Provider: copilot"] {
		t.Error("missing Provider: copilot result")
	}
}

func TestCheckProviderBinaries_UnknownProvider(t *testing.T) {
	cfg := &config.Config{
		AgentProvider: "nonexistent-provider-xyz",
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go", AgentProvider: "nonexistent-provider-xyz"},
		},
	}
	results := checkProviderBinaries(cfg)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].status != "WARN" {
		t.Errorf("status = %q, want WARN for unknown provider", results[0].status)
	}
}

func TestCheckProviderBinaries_EmptyConfig(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{},
	}
	results := checkProviderBinaries(cfg)
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty config, got %d", len(results))
	}
}

func TestProviderInstallHint(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"claude", "install: npm install -g @anthropic-ai/claude-code"},
		{"codex", "install: npm install -g @openai/codex"},
		{"gemini", "install: npm install -g @google/gemini-cli"},
		{"opencode", "install: go install github.com/opencode-ai/opencode@latest"},
		{"copilot", "install: gh extension install github/gh-copilot"},
		{"unknown", `ensure "unknown" is in PATH`},
	}
	for _, tt := range tests {
		got := providerInstallHint(tt.name)
		if got != tt.want {
			t.Errorf("providerInstallHint(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
