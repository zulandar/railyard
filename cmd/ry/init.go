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
	"github.com/zulandar/railyard/internal/audit"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"golang.org/x/term"
)

// Pre-compiled regexps for sanitizeOwner.
var (
	reNonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9-]`)
	reMultipleHyphens   = regexp.MustCompile(`-{2,}`)
)

// dbProbeFn checks whether a database is reachable. Overridden in tests.
var dbProbeFn = func(host string, port int, username, password string) error {
	_, err := db.ConnectAdmin(host, port, username, password)
	return err
}

// execCommandFn creates an exec.Cmd. Overridden in tests to avoid real docker calls.
var execCommandFn = exec.Command

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

// byteReader wraps an io.Reader so that each Read returns at most one byte.
// This prevents bufio.Scanner from buffering ahead and consuming input
// intended for subsequent prompts.
type byteReader struct{ r io.Reader }

func (b byteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return b.r.Read(p[:1])
}

// promptChoice asks the user to pick from a set of choices, showing a default.
// Returns the default if the user presses Enter without typing. If the input
// is not one of the valid choices, it re-prompts (up to 3 attempts) and then
// falls back to the default.
func promptChoice(in io.Reader, out io.Writer, label string, choices []string, defaultVal string) string {
	valid := make(map[string]bool, len(choices))
	for _, c := range choices {
		valid[c] = true
	}
	scanner := bufio.NewScanner(in)
	for range 3 {
		fmt.Fprintf(out, "  %s (%s) [%s]: ", label, strings.Join(choices, "/"), defaultVal)
		if scanner.Scan() {
			val := strings.TrimSpace(scanner.Text())
			if val == "" {
				return defaultVal
			}
			if valid[val] {
				return val
			}
			fmt.Fprintf(out, "  Invalid choice %q — must be one of: %s\n", val, strings.Join(choices, ", "))
			continue
		}
		break
	}
	return defaultVal
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

// promptPassword reads a password without echoing it to the terminal.
// If stdin is not a terminal (e.g., piped input in tests), it falls back
// to reading a line with bufio.Scanner.
func promptPassword(in io.Reader, out io.Writer, label, defaultVal string) string {
	hint := "(input hidden)"
	if defaultVal != "" {
		hint = "(input hidden, Enter to keep current)"
	}
	fmt.Fprintf(out, "  %s %s: ", label, hint)

	// If stdin is a real terminal file, use term.ReadPassword to suppress echo.
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		pw, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(out) // newline after hidden input
		if err != nil || len(pw) == 0 {
			return defaultVal
		}
		return strings.TrimSpace(string(pw))
	}

	// Fallback for non-terminal input (tests, pipes).
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

// ensureDBDataDir creates the database data directory if it doesn't exist.
func ensureDBDataDir(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create database data dir: %w", err)
	}
	return nil
}

// containerName is the Docker container name used for the Railyard MySQL instance.
const containerName = "railyard-mysql"

// ensureDBRunning checks if the database is reachable on host:port. If not,
// and the host is local, it starts a MySQL 8.0 Docker container. For remote
// hosts, it returns an error without touching local containers.
func ensureDBRunning(out io.Writer, host string, port int, username, password string) error {
	// Only manage local Docker containers. For remote hosts, the user is
	// responsible for ensuring the database is running.
	if !isLocalHost(host) {
		return fmt.Errorf("database host %s is not local — auto-provisioning only works for 127.0.0.1/localhost.\nEnsure the remote database at %s:%d is running, or use --skip-db", host, host, port)
	}

	// Local host: check if already running.
	if err := dbProbeFn(host, port, username, password); err == nil {
		fmt.Fprintf(out, "Database is already running on %s:%d\n", host, port)
		return nil
	}

	dataDir := os.ExpandEnv("${HOME}/.railyard/mysql-data")
	fmt.Fprintf(out, "Setting up database at %s...\n", dataDir)

	if err := ensureDBDataDir(dataDir); err != nil {
		return err
	}

	// Remove any stopped container with the same name to avoid conflicts.
	execCommandFn("docker", "rm", "-f", containerName).Run()

	// Start MySQL via Docker.
	// When a password is provided, configure the container to use it;
	// otherwise allow empty password for convenience in local dev.
	var mysqlEnv string
	if password != "" {
		mysqlEnv = "MYSQL_ROOT_PASSWORD=" + password
	} else {
		mysqlEnv = "MYSQL_ALLOW_EMPTY_PASSWORD=yes"
	}
	args := []string{
		"run", "-d",
		"--name", containerName,
		"-e", mysqlEnv,
		"-p", fmt.Sprintf("%d:3306", port),
		"-v", dataDir + ":/var/lib/mysql",
		"mysql:8.0",
	}
	dbCmd := execCommandFn("docker", args...)

	cmdOut, err := dbCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("start database container: %s: %w", strings.TrimSpace(string(cmdOut)), err)
	}

	fmt.Fprintf(out, "Starting MySQL container on %s:%d...\n", host, port)

	// Wait for readiness (MySQL in Docker can take 15-30s on first init).
	for i := range 60 {
		time.Sleep(500 * time.Millisecond)
		if err := dbProbeFn(host, port, username, password); err == nil {
			fmt.Fprintf(out, "Database is ready (took %dms)\n", (i+1)*500)
			return nil
		}
	}
	return fmt.Errorf("database did not become ready within 30s — check: docker logs %s", containerName)
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

database:
  host: {{ .DBHost }}
  port: {{ .DBPort }}
  username: {{ .DBUser }}
{{- if .DBPassword }}
  password: {{ .DBPassword }}
{{- end }}

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
{{- if .Telegraph }}

telegraph:
  platform: {{ .Telegraph.Platform }}
  channel: {{ .Telegraph.Channel }}
{{- if eq .Telegraph.Platform "slack" }}
  slack:
    bot_token: {{ printf "${%s}" .Telegraph.SlackBotVar }}
    app_token: {{ printf "${%s}" .Telegraph.SlackAppVar }}
{{- end }}
{{- if eq .Telegraph.Platform "discord" }}
  discord:
    bot_token: {{ printf "${%s}" .Telegraph.DiscordBotVar }}
{{- if .Telegraph.GuildID }}
    guild_id: {{ .Telegraph.GuildID }}
{{- end }}
{{- if .Telegraph.DiscordChanID }}
    channel_id: {{ .Telegraph.DiscordChanID }}
{{- end }}
{{- end }}
{{- end }}
`))

