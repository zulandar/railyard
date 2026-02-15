package dispatch

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
	cfg := testConfig() // no tracks
	err := Start(StartOpts{Config: cfg})
	if err == nil {
		t.Fatal("expected error for no tracks")
	}
	if !strings.Contains(err.Error(), "at least one track must be configured") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "at least one track must be configured")
	}
}

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

func TestRenderPrompt_ContainsCommands(t *testing.T) {
	cfg := testConfig(config.TrackConfig{
		Name:     "api",
		Language: "go",
	})

	prompt, err := RenderPrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "ry bead create") {
		t.Errorf("prompt does not contain %q", "ry bead create")
	}
}

func TestRenderPrompt_ContainsRules(t *testing.T) {
	cfg := testConfig(config.TrackConfig{
		Name:     "api",
		Language: "go",
	})

	prompt, err := RenderPrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "acceptance criteria") {
		t.Errorf("prompt does not contain %q", "acceptance criteria")
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
	if !strings.Contains(prompt, "go") {
		t.Errorf("prompt does not contain language %q", "go")
	}
	if !strings.Contains(prompt, "typescript") {
		t.Errorf("prompt does not contain language %q", "typescript")
	}
	if !strings.Contains(prompt, "cmd/**, internal/**") {
		t.Errorf("prompt does not contain backend file patterns")
	}
	if !strings.Contains(prompt, "src/**, *.tsx") {
		t.Errorf("prompt does not contain frontend file patterns")
	}
}

func TestStart_ValidConfig_FailsOnClaude(t *testing.T) {
	cfg := testConfig(config.TrackConfig{
		Name:     "backend",
		Language: "go",
	})
	err := Start(StartOpts{Config: cfg})
	// Start renders the prompt successfully but fails when trying to run claude.
	if err == nil {
		t.Fatal("expected error (claude binary not available in test)")
	}
	if !strings.Contains(err.Error(), "claude session") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "claude session")
	}
}

func TestBuildCommand_Args(t *testing.T) {
	cmd := BuildCommand("test prompt")
	if cmd.Path == "" {
		t.Fatal("command path is empty")
	}
	args := cmd.Args
	if len(args) < 3 {
		t.Fatalf("len(args) = %d, want at least 3", len(args))
	}
	// args[0] is the command name, args[1] should be "--system-prompt", args[2] the prompt
	if args[1] != "--system-prompt" {
		t.Errorf("args[1] = %q, want %q", args[1], "--system-prompt")
	}
	if args[2] != "test prompt" {
		t.Errorf("args[2] = %q, want %q", args[2], "test prompt")
	}
}
