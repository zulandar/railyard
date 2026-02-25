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
	err := ensureDoltRunning(&out, "127.0.0.1", 19999)
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
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", 3306, tracks)
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
	yamlStr, err := renderConfig("bob", "git@github.com:org/app.git", 3306, tracks)
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
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --yes --skip-db: %v", err)
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
}

func TestInitCmd_NonGitDir(t *testing.T) {
	dir := t.TempDir()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--config", filepath.Join(dir, "railyard.yaml")})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
}
