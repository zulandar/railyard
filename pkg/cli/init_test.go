package cli

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
	// A root tsconfig.json makes the Node flavor resolve to typescript rather
	// than javascript (railyard-a37.3).
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte("{}\n"), 0644); err != nil {
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

// TestDetectLanguages_DartFlutter verifies that detectLanguages identifies a
// Dart/Flutter project by the presence of pubspec.yaml.
func TestDetectLanguages_DartFlutter(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte("name: example\n"), 0644); err != nil {
		t.Fatal(err)
	}

	languages := detectLanguages(dir)
	if len(languages) != 1 || languages[0] != "dart" {
		t.Errorf("expected [dart], got %v", languages)
	}
}

// TestDetectLanguages_AndroidKotlin verifies that an Android project (Android
// Studio standard layout) is detected as kotlin, not java.
func TestDetectLanguages_AndroidKotlin(t *testing.T) {
	dir := t.TempDir()

	manifestDir := filepath.Join(dir, "app", "src", "main")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "AndroidManifest.xml"), []byte("<manifest/>\n"), 0644); err != nil {
		t.Fatal(err)
	}

	languages := detectLanguages(dir)
	if len(languages) != 1 || languages[0] != "kotlin" {
		t.Errorf("expected [kotlin], got %v", languages)
	}
}

// TestDetectLanguages_AndroidKotlinWithGradle verifies that an Android project
// with both an AndroidManifest.xml and build.gradle.kts is detected as kotlin
// (not java) — the Android signal wins over the generic JVM gradle signal.
func TestDetectLanguages_AndroidKotlinWithGradle(t *testing.T) {
	dir := t.TempDir()

	manifestDir := filepath.Join(dir, "app", "src", "main")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "AndroidManifest.xml"), []byte("<manifest/>\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.gradle.kts"), []byte("// kotlin DSL\n"), 0644); err != nil {
		t.Fatal(err)
	}

	languages := detectLanguages(dir)
	if len(languages) != 1 || languages[0] != "kotlin" {
		t.Errorf("expected [kotlin], got %v", languages)
	}
}

// TestDetectLanguages_GradleWithoutAndroid verifies that a pure JVM gradle
// project (no AndroidManifest.xml) is still detected as java — regression
// guard against the Kotlin/Android indicator stealing all gradle projects.
func TestDetectLanguages_GradleWithoutAndroid(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "build.gradle.kts"), []byte("// kotlin DSL\n"), 0644); err != nil {
		t.Fatal(err)
	}

	languages := detectLanguages(dir)
	if len(languages) != 1 || languages[0] != "java" {
		t.Errorf("expected [java], got %v", languages)
	}
}

// TestDetectLanguages_iOSXcodeProject verifies that an Xcode-only iOS project
// (no Package.swift, just a .xcodeproj bundle) is detected as swift.
func TestDetectLanguages_iOSXcodeProject(t *testing.T) {
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "MyApp.xcodeproj"), 0755); err != nil {
		t.Fatal(err)
	}

	languages := detectLanguages(dir)
	if len(languages) != 1 || languages[0] != "swift" {
		t.Errorf("expected [swift], got %v", languages)
	}
}

// TestDetectLanguages_AndroidKotlinWithMaven verifies the Android signal
// suppresses the generic JVM signal even when the JVM marker is pom.xml
// (railyard-382): AndroidManifest.xml + pom.xml yields [kotlin] only, not
// [java, kotlin].
func TestDetectLanguages_AndroidKotlinWithMaven(t *testing.T) {
	dir := t.TempDir()

	manifestDir := filepath.Join(dir, "app", "src", "main")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "AndroidManifest.xml"), []byte("<manifest/>\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>\n"), 0644); err != nil {
		t.Fatal(err)
	}

	languages := detectLanguages(dir)
	if len(languages) != 1 || languages[0] != "kotlin" {
		t.Errorf("expected [kotlin], got %v", languages)
	}
}

// TestDetectLanguages_CrossPlatformMobile verifies the dominant Flutter/React
// Native layout — Xcode bundle under ios/ and AndroidManifest under
// android/app/src/main/ — is fully detected (railyard-7ea). A root-only glob
// would miss swift and kotlin here.
func TestDetectLanguages_CrossPlatformMobile(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte("name: app\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ios", "Runner.xcodeproj"), 0755); err != nil {
		t.Fatal(err)
	}
	androidManifest := filepath.Join(dir, "android", "app", "src", "main")
	if err := os.MkdirAll(androidManifest, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(androidManifest, "AndroidManifest.xml"), []byte("<manifest/>\n"), 0644); err != nil {
		t.Fatal(err)
	}

	languages := detectLanguages(dir)
	want := map[string]bool{"dart": true, "kotlin": true, "swift": true}
	if len(languages) != 3 {
		t.Fatalf("expected 3 languages [dart kotlin swift], got %v", languages)
	}
	for _, l := range languages {
		if !want[l] {
			t.Errorf("unexpected language %q in %v", l, languages)
		}
	}
}

