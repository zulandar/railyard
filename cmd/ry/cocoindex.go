package main

import (
	_ "embed"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

//go:embed cocoindex_requirements.txt
var embeddedRequirements string

//go:embed cocoindex_docker_compose.yaml
var embeddedDockerCompose string

//go:embed cocoindex_init_pgvector.sql
var embeddedInitPGVector string

//go:embed cocoindex_pkg_init.py
var embeddedPkgInit string

//go:embed cocoindex_migrate.py
var embeddedMigrate string

//go:embed cocoindex_config.py
var embeddedConfig string

//go:embed cocoindex_main.py
var embeddedMain string

//go:embed cocoindex_build_all.py
var embeddedBuildAll string

//go:embed cocoindex_overlay.py
var embeddedOverlay string

//go:embed cocoindex_mcp_server.py
var embeddedMCPServer string

//go:embed cocoindex_migration_001.sql
var embeddedMigration001 string

// embeddedScript maps a relative file path (under the scripts directory) to
// its embedded content.  Used by ensureCocoIndexScripts to materialise the
// Python package on first run.
var embeddedScripts = []struct {
	path    string
	content *string
}{
	{"__init__.py", &embeddedPkgInit},
	{"migrate.py", &embeddedMigrate},
	{"config.py", &embeddedConfig},
	{"main.py", &embeddedMain},
	{"build_all.py", &embeddedBuildAll},
	{"overlay.py", &embeddedOverlay},
	{"mcp_server.py", &embeddedMCPServer},
	{filepath.Join("migrations", "001_create_overlay_meta.sql"), &embeddedMigration001},
}

const (
	defaultPGPort     = 5481
	fallbackPGPort    = 5482
	pgReadyTimeout    = 30 * time.Second
	pgReadyInterval   = 2 * time.Second
	defaultDBUser     = "cocoindex"
	defaultDBPassword = "cocoindex"
	defaultDBName     = "cocoindex"
)

func newCocoIndexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cocoindex",
		Short: "CocoIndex semantic search commands",
	}

	cmd.AddCommand(newCocoIndexInitCmd())
	return cmd
}

