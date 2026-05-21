package providers

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/zulandar/railyard/internal/engine"
)

// resetCopilotModelWarnOnce zeroes the package-level sync.Once so subsequent
// tests observe the first-call warning behavior fresh.
func resetCopilotModelWarnOnce(t *testing.T) {
	t.Helper()
	copilotModelWarnOnce = sync.Once{}
}

// captureLog redirects the default logger to a buffer for the test's duration.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	})
	return &buf
}

func TestCopilotProvider_Name(t *testing.T) {
	p := &CopilotProvider{}
	if p.Name() != "copilot" {
		t.Errorf("Name() = %q, want %q", p.Name(), "copilot")
	}
}

func TestCopilotProvider_BuildCommand_DefaultBinary(t *testing.T) {
	p := &CopilotProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "copilot" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "copilot")
	}
}

func TestCopilotProvider_BuildCommand_CustomBinary(t *testing.T) {
	p := &CopilotProvider{Binary: "/usr/local/bin/copilot"}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "/usr/local/bin/copilot" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "/usr/local/bin/copilot")
	}
}

func TestCopilotProvider_BuildCommand_RequiredFlags(t *testing.T) {
	p := &CopilotProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "my context",
		WorkDir:        "/tmp/work",
	})
	defer cancel()

	args := strings.Join(cmd.Args, " ")
	for _, flag := range []string{
		"--auto-approve",
		"--system-prompt my context",
		"-p",
	} {
		if !strings.Contains(args, flag) {
			t.Errorf("missing flag %q in args: %s", flag, args)
		}
	}

	if cmd.Dir != "/tmp/work" {
		t.Errorf("Dir = %q, want %q", cmd.Dir, "/tmp/work")
	}
}

func TestCopilotProvider_BuildCommand_Cancel(t *testing.T) {
	p := &CopilotProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test",
	})
	defer cancel()

	if cmd.Cancel == nil {
		t.Error("cmd.Cancel should be set (SIGTERM handler)")
	}
}

func TestCopilotProvider_BuildInteractiveCommand(t *testing.T) {
	p := &CopilotProvider{}
	cmd := p.BuildInteractiveCommand("my system prompt", "/tmp/work", "")

	if cmd.Args[0] != "copilot" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "copilot")
	}

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--system-prompt my system prompt") {
		t.Errorf("missing --system-prompt flag in args: %s", args)
	}

	if strings.Contains(args, "--auto-approve") {
		t.Errorf("interactive command should NOT contain --auto-approve, got: %s", args)
	}

	if cmd.Dir != "/tmp/work" {
		t.Errorf("Dir = %q, want %q", cmd.Dir, "/tmp/work")
	}
}

func TestCopilotProvider_BuildPromptCommand(t *testing.T) {
	p := &CopilotProvider{}
	cmd, cancel := p.BuildPromptCommand(context.Background(), "do something", "")
	defer cancel()

	if cmd.Args[0] != "copilot" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "copilot")
	}

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--auto-approve") {
		t.Errorf("missing --auto-approve flag in args: %s", args)
	}
	if !strings.Contains(args, "-p") {
		t.Errorf("missing -p flag in args: %s", args)
	}
}

func TestCopilotProvider_ParseOutput_ReturnsEmpty(t *testing.T) {
	p := &CopilotProvider{}
	stats := p.ParseOutput("some plain text output\nmore output")
	if stats.InputTokens != 0 || stats.OutputTokens != 0 {
		t.Errorf("expected zero stats for plain text, got %+v", stats)
	}
	if stats.Model != "" {
		t.Errorf("Model = %q, want empty", stats.Model)
	}
}

