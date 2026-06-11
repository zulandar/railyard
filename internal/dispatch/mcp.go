package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/engine"
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

// WriteDispatchMCPConfig writes or merges Railyard's MCP server entries into
// .mcp.json at workDir: the cocoindex entry (the dispatcher searches all
// track main tables with no overlay — it operates on the main branch) plus
// any railyard.yaml mcp_servers entries.
//
// If .mcp.json already exists, entries are added/updated while preserving
// other MCP server entries.
func WriteDispatchMCPConfig(workDir string, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("dispatch: config is nil")
	}
	if cfg.CocoIndex.DatabaseURL == "" && len(cfg.MCPServers) == 0 {
		return nil // nothing to write; leave any committed .mcp.json untouched
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

	for name, srv := range cfg.MCPServers {
		mcpCfg.MCPServers[name] = mcpServer{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
		}
	}

	if cfg.CocoIndex.DatabaseURL != "" {
		pythonPath, scriptPath := engine.CocoIndexPaths(cfg)
		mcpCfg.MCPServers[engine.CocoIndexMCPServerName] = mcpServer{
			Command: pythonPath,
			Args:    []string{scriptPath},
			Env:     engine.MainIndexCocoIndexEnv(cfg),
		}
	}

	data, err := json.MarshalIndent(mcpCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("dispatch: marshal .mcp.json: %w", err)
	}
	return os.WriteFile(mcpPath, data, 0600)
}
