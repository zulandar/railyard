package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

// TestMisconfigDispatchMCPFilePermissions verifies that WriteDispatchMCPConfig
// writes .mcp.json with 0600 permissions (owner-only read/write), not a more
// permissive mode like 0644. The file may contain secrets such as database URLs.
func TestMisconfigDispatchMCPFilePermissions(t *testing.T) {
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

	err := WriteDispatchMCPConfig(tmpDir, cfg)
	if err != nil {
		t.Fatalf("WriteDispatchMCPConfig returned error: %v", err)
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

// TestMisconfigDispatchMCPSkipsWhenNoDatabaseURL verifies that
// WriteDispatchMCPConfig is a no-op when CocoIndex.DatabaseURL is empty.
func TestMisconfigDispatchMCPSkipsWhenNoDatabaseURL(t *testing.T) {
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

	err := WriteDispatchMCPConfig(tmpDir, cfg)
	if err != nil {
		t.Fatalf("WriteDispatchMCPConfig returned error: %v", err)
	}

	mcpPath := filepath.Join(tmpDir, ".mcp.json")
	if _, err := os.Stat(mcpPath); !os.IsNotExist(err) {
		t.Errorf("expected .mcp.json to not exist when DatabaseURL is empty, but it does")
	}
}

// TestMisconfigDispatchMCPNilConfig verifies that WriteDispatchMCPConfig
// returns an error when given a nil config.
func TestMisconfigDispatchMCPNilConfig(t *testing.T) {
	tmpDir := t.TempDir()

	err := WriteDispatchMCPConfig(tmpDir, nil)
	if err == nil {
		t.Error("expected error for nil config, got nil")
	}
}
