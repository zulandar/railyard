package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

// initGitRepo creates a temporary git repository with user.name "TestUser",
// email "test@test.com", remote origin "git@github.com:org/myrepo.git",
// and one initial commit. Returns the path to the repo root.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.name", "TestUser"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "remote", "add", "origin", "git@github.com:org/myrepo.git"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}

	// Create an initial commit so the repo is non-empty.
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}

	return dir
}

func TestDetectGitRoot(t *testing.T) {
	dir := initGitRepo(t)

	// Create a subdirectory and call detectGitRoot from there.
	sub := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	root, err := detectGitRoot(sub)
	if err != nil {
		t.Fatalf("detectGitRoot(%q): %v", sub, err)
	}
	if root != dir {
		t.Errorf("detectGitRoot = %q, want %q", root, dir)
	}
}

func TestDetectGitRoot_NotARepo(t *testing.T) {
	// A plain temp directory is not a git repo.
	dir := t.TempDir()
	_, err := detectGitRoot(dir)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
}

func TestDetectGitRemote(t *testing.T) {
	dir := initGitRepo(t)

	remote, err := detectGitRemote(dir)
	if err != nil {
		t.Fatalf("detectGitRemote: %v", err)
	}
	if remote != "git@github.com:org/myrepo.git" {
		t.Errorf("detectGitRemote = %q, want %q", remote, "git@github.com:org/myrepo.git")
	}
}

func TestDetectGitRemote_NoRemote(t *testing.T) {
	dir := t.TempDir()

	// Create a repo with no remote.
	for _, args := range [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "user.email", "test@test.com"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}

	remote, err := detectGitRemote(dir)
	if err != nil {
		t.Fatalf("detectGitRemote: %v", err)
	}
	if remote != "" {
		t.Errorf("detectGitRemote = %q, want empty string", remote)
	}
}

func TestDetectOwner(t *testing.T) {
	dir := initGitRepo(t)

	owner := detectOwner(dir)
	if owner != "testuser" {
		t.Errorf("detectOwner = %q, want %q", owner, "testuser")
	}
}

func TestDetectOwner_NonASCIIFallsThrough(t *testing.T) {
	dir := initGitRepo(t)
	// Set user.name to something that sanitizes to empty.
	cmd := exec.Command("git", "config", "user.name", "日本語")
	cmd.Dir = dir
	cmd.Run()

	owner := detectOwner(dir)
	// Should fall through to $USER or "railyard", never empty.
	if owner == "" {
		t.Fatal("detectOwner returned empty string for non-ASCII name")
	}
}

// TestDetectLanguages_GoRepo verifies that detectLanguages identifies a Go
// repository by the presence of a go.mod file.
func TestDetectLanguages_GoRepo(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	languages := detectLanguages(dir)
	if len(languages) != 1 {
		t.Fatalf("expected 1 language, got %v", languages)
	}
	if languages[0] != "go" {
		t.Errorf("expected language %q, got %q", "go", languages[0])
	}
}

// TestDetectLanguages_MultiLanguage verifies that detectLanguages returns
// multiple languages when several manifest files are present, sorted
// alphabetically.
func TestDetectLanguages_MultiLanguage(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	languages := detectLanguages(dir)
	if len(languages) != 2 {
		t.Fatalf("expected 2 languages, got %v", languages)
	}

	// detectLanguages sorts results alphabetically.
	if languages[0] != "go" {
		t.Errorf("languages[0] = %q, want %q", languages[0], "go")
	}
	if languages[1] != "typescript" {
		t.Errorf("languages[1] = %q, want %q", languages[1], "typescript")
	}
}

// TestDetectLanguages_Empty verifies that detectLanguages returns an empty
// slice for a directory with no language indicator files.
func TestDetectLanguages_Empty(t *testing.T) {
	dir := t.TempDir()

	languages := detectLanguages(dir)
	if len(languages) != 0 {
		t.Errorf("expected no languages, got %v", languages)
	}
}