// TestGenerateTracks_ThreeWayMobile verifies the mobile track-name collision
// is resolved for a dart+kotlin+swift repo so all three tracks get unique
// names (railyard-7ea acceptance #4).
func TestGenerateTracks_ThreeWayMobile(t *testing.T) {
	tracks := generateTracks([]string{"dart", "kotlin", "swift"}, t.TempDir())
	if len(tracks) != 3 {
		t.Fatalf("expected 3 tracks, got %d", len(tracks))
	}
	names := map[string]bool{}
	for _, tr := range tracks {
		if names[tr.Name] {
			t.Errorf("duplicate track name %q", tr.Name)
		}
		names[tr.Name] = true
	}
}

// TestDetectLanguages_XcodeprojMustBeDirectory verifies that a regular file
// named like an Xcode bundle does NOT trigger swift detection — .xcodeproj /
// .xcworkspace are always directory bundles (railyard-j63).
func TestDetectLanguages_XcodeprojMustBeDirectory(t *testing.T) {
	dir := t.TempDir()

	// A plain file, not a bundle directory.
	if err := os.WriteFile(filepath.Join(dir, "Stub.xcodeproj"), []byte("not a bundle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	languages := detectLanguages(dir)
	if len(languages) != 0 {
		t.Errorf("expected no languages (a file is not an Xcode bundle), got %v", languages)
	}
}

// TestDetectLanguages_AndroidKotlinNonAppModule verifies multi-module / KMP
// layouts whose Android module isn't literally named "app" are still detected
// as kotlin (railyard-1ax).
func TestDetectLanguages_AndroidKotlinNonAppModule(t *testing.T) {
	for _, module := range []string{"androidApp", "mobile"} {
		t.Run(module, func(t *testing.T) {
			dir := t.TempDir()
			manifestDir := filepath.Join(dir, module, "src", "main")
			if err := os.MkdirAll(manifestDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(manifestDir, "AndroidManifest.xml"), []byte("<manifest/>\n"), 0644); err != nil {
				t.Fatal(err)
			}

			languages := detectLanguages(dir)
			if len(languages) != 1 || languages[0] != "kotlin" {
				t.Errorf("expected [kotlin], got %v", languages)
			}
		})
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

func TestEnsurePluginsDir_CreatesNew(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugins.d")
	var out bytes.Buffer
	if err := ensurePluginsDir(&out, dir); err != nil {
		t.Fatalf("ensurePluginsDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat created dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got mode %v", info.Mode())
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("created dir perm = %#o, want %#o", perm, 0o755)
	}
	if !strings.Contains(out.String(), "Created plugins directory") {
		t.Errorf("expected 'Created plugins directory' in output, got: %q", out.String())
	}
}

func TestEnsurePluginsDir_Idempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugins.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	var out bytes.Buffer
	if err := ensurePluginsDir(&out, dir); err != nil {
		t.Fatalf("ensurePluginsDir on existing dir: %v", err)
	}
	if !strings.Contains(out.String(), "already present") {
		t.Errorf("expected 'already present' in output, got: %q", out.String())
	}
}

func TestEnsurePluginsDir_ExistsAsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(path, []byte("oops"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	var out bytes.Buffer
	err := ensurePluginsDir(&out, path)
	if err == nil {
		t.Fatal("expected error when path exists as a file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should mention 'not a directory': %v", err)
	}
}

func TestEnsurePluginsDir_PermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — permission denied path is unreachable")
	}
	parent := filepath.Join(t.TempDir(), "locked")
	if err := os.MkdirAll(parent, 0o555); err != nil {
		t.Fatalf("seed locked parent: %v", err)
	}
	t.Cleanup(func() {
		// Restore writable mode so t.TempDir cleanup can remove it.
		_ = os.Chmod(parent, 0o755)
	})
	var out bytes.Buffer
	err := ensurePluginsDir(&out, filepath.Join(parent, "plugins.d"))
	if err == nil {
		t.Fatal("expected permission error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error should mention 'permission denied': %v", err)
	}
	if !strings.Contains(err.Error(), "--with-plugins") {
		t.Errorf("error should point to --with-plugins fallback: %v", err)
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
		{"swift", "mobile", "swift test", "**/*.swift"},
		{"kotlin", "mobile", "./gradlew test", "**/*.kt"},
		// No pubspec.yaml in the test root, so dart defaults to plain `dart
		// test` (the Flutter branch is covered by TestLanguagePreset_DartUsesPubspec).
		{"dart", "mobile", "dart test", "**/*.dart"},
		{"unknown-lang", "unknown-lang", "", ""},
	}
	root := t.TempDir()
	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			track := languagePreset(tt.lang, root)
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

// TestDartTestCommand covers both branches of the Dart test-command choice
// (railyard-csp): a Flutter project gets `flutter test`, a pure-Dart package
// gets `dart test`. The pure case includes flutter_lints in dev_dependencies
// to ensure a substring match doesn't wrongly classify it as Flutter.
func TestDartTestCommand(t *testing.T) {
	flutterRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(flutterRoot, "pubspec.yaml"),
		[]byte("name: app\ndependencies:\n  flutter:\n    sdk: flutter\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if cmd, reason := dartTestCommand(flutterRoot); cmd != "flutter test" {
		t.Errorf("flutter project: cmd = %q (%s), want 'flutter test'", cmd, reason)
	}

	pureRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(pureRoot, "pubspec.yaml"),
		[]byte("name: mylib\ndependencies:\n  http: ^1.0.0\ndev_dependencies:\n  flutter_lints: ^3.0.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if cmd, reason := dartTestCommand(pureRoot); cmd != "dart test" {
		t.Errorf("pure-dart project: cmd = %q (%s), want 'dart test' (flutter_lints must not count)", cmd, reason)
	}

	// No pubspec at all → safe default of plain dart.
	if cmd, _ := dartTestCommand(t.TempDir()); cmd != "dart test" {
		t.Errorf("missing pubspec: cmd = %q, want 'dart test'", cmd)
	}
}

// TestLanguagePreset_DartUsesPubspec verifies the dart preset's TestCommand
// reflects the pubspec inspection rather than always being 'flutter test'
// (railyard-csp).
func TestLanguagePreset_DartUsesPubspec(t *testing.T) {
	flutterRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(flutterRoot, "pubspec.yaml"),
		[]byte("dependencies:\n  flutter:\n    sdk: flutter\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if tr := languagePreset("dart", flutterRoot); tr.TestCommand != "flutter test" {
		t.Errorf("flutter: TestCommand = %q, want 'flutter test'", tr.TestCommand)
	}

	pureRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(pureRoot, "pubspec.yaml"), []byte("name: lib\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if tr := languagePreset("dart", pureRoot); tr.TestCommand != "dart test" {
		t.Errorf("pure-dart: TestCommand = %q, want 'dart test'", tr.TestCommand)
	}
}

// TestLanguagePreset_NodeFlavors verifies the typescript and javascript presets
// after the railyard-a37.3 widening: the typescript track now also matches
// .js/.jsx (real TS repos carry JS config/scripts), and the javascript track
// covers .js/.jsx/.mjs/.cjs. Both stay named "frontend" with `npm test`.
func TestLanguagePreset_NodeFlavors(t *testing.T) {
	tests := []struct {
		lang         string
		wantPatterns []string
	}{
		{"typescript", []string{"**/*.ts", "**/*.tsx", "**/*.js", "**/*.jsx"}},
		{"javascript", []string{"**/*.js", "**/*.jsx", "**/*.mjs", "**/*.cjs"}},
	}
	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			track := languagePreset(tt.lang, t.TempDir())
			if track.Name != "frontend" {
				t.Errorf("Name = %q, want %q", track.Name, "frontend")
			}
			if track.TestCommand != "npm test" {
				t.Errorf("TestCommand = %q, want %q", track.TestCommand, "npm test")
			}
			has := map[string]bool{}
			for _, p := range track.FilePatterns {
				has[p] = true
			}
			for _, want := range tt.wantPatterns {
				if !has[want] {
					t.Errorf("FilePatterns %v missing %q", track.FilePatterns, want)
				}
			}
		})
	}
}

