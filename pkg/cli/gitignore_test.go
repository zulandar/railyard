package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalLanguage(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"go", "Go"},
		{"golang", "Go"},
		{"Go", "Go"},
		{"python", "Python"},
		{"py", "Python"},
		{"typescript", "Node / TypeScript"},
		{"ts", "Node / TypeScript"},
		{"javascript", "Node / TypeScript"},
		{"js", "Node / TypeScript"},
		{"node", "Node / TypeScript"},
		{"rust", "Rust"},
		{"rs", "Rust"},
		{"java", "Java"},
		{"kotlin", "Kotlin"},
		{"swift", "Swift"},
		{"dart", "Dart"},
		{"flutter", "Dart"},
		{"ruby", "Ruby"},
		{"rb", "Ruby"},
		{"php", "PHP"},
		{"elixir", "Elixir"},
		{"ex", "Elixir"},
		{"c", "C / C++"},
		{"cpp", "C / C++"},
		{"csharp", "C# / .NET"},
		{"dotnet", "C# / .NET"},
		{"unknown", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := canonicalLanguage(tt.input)
		if got != tt.want {
			t.Errorf("canonicalLanguage(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCollectIgnoreGroups_Go(t *testing.T) {
	groups := collectIgnoreGroups([]string{"go"})
	if len(groups) == 0 {
		t.Fatal("expected at least one group")
	}

	found := false
	for _, g := range groups {
		if g.label == "Go" {
			found = true
			if len(g.patterns) == 0 {
				t.Error("Go group has no patterns")
			}
		}
	}
	if !found {
		t.Error("missing Go group")
	}

	// Should also include IDE/OS group.
	ideFound := false
	for _, g := range groups {
		if g.label == "IDE / OS" {
			ideFound = true
		}
	}
	if !ideFound {
		t.Error("missing IDE / OS group")
	}
}

func TestCollectIgnoreGroups_DeduplicatesLanguages(t *testing.T) {
	groups := collectIgnoreGroups([]string{"go", "golang", "Go"})
	goCount := 0
	for _, g := range groups {
		if g.label == "Go" {
			goCount++
		}
	}
	if goCount != 1 {
		t.Errorf("expected 1 Go group, got %d", goCount)
	}
}

func TestCollectIgnoreGroups_MultipleLanguages(t *testing.T) {
	groups := collectIgnoreGroups([]string{"go", "python", "typescript"})
	labels := map[string]bool{}
	for _, g := range groups {
		labels[g.label] = true
	}
	for _, want := range []string{"Go", "Python", "Node / TypeScript", "IDE / OS"} {
		if !labels[want] {
			t.Errorf("missing group %q", want)
		}
	}
}

// TestCollectIgnoreGroups_Kotlin verifies that a kotlin track gets its own
// Android-appropriate group rather than Java's patterns (railyard-382).
func TestCollectIgnoreGroups_Kotlin(t *testing.T) {
	groups := collectIgnoreGroups([]string{"kotlin"})

	var kotlin *ignoreGroup
	for i := range groups {
		if groups[i].label == "Kotlin" {
			kotlin = &groups[i]
		}
		if groups[i].label == "Java" {
			t.Error("kotlin must not emit a Java group")
		}
	}
	if kotlin == nil {
		t.Fatal("missing Kotlin group")
	}
	has := map[string]bool{}
	for _, p := range kotlin.patterns {
		has[p] = true
	}
	// Android-specific entries that Java's block lacks.
	for _, want := range []string{"local.properties", "*.apk", "*.aab"} {
		if !has[want] {
			t.Errorf("Kotlin group missing Android pattern %q", want)
		}
	}
	// Java-only artifacts that don't belong to an Android project.
	for _, unwanted := range []string{"*.jar", "*.war"} {
		if has[unwanted] {
			t.Errorf("Kotlin group should not contain Java artifact %q", unwanted)
		}
	}
}

// TestCollectIgnoreGroups_Dart verifies that a dart track actually emits
// Dart-specific patterns instead of being silently dropped (railyard-4cr).
func TestCollectIgnoreGroups_Dart(t *testing.T) {
	groups := collectIgnoreGroups([]string{"dart"})

	var dart *ignoreGroup
	for i := range groups {
		if groups[i].label == "Dart" {
			dart = &groups[i]
		}
	}
	if dart == nil {
		t.Fatal("missing Dart group — patterns were silently dropped")
	}
	has := map[string]bool{}
	for _, p := range dart.patterns {
		has[p] = true
	}
	for _, want := range []string{".dart_tool/", "build/"} {
		if !has[want] {
			t.Errorf("Dart group missing %q", want)
		}
	}
}

// TestCollectIgnoreGroups_UnknownDoesNotConsumeDedupSlot guards the structural
// bug in railyard-4cr: a language whose canonical has no patterns map entry
// must not consume the dedup slot and suppress a later real group. "flutter"
// and "dart" share the canonical "Dart"; the Dart group must still appear.
func TestCollectIgnoreGroups_FlutterAndDartShareOneGroup(t *testing.T) {
	groups := collectIgnoreGroups([]string{"flutter", "dart"})
	dartCount := 0
	for _, g := range groups {
		if g.label == "Dart" {
			dartCount++
		}
	}
	if dartCount != 1 {
		t.Errorf("expected exactly 1 Dart group, got %d", dartCount)
	}
}

func TestReadGitIgnoreEntries(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, ".gitignore")
	content := `# comment
.claude
/ry
.railyard/

# another comment
*.exe
`
	os.WriteFile(path, []byte(content), 0644)

	entries := readGitIgnoreEntries(path)
	for _, want := range []string{".claude", "/ry", ".railyard/", "*.exe"} {
		if !entries[want] {
			t.Errorf("missing entry %q", want)
		}
	}
	// Comments and empty lines should not be entries.
	if entries["# comment"] {
		t.Error("comments should not be entries")
	}
}

func TestReadGitIgnoreEntries_MissingFile(t *testing.T) {
	entries := readGitIgnoreEntries("/nonexistent/.gitignore")
	if len(entries) != 0 {
		t.Errorf("expected empty map, got %d entries", len(entries))
	}
}

func TestFormatIgnoreBlock(t *testing.T) {
	groups := []ignoreGroup{
		{label: "Go", patterns: []string{"*.exe", "*.test"}},
		{label: "Python", patterns: []string{"__pycache__/"}},
	}
	block := formatIgnoreBlock(groups)
	if !strings.Contains(block, "# Go") {
		t.Error("missing Go header")
	}
	if !strings.Contains(block, "*.exe") {
		t.Error("missing *.exe")
	}
	if !strings.Contains(block, "# Python") {
		t.Error("missing Python header")
	}
	if !strings.Contains(block, "__pycache__/") {
		t.Error("missing __pycache__/")
	}
}

func TestDetectLanguages(t *testing.T) {
	tmpDir := t.TempDir()

	// Create go.mod and package.json.
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte("{}"), 0644)

	languages := detectLanguages(tmpDir)
	if len(languages) < 2 {
		t.Fatalf("expected >=2 languages, got %v", languages)
	}

	goFound, tsFound := false, false
	for _, l := range languages {
		switch canonicalLanguage(l) {
		case "Go":
			goFound = true
		case "Node / TypeScript":
			tsFound = true
		}
	}
	if !goFound {
		t.Error("did not detect Go")
	}
	if !tsFound {
		t.Error("did not detect TypeScript")
	}
}

