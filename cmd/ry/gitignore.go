package main

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
		seen[key] = true
		patterns, ok := languageIgnorePatterns[key]
		if !ok {
			continue
		}
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
	case "java", "kotlin":
		return "Java"
	case "swift":
		return "Swift"
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
		"engines/",
		"cocoindex/.venv/",
		"cocoindex/__pycache__/",
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

// detectLanguages scans the project root for language indicator files.
func detectLanguages(root string) []string {
	indicators := []struct {
		path string // file or dir relative to root
		lang string
	}{
		{"go.mod", "go"},
		{"package.json", "typescript"},
		{"Cargo.toml", "rust"},
		{"setup.py", "python"},
		{"pyproject.toml", "python"},
		{"requirements.txt", "python"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"build.gradle.kts", "java"},
		{"Package.swift", "swift"},
		{"Gemfile", "ruby"},
		{"composer.json", "php"},
		{"mix.exs", "elixir"},
		{"CMakeLists.txt", "c"},
		{"Makefile.am", "c"},
		{"*.csproj", "csharp"},
		{"*.sln", "csharp"},
	}

	seen := map[string]bool{}
	var languages []string

	for _, ind := range indicators {
		if strings.Contains(ind.path, "*") {
			// Glob pattern.
			matches, _ := filepath.Glob(filepath.Join(root, ind.path))
			if len(matches) > 0 {
				key := canonicalLanguage(ind.lang)
				if key != "" && !seen[key] {
					languages = append(languages, ind.lang)
					seen[key] = true
				}
			}
		} else {
			if _, err := os.Stat(filepath.Join(root, ind.path)); err == nil {
				key := canonicalLanguage(ind.lang)
				if key != "" && !seen[key] {
					languages = append(languages, ind.lang)
					seen[key] = true
				}
			}
		}
	}

	sort.Strings(languages)
	return languages
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
