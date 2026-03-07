package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

// TestMCPConfigFilePermissions verifies that WriteMCPConfig writes .mcp.json
// with 0600 permissions (owner-only read/write), not a more permissive mode
// like 0644. The file may contain secrets such as database URLs.
func TestMCPConfigFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Owner: "testowner",
		Repo:  "testrepo",
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go"},
		},
		CocoIndex: config.CocoIndexConfig{
			DatabaseURL: "postgres://localhost:5432/test",
			VenvPath:    "/tmp/fakevenv",
			ScriptsPath: "/tmp/fakescripts",
		},
	}

	err := WriteMCPConfig(tmpDir, "eng-abc123", "backend", cfg)
	if err != nil {
		t.Fatalf("WriteMCPConfig returned error: %v", err)
	}

	mcpPath := filepath.Join(tmpDir, ".mcp.json")
	info, err := os.Stat(mcpPath)
	if err != nil {
		t.Fatalf("failed to stat %s: %v", mcpPath, err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected .mcp.json permissions 0600, got %04o", perm)
	}
}

// TestMCPConfigSkipsWhenNoDatabaseURL verifies that WriteMCPConfig is a no-op
// when CocoIndex.DatabaseURL is empty (no file should be created).
func TestMCPConfigSkipsWhenNoDatabaseURL(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Owner: "testowner",
		Repo:  "testrepo",
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go"},
		},
		CocoIndex: config.CocoIndexConfig{
			DatabaseURL: "",
		},
	}

	err := WriteMCPConfig(tmpDir, "eng-abc123", "backend", cfg)
	if err != nil {
		t.Fatalf("WriteMCPConfig returned error: %v", err)
	}

	mcpPath := filepath.Join(tmpDir, ".mcp.json")
	if _, err := os.Stat(mcpPath); !os.IsNotExist(err) {
		t.Errorf("expected .mcp.json to not exist when DatabaseURL is empty, but it does")
	}
}

// TestMCPConfigNilConfig verifies that WriteMCPConfig returns an error when
// given a nil config.
func TestMCPConfigNilConfig(t *testing.T) {
	tmpDir := t.TempDir()

	err := WriteMCPConfig(tmpDir, "eng-abc123", "backend", nil)
	if err == nil {
		t.Error("expected error for nil config, got nil")
	}
}
