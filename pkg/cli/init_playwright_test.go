package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// TestRenderConfig_WithPlaywright verifies that a track carrying a Playwright
// config renders a `playwright:` block that config.Parse reads back with the
// expected Enabled/SpecPath/Filename/Template values (round trip).
func TestRenderConfig_WithPlaywright(t *testing.T) {
	tracks := []config.TrackConfig{
		{
			Name: "frontend", Language: "typescript",
			FilePatterns: []string{"**/*.ts", "**/*.tsx"},
			EngineSlots:  2,
			TestCommand:  "npm test",
			Playwright: &models.PlaywrightConfig{
				Enabled:  true,
				SpecPath: "tests/pr-demos",
				Filename: "{car_id}.spec.ts",
				Template: "tests/pr-demos/_template.spec.ts",
			},
		},
	}
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", "", tracks, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	if !strings.Contains(yamlStr, "playwright:") {
		t.Errorf("rendered YAML missing playwright block:\n%s", yamlStr)
	}
	cfg, err := config.Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("config.Parse failed on rendered YAML: %v\n---\n%s", err, yamlStr)
	}
	if len(cfg.Tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(cfg.Tracks))
	}
	pw := cfg.Tracks[0].Playwright
	if pw == nil {
		t.Fatal("Playwright config is nil after round trip")
	}
	if !pw.Enabled {
		t.Error("Playwright.Enabled = false, want true")
	}
	if pw.SpecPath != "tests/pr-demos" {
		t.Errorf("Playwright.SpecPath = %q, want %q", pw.SpecPath, "tests/pr-demos")
	}
	if pw.Filename != "{car_id}.spec.ts" {
		t.Errorf("Playwright.Filename = %q, want %q", pw.Filename, "{car_id}.spec.ts")
	}
	if pw.Template != "tests/pr-demos/_template.spec.ts" {
		t.Errorf("Playwright.Template = %q, want %q", pw.Template, "tests/pr-demos/_template.spec.ts")
	}
}

