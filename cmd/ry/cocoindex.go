package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

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
  5. Creates/updates cocoindex.yaml with the database URL`,
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

// ensureCocoIndexVenv creates the Python venv and installs dependencies if needed.
func ensureCocoIndexVenv(out io.Writer) error {
	venvPath := filepath.Join("cocoindex", ".venv")
	requirementsPath := filepath.Join("cocoindex", "requirements.txt")

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
		return fmt.Errorf("create venv: %s: %w", strings.TrimSpace(string(output)), err)
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
	composeFile := filepath.Join("docker", "docker-compose.pgvector.yaml")
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