// telegraphTemplateData holds the values for rendering the telegraph section.
type telegraphTemplateData struct {
	Platform      string // "slack" or "discord"
	Channel       string
	SlackBotVar   string // env var name, e.g. "SLACK_BOT_TOKEN"
	SlackAppVar   string // env var name, e.g. "SLACK_APP_TOKEN"
	DiscordBotVar string // env var name, e.g. "DISCORD_BOT_TOKEN"
	GuildID       string
	DiscordChanID string
}

// configTemplateData holds the values for rendering railyard.yaml.
type configTemplateData struct {
	Owner      string
	Repo       string
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	Tracks     []config.TrackConfig
	Telegraph  *telegraphTemplateData
}

// renderConfig generates a railyard.yaml string from the given parameters.
func renderConfig(owner, repo, dbHost string, dbPort int, dbUser, dbPassword string, tracks []config.TrackConfig, tg *telegraphTemplateData) (string, error) {
	var buf bytes.Buffer
	data := configTemplateData{
		Owner:      owner,
		Repo:       repo,
		DBHost:     dbHost,
		DBPort:     dbPort,
		DBUser:     dbUser,
		DBPassword: dbPassword,
		Tracks:     tracks,
		Telegraph:  tg,
	}
	if err := configTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render config: %w", err)
	}
	return buf.String(), nil
}