// TestRenderConfig_WithoutPlaywright verifies the playwright block is absent
// when a track has no Playwright config.
func TestRenderConfig_WithoutPlaywright(t *testing.T) {
	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", FilePatterns: []string{"**/*.go"}, EngineSlots: 2, TestCommand: "go test ./..."},
	}
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", "", tracks, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	if strings.Contains(yamlStr, "playwright:") {
		t.Errorf("rendered YAML should not contain playwright block:\n%s", yamlStr)
	}
}

// TestInitCmd_Yes_NoPlaywright verifies that `ry init --yes` (no --with-playwright)
// produces a config with NO playwright block, even on a ts/js project.
func TestInitCmd_Yes_NoPlaywright(t *testing.T) {
	dir := initTSGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --yes: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "playwright:") {
		t.Errorf("config should NOT contain playwright with --yes and no --with-playwright:\n%s", string(data))
	}
	// No scaffolding should have been written either.
	if _, err := os.Stat(filepath.Join(dir, "tests", "pr-demos", "_template.spec.ts")); err == nil {
		t.Error("template spec should NOT be scaffolded without opt-in")
	}
}

// TestInitCmd_Yes_WithPlaywright verifies that `ry init --yes --with-playwright`
// on a ts/js project enables Playwright in the generated config WITHOUT prompting
// and scaffolds the template spec + example workflow.
func TestInitCmd_Yes_WithPlaywright(t *testing.T) {
	dir := initTSGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--with-playwright", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --yes --with-playwright: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parse generated config: %v\n---\n%s", err, string(data))
	}
	var foundEnabled bool
	for _, tr := range cfg.Tracks {
		if tr.Language == "typescript" || tr.Language == "javascript" {
			if tr.Playwright == nil || !tr.Playwright.Enabled {
				t.Errorf("frontend track %q should have Playwright enabled", tr.Name)
				continue
			}
			foundEnabled = true
			if tr.Playwright.SpecPath != "tests/pr-demos" {
				t.Errorf("SpecPath = %q, want tests/pr-demos", tr.Playwright.SpecPath)
			}
		}
	}
	if !foundEnabled {
		t.Fatalf("no frontend track had Playwright enabled; tracks: %+v", cfg.Tracks)
	}

	// Scaffolded files must exist.
	tmpl := filepath.Join(dir, "tests", "pr-demos", "_template.spec.ts")
	if _, err := os.Stat(tmpl); err != nil {
		t.Errorf("expected template spec at %s: %v", tmpl, err)
	}
	wf := filepath.Join(dir, ".github", "workflows", "pr-demo.yml.example")
	if _, err := os.Stat(wf); err != nil {
		t.Errorf("expected example workflow at %s: %v", wf, err)
	}
	// Summary should mention the rename-to-activate note.
	if !strings.Contains(out.String(), "pr-demo.yml") {
		t.Errorf("expected summary to mention the example workflow rename note: %s", out.String())
	}
}

// TestInitCmd_Interactive_PlaywrightAccept verifies that piping "y" to the
// Playwright prompt enables it on the frontend track.
func TestInitCmd_Interactive_PlaywrightAccept(t *testing.T) {
	dir := initTSGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	// Prompts in order: owner, remote, host, user, password, port, use tracks (y),
	// playwright for the single frontend track (y), telegraph (n).
	cmd.SetIn(strings.NewReader("\n\n\n\n\n\ny\ny\nn\n"))
	cmd.SetArgs([]string{"init", "--skip-db", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("interactive init: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "playwright:") {
		t.Errorf("config should contain playwright block after accepting prompt:\n%s", string(data))
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parse config: %v\n---\n%s", err, string(data))
	}
	var enabled bool
	for _, tr := range cfg.Tracks {
		if tr.Playwright != nil && tr.Playwright.Enabled {
			enabled = true
		}
	}
	if !enabled {
		t.Error("expected at least one track with Playwright enabled")
	}
}

// TestInitCmd_Interactive_PlaywrightDecline verifies that declining (empty/"n")
// the Playwright prompt leaves it off.
func TestInitCmd_Interactive_PlaywrightDecline(t *testing.T) {
	dir := initTSGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	// owner, remote, host, user, password, port, use tracks (y),
	// playwright prompt (n), telegraph (n).
	cmd.SetIn(strings.NewReader("\n\n\n\n\n\ny\nn\nn\n"))
	cmd.SetArgs([]string{"init", "--skip-db", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("interactive init: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "playwright:") {
		t.Errorf("config should NOT contain playwright block after declining:\n%s", string(data))
	}
}

// TestScaffoldPlaywright_WritesFiles verifies the scaffolder writes both the
// template spec and the example workflow, and that it does not clobber an
// existing template spec.
func TestScaffoldPlaywright_WritesFiles(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer

	scaffoldPlaywright(&out, root)

	tmpl := filepath.Join(root, "tests", "pr-demos", "_template.spec.ts")
	tmplData, err := os.ReadFile(tmpl)
	if err != nil {
		t.Fatalf("expected template spec: %v", err)
	}
	if !strings.Contains(string(tmplData), "test(") {
		t.Errorf("template spec should contain a test() call:\n%s", string(tmplData))
	}

	wf := filepath.Join(root, ".github", "workflows", "pr-demo.yml.example")
	wfData, err := os.ReadFile(wf)
	if err != nil {
		t.Fatalf("expected example workflow: %v", err)
	}
	// The reference workflow must demonstrate diff-scoped execution + video.
	wfStr := string(wfData)
	for _, want := range []string{"video", "upload-artifact", "rename"} {
		if !strings.Contains(strings.ToLower(wfStr), strings.ToLower(want)) {
			t.Errorf("example workflow missing %q:\n%s", want, wfStr)
		}
	}

	// Re-running must not clobber a user-edited template spec.
	custom := []byte("// user edited\n")
	if err := os.WriteFile(tmpl, custom, 0644); err != nil {
		t.Fatal(err)
	}
	scaffoldPlaywright(&out, root)
	after, _ := os.ReadFile(tmpl)
	if string(after) != string(custom) {
		t.Error("scaffoldPlaywright clobbered an existing template spec")
	}
}

// initTSGitRepo creates a temporary git repo that detectLanguages classifies
// as a TypeScript project (package.json + tsconfig.json) and chdir's into it
// so runInit's detectGitRoot(os.Getwd()) resolves to this repo rather than the
// surrounding railyard repo. The original working directory is restored on
// cleanup.
func initTSGitRepo(t *testing.T) string {
	t.Helper()
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir
}