func TestSanitizeOwner(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Alice Smith", "alice-smith"},
		{"bob_jones", "bob-jones"},
		{"charlie123", "charlie123"},
		{"  spaces  ", "spaces"},
		{"UPPERCASE", "uppercase"},
		{"special!@#chars", "specialchars"},
	}

	for _, tt := range tests {
		got := sanitizeOwner(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeOwner(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPromptValue_AcceptDefault(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got := promptValue(in, &out, "Owner", "alice")
	if got != "alice" {
		t.Errorf("got %q, want %q", got, "alice")
	}
	if !strings.Contains(out.String(), "alice") {
		t.Errorf("output should show default: %q", out.String())
	}
}

func TestPromptValue_Override(t *testing.T) {
	in := strings.NewReader("bob\n")
	var out bytes.Buffer
	got := promptValue(in, &out, "Owner", "alice")
	if got != "bob" {
		t.Errorf("got %q, want %q", got, "bob")
	}
}

func TestPromptYesNo_DefaultYes(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got := promptYesNo(in, &out, "Continue?", true)
	if !got {
		t.Error("expected true for empty input with defaultYes=true")
	}
}

func TestPromptYesNo_ExplicitNo(t *testing.T) {
	in := strings.NewReader("n\n")
	var out bytes.Buffer
	got := promptYesNo(in, &out, "Continue?", true)
	if got {
		t.Error("expected false for 'n' input")
	}
}

func TestPromptYesNo_DefaultNo(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got := promptYesNo(in, &out, "Continue?", false)
	if got {
		t.Error("expected false for empty input with defaultYes=false")
	}
}

func TestPromptYesNo_ExplicitYes(t *testing.T) {
	in := strings.NewReader("yes\n")
	var out bytes.Buffer
	got := promptYesNo(in, &out, "Continue?", false)
	if !got {
		t.Error("expected true for 'yes' input")
	}
}

func TestEnsureDoltDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dolt-data")
	// Directory doesn't exist yet.
	if err := ensureDoltDataDir(dir); err != nil {
		// dolt may not be installed — skip if so.
		if strings.Contains(err.Error(), "executable file not found") {
			t.Skip("dolt not installed")
		}
		t.Fatalf("ensureDoltDataDir: %v", err)
	}
	// Should have created the directory.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("directory was not created")
	}
	// Should have .dolt subdirectory.
	if _, err := os.Stat(filepath.Join(dir, ".dolt")); os.IsNotExist(err) {
		t.Fatal(".dolt directory was not created")
	}
	// Calling again should be idempotent (skips dolt init).
	if err := ensureDoltDataDir(dir); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestLanguagePreset(t *testing.T) {
	tests := []struct {
		lang        string
		wantName    string
		wantTest    string
		wantPattern string // first file_pattern
	}{
		{"go", "backend", "go test ./...", "**/*.go"},
		{"typescript", "frontend", "npm test", "**/*.ts"},
		{"python", "backend", "pytest", "**/*.py"},
		{"rust", "backend", "cargo test", "**/*.rs"},
		{"unknown-lang", "unknown-lang", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			track := languagePreset(tt.lang)
			if track.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", track.Name, tt.wantName)
			}
			if track.TestCommand != tt.wantTest {
				t.Errorf("TestCommand = %q, want %q", track.TestCommand, tt.wantTest)
			}
			if tt.wantPattern != "" && (len(track.FilePatterns) == 0 || track.FilePatterns[0] != tt.wantPattern) {
				t.Errorf("FilePatterns[0] = %v, want %q", track.FilePatterns, tt.wantPattern)
			}
		})
	}
}

func TestGenerateTracks(t *testing.T) {
	tracks := generateTracks([]string{"go", "typescript"})
	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}
	if tracks[0].Name != "backend" {
		t.Errorf("tracks[0].Name = %q, want %q", tracks[0].Name, "backend")
	}
	if tracks[1].Name != "frontend" {
		t.Errorf("tracks[1].Name = %q, want %q", tracks[1].Name, "frontend")
	}
}