func newCocoIndexInitCmd() *cobra.Command {
	var (
		configPath     string
		port           int
		skipMigrations bool
		skipVenv       bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize pgvector for CocoIndex semantic search",
		Long: `Sets up the pgvector infrastructure for CocoIndex:
  1. Checks for Python >= 3.13
  2. Creates Python venv and installs dependencies
  3. Starts postgres+pgvector via Docker Compose
  4. Runs schema migrations
  5. Creates/updates cocoindex.yaml with the database URL
  6. Updates the cocoindex section in railyard.yaml (-c flag)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCocoIndexInit(cmd, configPath, port, skipMigrations, skipVenv)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().IntVar(&port, "port", 0, "pgvector port (default: 5481, auto-detects conflicts)")
	cmd.Flags().BoolVar(&skipMigrations, "skip-migrations", false, "skip running schema migrations")
	cmd.Flags().BoolVar(&skipVenv, "skip-venv", false, "skip Python venv creation")
	return cmd
}

func runCocoIndexInit(cmd *cobra.Command, configPath string, port int, skipMigrations, skipVenv bool) error {
	out := cmd.OutOrStdout()

	// Step 1: Check Docker is available.
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker is required but not found.\n  Install: https://docs.docker.com/engine/install/")
	}

	// Verify docker compose is available (plugin or standalone).
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		return fmt.Errorf("docker compose is required but not found.\n  Install: https://docs.docker.com/compose/install/")
	}
	fmt.Fprintln(out, "Docker and docker compose found")

	// Ensure embedded Python scripts and migration SQL exist on disk.
	if err := ensureCocoIndexScripts("cocoindex"); err != nil {
		return fmt.Errorf("ensure cocoindex scripts: %w", err)
	}

	// Step 2: Set up Python venv with dependencies.
	if !skipVenv {
		fmt.Fprintln(out, "Setting up Python venv...")
		if err := ensureCocoIndexVenv(out); err != nil {
			return fmt.Errorf("python venv setup: %w", err)
		}
	}

	// Step 3: Check if railyard-pgvector is already running.
	running, runningPort := isPGVectorRunning()
	if running {
		fmt.Fprintf(out, "pgvector container already running on port %d\n", runningPort)
		port = runningPort
	} else {
		// Step 4: Port conflict detection.
		if port == 0 {
			port = detectPGPort()
		}

		// Step 5: Start the container.
		fmt.Fprintf(out, "Starting pgvector on port %d...\n", port)
		if err := startPGVector(port); err != nil {
			return fmt.Errorf("start pgvector: %w", err)
		}

		// Step 6: Wait for health check.
		fmt.Fprint(out, "Waiting for postgres to be ready...")
		if err := waitForPG(port); err != nil {
			fmt.Fprintln(out, " failed")
			return fmt.Errorf("pgvector not ready after %s: %w", pgReadyTimeout, err)
		}
		fmt.Fprintln(out, " ready")
	}

	databaseURL := fmt.Sprintf("postgresql://%s:%s@localhost:%d/%s",
		defaultDBUser, defaultDBPassword, port, defaultDBName)

	// Step 7: Run migrations.
	if !skipMigrations {
		fmt.Fprintln(out, "Running migrations...")
		if err := runMigrations(databaseURL); err != nil {
			return fmt.Errorf("migrations: %w", err)
		}
		fmt.Fprintln(out, "Migrations applied")
	}

	// Step 8: Create/update cocoindex.yaml.
	if err := updateCocoIndexYAML(databaseURL); err != nil {
		return fmt.Errorf("update cocoindex.yaml: %w", err)
	}
	fmt.Fprintln(out, "Updated cocoindex/cocoindex.yaml with database_url")

	// Step 9: Update railyard.yaml cocoindex section.
	if err := updateRailyardYAML(configPath, databaseURL); err != nil {
		return fmt.Errorf("update %s: %w", configPath, err)
	}
	fmt.Fprintf(out, "Updated %s cocoindex section\n", configPath)

	// Print summary.
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "CocoIndex pgvector initialized:")
	fmt.Fprintf(out, "  Port:         %d\n", port)
	fmt.Fprintf(out, "  Database URL: %s\n", databaseURL)
	fmt.Fprintf(out, "  Container:    railyard-pgvector\n")
	fmt.Fprintf(out, "  Venv:         cocoindex/.venv\n")
	fmt.Fprintln(out, "")
	fmt.Fprintf(out, "  Connect: psql -h localhost -p %d -U cocoindex -d cocoindex\n", port)
	fmt.Fprintf(out, "  Stop:    docker compose -f docker/docker-compose.pgvector.yaml down\n")

	return nil
}

// ensureRequirementsTxt writes cocoindex/requirements.txt if it doesn't exist,
// using the copy embedded in the ry binary.
func ensureRequirementsTxt(requirementsPath string) error {
	if _, err := os.Stat(requirementsPath); err == nil {
		return nil // already exists
	}
	// Ensure the parent directory exists.
	if err := os.MkdirAll(filepath.Dir(requirementsPath), 0755); err != nil {
		return fmt.Errorf("create cocoindex dir: %w", err)
	}
	return os.WriteFile(requirementsPath, []byte(embeddedRequirements), 0644)
}

// ensureDockerFiles writes the docker compose and init SQL files to the project's
// docker/ directory if they don't already exist, using copies embedded in the ry binary.
func ensureDockerFiles(dockerDir string) error {
	if err := os.MkdirAll(dockerDir, 0755); err != nil {
		return fmt.Errorf("create docker dir: %w", err)
	}

	composePath := filepath.Join(dockerDir, "docker-compose.pgvector.yaml")
	if _, err := os.Stat(composePath); err != nil {
		if err := os.WriteFile(composePath, []byte(embeddedDockerCompose), 0644); err != nil {
			return fmt.Errorf("write docker-compose: %w", err)
		}
	}

	initSQLPath := filepath.Join(dockerDir, "init-pgvector.sql")
	if _, err := os.Stat(initSQLPath); err != nil {
		if err := os.WriteFile(initSQLPath, []byte(embeddedInitPGVector), 0644); err != nil {
			return fmt.Errorf("write init-pgvector.sql: %w", err)
		}
	}

	return nil
}

// ensureCocoIndexScripts writes the embedded Python scripts and migration SQL
// into scriptsDir (typically "cocoindex") if they don't already exist.
func ensureCocoIndexScripts(scriptsDir string) error {
	for _, s := range embeddedScripts {
		dest := filepath.Join(scriptsDir, s.path)
		if _, err := os.Stat(dest); err == nil {
			continue // already exists
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return fmt.Errorf("create dir for %s: %w", s.path, err)
		}
		if err := os.WriteFile(dest, []byte(*s.content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", s.path, err)
		}
	}
	return nil
}

// ensureCocoIndexVenv creates the Python venv and installs dependencies if needed.
func ensureCocoIndexVenv(out io.Writer) error {
	venvPath := filepath.Join("cocoindex", ".venv")
	requirementsPath := filepath.Join("cocoindex", "requirements.txt")

	// Write requirements.txt from embedded copy if missing.
	if err := ensureRequirementsTxt(requirementsPath); err != nil {
		return fmt.Errorf("write requirements.txt: %w", err)
	}

	// Check if venv already exists and has pip.
	venvPip := filepath.Join(venvPath, "bin", "pip")
	if _, err := os.Stat(venvPip); err == nil {
		fmt.Fprintln(out, "Python venv already exists at cocoindex/.venv")
		// Still run pip install to pick up any new deps.
		fmt.Fprintln(out, "Updating dependencies...")
		return runPipInstall(venvPath, requirementsPath)
	}

	// Find Python >= 3.13.
	pythonBin, err := findPython313()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Using %s\n", pythonBin)

	// Create venv.
	cmd := exec.Command(pythonBin, "-m", "venv", venvPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(output))
		if strings.Contains(msg, "ensurepip") || strings.Contains(msg, "No module named") {
			// Try --without-pip fallback.
			fmt.Fprintln(out, "ensurepip not available, creating venv without pip...")
			fallbackCmd := exec.Command(pythonBin, "-m", "venv", "--without-pip", venvPath)
			if fbOut, fbErr := fallbackCmd.CombinedOutput(); fbErr != nil {
				return fmt.Errorf("create venv (--without-pip): %s: %w\n"+
					"  Install the venv package for your Python version, e.g.:\n"+
					"  Ubuntu/Debian: sudo apt install python3.13-venv",
					strings.TrimSpace(string(fbOut)), fbErr)
			}
			// Bootstrap pip via get-pip.py.
			fmt.Fprintln(out, "Bootstrapping pip...")
			if err := bootstrapPip(venvPath); err != nil {
				return fmt.Errorf("bootstrap pip: %w\n"+
					"  Or install the venv package: sudo apt install python3.13-venv", err)
			}
		} else {
			return fmt.Errorf("create venv: %s: %w", msg, err)
		}
	}
	fmt.Fprintln(out, "Created venv at cocoindex/.venv")

	// Install dependencies.
	fmt.Fprintln(out, "Installing dependencies (this may take a few minutes)...")
	if err := runPipInstall(venvPath, requirementsPath); err != nil {
		return err
	}
	fmt.Fprintln(out, "Dependencies installed")

	return nil
}

// findPython313 finds a Python >= 3.13 binary on the system.
// Checks python3.13, python3.14, ..., python3.20, then python3.
func findPython313() (string, error) {
	// Check specific version binaries first.
	for minor := 13; minor <= 20; minor++ {
		name := fmt.Sprintf("python3.%d", minor)
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}

	// Check generic python3 and verify version.
	if path, err := exec.LookPath("python3"); err == nil {
		out, err := exec.Command(path, "-c",
			"import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')").Output()
		if err == nil {
			version := strings.TrimSpace(string(out))
			parts := strings.SplitN(version, ".", 2)
			if len(parts) == 2 {
				major, _ := strconv.Atoi(parts[0])
				minor, _ := strconv.Atoi(parts[1])
				if major >= 3 && minor >= 13 {
					return path, nil
				}
			}
		}
	}

	return "", fmt.Errorf("Python >= 3.13 is required but not found.\n" +
		"  Install: https://docs.python.org/3/using/unix.html\n" +
		"  Ubuntu/Debian: sudo add-apt-repository ppa:deadsnakes/ppa && sudo apt install python3.13 python3.13-venv")
}

// runPipInstall runs pip install -r requirements.txt in the venv.
func runPipInstall(venvPath, requirementsPath string) error {
	pipPath := filepath.Join(venvPath, "bin", "pip")
	cmd := exec.Command(pipPath, "install", "-r", requirementsPath)
	cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venvPath))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pip install: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// bootstrapPip downloads get-pip.py and runs it inside a venv that was created
// with --without-pip (i.e. when ensurepip is not available).
func bootstrapPip(venvPath string) error {
	getPipURL := "https://bootstrap.pypa.io/get-pip.py"
	getPipPath := filepath.Join(venvPath, "get-pip.py")

	resp, err := http.Get(getPipURL)
	if err != nil {
		return fmt.Errorf("download get-pip.py: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download get-pip.py: HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(getPipPath)
	if err != nil {
		return fmt.Errorf("create get-pip.py: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("write get-pip.py: %w", err)
	}
	f.Close()

	pythonPath := filepath.Join(venvPath, "bin", "python")
	cmd := exec.Command(pythonPath, getPipPath)
	cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venvPath))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run get-pip.py: %s: %w", strings.TrimSpace(string(output)), err)
	}

	// Clean up.
	os.Remove(getPipPath)
	return nil
}

// isPGVectorRunning checks if the railyard-pgvector container is running
// and returns its host port.
func isPGVectorRunning() (bool, int) {
	out, err := exec.Command("docker", "inspect", "--format",
		"{{.State.Running}}|{{(index (index .NetworkSettings.Ports \"5432/tcp\") 0).HostPort}}",
		"railyard-pgvector").Output()
	if err != nil {
		return false, 0
	}

	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(parts) != 2 || parts[0] != "true" {
		return false, 0
	}

	port, err := strconv.Atoi(parts[1])
	if err != nil {
		return false, 0
	}
	return true, port
}

// detectPGPort picks an available port, preferring defaultPGPort.
func detectPGPort() int {
	if !isPortInUse(defaultPGPort) {
		return defaultPGPort
	}
	if !isPortInUse(fallbackPGPort) {
		return fallbackPGPort
	}
	// Both in use — let the user specify via --port.
	return defaultPGPort
}

// isPortInUse checks if a TCP port is listening.
func isPortInUse(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// startPGVector runs docker compose up for the pgvector service.
func startPGVector(port int) error {
	dockerDir := "docker"
	if err := ensureDockerFiles(dockerDir); err != nil {
		return fmt.Errorf("ensure docker files: %w", err)
	}
	composeFile := filepath.Join(dockerDir, "docker-compose.pgvector.yaml")
	cmd := exec.Command("docker", "compose", "-f", composeFile, "up", "-d")
	cmd.Env = append(os.Environ(), fmt.Sprintf("COCOINDEX_PG_PORT=%d", port))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// waitForPG polls pg_isready until the database is accepting connections.
func waitForPG(port int) error {
	deadline := time.Now().Add(pgReadyTimeout)
	for time.Now().Before(deadline) {
		cmd := exec.Command("docker", "exec", "railyard-pgvector",
			"pg_isready", "-U", defaultDBUser, "-d", defaultDBName)
		if err := cmd.Run(); err == nil {
			return nil
		}
		time.Sleep(pgReadyInterval)
	}
	return fmt.Errorf("postgres not ready on port %d", port)
}

// runMigrations shells out to cocoindex/migrate.py.
func runMigrations(databaseURL string) error {
	// Try venv python first, fall back to system python3.
	pythonPath := filepath.Join("cocoindex", ".venv", "bin", "python")
	if _, err := os.Stat(pythonPath); err != nil {
		pythonPath = "python3"
	}

	migratePath := filepath.Join("cocoindex", "migrate.py")
	cmd := exec.Command(pythonPath, migratePath, "--database-url", databaseURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// updateRailyardYAML adds or updates the cocoindex section in railyard.yaml.
// Uses yaml.Node to preserve comments and formatting in the rest of the file.
func updateRailyardYAML(configPath, databaseURL string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s not found — create it first or specify the correct path with -c", configPath)
		}
		return fmt.Errorf("read %s: %w", configPath, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}

	// doc.Content[0] is the root mapping node.
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: expected a YAML mapping at the root", configPath)
	}
	root := doc.Content[0]

	// Find existing cocoindex key in the root mapping.
	var cocoNode *yaml.Node
	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == "cocoindex" {
			cocoNode = root.Content[i+1]
			break
		}
	}

	if cocoNode != nil && cocoNode.Kind == yaml.MappingNode {
		// Update existing cocoindex mapping — set database_url, venv_path, scripts_path.
		setNodeValue(cocoNode, "database_url", databaseURL)
		setNodeValue(cocoNode, "venv_path", "cocoindex/.venv")
		setNodeValue(cocoNode, "scripts_path", "cocoindex")
	} else {
		// Append a new cocoindex section.
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: "cocoindex"}
		valNode := &yaml.Node{
			Kind: yaml.MappingNode,
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "database_url"},
				{Kind: yaml.ScalarNode, Value: databaseURL, Style: yaml.DoubleQuotedStyle},
				{Kind: yaml.ScalarNode, Value: "venv_path"},
				{Kind: yaml.ScalarNode, Value: "cocoindex/.venv", Style: yaml.DoubleQuotedStyle},
				{Kind: yaml.ScalarNode, Value: "scripts_path"},
				{Kind: yaml.ScalarNode, Value: "cocoindex", Style: yaml.DoubleQuotedStyle},
				{Kind: yaml.ScalarNode, Value: "overlay"},
				{Kind: yaml.MappingNode, Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Value: "enabled"},
					{Kind: yaml.ScalarNode, Value: "true", Tag: "!!bool"},
				}},
			},
		}
		root.Content = append(root.Content, keyNode, valNode)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", configPath, err)
	}
	return os.WriteFile(configPath, out, 0644)
}

// setNodeValue sets or creates a key-value pair in a YAML mapping node.
func setNodeValue(mapping *yaml.Node, key, value string) {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Style = yaml.DoubleQuotedStyle
			return
		}
	}
	// Key not found — append it.
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value, Style: yaml.DoubleQuotedStyle},
	)
}

// updateCocoIndexYAML creates or updates cocoindex/cocoindex.yaml with the database_url.
func updateCocoIndexYAML(databaseURL string) error {
	yamlPath := filepath.Join("cocoindex", "cocoindex.yaml")

	// Read existing file if present.
	existing, err := os.ReadFile(yamlPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", yamlPath, err)
	}

	content := string(existing)

	if strings.Contains(content, "database_url:") {
		// Update existing database_url line.
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "database_url:") || strings.HasPrefix(trimmed, "# database_url:") {
				lines[i] = fmt.Sprintf("database_url: %q", databaseURL)
				break
			}
		}
		content = strings.Join(lines, "\n")
	} else if len(content) > 0 {
		// Prepend database_url to existing content.
		content = fmt.Sprintf("database_url: %q\n%s", databaseURL, content)
	} else {
		// Create fresh file.
		content = fmt.Sprintf(`# CocoIndex configuration — per-track index settings
#
# database_url is set by "ry cocoindex init".
database_url: %q

main_table_template: "main_{track}_embeddings"
overlay_table_prefix: "ovl_"

# Default exclusion patterns applied to all tracks unless overridden per-track.
excluded_patterns:
  - ".*"
  - vendor
  - node_modules
  - dist
  - __pycache__
  - .git
`, databaseURL)
	}

	return os.WriteFile(yamlPath, []byte(content), 0644)
}