// TestLanguagePreset_BackendExtras verifies the elixir/csharp presets and the
// c preset's two test-command branches added in railyard-a37.4. Before the fix
// these languages fell through to the default branch and produced empty
// FilePatterns/TestCommand.
func TestLanguagePreset_BackendExtras(t *testing.T) {
	t.Run("elixir", func(t *testing.T) {
		tr := languagePreset("elixir", t.TempDir())
		if tr.Name != "backend" || tr.TestCommand != "mix test" {
			t.Errorf("elixir = name %q test %q, want backend/mix test", tr.Name, tr.TestCommand)
		}
		if len(tr.FilePatterns) == 0 || tr.FilePatterns[0] != "**/*.ex" {
			t.Errorf("elixir FilePatterns = %v, want first **/*.ex", tr.FilePatterns)
		}
	})

	t.Run("csharp", func(t *testing.T) {
		tr := languagePreset("csharp", t.TempDir())
		if tr.Name != "backend" || tr.TestCommand != "dotnet test" {
			t.Errorf("csharp = name %q test %q, want backend/dotnet test", tr.Name, tr.TestCommand)
		}
		if len(tr.FilePatterns) == 0 || tr.FilePatterns[0] != "**/*.cs" {
			t.Errorf("csharp FilePatterns = %v, want first **/*.cs", tr.FilePatterns)
		}
	})

	t.Run("c-cmake", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "CMakeLists.txt"), []byte("project(x)\n"), 0644); err != nil {
			t.Fatal(err)
		}
		tr := languagePreset("c", dir)
		if tr.Name != "backend" || tr.TestCommand != "ctest" {
			t.Errorf("c+cmake = name %q test %q, want backend/ctest", tr.Name, tr.TestCommand)
		}
		if len(tr.FilePatterns) == 0 || tr.FilePatterns[0] != "**/*.c" {
			t.Errorf("c FilePatterns = %v, want first **/*.c", tr.FilePatterns)
		}
	})

	t.Run("c-make", func(t *testing.T) {
		// No CMakeLists.txt → fall back to make test.
		tr := languagePreset("c", t.TempDir())
		if tr.TestCommand != "make test" {
			t.Errorf("c without cmake: TestCommand = %q, want 'make test'", tr.TestCommand)
		}
	})
}