func TestGenerateTracks_NamingConflict(t *testing.T) {
	tracks := generateTracks([]string{"go", "python"})
	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}
	names := map[string]bool{}
	for _, tr := range tracks {
		if names[tr.Name] {
			t.Errorf("duplicate track name: %q", tr.Name)
		}
		names[tr.Name] = true
	}
}

func TestGenerateTracks_Empty(t *testing.T) {
	tracks := generateTracks(nil)
	if len(tracks) != 0 {
		t.Errorf("expected 0 tracks, got %d", len(tracks))
	}
}

func TestEnsureDoltRunning_AlreadyRunning(t *testing.T) {
	// We can't easily test the full startup without a real Dolt server,
	// so we test the error path: connection to a dead port should return
	// an error that mentions dolt.
	var out bytes.Buffer
	err := ensureDoltRunning(&out, "127.0.0.1", 19999, "root", "")
	// Should fail because nothing is on port 19999 and dolt data dir
	// may not exist. The exact error doesn't matter — just verify it
	// doesn't panic and returns an error.
	if err == nil {
		// If it somehow succeeded (unlikely), that's fine too.
		return
	}
	// Error should be informative.
	errStr := err.Error()
	if !strings.Contains(errStr, "dolt") && !strings.Contains(errStr, "Dolt") {
		t.Errorf("error should mention dolt: %v", err)
	}
}

func TestRenderConfig(t *testing.T) {
	tracks := []config.TrackConfig{
		{
			Name: "backend", Language: "go",
			FilePatterns: []string{"**/*.go"},
			EngineSlots:  2,
			TestCommand:  "go test ./...",
		},
	}
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", tracks, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}

	// Validate the output can be parsed by config.Parse.
	cfg, err := config.Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("config.Parse failed on rendered YAML: %v\n---\n%s", err, yamlStr)
	}
	if cfg.Owner != "alice" {
		t.Errorf("Owner = %q, want %q", cfg.Owner, "alice")
	}
	if cfg.Repo != "git@github.com:org/repo.git" {
		t.Errorf("Repo = %q, want %q", cfg.Repo, "git@github.com:org/repo.git")
	}
	if len(cfg.Tracks) != 1 || cfg.Tracks[0].Name != "backend" {
		t.Errorf("Tracks = %+v, want 1 track named 'backend'", cfg.Tracks)
	}
}

