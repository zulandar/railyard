package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
)

// Pre-compiled regexps for sanitizeOwner.
var (
	reNonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9-]`)
	reMultipleHyphens   = regexp.MustCompile(`-{2,}`)
)

// detectGitRoot runs `git rev-parse --show-toplevel` from dir and returns
// the trimmed absolute path to the repository root, or an error if dir is
// not inside a git repository.
func detectGitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// detectGitRemote runs `git remote get-url origin` from dir and returns
// the remote URL. If no remote named "origin" is configured, it returns
// an empty string with no error.
func detectGitRemote(dir string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// No remote configured is not an error for our purposes.
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// detectOwner returns a sanitized owner name for the repository.
// It tries git config user.name first, then falls back to $USER,
// then to "railyard" as a last resort.
func detectOwner(dir string) string {
	cmd := exec.Command("git", "config", "user.name")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		if s := sanitizeOwner(strings.TrimSpace(string(out))); s != "" {
			return s
		}
	}

	if user := os.Getenv("USER"); user != "" {
		if s := sanitizeOwner(user); s != "" {
			return s
		}
	}

	return "railyard"
}

// sanitizeOwner normalises a human name into a lowercase, hyphen-separated
// identifier suitable for use in config files and branch names.
// It lowercases the input, replaces spaces and underscores with hyphens,
// strips any remaining non-alphanumeric/non-hyphen characters, and
// collapses consecutive hyphens. Leading/trailing hyphens are trimmed.
func sanitizeOwner(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

	s = reNonAlphanumHyphen.ReplaceAllString(s, "")
	s = reMultipleHyphens.ReplaceAllString(s, "-")

	// Trim leading/trailing hyphens.
	s = strings.Trim(s, "-")

	return s
}

// promptValue asks the user for a value, showing a default.
// Returns the default if the user presses Enter without typing.
func promptValue(in io.Reader, out io.Writer, label, defaultVal string) string {
	fmt.Fprintf(out, "  %s [%s]: ", label, defaultVal)
	scanner := bufio.NewScanner(in)
	if scanner.Scan() {
		val := strings.TrimSpace(scanner.Text())
		if val != "" {
			return val
		}
	}
	return defaultVal
}

// promptYesNo asks a yes/no question. Returns the default if Enter is pressed.
func promptYesNo(in io.Reader, out io.Writer, question string, defaultYes bool) bool {
	hint := "Y/n"
	if !defaultYes {
		hint = "y/N"
	}
	fmt.Fprintf(out, "  %s [%s]: ", question, hint)
	scanner := bufio.NewScanner(in)
	if scanner.Scan() {
		ans := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if ans == "" {
			return defaultYes
		}
		return ans == "y" || ans == "yes"
	}
	return defaultYes
}

// ensureDoltDataDir creates the Dolt data directory and initializes it
// with `dolt init` if the .dolt subdirectory doesn't exist.
func ensureDoltDataDir(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create dolt data dir: %w", err)
	}
	dotDolt := filepath.Join(dataDir, ".dolt")
	if _, err := os.Stat(dotDolt); err == nil {
		return nil // already initialized
	}
	cmd := exec.Command("dolt", "init", "--name", "railyard", "--email", "railyard@local")
	cmd.Dir = dataDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dolt init: %s: %w", out, err)
	}
	return nil
}

// ensureDoltRunning checks if Dolt is reachable on host:port. If not, it
// starts dolt sql-server in the background using ~/.railyard/dolt-data.
func ensureDoltRunning(out io.Writer, host string, port int) error {
	// Check if already running.
	if _, err := db.ConnectAdmin(host, port); err == nil {
		fmt.Fprintf(out, "Dolt is already running on %s:%d\n", host, port)
		return nil
	}

	dataDir := os.ExpandEnv("${HOME}/.railyard/dolt-data")
	fmt.Fprintf(out, "Setting up Dolt at %s...\n", dataDir)

	if err := ensureDoltDataDir(dataDir); err != nil {
		return err
	}

	// Start Dolt in the background.
	logFile := os.ExpandEnv("${HOME}/.railyard/dolt.log")
	doltCmd := exec.Command("dolt", "sql-server",
		"--host", host,
		"--port", fmt.Sprintf("%d", port),
	)
	doltCmd.Dir = dataDir

	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open dolt log %s: %w", logFile, err)
	}
	doltCmd.Stdout = lf
	doltCmd.Stderr = lf

	if err := doltCmd.Start(); err != nil {
		lf.Close()
		return fmt.Errorf("start dolt: %w", err)
	}
	go func() {
		doltCmd.Wait()
		lf.Close()
	}()

	fmt.Fprintf(out, "Starting Dolt on %s:%d (PID %d)...\n", host, port, doltCmd.Process.Pid)

	// Wait for readiness.
	for i := range 20 {
		time.Sleep(500 * time.Millisecond)
		if _, err := db.ConnectAdmin(host, port); err == nil {
			fmt.Fprintf(out, "Dolt is ready (took %dms)\n", (i+1)*500)
			return nil
		}
	}
	return fmt.Errorf("dolt did not become ready within 10s — check %s", logFile)
}

// languagePreset returns a sensible default TrackConfig for a given language.
func languagePreset(lang string) config.TrackConfig {
	switch lang {
	case "go":
		return config.TrackConfig{
			Name: "backend", Language: "go",
			FilePatterns: []string{"**/*.go"},
			EngineSlots:  2,
			TestCommand:  "go test ./...",
			Conventions:  map[string]interface{}{"style": "stdlib-first"},
		}
	case "typescript":
		return config.TrackConfig{
			Name: "frontend", Language: "typescript",
			FilePatterns: []string{"**/*.ts", "**/*.tsx"},
			EngineSlots:  2,
			TestCommand:  "npm test",
		}
	case "javascript":
		return config.TrackConfig{
			Name: "frontend", Language: "javascript",
			FilePatterns: []string{"**/*.js", "**/*.jsx"},
			EngineSlots:  2,
			TestCommand:  "npm test",
		}
	case "python":
		return config.TrackConfig{
			Name: "backend", Language: "python",
			FilePatterns: []string{"**/*.py"},
			EngineSlots:  2,
			TestCommand:  "pytest",
		}
	case "rust":
		return config.TrackConfig{
			Name: "backend", Language: "rust",
			FilePatterns: []string{"**/*.rs"},
			EngineSlots:  2,
			TestCommand:  "cargo test",
		}
	case "java":
		return config.TrackConfig{
			Name: "backend", Language: "java",
			FilePatterns: []string{"**/*.java"},
			EngineSlots:  2,
			TestCommand:  "mvn test",
		}
	case "ruby":
		return config.TrackConfig{
			Name: "backend", Language: "ruby",
			FilePatterns: []string{"**/*.rb"},
			EngineSlots:  2,
			TestCommand:  "bundle exec rspec",
		}
	default:
		return config.TrackConfig{
			Name: lang, Language: lang,
			EngineSlots: 2,
		}
	}
}

// generateTracks builds TrackConfig entries from detected languages,
// resolving name conflicts by suffixing with the language name.
func generateTracks(languages []string) []config.TrackConfig {
	var tracks []config.TrackConfig
	usedNames := map[string]bool{}

	for _, lang := range languages {
		track := languagePreset(lang)
		if usedNames[track.Name] {
			track.Name = track.Name + "-" + lang
		}
		usedNames[track.Name] = true
		tracks = append(tracks, track)
	}
	return tracks
}

// configTemplate is the Go template for generating railyard.yaml.
var configTemplate = template.Must(template.New("config").Funcs(template.FuncMap{
	"joinPatterns": func(patterns []string) string {
		if len(patterns) == 0 {
			return ""
		}
		quoted := make([]string, len(patterns))
		for i, p := range patterns {
			quoted[i] = `"` + p + `"`
		}
		return strings.Join(quoted, ", ")
	},
}).Parse(`# Railyard configuration — generated by ry init
# See railyard.example.yaml for all available options.