func TestDetectLanguages_NoFiles(t *testing.T) {
	tmpDir := t.TempDir()
	languages := detectLanguages(tmpDir)
	if len(languages) != 0 {
		t.Errorf("expected no languages, got %v", languages)
	}
}

func TestRunGitIgnore_DryRun(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	// Create go.mod so Go is detected.
	os.WriteFile("go.mod", []byte("module test"), 0644)

	var buf bytes.Buffer
	err := runGitIgnore(&buf, "railyard.yaml", true, true)
	if err != nil {
		t.Fatalf("runGitIgnore() error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Would append") {
		t.Error("dry-run should say 'Would append'")
	}
	if !strings.Contains(output, "*.exe") {
		t.Error("dry-run should include Go patterns")
	}

	// .gitignore should not exist.
	if _, err := os.Stat(".gitignore"); err == nil {
		t.Error(".gitignore should not be created in dry-run mode")
	}
}

func TestRunGitIgnore_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	os.WriteFile("go.mod", []byte("module test"), 0644)

	var buf bytes.Buffer
	err := runGitIgnore(&buf, "railyard.yaml", false, true)
	if err != nil {
		t.Fatalf("runGitIgnore() error: %v", err)
	}

	data, err := os.ReadFile(".gitignore")
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "*.exe") {
		t.Error(".gitignore missing Go patterns")
	}
	if !strings.Contains(content, ".claude") {
		t.Error(".gitignore missing Railyard patterns")
	}
	if !strings.Contains(content, ".DS_Store") {
		t.Error(".gitignore missing IDE/OS patterns")
	}
}

func TestRunGitIgnore_SkipsDuplicates(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	os.WriteFile("go.mod", []byte("module test"), 0644)

	// Pre-create .gitignore with some Go patterns.
	existing := ".claude\n.railyard/\n*.exe\n*.test\n"
	os.WriteFile(".gitignore", []byte(existing), 0644)

	var buf bytes.Buffer
	err := runGitIgnore(&buf, "railyard.yaml", false, true)
	if err != nil {
		t.Fatalf("runGitIgnore() error: %v", err)
	}

	data, _ := os.ReadFile(".gitignore")
	content := string(data)

	// *.exe should appear only once (the original).
	if strings.Count(content, "*.exe") != 1 {
		t.Errorf("*.exe should appear exactly once, got %d", strings.Count(content, "*.exe"))
	}
}

func TestRunGitIgnore_AlreadyUpToDate(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	// No language indicators.
	var buf bytes.Buffer
	err := runGitIgnore(&buf, "railyard.yaml", false, true)
	if err != nil {
		t.Fatalf("runGitIgnore() error: %v", err)
	}

	if !strings.Contains(buf.String(), "No languages detected") {
		t.Error("expected 'No languages detected' message")
	}
}

func TestNewGitIgnoreCmd_Structure(t *testing.T) {
	cmd := newGitIgnoreCmd()
	if cmd.Use != "gitignore" {
		t.Errorf("Use = %q, want %q", cmd.Use, "gitignore")
	}

	for _, flag := range []string{"config", "dry-run", "detect"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("missing --%s flag", flag)
		}
	}
}

func TestRailyardPatterns(t *testing.T) {
	patterns := railyardPatterns()
	if len(patterns) == 0 {
		t.Error("expected railyard patterns")
	}
	has := map[string]bool{}
	for _, p := range patterns {
		has[p] = true
	}
	for _, want := range []string{".claude", ".railyard/", ".claudeignore", ".mcp.json", ".beads/"} {
		if !has[want] {
			t.Errorf("missing pattern %q", want)
		}
	}
}