// newInitCmd creates the "ry init" cobra command.
func newInitCmd() *cobra.Command {
	var (
		configPath    string
		yes           bool
		skipDB        bool
		skipCoco      bool
		skipTelegraph bool
		dbHost        string
		dbPort        int
		dbUser        string
		dbPassword    string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize Railyard in this repository",
		Long: `Initialize Railyard in this repository.

Detects your repo's languages, generates railyard.yaml, starts the database,
initializes tables, and optionally sets up CocoIndex semantic search
and Telegraph chat bridge (Slack/Discord).

Run this once in any git repository to get started with Railyard.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, configPath, yes, skipDB, skipCoco, skipTelegraph, dbHost, dbPort, dbUser, dbPassword)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to write the config file")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "accept all defaults without prompting")
	cmd.Flags().BoolVar(&skipDB, "skip-db", false, "skip database startup and initialization")
	cmd.Flags().BoolVar(&skipCoco, "skip-cocoindex", false, "skip CocoIndex setup prompt")
	cmd.Flags().BoolVar(&skipTelegraph, "skip-telegraph", false, "skip Telegraph chat bridge setup")
	cmd.Flags().IntVarP(&dbPort, "port", "p", 3306, "database server port")
	cmd.Flags().StringVarP(&dbHost, "host", "H", "127.0.0.1", "database server host address")
	cmd.Flags().StringVarP(&dbUser, "user", "u", "root", "database server username")
	cmd.Flags().StringVar(&dbPassword, "password", "", "database server password (or use ${ENV_VAR} in config)")
	return cmd
}

// runInit is the main orchestrator for the "ry init" command.
func runInit(cmd *cobra.Command, configPath string, yes, skipDB, skipCoco, skipTelegraph bool, dbHost string, dbPort int, dbUser, dbPassword string) error {
	out := cmd.OutOrStdout()
	in := io.Reader(byteReader{cmd.InOrStdin()})

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
		dbHost = promptValue(in, out, "Database host", dbHost)
		dbUser = promptValue(in, out, "Database user", dbUser)
		dbPassword = promptPassword(in, out, "Database password (empty for none)", dbPassword)
		portStr := promptValue(in, out, "Database port", fmt.Sprintf("%d", dbPort))
		if v, err := fmt.Sscanf(portStr, "%d", &dbPort); v != 1 || err != nil {
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

	// Step 4b: Telegraph chat bridge setup.
	var tg *telegraphTemplateData
	if skipTelegraph {
		// Silent skip — no message needed.
	} else if yes {
		fmt.Fprintln(out, "\nTelegraph chat bridge: skipped (--yes)")
		fmt.Fprintln(out, "You can set up Telegraph later by editing railyard.yaml.")
		fmt.Fprintln(out, "See docs/telegraph-setup.md for details.")
	} else {
		if promptYesNo(in, out, "\nSet up Telegraph chat bridge? (Slack/Discord)", false) {
			platform := promptChoice(in, out, "Platform", []string{"slack", "discord"}, "slack")
			channel := promptValue(in, out, "Default channel ID", "")
			if channel == "" {
				return fmt.Errorf("channel ID is required for Telegraph setup")
			}
			tg = &telegraphTemplateData{
				Platform: platform,
				Channel:  channel,
			}
			switch platform {
			case "slack":
				tg.SlackBotVar = promptValue(in, out, "Slack bot token env var", "SLACK_BOT_TOKEN")
				tg.SlackAppVar = promptValue(in, out, "Slack app token env var", "SLACK_APP_TOKEN")
				fmt.Fprintln(out, "\n  Set these environment variables before running Telegraph:")
				fmt.Fprintf(out, "    export %s=\"xoxb-...\"\n", tg.SlackBotVar)
				fmt.Fprintf(out, "    export %s=\"xapp-...\"\n", tg.SlackAppVar)
			case "discord":
				tg.DiscordBotVar = promptValue(in, out, "Discord bot token env var", "DISCORD_BOT_TOKEN")
				tg.GuildID = promptValue(in, out, "Guild ID (optional)", "")
				tg.DiscordChanID = promptValue(in, out, "Channel ID (optional)", "")
				fmt.Fprintln(out, "\n  Set this environment variable before running Telegraph:")
				fmt.Fprintf(out, "    export %s=\"your-bot-token\"\n", tg.DiscordBotVar)
			}
			fmt.Fprintln(out, "\n  See docs/telegraph-setup.md for full setup instructions.")
		} else {
			fmt.Fprintln(out, "You can set up Telegraph later by editing railyard.yaml.")
			fmt.Fprintln(out, "See docs/telegraph-setup.md for details.")
		}
	}

	// Step 5: Render and write config.
	yamlContent, err := renderConfig(owner, remote, dbHost, dbPort, dbUser, dbPassword, tracks, tg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Fprintf(out, "\nWrote %s\n", configPath)

	// Commit railyard.yaml so it's tracked and survives git clean in worktrees.
	gitAdd := exec.Command("git", "add", filepath.Base(configPath))
	gitAdd.Dir = gitRoot
	if addOut, err := gitAdd.CombinedOutput(); err != nil {
		fmt.Fprintf(out, "Warning: could not stage %s: %s\n", configPath, strings.TrimSpace(string(addOut)))
	} else {
		gitCommit := exec.Command("git", "commit", "-m", "Add railyard configuration")
		gitCommit.Dir = gitRoot
		if commitOut, err := gitCommit.CombinedOutput(); err != nil {
			fmt.Fprintf(out, "Warning: could not commit %s: %s\n", configPath, strings.TrimSpace(string(commitOut)))
		}
	}

	if skipDB {
		fmt.Fprintln(out, "\nSkipped database initialization (--skip-db).")
		fmt.Fprintln(out, "Run these when ready:")
		fmt.Fprintf(out, "  ry db start -c %s\n", configPath)
		fmt.Fprintf(out, "  ry db init -c %s\n", configPath)
		return nil
	}

	// Step 6: Ensure database is running.
	fmt.Fprintln(out, "")
	if err := ensureDBRunning(out, dbHost, dbPort, dbUser, dbPassword); err != nil {
		return fmt.Errorf("ensure database: %w", err)
	}

	// Step 7: Initialize the database.
	fmt.Fprintln(out, "")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load generated config: %w", err)
	}

	adminDB, err := db.ConnectAdmin(cfg.Database.Host, cfg.Database.Port, cfg.Database.Username, cfg.Database.Password)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	if err := db.CreateDatabase(adminDB, cfg.Database.Database); err != nil {
		return err
	}
	fmt.Fprintf(out, "Database %s ready\n", cfg.Database.Database)

	gormDB, err := db.Connect(cfg.Database.Host, cfg.Database.Port, cfg.Database.Database, cfg.Database.Username, cfg.Database.Password)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", cfg.Database.Database, err)
	}

	// Best-effort audit; do not fail init if audit logging fails.
	_ = audit.Log(gormDB, os.Stderr, "config.loaded", "system", configPath, map[string]interface{}{
		"owner":  cfg.Owner,
		"tracks": len(cfg.Tracks),
	})

	if err := db.AutoMigrate(gormDB); err != nil {
		return err
	}
	fmt.Fprintf(out, "Migrated %d tables\n", len(db.AllModels()))

	if err := db.SeedTracks(gormDB, cfg.Tracks, os.Stderr); err != nil {
		return err
	}
	if err := db.SeedConfig(gormDB, cfg, os.Stderr); err != nil {
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