owner: {{ .Owner }}
repo: {{ .Repo }}

dolt:
  host: 127.0.0.1
  port: {{ .DoltPort }}

tracks:
{{- range .Tracks }}
  - name: {{ .Name }}
    language: {{ .Language }}
{{- if .FilePatterns }}
    file_patterns: [{{ joinPatterns .FilePatterns }}]
{{- end }}
    engine_slots: {{ .EngineSlots }}
{{- if .TestCommand }}
    test_command: "{{ .TestCommand }}"
{{- end }}
{{- end }}
`))

// configTemplateData holds the values for rendering railyard.yaml.
type configTemplateData struct {
	Owner    string
	Repo     string
	DoltPort int
	Tracks   []config.TrackConfig
}

// renderConfig generates a railyard.yaml string from the given parameters.
func renderConfig(owner, repo string, doltPort int, tracks []config.TrackConfig) (string, error) {
	var buf bytes.Buffer
	data := configTemplateData{
		Owner:    owner,
		Repo:     repo,
		DoltPort: doltPort,
		Tracks:   tracks,
	}
	if err := configTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render config: %w", err)
	}
	return buf.String(), nil
}

// newInitCmd creates the "ry init" cobra command.
func newInitCmd() *cobra.Command {
	var (
		configPath string
		yes        bool
		skipDB     bool
		skipCoco   bool
		doltPort   int
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize Railyard in this repository",
		Long: `Initialize Railyard in this repository.

