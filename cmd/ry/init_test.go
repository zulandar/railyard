package main

import (
	"bytes"
	"fmt"
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

// TestDetectLanguages_PHPRepo verifies that detectLanguages identifies a PHP
// project by the presence of a composer.json file.
func TestDetectLanguages_PHPRepo(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	languages := detectLanguages(dir)
	if len(languages) != 1 {
		t.Fatalf("expected 1 language, got %v", languages)
	}
	if languages[0] != "php" {
		t.Errorf("expected language %q, got %q", "php", languages[0])
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

func TestEnsureDBDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db-data")
	// Directory doesn't exist yet.
	if err := ensureDBDataDir(dir); err != nil {
		t.Fatalf("ensureDBDataDir: %v", err)
	}
	// Should have created the directory.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("directory was not created")
	}
	// Calling again should be idempotent.
	if err := ensureDBDataDir(dir); err != nil {
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
		{"php", "backend", "vendor/bin/phpunit", "**/*.php"},
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

func TestEnsureDBRunning_SkipsDockerWhenAlreadyReachable(t *testing.T) {
	origProbe := dbProbeFn
	origExec := execCommandFn
	defer func() { dbProbeFn = origProbe; execCommandFn = origExec }()

	dbProbeFn = func(host string, port int, username, password string) error {
		return nil // DB is reachable
	}
	dockerCalled := false
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		if name == "docker" {
			dockerCalled = true
		}
		return exec.Command("echo") // no-op
	}

	var out bytes.Buffer
	err := ensureDBRunning(&out, "127.0.0.1", 3306, "root", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dockerCalled {
		t.Error("docker should not be called when DB is already reachable")
	}
	if !strings.Contains(out.String(), "already running") {
		t.Errorf("output should mention 'already running': %s", out.String())
	}
}

func TestEnsureDBRunning_StartsDockerWhenUnreachable(t *testing.T) {
	origProbe := dbProbeFn
	origExec := execCommandFn
	defer func() { dbProbeFn = origProbe; execCommandFn = origExec }()

	probeCount := 0
	dbProbeFn = func(host string, port int, username, password string) error {
		probeCount++
		if probeCount <= 2 {
			return fmt.Errorf("connection refused")
		}
		return nil // ready on 3rd attempt
	}

	var capturedArgs []string
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		all := append([]string{name}, arg...)
		capturedArgs = append(capturedArgs, strings.Join(all, " "))
		// Return a command that succeeds
		return exec.Command("echo", "ok")
	}

	var out bytes.Buffer
	err := ensureDBRunning(&out, "127.0.0.1", 3307, "root", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify docker run was called with expected args.
	found := false
	for _, args := range capturedArgs {
		if strings.Contains(args, "docker run -d") {
			found = true
			if !strings.Contains(args, "--name railyard-mysql") {
				t.Errorf("missing --name railyard-mysql in: %s", args)
			}
			if !strings.Contains(args, "MYSQL_ALLOW_EMPTY_PASSWORD=yes") {
				t.Errorf("missing MYSQL_ALLOW_EMPTY_PASSWORD=yes in: %s", args)
			}
			if !strings.Contains(args, "3307:3306") {
				t.Errorf("missing port mapping 3307:3306 in: %s", args)
			}
			if !strings.Contains(args, "mysql:8.0") {
				t.Errorf("missing mysql:8.0 image in: %s", args)
			}
		}
	}
	if !found {
		t.Errorf("docker run not found in captured commands: %v", capturedArgs)
	}
}

func TestEnsureDBRunning_DockerRunFails_ReturnsError(t *testing.T) {
	origProbe := dbProbeFn
	origExec := execCommandFn
	defer func() { dbProbeFn = origProbe; execCommandFn = origExec }()

	dbProbeFn = func(host string, port int, username, password string) error {
		return fmt.Errorf("connection refused")
	}

	callCount := 0
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		callCount++
		if callCount == 1 {
			// docker rm -f — let it succeed
			return exec.Command("echo")
		}
		// docker run — make it fail
		return exec.Command("sh", "-c", "echo 'Cannot connect to Docker daemon' >&2; exit 1")
	}

	var out bytes.Buffer
	err := ensureDBRunning(&out, "127.0.0.1", 3306, "root", "")
	if err == nil {
		t.Fatal("expected error when docker run fails")
	}
	if !strings.Contains(err.Error(), "start database container") {
		t.Errorf("error should mention container start failure: %v", err)
	}
}

func TestEnsureDBRunning_RemoteHostSkipsDocker(t *testing.T) {
	// When host is not local, ensureDBRunning should return immediately
	// without attempting any network or Docker operations.
	var out bytes.Buffer
	err := ensureDBRunning(&out, "10.0.0.5", 3306, "root", "")
	if err == nil {
		t.Fatal("expected error for non-local host")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "not local") {
		t.Errorf("error should mention 'not local': %v", err)
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
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", "", tracks, nil)
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
	yamlStr, err := renderConfig("bob", "git@github.com:org/app.git", "127.0.0.1", 3306, "root", "", tracks, nil)
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
	if !strings.Contains(output, "--user") {
		t.Errorf("help should show --user flag: %s", output)
	}
	if !strings.Contains(output, "--password") {
		t.Errorf("help should show --password flag: %s", output)
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
	if cfg.Database.Port != 3307 {
		t.Errorf("Database.Port = %d, want 3307", cfg.Database.Port)
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
	if cfg.Database.Host != "10.0.0.5" {
		t.Errorf("Database.Host = %q, want %q", cfg.Database.Host, "10.0.0.5")
	}
}

func TestInitCmd_NonInteractive_CustomUser(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--user", "admin", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --yes --skip-db --user admin: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parse generated config: %v\n---\n%s", err, string(data))
	}
	if cfg.Database.Username != "admin" {
		t.Errorf("Database.Username = %q, want %q", cfg.Database.Username, "admin")
	}
}

func TestInitCmd_NonInteractive_CustomPassword(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--password", "secret123", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --yes --skip-db --password secret123: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parse generated config: %v\n---\n%s", err, string(data))
	}
	if cfg.Database.Password != "secret123" {
		t.Errorf("Database.Password = %q, want %q", cfg.Database.Password, "secret123")
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
	// host, user, password, and port, then accept tracks, then decline telegraph.
	cmd.SetIn(strings.NewReader("yes\n\n\n\n\n\n\ny\nn\n"))
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
	yamlStr, err := renderConfig("alice", "", "127.0.0.1", 3306, "root", "", tracks, nil)
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
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", "", tracks, tg)
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
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", "", tracks, tg)
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
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", "", tracks, nil)
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
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "10.0.0.5", 3306, "root", "", tracks, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	cfg, err := config.Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("config.Parse: %v\n---\n%s", err, yamlStr)
	}
	if cfg.Database.Host != "10.0.0.5" {
		t.Errorf("Database.Host = %q, want %q", cfg.Database.Host, "10.0.0.5")
	}
}

func TestRenderConfig_CustomUser(t *testing.T) {
	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", FilePatterns: []string{"**/*.go"}, EngineSlots: 2, TestCommand: "go test ./..."},
	}
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "deploy", "", tracks, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	cfg, err := config.Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("config.Parse: %v\n---\n%s", err, yamlStr)
	}
	if cfg.Database.Username != "deploy" {
		t.Errorf("Database.Username = %q, want %q", cfg.Database.Username, "deploy")
	}
}

func TestRenderConfig_WithPassword(t *testing.T) {
	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", FilePatterns: []string{"**/*.go"}, EngineSlots: 2, TestCommand: "go test ./..."},
	}
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", "secret", tracks, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	cfg, err := config.Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("config.Parse: %v\n---\n%s", err, yamlStr)
	}
	if cfg.Database.Password != "secret" {
		t.Errorf("Database.Password = %q, want %q", cfg.Database.Password, "secret")
	}
}

