package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/config"
)

func newGitIgnoreCmd() *cobra.Command {
	var (
		configPath string
		dryRun     bool
		detect     bool
	)

	cmd := &cobra.Command{
		Use:   "gitignore",
		Short: "Update .gitignore with language-appropriate entries",
		Long: `Detects programming languages in the project and appends missing
.gitignore entries for binaries, build artifacts, caches, and IDE files.

Languages are detected from railyard.yaml tracks by default.
Use --detect to scan the project for language indicators (go.mod,
package.json, Cargo.toml, etc.) instead.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGitIgnore(cmd.OutOrStdout(), configPath, dryRun, detect)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print proposed changes without modifying .gitignore")
	cmd.Flags().BoolVar(&detect, "detect", false, "detect languages from project files instead of config")
	return cmd
}

func runGitIgnore(out io.Writer, configPath string, dryRun, detect bool) error {
	// Step 1: Detect languages.
	var languages []string

	if detect {
		languages = detectLanguages(".")
	} else {
		cfg, err := config.Load(configPath)
		if err != nil {
			// Fall back to detection if config is unavailable.
			fmt.Fprintln(out, "Could not load config, falling back to language detection...")
			languages = detectLanguages(".")
		} else {
			seen := map[string]bool{}
			for _, t := range cfg.Tracks {
				lang := strings.ToLower(t.Language)
				if lang != "" && !seen[lang] {
					languages = append(languages, lang)
					seen[lang] = true
				}
			}
		}
	}

	if len(languages) == 0 {
		fmt.Fprintln(out, "No languages detected")
		return nil
	}

	fmt.Fprintf(out, "Languages: %s\n", strings.Join(languages, ", "))

	// Step 2: Collect ignore patterns for all detected languages.
	groups := collectIgnoreGroups(languages)

	// Always include railyard patterns.
	groups = append([]ignoreGroup{{
		label:    "Railyard",
		patterns: railyardPatterns(),
	}}, groups...)

	// Step 3: Read existing .gitignore.
	existing := readGitIgnoreEntries(".gitignore")

	// Step 4: Compute missing entries.
	var missing []ignoreGroup
	for _, g := range groups {
		var needed []string
		for _, p := range g.patterns {
			if !existing[p] {
				needed = append(needed, p)
			}
		}
		if len(needed) > 0 {
			missing = append(missing, ignoreGroup{label: g.label, patterns: needed})
		}
	}

	if len(missing) == 0 {
		fmt.Fprintln(out, ".gitignore is already up to date")
		return nil
	}

	// Step 5: Format the block to append.
	block := formatIgnoreBlock(missing)

	if dryRun {
		fmt.Fprintln(out, "Would append to .gitignore:")
		fmt.Fprintln(out, "")
		fmt.Fprint(out, block)
		return nil
	}

	// Step 6: Append to .gitignore.
	if err := appendToGitIgnore(".gitignore", block); err != nil {
		return err
	}

	total := 0
	for _, g := range missing {
		total += len(g.patterns)
	}
	fmt.Fprintf(out, "Added %d entries to .gitignore\n", total)
	return nil
}

// ignoreGroup is a labeled set of .gitignore patterns.
type ignoreGroup struct {
	label    string
	patterns []string
}

// collectIgnoreGroups returns ignore groups for the given languages.
func collectIgnoreGroups(languages []string) []ignoreGroup {
	var groups []ignoreGroup
	seen := map[string]bool{}
	for _, lang := range languages {
		key := canonicalLanguage(lang)
		if seen[key] {
			continue
		}
		patterns, ok := languageIgnorePatterns[key]
		if !ok {
			// Don't mark the slot seen on a lookup miss: an unknown or
			// patternless canonical must not suppress a later language that
			// shares it (railyard-4cr).
			continue
		}
		seen[key] = true
		groups = append(groups, ignoreGroup{
			label:    key,
			patterns: patterns,
		})
	}

	// Always add IDE/OS patterns.
	if !seen["ide"] {
		groups = append(groups, ignoreGroup{
			label:    "IDE / OS",
			patterns: ideOSPatterns(),
		})
	}
	return groups
}

// canonicalLanguage normalizes language names.
func canonicalLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "go", "golang":
		return "Go"
	case "python", "py":
		return "Python"
	case "typescript", "ts", "javascript", "js", "node":
		return "Node / TypeScript"
	case "rust", "rs":
		return "Rust"
	case "java":
		return "Java"
	case "kotlin":
		return "Kotlin"
	case "swift":
		return "Swift"
	case "dart", "flutter":
		return "Dart"
	case "ruby", "rb":
		return "Ruby"
	case "php":
		return "PHP"
	case "elixir", "ex":
		return "Elixir"
	case "c", "cpp", "c++", "cxx":
		return "C / C++"
	case "csharp", "c#", "dotnet":
		return "C# / .NET"
	default:
		return ""
	}
}

// languageIgnorePatterns maps canonical language names to ignore patterns.
var languageIgnorePatterns = map[string][]string{
	"Go": {
		"*.exe",
		"*.test",
		"*.out",
		"*.prof",
		"vendor/",
	},
	"Python": {
		"__pycache__/",
		"*.py[cod]",
		"*.egg-info/",
		"*.egg",
		".eggs/",
		"dist/",
		"build/",
		".venv/",
		".mypy_cache/",
		".pytest_cache/",
	},
	"Node / TypeScript": {
		"node_modules/",
		"dist/",
		"build/",
		".next/",
		"*.tsbuildinfo",
		".npm/",
		".yarn/",
	},
	"Rust": {
		"target/",
		"*.pdb",
	},
	"Java": {
		"target/",
		"*.class",
		"*.jar",
		"*.war",
		".gradle/",
		"build/",
	},
	"Kotlin": {
		"*.class",
		".gradle/",
		"build/",
		"local.properties",
		"captures/",
		".cxx/",
		".externalNativeBuild/",
		"*.apk",
		"*.aab",
		"*.ap_",
		"*.dex",
		"*.keystore",
		"app/release/",
	},
	"Dart": {
		".dart_tool/",
		".packages",
		"build/",
		".flutter-plugins",
		".flutter-plugins-dependencies",
		".pub-cache/",
		"*.dart.js",
	},
	"Swift": {
		".build/",
		"DerivedData/",
		"*.xcuserstate",
		"Pods/",
	},
	"Ruby": {
		"vendor/bundle/",
		".bundle/",
		"*.gem",
		"tmp/",
	},
	"PHP": {
		"vendor/",
		"composer.lock",
		".phpunit.cache/",
	},
	"Elixir": {
		"_build/",
		"deps/",
		"*.ez",
	},
	"C / C++": {
		"*.o",
		"*.a",
		"*.so",
		"*.dylib",
		"*.dll",
		"build/",
		"cmake-build-*/",
	},
	"C# / .NET": {
		"bin/",
		"obj/",
		"*.user",
		"*.suo",
		"packages/",
	},
}

func railyardPatterns() []string {
	return []string{
		".claude",
		".railyard/",
		".claudeignore",
		".mcp.json",
		".beads/",
		"cocoindex/.venv/",
		"cocoindex/__pycache__/",
		"coverage.out",
		".env",
		".env.*",
		"*.pem",
		"*.key",
		"*.p12",
		"*.pfx",
		"*.secret",
		"credentials.json",
	}
}

func ideOSPatterns() []string {
	return []string{
		".DS_Store",
		".idea/",
		".vscode/",
		"*.swp",
		"*.swo",
		"*~",
		"Thumbs.db",
	}
}

// detectableLanguages is the static superset of every language detectLanguages
// can emit. It exists so a single anti-fall-through test can assert that every
// detectable language has a real languagePreset (non-empty FilePatterns AND
// TestCommand) — see TestLanguagePreset_NoFallThrough (railyard-a37.4). Keep
// this list in sync with the indicators in detectLanguages and the jsTrackLanguage
// flavors ("javascript"/"typescript").
func detectableLanguages() []string {
	return []string{
		"go", "typescript", "javascript", "rust", "python", "dart",
		"kotlin", "java", "swift", "ruby", "php", "elixir", "c", "csharp",
	}
}

// jsTrackLanguage classifies a Node project rooted at root as either
// "typescript" or "javascript". Signals, in priority order:
//
//  1. PRIMARY: a root-level tsconfig.json exists → typescript. This is the
//     authoritative declaration that the repo is a TypeScript project.
//  2. SECONDARY (cheap): package.json mentions "typescript" (e.g. as a dep) →
//     typescript. A plain substring check is sufficient here.
//
// Otherwise the repo is plain JavaScript. Detection only inspects the ROOT
// tsconfig.json: a monorepo with package.json at the root but tsconfig.json
// only in a SUBDIR classifies as "javascript" (we don't walk subtrees).
func jsTrackLanguage(root string) string {
	if _, err := os.Stat(filepath.Join(root, "tsconfig.json")); err == nil {
		return "typescript"
	}
	if data, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		if strings.Contains(string(data), `"typescript"`) {
			return "typescript"
		}
	}
	return "javascript"
}

// detectLanguages scans the project root for language indicator files.
func detectLanguages(root string) []string {
	// Each indicator lists one or more candidate paths (globs are supported);
	// the language is detected if ANY candidate matches. Mobile markers cover
	// both the single-module Android Studio layout and the nested ios/android
	// layouts that Flutter, React Native, and Capacitor generate (railyard-7ea).
	//
	// The package.json indicator's lang is resolved per-repo via jsTrackLanguage
	// (ts vs js) rather than hardcoded "typescript": a plain-JS repo must get a
	// javascript track whose patterns match its .js files, not a dead TS track
	// (railyard-a37.3).
	indicators := []struct {
		paths   []string // file/dir globs relative to root; any match counts
		lang    string
		dirOnly bool // require a matched glob to be a directory (bundle markers)
	}{
		{paths: []string{"go.mod"}, lang: "go"},
		{paths: []string{"package.json"}, lang: jsTrackLanguage(root)},
		{paths: []string{"Cargo.toml"}, lang: "rust"},
		{paths: []string{"setup.py"}, lang: "python"},
		{paths: []string{"pyproject.toml"}, lang: "python"},
		{paths: []string{"requirements.txt"}, lang: "python"},
		{paths: []string{"pubspec.yaml"}, lang: "dart"},
		// Android marker. Listed before the generic JVM entries; an explicit
		// suppression pass below drops java when kotlin is detected so an
		// Android repo doesn't also get the Java track/gitignore (railyard-382).
		// The module dir isn't always "app": multi-module and Kotlin
		// Multiplatform projects use other names (railyard-1ax).
		{paths: []string{
			"app/src/main/AndroidManifest.xml",         // single-module Android Studio
			"mobile/src/main/AndroidManifest.xml",      // multi-module
			"androidApp/src/main/AndroidManifest.xml",  // Kotlin Multiplatform
			"android/app/src/main/AndroidManifest.xml", // Flutter / React Native
		}, lang: "kotlin"},
		{paths: []string{"pom.xml"}, lang: "java"},
		{paths: []string{"build.gradle", "build.gradle.kts"}, lang: "java"},
		{paths: []string{"Package.swift"}, lang: "swift"},
		// .xcodeproj / .xcworkspace are always directory bundles; a regular
		// file with that name must not trigger a phantom swift track
		// (railyard-j63).
		{paths: []string{
			"*.xcodeproj", "*.xcworkspace", // repo root
			"ios/*.xcodeproj", "ios/*.xcworkspace", // Flutter / React Native
			"ios/*/*.xcodeproj", "ios/*/*.xcworkspace", // Capacitor / Cordova
		}, lang: "swift", dirOnly: true},
		{paths: []string{"Gemfile"}, lang: "ruby"},
		{paths: []string{"composer.json"}, lang: "php"},
		{paths: []string{"mix.exs"}, lang: "elixir"},
		{paths: []string{"CMakeLists.txt"}, lang: "c"},
		{paths: []string{"Makefile.am"}, lang: "c"},
		{paths: []string{"*.csproj"}, lang: "csharp"},
		{paths: []string{"*.sln"}, lang: "csharp"},
	}

	seen := map[string]bool{}
	var languages []string

	for _, ind := range indicators {
		key := canonicalLanguage(ind.lang)
		if key == "" || seen[key] {
			continue
		}
		if indicatorMatches(root, ind.paths, ind.dirOnly) {
			languages = append(languages, ind.lang)
			seen[key] = true
		}
	}

	languages = suppressRedundantLanguages(languages)
	languages = suppressGeneratedNativeTracks(languages, root)
	sort.Strings(languages)
	return languages
}

// suppressGeneratedNativeTracks drops kotlin/swift from a React Native or Expo
// repo. A bare/ejected RN (or prebuilt Expo) app has android/ and ios/
// directories, which trip the Android and Swift markers — but those native
// dirs are generated scaffolding, not hand-authored native code, so emitting
// kotlin+swift tracks alongside the JS/TS track is a confusing multi-track mix
// for what is a single JS/TS codebase (railyard-rdk). Managed Expo apps have
// no native dirs, so this is a no-op for them; it only matters once a repo has
// ejected/prebuilt. Flutter and hand-written native apps have no react-native/
// expo dependency, so they keep their kotlin/swift tracks.
func suppressGeneratedNativeTracks(languages []string, root string) []string {
	if !isReactNativeProject(root) && !isExpoProject(root) {
		return languages
	}
	filtered := languages[:0]
	for _, l := range languages {
		if l == "kotlin" || l == "swift" {
			continue
		}
		filtered = append(filtered, l)
	}
	return filtered
}

// indicatorMatches reports whether any candidate path (literal or glob) exists
// under root. filepath.Glob matches a metacharacter-free pattern as a literal
// path, so it covers both cases. When dirOnly is set, a match only counts if
// it is a directory — used for bundle markers like *.xcodeproj that are always
// directories, so a same-named regular file doesn't false-trigger detection.
func indicatorMatches(root string, paths []string, dirOnly bool) bool {
	for _, p := range paths {
		matches, _ := filepath.Glob(filepath.Join(root, p))
		for _, m := range matches {
			if !dirOnly {
				return true
			}
			if info, err := os.Stat(m); err == nil && info.IsDir() {
				return true
			}
		}
	}
	return false
}

// suppressRedundantLanguages removes detections that a more specific signal
// makes redundant. Today: a kotlin (Android) project also carrying generic JVM
// build files (pom.xml / build.gradle) should not additionally surface as java
// — the Android signal wins (railyard-382). Previously this fell out of kotlin
// and java sharing a canonical; now that they're distinct it is explicit.
func suppressRedundantLanguages(languages []string) []string {
	hasKotlin := false
	for _, l := range languages {
		if l == "kotlin" {
			hasKotlin = true
			break
		}
	}
	if !hasKotlin {
		return languages
	}
	filtered := languages[:0]
	for _, l := range languages {
		if l == "java" {
			continue
		}
		filtered = append(filtered, l)
	}
	return filtered
}

// readGitIgnoreEntries reads the existing .gitignore and returns a set of
// trimmed, non-comment, non-empty lines.
func readGitIgnoreEntries(path string) map[string]bool {
	entries := map[string]bool{}
	f, err := os.Open(path)
	if err != nil {
		return entries
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries[line] = true
	}
	return entries
}

// formatIgnoreBlock formats grouped ignore patterns for appending.
func formatIgnoreBlock(groups []ignoreGroup) string {
	var b strings.Builder
	b.WriteString("\n")
	for _, g := range groups {
		b.WriteString(fmt.Sprintf("# %s\n", g.label))
		for _, p := range g.patterns {
			b.WriteString(p + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// appendToGitIgnore appends a block of text to .gitignore, creating it if needed.
func appendToGitIgnore(path, block string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open .gitignore: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(block); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	return nil
}
