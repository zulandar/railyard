package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zulandar/railyard/internal/config"
)

// mcpServerConfig represents a .mcp.json file.
type mcpServerConfig struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

// mcpServer represents a single MCP server entry.
type mcpServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// WriteDispatchMCPConfig writes or merges a cocoindex MCP server entry into
// .mcp.json at workDir. The dispatcher searches all track main tables with
// no overlay (it operates on the main branch).
//
// If .mcp.json already exists, the railyard_cocoindex entry is added/updated
// while preserving other MCP server entries.
func WriteDispatchMCPConfig(workDir string, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("dispatch: config is nil")
	}
	if cfg.CocoIndex.DatabaseURL == "" {
		return nil // no pgvector configured, skip MCP config
	}

	mcpPath := filepath.Join(workDir, ".mcp.json")

	// Load existing .mcp.json if present.
	var mcpCfg mcpServerConfig
	if data, err := os.ReadFile(mcpPath); err == nil {
		if err := json.Unmarshal(data, &mcpCfg); err != nil {
			// Malformed JSON — start fresh but warn via return.
			mcpCfg = mcpServerConfig{}
		}
	}
	if mcpCfg.MCPServers == nil {
		mcpCfg.MCPServers = make(map[string]mcpServer)
	}

	// Build comma-separated list of all track main tables.
	var tables []string
	for _, t := range cfg.Tracks {
		tables = append(tables, fmt.Sprintf("main_%s_embeddings", t.Name))
	}

	pythonPath := filepath.Join(cfg.CocoIndex.VenvPath, "bin", "python")
	scriptPath := filepath.Join(cfg.CocoIndex.ScriptsPath, "mcp_server.py")

	mcpCfg.MCPServers["railyard_cocoindex"] = mcpServer{
		Command: pythonPath,
		Args:    []string{scriptPath},
		Env: map[string]string{
			"COCOINDEX_DATABASE_URL": cfg.CocoIndex.DatabaseURL,
			"COCOINDEX_MAIN_TABLE":   strings.Join(tables, ","),
		},
	}

	data, err := json.MarshalIndent(mcpCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("dispatch: marshal .mcp.json: %w", err)
	}
	return os.WriteFile(mcpPath, data, 0644)
}