// TestLanguagePreset_NoFallThrough is an acceptance criterion for railyard-a37.4:
// EVERY language detectLanguages can emit must have a real (non-default) preset
// with non-empty FilePatterns AND a non-empty TestCommand. This kills future
// fall-through where a detector emits a language with no languagePreset case.
func TestLanguagePreset_NoFallThrough(t *testing.T) {
	for _, lang := range detectableLanguages() {
		t.Run(lang, func(t *testing.T) {
			tr := languagePreset(lang, t.TempDir())
			if len(tr.FilePatterns) == 0 {
				t.Errorf("languagePreset(%q) has empty FilePatterns — falls through to default", lang)
			}
			if tr.TestCommand == "" {
				t.Errorf("languagePreset(%q) has empty TestCommand — falls through to default", lang)
			}
		})
	}
}

func TestGenerateTracks(t *testing.T) {
	tracks := generateTracks([]string{"go", "typescript"}, t.TempDir())
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
	tracks := generateTracks([]string{"go", "python"}, t.TempDir())
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
	tracks := generateTracks(nil, t.TempDir())
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
	if !strings.Contains(output, "--with-plugins") {
		t.Errorf("help should show --with-plugins flag: %s", output)
	}
	if !strings.Contains(output, "--with-plugins-global") {
		t.Errorf("help should show --with-plugins-global flag: %s", output)
	}
}

func TestInitCmd_WithPlugins_CreatesPerUserDir(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--with-plugins", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --with-plugins: %v", err)
	}

	pluginsDir := filepath.Join(fakeHome, ".railyard", "plugins")
	info, err := os.Stat(pluginsDir)
	if err != nil {
		t.Fatalf("expected plugins dir at %s: %v", pluginsDir, err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory at %s, got mode %v", pluginsDir, info.Mode())
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("plugins dir perm = %#o, want %#o", perm, 0o755)
	}
	if !strings.Contains(out.String(), "Created plugins directory") {
		t.Errorf("expected creation message in output: %s", out.String())
	}
}

func TestInitCmd_WithPlugins_IdempotentWhenPresent(t *testing.T) {
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	// Pre-seed the per-user plugins dir.
	pluginsDir := filepath.Join(fakeHome, ".railyard", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("seed plugins dir: %v", err)
	}

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--with-plugins", "--config", configPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --with-plugins (re-run): %v", err)
	}
	if !strings.Contains(out.String(), "already present") {
		t.Errorf("expected 'already present' message: %s", out.String())
	}
}

func TestInitCmd_WithPluginsGlobal_PermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — --with-plugins-global would actually succeed and touch /etc")
	}
	dir := initGitRepo(t)
	configPath := filepath.Join(dir, "railyard.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--yes", "--skip-db", "--with-plugins-global", "--config", configPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error from unprivileged --with-plugins-global")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error should mention 'permission denied': %v", err)
	}
	if !strings.Contains(err.Error(), "--with-plugins") {
		t.Errorf("error should point to --with-plugins fallback: %v", err)
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