func TestCopilotProvider_ValidateBinary_Missing(t *testing.T) {
	p := &CopilotProvider{Binary: "/nonexistent/path/to/copilot-xyz"}
	err := p.ValidateBinary()
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestCopilotEnv_NoOverride(t *testing.T) {
	// When GITHUB_COPILOT_TOKEN is unset, copilotEnv returns nil (inherit env).
	os.Unsetenv("GITHUB_COPILOT_TOKEN")
	env := copilotEnv()
	if env != nil {
		t.Errorf("expected nil when GITHUB_COPILOT_TOKEN unset, got %d entries", len(env))
	}
}

func TestCopilotEnv_OverridesGHToken(t *testing.T) {
	original := os.Getenv("GH_TOKEN")
	defer func() {
		if original != "" {
			os.Setenv("GH_TOKEN", original)
		} else {
			os.Unsetenv("GH_TOKEN")
		}
		os.Unsetenv("GITHUB_COPILOT_TOKEN")
	}()

	os.Setenv("GH_TOKEN", "repo-scoped-token")
	os.Setenv("GITHUB_COPILOT_TOKEN", "copilot-scoped-token")

	env := copilotEnv()
	if env == nil {
		t.Fatal("expected non-nil env")
	}

	var ghToken string
	ghTokenCount := 0
	for _, e := range env {
		if strings.HasPrefix(e, "GH_TOKEN=") {
			ghToken = strings.TrimPrefix(e, "GH_TOKEN=")
			ghTokenCount++
		}
	}
	if ghTokenCount != 1 {
		t.Errorf("expected exactly 1 GH_TOKEN entry, got %d", ghTokenCount)
	}
	if ghToken != "copilot-scoped-token" {
		t.Errorf("GH_TOKEN = %q, want %q", ghToken, "copilot-scoped-token")
	}
}

func TestCopilotProvider_BuildCommand_SetsEnv(t *testing.T) {
	original := os.Getenv("GITHUB_COPILOT_TOKEN")
	defer func() {
		if original != "" {
			os.Setenv("GITHUB_COPILOT_TOKEN", original)
		} else {
			os.Unsetenv("GITHUB_COPILOT_TOKEN")
		}
	}()

	os.Setenv("GITHUB_COPILOT_TOKEN", "test-copilot-token")

	p := &CopilotProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test",
	})
	defer cancel()

	if cmd.Env == nil {
		t.Fatal("expected cmd.Env to be set when GITHUB_COPILOT_TOKEN is present")
	}

	found := false
	for _, e := range cmd.Env {
		if e == "GH_TOKEN=test-copilot-token" {
			found = true
		}
	}
	if !found {
		t.Error("cmd.Env missing GH_TOKEN=test-copilot-token")
	}
}

func TestCopilotProvider_RegisteredViaInit(t *testing.T) {
	got, err := engine.GetProvider("copilot")
	if err != nil {
		t.Fatalf("GetProvider(copilot): %v", err)
	}
	if got.Name() != "copilot" {
		t.Errorf("Name() = %q, want %q", got.Name(), "copilot")
	}
}

// TestCopilotProvider_Model_NoOp verifies the provider does NOT mutate the
// constructed command (args / env apart from the GH_TOKEN handling) in
// response to a non-empty model.
func TestCopilotProvider_Model_NoOp(t *testing.T) {
	resetCopilotModelWarnOnce(t)
	_ = captureLog(t)

	p := &CopilotProvider{}

	cmd1, cancel1 := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "ctx",
		Model:          "gpt-4o",
	})
	defer cancel1()
	for _, a := range cmd1.Args {
		if a == "--model" || a == "-m" {
			t.Errorf("expected no model flag in BuildCommand args, got: %v", cmd1.Args)
		}
	}

	resetCopilotModelWarnOnce(t)
	cmd2 := p.BuildInteractiveCommand("sys", "/tmp/work", "gpt-4o")
	for _, a := range cmd2.Args {
		if a == "--model" || a == "-m" {
			t.Errorf("expected no model flag in BuildInteractiveCommand args, got: %v", cmd2.Args)
		}
	}

	resetCopilotModelWarnOnce(t)
	cmd3, cancel3 := p.BuildPromptCommand(context.Background(), "do thing", "gpt-4o")
	defer cancel3()
	for _, a := range cmd3.Args {
		if a == "--model" || a == "-m" {
			t.Errorf("expected no model flag in BuildPromptCommand args, got: %v", cmd3.Args)
		}
	}
}

func TestCopilotProvider_Model_WarnOnce(t *testing.T) {
	resetCopilotModelWarnOnce(t)
	buf := captureLog(t)
	p := &CopilotProvider{}

	_, cancel1 := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "ctx",
		Model:          "gpt-4o",
	})
	cancel1()

	if !strings.Contains(buf.String(), "copilot provider doesn't support model selection") {
		t.Errorf("expected warning log on first non-empty model, got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), `agent_model="gpt-4o"`) {
		t.Errorf("expected warning to include model name, got: %q", buf.String())
	}

	// Subsequent calls (any Build* method) with non-empty model must NOT re-log.
	before := buf.String()
	_ = p.BuildInteractiveCommand("sys", "/tmp/work", "gpt-4o")
	_, cancel2 := p.BuildPromptCommand(context.Background(), "do thing", "gpt-4o")
	cancel2()
	_, cancel3 := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "ctx",
		Model:          "gpt-5",
	})
	cancel3()
	if buf.String() != before {
		t.Errorf("expected no additional warnings after first, got extra: %q", strings.TrimPrefix(buf.String(), before))
	}
}

func TestCopilotProvider_Model_EmptyNoWarn(t *testing.T) {
	resetCopilotModelWarnOnce(t)
	buf := captureLog(t)
	p := &CopilotProvider{}

	_, cancel1 := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "ctx",
		// Model intentionally empty.
	})
	cancel1()
	_ = p.BuildInteractiveCommand("sys", "/tmp/work", "")
	_, cancel2 := p.BuildPromptCommand(context.Background(), "do thing", "")
	cancel2()

	if buf.String() != "" {
		t.Errorf("expected no warnings for empty model, got: %q", buf.String())
	}
}
