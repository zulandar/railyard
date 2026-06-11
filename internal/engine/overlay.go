// Package engine — overlay lifecycle management for per-engine CocoIndex overlays.
//
// All functions in this file are non-fatal by design: callers should log errors
// but continue operating with the main index only if overlay operations fail.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zulandar/railyard/internal/config"
)

// OverlayTableName derives the pgvector overlay table name from an engine ID.
// eng-a1b2c3d4 -> ovl_eng_a1b2c3d4
func OverlayTableName(engineID string) string {
	return "ovl_" + strings.ReplaceAll(engineID, "-", "_")
}

// BuildOverlayResult holds the JSON output from overlay.py build.
type BuildOverlayResult struct {
	FilesIndexed  int    `json:"files_indexed"`
	ChunksIndexed int    `json:"chunks_indexed"`
	DeletedFiles  int    `json:"deleted_files"`
	Table         string `json:"table"`
	Status        string `json:"status"`
}

// BuildOverlay shells out to overlay.py build to create/update the per-engine
// overlay index. Returns the overlay table name on success.
func BuildOverlay(workDir, engineID, track string, cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("overlay: config is nil")
	}
	if !cfg.CocoIndex.Overlay.Enabled {
		return "", nil
	}

	trackCfg := findTrack(cfg, track)
	if trackCfg == nil {
		return "", fmt.Errorf("overlay: track %q not found in config", track)
	}

	timeout := time.Duration(cfg.CocoIndex.Overlay.BuildTimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	pythonPath, _ := filepath.Abs(filepath.Join(cfg.CocoIndex.VenvPath, "bin", "python"))
	scriptPath, _ := filepath.Abs(filepath.Join(cfg.CocoIndex.ScriptsPath, "overlay.py"))

	args := []string{
		scriptPath, "build",
		"--engine-id", engineID,
		"--worktree", workDir,
		"--track", track,
		"--database-url", cfg.CocoIndex.DatabaseURL,
	}
	// Add file patterns.
	if len(trackCfg.FilePatterns) > 0 {
		args = append(args, "--file-patterns")
		args = append(args, trackCfg.FilePatterns...)
	}

	cmd := exec.CommandContext(ctx, pythonPath, args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("overlay: build failed: %s: %w", string(out), err)
	}

	// Parse JSON output to get the table name.
	var result BuildOverlayResult
	if err := json.Unmarshal(out, &result); err != nil {
		// Build succeeded but output wasn't parseable — derive table name.
		return OverlayTableName(engineID), nil
	}

	if result.Table != "" {
		return result.Table, nil
	}
	return OverlayTableName(engineID), nil
}

// CleanupOverlay shells out to overlay.py cleanup to drop the overlay table
// and delete the overlay_meta row for the given engine.
func CleanupOverlay(engineID string, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("overlay: config is nil")
	}
	if cfg.CocoIndex.DatabaseURL == "" {
		return nil // no pgvector configured, nothing to clean up
	}

	pythonPath, _ := filepath.Abs(filepath.Join(cfg.CocoIndex.VenvPath, "bin", "python"))
	scriptPath, _ := filepath.Abs(filepath.Join(cfg.CocoIndex.ScriptsPath, "overlay.py"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, pythonPath, scriptPath, "cleanup",
		"--engine-id", engineID,
		"--database-url", cfg.CocoIndex.DatabaseURL,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("overlay: cleanup failed: %s: %w", string(out), err)
	}
	return nil
}

// CocoIndexMCPServerName is the single source of truth for the cocoindex MCP
// server's name. It is the .mcp.json server key the claude CLI reads AND the
// basis for the tool_use prefix below, so the .mcp.json writers (dispatch +
// engine) and the stream-json observability detector cannot drift apart.
// (railyard-cpn) It aliases config.ReservedMCPServerName so config validation
// rejects user mcp_servers entries that would collide with it.
const CocoIndexMCPServerName = config.ReservedMCPServerName

// CocoIndexMCPToolPrefix is the prefix claude assigns MCP tool_use blocks from
// the cocoindex server (e.g. mcp__railyard_cocoindex__search_code). Matching it
// in the stream-json gives a positive "codesearch was called" signal on the
// claude CLI path without grepping raw transcript text.
const CocoIndexMCPToolPrefix = "mcp__" + CocoIndexMCPServerName + "__"

// MCPServerConfig represents the .mcp.json file written to each engine worktree.
type MCPServerConfig struct {
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

// MCPServer represents a single MCP server entry.
type MCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// WriteMCPConfig writes or merges Railyard's MCP server entries into the
// .mcp.json at the engine's worktree so that Claude Code discovers them. A
// committed .mcp.json is preserved: the railyard_cocoindex entry is upserted
// (when cocoindex is configured) and railyard.yaml mcp_servers entries are
// folded in alongside any pre-existing entries.
func WriteMCPConfig(workDir, engineID, track string, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("overlay: config is nil")
	}
	if cfg.CocoIndex.DatabaseURL == "" && len(cfg.MCPServers) == 0 {
		return nil // nothing to write; leave any committed .mcp.json untouched
	}

	mcpPath := filepath.Join(workDir, ".mcp.json")

	// Load existing .mcp.json (committed to the repo or left by a prior
	// write) so its entries are preserved.
	var mcpCfg MCPServerConfig
	if data, err := os.ReadFile(mcpPath); err == nil {
		if err := json.Unmarshal(data, &mcpCfg); err != nil {
			mcpCfg = MCPServerConfig{} // malformed JSON — start fresh
		}
	}
	if mcpCfg.MCPServers == nil {
		mcpCfg.MCPServers = make(map[string]MCPServer)
	}

	for name, srv := range cfg.MCPServers {
		mcpCfg.MCPServers[name] = MCPServer{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
		}
	}

	if cfg.CocoIndex.DatabaseURL != "" {
		pythonPath, scriptPath := CocoIndexPaths(cfg)
		mcpCfg.MCPServers[CocoIndexMCPServerName] = MCPServer{
			Command: pythonPath,
			Args:    []string{scriptPath},
			Env:     engineCocoIndexEnv(workDir, engineID, track, cfg),
		}
	}

	data, err := json.MarshalIndent(mcpCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("overlay: marshal .mcp.json: %w", err)
	}

	if err := os.WriteFile(mcpPath, data, 0600); err != nil {
		return fmt.Errorf("overlay: write %s: %w", mcpPath, err)
	}

	return nil
}

// findTrack looks up a track config by name.
func findTrack(cfg *config.Config, name string) *config.TrackConfig {
	for i := range cfg.Tracks {
		if cfg.Tracks[i].Name == name {
			return &cfg.Tracks[i]
		}
	}
	return nil
}