func TestRenderConfig_MultipleTracks(t *testing.T) {
	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", FilePatterns: []string{"**/*.go"}, EngineSlots: 2, TestCommand: "go test ./..."},
		{Name: "frontend", Language: "typescript", FilePatterns: []string{"**/*.ts", "**/*.tsx"}, EngineSlots: 2, TestCommand: "npm test"},
	}
	yamlStr, err := renderConfig("bob", "git@github.com:org/app.git", "127.0.0.1", 3306, "root", tracks, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	cfg, err := config.Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("config.Parse: %v\n---\n%s", err, yamlStr)
	}
	if len(cfg.Tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(cfg.Tracks))
	}
}

func TestInitCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"init", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --help: %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "Initialize Railyard") {
		t.Errorf("help should mention 'Initialize Railyard': %s", output)
	}
	if !strings.Contains(output, "--yes") {
		t.Errorf("help should show --yes flag: %s", output)
	}
	if !strings.Contains(output, "--config") {
		t.Errorf("help should show --config flag: %s", output)
	}
	if !strings.Contains(output, "--skip-db") {
		t.Errorf("help should show --skip-db flag: %s", output)
	}
	if !strings.Contains(output, "--port") {
		t.Errorf("help should show --port flag: %s", output)
	}
	if !strings.Contains(output, "--host") {
		t.Errorf("help should show --host flag: %s", output)
	}
	if !strings.Contains(output, "--skip-telegraph") {
		t.Errorf("help should show --skip-telegraph flag: %s", output)
	}
}

func TestInitCmd_AlreadyExists_Abort(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")
	os.WriteFile(configPath, []byte("owner: existing\nrepo: x\ntracks:\n  - name: t\n    language: go\n"), 0644)

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader("n\n"))
	cmd.SetArgs([]string{"init", "--config", configPath})
	cmd.Execute()
	output := out.String()
	if !strings.Contains(output, "already exists") {
		t.Errorf("expected 'already exists' warning: %s", output)
	}
	if !strings.Contains(output, "Aborted") {
		t.Errorf("expected 'Aborted' message: %s", output)
	}
}

func TestInitCmd_NonInteractive_SkipDB(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--port", "3307", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --yes --skip-db --port 3307: %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "Wrote") {
		t.Errorf("expected 'Wrote' message: %s", output)
	}
	if !strings.Contains(output, "Skipped database") {
		t.Errorf("expected 'Skipped database' message: %s", output)
	}
	// Verify config file was created and is valid.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parse generated config: %v\n---\n%s", err, string(data))
	}
	if cfg.Owner == "" {
		t.Error("owner should not be empty")
	}
	if len(cfg.Tracks) == 0 {
		t.Error("should have at least one track")
	}
	if cfg.Dolt.Port != 3307 {
		t.Errorf("Dolt.Port = %d, want 3307", cfg.Dolt.Port)
	}
}

func TestInitCmd_NonInteractive_CustomHost(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--host", "10.0.0.5", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --yes --skip-db --host 10.0.0.5: %v", err)
	}
	// Verify config file has the custom host.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parse generated config: %v\n---\n%s", err, string(data))
	}
	if cfg.Dolt.Host != "10.0.0.5" {
		t.Errorf("Dolt.Host = %q, want %q", cfg.Dolt.Host, "10.0.0.5")
	}
}

func TestDetectLanguages_SkipsDirs(t *testing.T) {
	dir := t.TempDir()
	// Files in skipped directories should not count.
	// detectLanguages (from gitignore.go) uses manifest file detection,
	// so put manifest files in dirs that should be ignored.
	os.MkdirAll(filepath.Join(dir, "vendor"), 0755)
	os.WriteFile(filepath.Join(dir, "vendor", "go.mod"), []byte("module vendor"), 0644)

	langs := detectLanguages(dir)
	// vendor/go.mod should not be detected as a language.
	for _, l := range langs {
		if l == "go" {
			t.Error("should not detect languages from vendor/ directory")
		}
	}
}

func TestInitCmd_InteractiveOverwrite(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")
	// Write an existing config.
	os.WriteFile(configPath, []byte("owner: old\nrepo: x\ntracks:\n  - name: t\n    language: go\n"), 0644)

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	// Answer "yes" to overwrite, then accept defaults for owner, remote,
	// host, and port, then accept tracks, then decline telegraph.
	cmd.SetIn(strings.NewReader("yes\n\n\n\n\ny\nn\n"))
	cmd.SetArgs([]string{"init", "--skip-db", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init with overwrite: %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "already exists") {
		t.Error("should mention existing config")
	}
	if !strings.Contains(output, "Wrote") {
		t.Error("should confirm config was written")
	}
}

func TestInitCmd_FailsOnEmptyRepo(t *testing.T) {
	// Create a git repo with NO remote.
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@test.com")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0644)
	run("add", ".")
	run("commit", "-m", "init")

	// Must chdir into the no-remote repo so detectGitRoot finds it.
	orig, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(orig) })

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when repo URL is empty")
	}
	if !strings.Contains(err.Error(), "repo URL is required") {
		t.Errorf("error should mention repo URL: %v", err)
	}
	// Config file should NOT have been written.
	if _, statErr := os.Stat(filepath.Join(dir, "railyard.yaml")); statErr == nil {
		t.Error("config file should not be written when repo URL is empty")
	}
}

func TestRenderConfig_EmptyRepo(t *testing.T) {
	// Config with empty repo field should fail validation.
	tracks := []config.TrackConfig{
		{Name: "test", Language: "go", EngineSlots: 2},
	}
	yamlStr, err := renderConfig("alice", "", "127.0.0.1", 3306, "root", tracks, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	// The rendered YAML with empty repo should fail config.Parse validation.
	_, err = config.Parse([]byte(yamlStr))
	if err == nil {
		t.Error("expected config.Parse to fail with empty repo")
	}
}

func TestPromptChoice_Default(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got := promptChoice(in, &out, "Platform", []string{"slack", "discord"}, "slack")
	if got != "slack" {
		t.Errorf("got %q, want %q", got, "slack")
	}
	if !strings.Contains(out.String(), "slack/discord") {
		t.Errorf("output should show choices: %q", out.String())
	}
}

func TestPromptChoice_Override(t *testing.T) {
	in := strings.NewReader("discord\n")
	var out bytes.Buffer
	got := promptChoice(in, &out, "Platform", []string{"slack", "discord"}, "slack")
	if got != "discord" {
		t.Errorf("got %q, want %q", got, "discord")
	}
}

func TestPromptChoice_InvalidThenValid(t *testing.T) {
	in := strings.NewReader("slcak\ndiscord\n")
	var out bytes.Buffer
	got := promptChoice(in, &out, "Platform", []string{"slack", "discord"}, "slack")
	if got != "discord" {
		t.Errorf("got %q, want %q", got, "discord")
	}
	if !strings.Contains(out.String(), "Invalid choice") {
		t.Errorf("output should show invalid choice message: %q", out.String())
	}
}

func TestPromptChoice_InvalidExhausted(t *testing.T) {
	in := strings.NewReader("bad1\nbad2\nbad3\n")
	var out bytes.Buffer
	got := promptChoice(in, &out, "Platform", []string{"slack", "discord"}, "slack")
	if got != "slack" {
		t.Errorf("got %q, want default %q after 3 invalid attempts", got, "slack")
	}
}

func TestRenderConfig_WithTelegraphSlack(t *testing.T) {
	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", FilePatterns: []string{"**/*.go"}, EngineSlots: 2, TestCommand: "go test ./..."},
	}
	tg := &telegraphTemplateData{
		Platform:    "slack",
		Channel:     "C123456",
		SlackBotVar: "SLACK_BOT_TOKEN",
		SlackAppVar: "SLACK_APP_TOKEN",
	}
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", tracks, tg)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	// Verify the telegraph section is present with correct values.
	if !strings.Contains(yamlStr, "telegraph:") {
		t.Error("rendered YAML missing telegraph section")
	}
	if !strings.Contains(yamlStr, "platform: slack") {
		t.Error("rendered YAML missing platform: slack")
	}
	if !strings.Contains(yamlStr, "channel: C123456") {
		t.Error("rendered YAML missing channel")
	}
	if !strings.Contains(yamlStr, "${SLACK_BOT_TOKEN}") {
		t.Error("rendered YAML missing ${SLACK_BOT_TOKEN}")
	}
	if !strings.Contains(yamlStr, "${SLACK_APP_TOKEN}") {
		t.Error("rendered YAML missing ${SLACK_APP_TOKEN}")
	}

	// Set env vars so config.Parse can validate.
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	t.Setenv("SLACK_APP_TOKEN", "xapp-test")
	cfg, err := config.Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("config.Parse: %v\n---\n%s", err, yamlStr)
	}
	if cfg.Telegraph.Platform != "slack" {
		t.Errorf("Telegraph.Platform = %q, want %q", cfg.Telegraph.Platform, "slack")
	}
	if cfg.Telegraph.Channel != "C123456" {
		t.Errorf("Telegraph.Channel = %q, want %q", cfg.Telegraph.Channel, "C123456")
	}
}

