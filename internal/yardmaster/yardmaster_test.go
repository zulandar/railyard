package yardmaster

import (
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

func testConfig(tracks ...config.TrackConfig) *config.Config {
	return &config.Config{
		Owner:        "alice",
		Repo:         "git@github.com:org/myapp.git",
		BranchPrefix: "ry/alice",
		Tracks:       tracks,
	}
}

// --- Start validation tests ---

func TestStart_NilConfig(t *testing.T) {
	err := Start(StartOpts{Config: nil})
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "config is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "config is required")
	}
}

func TestStart_NoTracks(t *testing.T) {
	cfg := testConfig()
	err := Start(StartOpts{Config: cfg})
	if err == nil {
		t.Fatal("expected error for no tracks")
	}
	if !strings.Contains(err.Error(), "at least one track must be configured") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "at least one track must be configured")
	}
}

func TestStart_ValidConfig_FailsOnClaude(t *testing.T) {
	cfg := testConfig(config.TrackConfig{
		Name:     "backend",
		Language: "go",
	})
	err := Start(StartOpts{Config: cfg})
	if err == nil {
		t.Fatal("expected error (claude binary not available in test)")
	}
	if !strings.Contains(err.Error(), "claude session") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "claude session")
	}
}

// --- RenderPrompt tests ---

func TestRenderPrompt_NilConfig(t *testing.T) {
	_, err := RenderPrompt(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "config is nil") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "config is nil")
	}
}

func TestRenderPrompt_ContainsTrackNames(t *testing.T) {
	cfg := testConfig(config.TrackConfig{
		Name:         "backend",
		Language:     "go",
		FilePatterns: []string{"cmd/**", "internal/**"},
		EngineSlots:  5,
	})

	prompt, err := RenderPrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "backend") {
		t.Errorf("prompt does not contain track name %q", "backend")
	}
	if !strings.Contains(prompt, "go") {
		t.Errorf("prompt does not contain track language %q", "go")
	}
}

func TestRenderPrompt_ContainsBranchPrefix(t *testing.T) {
	cfg := testConfig(config.TrackConfig{
		Name:     "api",
		Language: "go",
	})

	prompt, err := RenderPrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "ry/alice") {
		t.Errorf("prompt does not contain branch prefix %q", "ry/alice")
	}
}

func TestRenderPrompt_ContainsResponsibilities(t *testing.T) {
	cfg := testConfig(config.TrackConfig{
		Name:     "api",
		Language: "go",
	})

	prompt, err := RenderPrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, keyword := range []string{
		"Monitor engine health",
		"Switch completed branches",
		"Handle stalls",
		"Manage dependencies",
		"Trigger reindexing",
		"Escalate to human",
	} {
		if !strings.Contains(prompt, keyword) {
			t.Errorf("prompt does not contain responsibility %q", keyword)
		}
	}
}

func TestRenderPrompt_ContainsCommands(t *testing.T) {
	cfg := testConfig(config.TrackConfig{
		Name:     "api",
		Language: "go",
	})

	prompt, err := RenderPrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, cmd := range []string{
		"ry bead list",
		"ry message send",
		"ry inbox",
		"ry bead update",
		"ry switch",
	} {
		if !strings.Contains(prompt, cmd) {
			t.Errorf("prompt does not contain command %q", cmd)
		}
	}
}

func TestRenderPrompt_ContainsDecisionRules(t *testing.T) {
	cfg := testConfig(config.TrackConfig{
		Name:     "api",
		Language: "go",
	})

	prompt, err := RenderPrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, rule := range []string{
		"Stalled Engine",
		"Completed Bead",
		"Escalation to Human",
	} {
		if !strings.Contains(prompt, rule) {
			t.Errorf("prompt does not contain decision rule %q", rule)
		}
	}
}

func TestRenderPrompt_MultipleTracksRendered(t *testing.T) {
	cfg := testConfig(
		config.TrackConfig{
			Name:         "backend",
			Language:     "go",
			FilePatterns: []string{"cmd/**", "internal/**"},
			EngineSlots:  5,
		},
		config.TrackConfig{
			Name:         "frontend",
			Language:     "typescript",
			FilePatterns: []string{"src/**", "*.tsx"},
			EngineSlots:  3,
		},
	)

	prompt, err := RenderPrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "backend") {
		t.Errorf("prompt does not contain track name %q", "backend")
	}
	if !strings.Contains(prompt, "frontend") {
		t.Errorf("prompt does not contain track name %q", "frontend")
	}
	if !strings.Contains(prompt, "cmd/**, internal/**") {
		t.Errorf("prompt does not contain backend file patterns")
	}
	if !strings.Contains(prompt, "src/**, *.tsx") {
		t.Errorf("prompt does not contain frontend file patterns")
	}
}

// --- BuildCommand tests ---

func TestBuildCommand_Args(t *testing.T) {
	cmd := BuildCommand("test prompt")
	if cmd.Path == "" {
		t.Fatal("command path is empty")
	}
	args := cmd.Args

	// Find required flags.
	foundSkipPerms := false
	foundSP := false
	foundP := false
	for i, a := range args {
		if a == "--dangerously-skip-permissions" {
			foundSkipPerms = true
		}
		if a == "--system-prompt" && i+1 < len(args) {
			foundSP = true
			if args[i+1] != "test prompt" {
				t.Errorf("--system-prompt value = %q, want %q", args[i+1], "test prompt")
			}
		}
		if a == "-p" && i+1 < len(args) {
			foundP = true
			if args[i+1] == "" {
				t.Error("-p value should not be empty")
			}
		}
	}
	if !foundSkipPerms {
		t.Error("--dangerously-skip-permissions flag not found in args")
	}
	if !foundSP {
		t.Error("--system-prompt flag not found in args")
	}
	if !foundP {
		t.Error("-p flag not found in args")
	}
}

// --- StartOpts tests ---

func TestStartOpts_ZeroValue(t *testing.T) {
	opts := StartOpts{}
	if opts.ConfigPath != "" || opts.Config != nil {
		t.Error("zero-value StartOpts should have empty fields")
	}
}

func TestRenderPrompt_ContainsMonitoringLoop(t *testing.T) {
	cfg := testConfig(config.TrackConfig{
		Name:     "api",
		Language: "go",
	})

	prompt, err := RenderPrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "30 seconds") {
		t.Errorf("prompt does not contain monitoring interval")
	}
	if !strings.Contains(prompt, "Check inbox") {
		t.Errorf("prompt does not contain inbox check")
	}
}