Detects your repo's languages, generates railyard.yaml, starts Dolt,
initializes the database, and optionally sets up CocoIndex semantic search.

Run this once in any git repository to get started with Railyard.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, configPath, yes, skipDB, skipCoco, doltPort)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to write the config file")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "accept all defaults without prompting")
	cmd.Flags().BoolVar(&skipDB, "skip-db", false, "skip Dolt startup and database initialization")
	cmd.Flags().BoolVar(&skipCoco, "skip-cocoindex", false, "skip CocoIndex setup prompt")
	cmd.Flags().IntVarP(&doltPort, "port", "p", 3306, "Dolt SQL server port")
	return cmd
}

// runInit is the main orchestrator for the "ry init" command.
func runInit(cmd *cobra.Command, configPath string, yes, skipDB, skipCoco bool, doltPort int) error {
	out := cmd.OutOrStdout()
	in := cmd.InOrStdin()

	// Step 1: Detect git root from the current directory.
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	gitRoot, err := detectGitRoot(wd)
	if err != nil {
		return fmt.Errorf("ry init must be run inside a git repository: %w", err)
	}

	// Anchor the config path to the git root (not cwd) so that running
	// `ry init` from a subdirectory still writes to the repo root.
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(gitRoot, configPath)
	}
	fmt.Fprintf(out, "Detected git repository: %s\n", gitRoot)

	// Step 2: Check if config already exists.
	if _, err := os.Stat(configPath); err == nil {
		if yes {
			fmt.Fprintf(out, "Config %s already exists — overwriting (--yes).\n", configPath)
		} else {
			fmt.Fprintf(out, "Config %s already exists.\n", configPath)
			if !promptYesNo(in, out, "Overwrite?", false) {
				fmt.Fprintln(out, "Aborted.")
				return nil
			}
		}
	}

	// Step 3: Detect repo info.
	remote, _ := detectGitRemote(gitRoot)
	owner := detectOwner(gitRoot)
	langs := detectLanguages(gitRoot)

	fmt.Fprintf(out, "Detected remote: %s\n", remote)
	fmt.Fprintf(out, "Detected owner: %s\n", owner)
	if len(langs) > 0 {
		fmt.Fprintf(out, "Detected languages: %s\n", strings.Join(langs, ", "))
	} else {
		fmt.Fprintln(out, "No languages detected — you can add tracks manually later.")
	}

	// Step 4: Interactive confirmation (unless --yes).
	if !yes {
		fmt.Fprintln(out, "\nConfigure Railyard:")
		owner = promptValue(in, out, "Owner", owner)
		remote = promptValue(in, out, "Git remote URL", remote)
		portStr := promptValue(in, out, "Dolt port", fmt.Sprintf("%d", doltPort))
		if v, err := fmt.Sscanf(portStr, "%d", &doltPort); v != 1 || err != nil {
			return fmt.Errorf("invalid port: %s", portStr)
		}
	}

	// Fail fast if repo URL is still empty — config.Load will reject it,
	// so don't write an unusable file.
	if remote == "" {
		return fmt.Errorf("repo URL is required (no origin remote detected and none provided)")
	}

	// Generate tracks.
	tracks := generateTracks(langs)
	if len(tracks) == 0 {
		tracks = []config.TrackConfig{
			{Name: "default", Language: "mixed", EngineSlots: 2},
		}
	}

	if !yes && len(tracks) > 0 {
		fmt.Fprintf(out, "\nGenerated %d track(s):\n", len(tracks))
		for _, tr := range tracks {
			fmt.Fprintf(out, "  - %s (%s)\n", tr.Name, tr.Language)
		}
		if !promptYesNo(in, out, "Use these tracks?", true) {
			fmt.Fprintln(out, "Edit the generated railyard.yaml manually after init completes.")
		}
	}

	// Step 5: Render and write config.
	yamlContent, err := renderConfig(owner, remote, doltPort, tracks)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Fprintf(out, "\nWrote %s\n", configPath)

	if skipDB {
		fmt.Fprintln(out, "\nSkipped database initialization (--skip-db).")
		fmt.Fprintln(out, "Run these when ready:")
		fmt.Fprintf(out, "  ry db start -c %s\n", configPath)
		fmt.Fprintf(out, "  ry db init -c %s\n", configPath)
		return nil
	}

	// Step 6: Ensure Dolt is running.
	fmt.Fprintln(out, "")
	if err := ensureDoltRunning(out, "127.0.0.1", doltPort); err != nil {
		return fmt.Errorf("ensure dolt: %w", err)
	}

	// Step 7: Initialize the database.
	fmt.Fprintln(out, "")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load generated config: %w", err)
	}

	adminDB, err := db.ConnectAdmin(cfg.Dolt.Host, cfg.Dolt.Port)
	if err != nil {
		return fmt.Errorf("connect to Dolt: %w", err)
	}
	if err := db.CreateDatabase(adminDB, cfg.Dolt.Database); err != nil {
		return err
	}
	fmt.Fprintf(out, "Database %s ready\n", cfg.Dolt.Database)

	gormDB, err := db.Connect(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", cfg.Dolt.Database, err)
	}
	if err := db.AutoMigrate(gormDB); err != nil {
		return err
	}
	fmt.Fprintf(out, "Migrated %d tables\n", len(db.AllModels()))

	if err := db.SeedTracks(gormDB, cfg.Tracks); err != nil {
		return err
	}
	if err := db.SeedConfig(gormDB, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "Seeded %d track(s) and config for owner %q\n", len(cfg.Tracks), cfg.Owner)

	// Step 8: Optionally set up CocoIndex.
	if skipCoco || yes {
		fmt.Fprintln(out, "\nSkipped CocoIndex setup.")
		fmt.Fprintf(out, "To set up later: ry cocoindex init -c %s\n", configPath)
	} else {
		if promptYesNo(in, out, "\nSet up CocoIndex semantic search? (requires Docker)", false) {
			fmt.Fprintln(out, "\nSetting up CocoIndex...")
			cocoCmd := newRootCmd()
			cocoCmd.SetOut(out)
			cocoCmd.SetErr(cmd.ErrOrStderr())
			cocoCmd.SetArgs([]string{"cocoindex", "init", "-c", configPath})
			if err := cocoCmd.Execute(); err != nil {
				fmt.Fprintf(out, "CocoIndex setup failed: %v\n", err)
				fmt.Fprintln(out, "You can retry later with: ry cocoindex init -c "+configPath)
			}
		}
	}

	// Step 9: Summary.
	fmt.Fprintln(out, "\n"+strings.Repeat("─", 50))
	fmt.Fprintln(out, "Railyard initialized successfully!")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintf(out, "  ry start -c %s --engines 2   # Start orchestration\n", configPath)
	fmt.Fprintf(out, "  ry status -c %s              # Check status\n", configPath)
	fmt.Fprintln(out, "  tmux attach -t railyard                # Watch agents work")
	return nil
}
