package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
		if name := strings.TrimSpace(string(out)); name != "" {
			return sanitizeOwner(name)
		}
	}

	if user := os.Getenv("USER"); user != "" {
		return sanitizeOwner(user)
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
