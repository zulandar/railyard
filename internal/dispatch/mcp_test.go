package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

func TestWriteDispatchMCPConfig_NilConfig(t *testing.T) {
	err := WriteDispatchMCPConfig(t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestWriteDispatchMCPConfig_NoDatabaseURL(t *testing.T) {
	cfg := &config.Config{}
	err := WriteDispatchMCPConfig(t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("expected nil error when no database_url, got: %v", err)
	}
	// .mcp.json should not be created.
	if _, err := os.Stat(filepath.Join(t.TempDir(), ".mcp.json")); !os.IsNotExist(err) {
		t.Error(".mcp.json should not be written when database_url is empty")
	}
}

func TestWriteDispatchMCPConfig_CreatesFreshFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		CocoIndex: config.CocoIndexConfig{
			DatabaseURL: "postgresql://cocoindex:cocoindex@localhost:5481/cocoindex",
			VenvPath:    "cocoindex/.venv",
			ScriptsPath: "cocoindex",
		},
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go"},
			{Name: "frontend", Language: "typescript"},
		},
	}

	err := WriteDispatchMCPConfig(tmpDir, cfg)
	if err != nil {
		t.Fatalf("WriteDispatchMCPConfig() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}

	var mcpCfg mcpServerConfig
	if err := json.Unmarshal(data, &mcpCfg); err != nil {
		t.Fatalf("unmarshal .mcp.json: %v", err)
	}

	server, ok := mcpCfg.MCPServers["railyard_cocoindex"]
	if !ok {
		t.Fatal(".mcp.json missing railyard_cocoindex server")
	}
	if server.Env["COCOINDEX_DATABASE_URL"] != cfg.CocoIndex.DatabaseURL {
		t.Errorf("database_url = %q, want %q", server.Env["COCOINDEX_DATABASE_URL"], cfg.CocoIndex.DatabaseURL)
	}

	mainTable := server.Env["COCOINDEX_MAIN_TABLE"]
	if !strings.Contains(mainTable, "main_backend_embeddings") {
		t.Errorf("COCOINDEX_MAIN_TABLE missing backend table: %q", mainTable)
	}
	if !strings.Contains(mainTable, "main_frontend_embeddings") {
		t.Errorf("COCOINDEX_MAIN_TABLE missing frontend table: %q", mainTable)
	}

	// Should NOT have overlay or engine_id env vars.
	if _, ok := server.Env["COCOINDEX_OVERLAY_TABLE"]; ok {
		t.Error("dispatcher should not have COCOINDEX_OVERLAY_TABLE")
	}
	if _, ok := server.Env["COCOINDEX_ENGINE_ID"]; ok {
		t.Error("dispatcher should not have COCOINDEX_ENGINE_ID")
	}
}

func TestWriteDispatchMCPConfig_PreservesExistingServers(t *testing.T) {
	tmpDir := t.TempDir()

	// Write existing .mcp.json with another server.
	existing := mcpServerConfig{
		MCPServers: map[string]mcpServer{
			"other_server": {
				Command: "/usr/bin/other",
				Args:    []string{"serve"},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), data, 0644)

	cfg := &config.Config{
		CocoIndex: config.CocoIndexConfig{
			DatabaseURL: "postgresql://x",
			VenvPath:    "cocoindex/.venv",
			ScriptsPath: "cocoindex",
		},
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go"},
		},
	}

	err := WriteDispatchMCPConfig(tmpDir, cfg)
	if err != nil {
		t.Fatalf("WriteDispatchMCPConfig() error: %v", err)
	}

	data, _ = os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	var result mcpServerConfig
	json.Unmarshal(data, &result)

	// Should have both servers.
	if _, ok := result.MCPServers["other_server"]; !ok {
		t.Error("existing other_server was lost")
	}
	if _, ok := result.MCPServers["railyard_cocoindex"]; !ok {
		t.Error("railyard_cocoindex was not added")
	}
}

func TestWriteDispatchMCPConfig_SingleTrack(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		CocoIndex: config.CocoIndexConfig{
			DatabaseURL: "postgresql://x",
			VenvPath:    "cocoindex/.venv",
			ScriptsPath: "cocoindex",
		},
		Tracks: []config.TrackConfig{
			{Name: "api", Language: "go"},
		},
	}

	err := WriteDispatchMCPConfig(tmpDir, cfg)
	if err != nil {
		t.Fatalf("WriteDispatchMCPConfig() error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	var result mcpServerConfig
	json.Unmarshal(data, &result)

	server := result.MCPServers["railyard_cocoindex"]
	if server.Env["COCOINDEX_MAIN_TABLE"] != "main_api_embeddings" {
		t.Errorf("COCOINDEX_MAIN_TABLE = %q, want %q", server.Env["COCOINDEX_MAIN_TABLE"], "main_api_embeddings")
	}
}