func TestRenderConfig_EmptyPassword(t *testing.T) {
	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", FilePatterns: []string{"**/*.go"}, EngineSlots: 2, TestCommand: "go test ./..."},
	}
	yamlStr, err := renderConfig("alice", "git@github.com:org/repo.git", "127.0.0.1", 3306, "root", "", tracks, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	if strings.Contains(yamlStr, "password:") {
		t.Errorf("rendered YAML should not contain password line when empty:\n%s", yamlStr)
	}
}

func TestInitCmd_InteractiveWithTelegraphSlack(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	// Prompts: owner, remote, host, user, password, port, tracks, telegraph yes, platform, channel,
	// bot token var, app token var.
	cmd.SetIn(strings.NewReader("\n\n\n\n\n\ny\ny\nslack\nC999\n\n\n"))
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

// TestPromptPassword_PipedInput verifies that promptPassword falls back to
// line-based reading when stdin is not a terminal (e.g., piped in tests).
func TestPromptPassword_PipedInput(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		defVal string
		want   string
	}{
		{"typed password", "mySecret\n", "", "mySecret"},
		{"empty uses default", "\n", "existing", "existing"},
		{"whitespace trimmed", "  secret123  \n", "", "secret123"},
		{"no default empty input", "\n", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := strings.NewReader(tt.input)
			var out bytes.Buffer
			got := promptPassword(in, &out, "Password", tt.defVal)
			if got != tt.want {
				t.Errorf("promptPassword() = %q, want %q", got, tt.want)
			}
			// Verify the prompt label was written to output.
			if !strings.Contains(out.String(), "Password") {
				t.Error("expected prompt label in output")
			}
			// Verify the prompt shows "(input hidden)" hint.
			if !strings.Contains(out.String(), "input hidden") {
				t.Error("expected '(input hidden)' hint in output")
			}
		})
	}
}

// TestPromptPassword_NotEchoed verifies the prompt does not echo the password
// back in its output (the output buffer should only contain the prompt label).
func TestPromptPassword_NotEchoed(t *testing.T) {
	in := strings.NewReader("superSecret123\n")
	var out bytes.Buffer
	got := promptPassword(in, &out, "Enter password", "")
	if got != "superSecret123" {
		t.Fatalf("promptPassword() = %q, want %q", got, "superSecret123")
	}
	// The output should contain the prompt but NOT the password.
	if strings.Contains(out.String(), "superSecret123") {
		t.Error("password was echoed in output — should be hidden")
	}
}
