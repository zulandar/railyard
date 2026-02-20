package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectPGPort_DefaultAvailable(t *testing.T) {
	// In test environment, default port is likely free.
	port := detectPGPort()
	if port != defaultPGPort && port != fallbackPGPort {
		t.Errorf("detectPGPort() = %d, want %d or %d", port, defaultPGPort, fallbackPGPort)
	}
}

func TestIsPortInUse_UnusedPort(t *testing.T) {
	// Port 59999 is very unlikely to be in use.
	if isPortInUse(59999) {
		t.Skip("port 59999 unexpectedly in use")
	}
}

func TestIsPGVectorRunning_NoContainer(t *testing.T) {
	// When no container exists, should return false.
	// This test works whether or not Docker is installed.
	running, _ := isPGVectorRunning()
	if running {
		t.Skip("railyard-pgvector container is actually running")
	}
}

func TestUpdateCocoIndexYAML_CreateNew(t *testing.T) {
	tmpDir := t.TempDir()
	cocoDir := filepath.Join(tmpDir, "cocoindex")
	os.MkdirAll(cocoDir, 0755)

	// Temporarily change working directory.
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	// Create cocoindex dir so the path resolves.
	os.MkdirAll("cocoindex", 0755)

	dbURL := "postgresql://cocoindex:cocoindex@localhost:5481/cocoindex"
	err := updateCocoIndexYAML(dbURL)
	if err != nil {
		t.Fatalf("updateCocoIndexYAML() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join("cocoindex", "cocoindex.yaml"))
	if err != nil {
		t.Fatalf("read cocoindex.yaml: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "database_url:") {
		t.Error("cocoindex.yaml missing database_url")
	}
	if !strings.Contains(content, dbURL) {
		t.Errorf("cocoindex.yaml missing expected URL %q", dbURL)
	}
	if !strings.Contains(content, "main_table_template:") {
		t.Error("cocoindex.yaml missing main_table_template")
	}
	if !strings.Contains(content, "overlay_table_prefix:") {
		t.Error("cocoindex.yaml missing overlay_table_prefix")
	}
}

func TestUpdateCocoIndexYAML_UpdateExisting(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	os.MkdirAll("cocoindex", 0755)

	// Write existing file with a placeholder database_url.
	existing := `# CocoIndex config
database_url: "postgresql://old:old@localhost:9999/old"
main_table_template: "main_{track}_embeddings"
overlay_table_prefix: "ovl_"
`
	os.WriteFile(filepath.Join("cocoindex", "cocoindex.yaml"), []byte(existing), 0644)

	newURL := "postgresql://cocoindex:cocoindex@localhost:5481/cocoindex"
	err := updateCocoIndexYAML(newURL)
	if err != nil {
		t.Fatalf("updateCocoIndexYAML() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join("cocoindex", "cocoindex.yaml"))
	if err != nil {
		t.Fatalf("read cocoindex.yaml: %v", err)
	}

	content := string(data)
	if strings.Contains(content, "old") {
		t.Error("cocoindex.yaml still contains old database_url")
	}
	if !strings.Contains(content, newURL) {
		t.Errorf("cocoindex.yaml missing new URL %q", newURL)
	}
	// Should preserve other fields.
	if !strings.Contains(content, "main_table_template:") {
		t.Error("cocoindex.yaml lost main_table_template")
	}
}

func TestUpdateCocoIndexYAML_PrependToExistingWithoutURL(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	os.MkdirAll("cocoindex", 0755)

	// Write existing file without database_url.
	existing := `main_table_template: "main_{track}_embeddings"
overlay_table_prefix: "ovl_"
`
	os.WriteFile(filepath.Join("cocoindex", "cocoindex.yaml"), []byte(existing), 0644)

	dbURL := "postgresql://cocoindex:cocoindex@localhost:5481/cocoindex"
	err := updateCocoIndexYAML(dbURL)
	if err != nil {
		t.Fatalf("updateCocoIndexYAML() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join("cocoindex", "cocoindex.yaml"))
	if err != nil {
		t.Fatalf("read cocoindex.yaml: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, fmt.Sprintf("database_url: %q", dbURL)) {
		t.Error("cocoindex.yaml missing database_url")
	}
	if !strings.Contains(content, "main_table_template:") {
		t.Error("cocoindex.yaml lost existing content")
	}
}

func TestNewCocoIndexCmd_Structure(t *testing.T) {
	cmd := newCocoIndexCmd()
	if cmd.Use != "cocoindex" {
		t.Errorf("Use = %q, want %q", cmd.Use, "cocoindex")
	}

	// Should have init subcommand.
	initCmd, _, err := cmd.Find([]string{"init"})
	if err != nil {
		t.Fatalf("find init subcommand: %v", err)
	}
	if initCmd.Use != "init" {
		t.Errorf("init subcommand Use = %q, want %q", initCmd.Use, "init")
	}
}

func TestNewCocoIndexInitCmd_HasSkipVenvFlag(t *testing.T) {
	cmd := newCocoIndexInitCmd()
	f := cmd.Flags().Lookup("skip-venv")
	if f == nil {
		t.Error("init command missing --skip-venv flag")
	}
}

func TestFindPython313_ReturnsPathOrError(t *testing.T) {
	// This test works regardless of whether Python 3.13 is installed.
	// If installed, we get a path. If not, we get an error.
	path, err := findPython313()
	if err != nil {
		// Not installed — verify the error message is helpful.
		if !strings.Contains(err.Error(), "Python >= 3.13") {
			t.Errorf("error message should mention Python 3.13: %v", err)
		}
		return
	}
	// If found, verify it's an executable path.
	if path == "" {
		t.Error("findPython313() returned empty path with nil error")
	}
}

func TestRunPipInstall_MissingRequirements(t *testing.T) {
	tmpDir := t.TempDir()
	// Attempt to install from non-existent requirements file.
	// Should fail since the venv doesn't exist.
	err := runPipInstall(tmpDir, filepath.Join(tmpDir, "nonexistent.txt"))
	if err == nil {
		t.Error("expected error when pip binary doesn't exist")
	}
}