func TestRenderConfig_WithTelegraphDiscord(t *testing.T) {
	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", FilePatterns: []string{"**/*.go"}, EngineSlots: 2, TestCommand: "go test ./..."},
	}
	tg := &telegraphTemplateData{
		Platform:      "discord",
		Channel:       "123456789",
		DiscordBotVar: "DISCORD_BOT_TOKEN",
		GuildID:       "guild-123",
		DiscordChanID: "chan-456",
	}
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", tracks, tg)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	if !strings.Contains(yamlStr, "platform: discord") {
		t.Error("rendered YAML missing platform: discord")
	}
	if !strings.Contains(yamlStr, "${DISCORD_BOT_TOKEN}") {
		t.Error("rendered YAML missing ${DISCORD_BOT_TOKEN}")
	}
	if !strings.Contains(yamlStr, "guild_id: guild-123") {
		t.Error("rendered YAML missing guild_id")
	}
	if !strings.Contains(yamlStr, "channel_id: chan-456") {
		t.Error("rendered YAML missing channel_id")
	}

	// Set env vars so config.Parse can validate.
	t.Setenv("DISCORD_BOT_TOKEN", "discord-test-token")
	cfg, err := config.Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("config.Parse: %v\n---\n%s", err, yamlStr)
	}
	if cfg.Telegraph.Platform != "discord" {
		t.Errorf("Telegraph.Platform = %q, want %q", cfg.Telegraph.Platform, "discord")
	}
}

func TestRenderConfig_WithoutTelegraph(t *testing.T) {
	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", FilePatterns: []string{"**/*.go"}, EngineSlots: 2, TestCommand: "go test ./..."},
	}
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", tracks, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	if strings.Contains(yamlStr, "telegraph:") {
		t.Error("rendered YAML should not contain telegraph section when nil")
	}
}

