package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

func TestOverlayTableName(t *testing.T) {
	tests := []struct {
		engineID string
		want     string
	}{
		{"eng-a1b2c3d4", "ovl_eng_a1b2c3d4"},
		{"eng-00000000", "ovl_eng_00000000"},
		{"eng-deadbeef", "ovl_eng_deadbeef"},
	}

	for _, tt := range tests {
		got := OverlayTableName(tt.engineID)
		if got != tt.want {
			t.Errorf("OverlayTableName(%q) = %q, want %q", tt.engineID, got, tt.want)
		}
	}
}

func TestWriteMCPConfig(t *testing.T) {
	tmpDir := t.TempDir()
	engineID := "eng-a1b2c3d4"
	track := "backend"

	cfg := &config.Config{
		CocoIndex: config.CocoIndexConfig{
			DatabaseURL: "postgresql://user:pass@localhost:5432/cocoindex",
			VenvPath:    "/opt/cocoindex/.venv",
			ScriptsPath: "/opt/cocoindex",
			Overlay: config.OverlayConfig{
				Enabled:         true,
				BuildTimeoutSec: 60,
			},
		},
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go", FilePatterns: []string{"*.go"}},
		},
	}

	err := WriteMCPConfig(tmpDir, engineID, track, cfg)
	if err != nil {
		t.Fatalf("WriteMCPConfig() error: %v", err)
	}

	// Verify .mcp.json was written.
	mcpPath := filepath.Join(tmpDir, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}

	var mcpCfg MCPServerConfig
	if err := json.Unmarshal(data, &mcpCfg); err != nil {
		t.Fatalf("unmarshal .mcp.json: %v", err)
	}

	server, ok := mcpCfg.MCPServers["railyard_cocoindex"]
	if !ok {
		t.Fatal(".mcp.json missing railyard_cocoindex server")
	}

	if server.Env["COCOINDEX_ENGINE_ID"] != engineID {
		t.Errorf("COCOINDEX_ENGINE_ID = %q, want %q", server.Env["COCOINDEX_ENGINE_ID"], engineID)
	}
	if server.Env["COCOINDEX_TRACK"] != track {
		t.Errorf("COCOINDEX_TRACK = %q, want %q", server.Env["COCOINDEX_TRACK"], track)
	}
	if server.Env["COCOINDEX_MAIN_TABLE"] != "main_backend_embeddings" {
		t.Errorf("COCOINDEX_MAIN_TABLE = %q, want %q", server.Env["COCOINDEX_MAIN_TABLE"], "main_backend_embeddings")
	}
	if server.Env["COCOINDEX_OVERLAY_TABLE"] != "ovl_eng_a1b2c3d4" {
		t.Errorf("COCOINDEX_OVERLAY_TABLE = %q, want %q", server.Env["COCOINDEX_OVERLAY_TABLE"], "ovl_eng_a1b2c3d4")
	}
	if server.Env["COCOINDEX_DATABASE_URL"] != cfg.CocoIndex.DatabaseURL {
		t.Errorf("COCOINDEX_DATABASE_URL = %q, want %q", server.Env["COCOINDEX_DATABASE_URL"], cfg.CocoIndex.DatabaseURL)
	}
	if server.Env["COCOINDEX_WORKTREE"] != tmpDir {
		t.Errorf("COCOINDEX_WORKTREE = %q, want %q", server.Env["COCOINDEX_WORKTREE"], tmpDir)
	}

	// Verify command and args point to correct paths.
	wantCmd := "/opt/cocoindex/.venv/bin/python"
	if server.Command != wantCmd {
		t.Errorf("Command = %q, want %q", server.Command, wantCmd)
	}
	wantArg := "/opt/cocoindex/mcp_server.py"
	if len(server.Args) != 1 || server.Args[0] != wantArg {
		t.Errorf("Args = %v, want [%q]", server.Args, wantArg)
	}
}

func TestWriteMCPConfig_NilConfig(t *testing.T) {
	err := WriteMCPConfig("/tmp", "eng-test", "backend", nil)
	if err == nil {
		t.Error("WriteMCPConfig(nil config) should return error")
	}
}

func TestWriteMCPConfig_NoDatabaseURL(t *testing.T) {
	cfg := &config.Config{}
	err := WriteMCPConfig("/tmp", "eng-test", "backend", cfg)
	if err != nil {
		t.Errorf("WriteMCPConfig(no database URL) should be no-op, got: %v", err)
	}
}

func TestBuildOverlay_Disabled(t *testing.T) {
	cfg := &config.Config{
		CocoIndex: config.CocoIndexConfig{
			Overlay: config.OverlayConfig{Enabled: false},
		},
	}

	table, err := BuildOverlay("/tmp", "eng-test", "backend", cfg)
	if err != nil {
		t.Errorf("BuildOverlay(disabled) should not error, got: %v", err)
	}
	if table != "" {
		t.Errorf("BuildOverlay(disabled) should return empty table, got: %q", table)
	}
}

func TestBuildOverlay_NilConfig(t *testing.T) {
	_, err := BuildOverlay("/tmp", "eng-test", "backend", nil)
	if err == nil {
		t.Error("BuildOverlay(nil config) should return error")
	}
}

func TestCleanupOverlay_NilConfig(t *testing.T) {
	err := CleanupOverlay("eng-test", nil)
	if err == nil {
		t.Error("CleanupOverlay(nil config) should return error")
	}
}

func TestCleanupOverlay_NoDatabaseURL(t *testing.T) {
	cfg := &config.Config{}
	err := CleanupOverlay("eng-test", cfg)
	if err != nil {
		t.Errorf("CleanupOverlay(no database URL) should be no-op, got: %v", err)
	}
}

func TestFindTrack(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go"},
			{Name: "frontend", Language: "typescript"},
		},
	}

	tc := findTrack(cfg, "backend")
	if tc == nil {
		t.Fatal("findTrack(backend) returned nil")
	}
	if tc.Name != "backend" {
		t.Errorf("findTrack(backend).Name = %q", tc.Name)
	}

	tc = findTrack(cfg, "nonexistent")
	if tc != nil {
		t.Error("findTrack(nonexistent) should return nil")
	}
}