func TestRenderConfig_CustomHost(t *testing.T) {
	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", FilePatterns: []string{"**/*.go"}, EngineSlots: 2, TestCommand: "go test ./..."},
	}
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "10.0.0.5", 3306, "root", tracks, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	cfg, err := config.Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("config.Parse: %v\n---\n%s", err, yamlStr)
	}
	if cfg.Dolt.Host != "10.0.0.5" {
		t.Errorf("Dolt.Host = %q, want %q", cfg.Dolt.Host, "10.0.0.5")
	}
}

func TestInitCmd_InteractiveWithTelegraphSlack(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	// Prompts: owner, remote, host, port, tracks, telegraph yes, platform, channel,
	// bot token var, app token var.
	cmd.SetIn(strings.NewReader("\n\n\n\ny\ny\nslack\nC999\n\n\n"))
	cmd.SetArgs([]string{"init", "--skip-db", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init with telegraph slack: %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "export SLACK_BOT_TOKEN") {
		t.Error("should show export instructions for SLACK_BOT_TOKEN")
	}
	if !strings.Contains(output, "export SLACK_APP_TOKEN") {
		t.Error("should show export instructions for SLACK_APP_TOKEN")
	}
	if !strings.Contains(output, "telegraph-setup.md") {
		t.Error("should reference telegraph-setup.md")
	}
	// Verify config file contains telegraph section.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "telegraph:") {
		t.Error("config file should contain telegraph section")
	}
	if !strings.Contains(string(data), "platform: slack") {
		t.Error("config file should contain platform: slack")
	}
}

func TestInitCmd_SkipTelegraphFlag(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--skip-telegraph", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --skip-telegraph: %v", err)
	}
	// Verify config file does NOT contain telegraph section.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "telegraph:") {
		t.Error("config file should NOT contain telegraph section with --skip-telegraph")
	}
}

func TestInitCmd_YesSkipsTelegraph(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --yes: %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "Telegraph chat bridge: skipped") {
		t.Error("should show telegraph skipped message")
	}
	// Verify config file does NOT contain telegraph section.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "telegraph:") {
		t.Error("config file should NOT contain telegraph section with --yes")
	}
}

func TestInitCmd_ConfigAnchoredToGitRoot(t *testing.T) {
	// When given a relative config path, the file should be written
	// to the git root, not the current working directory.
	dir := initGitRepo(t)
	sub := filepath.Join(dir, "deep", "sub", "dir")
	os.MkdirAll(sub, 0755)

	// Change to subdirectory for this test.
	orig, _ := os.Getwd()
	os.Chdir(sub)
	t.Cleanup(func() { os.Chdir(orig) })

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init from subdirectory: %v", err)
	}
	// Config should be at the git root, not in the subdirectory.
	if _, err := os.Stat(filepath.Join(dir, "railyard.yaml")); err != nil {
		t.Errorf("expected railyard.yaml at git root %s: %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(sub, "railyard.yaml")); err == nil {
		t.Error("railyard.yaml should NOT be in the subdirectory")
	}
}
