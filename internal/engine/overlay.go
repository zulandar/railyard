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

	pythonPath := filepath.Join(cfg.CocoIndex.VenvPath, "bin", "python")
	scriptPath := filepath.Join(cfg.CocoIndex.ScriptsPath, "overlay.py")

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

	pythonPath := filepath.Join(cfg.CocoIndex.VenvPath, "bin", "python")
	scriptPath := filepath.Join(cfg.CocoIndex.ScriptsPath, "overlay.py")

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

// WriteMCPConfig writes a .mcp.json file into the engine's worktree so that
// Claude Code discovers the CocoIndex MCP server with the correct env vars.
func WriteMCPConfig(workDir, engineID, track string, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("overlay: config is nil")
	}
	if cfg.CocoIndex.DatabaseURL == "" {
		return nil // no pgvector configured, skip MCP config
	}

	pythonPath := filepath.Join(cfg.CocoIndex.VenvPath, "bin", "python")
	scriptPath := filepath.Join(cfg.CocoIndex.ScriptsPath, "mcp_server.py")

	mainTable := fmt.Sprintf("main_%s_embeddings", track)
	overlayTable := OverlayTableName(engineID)

	mcpCfg := MCPServerConfig{
		MCPServers: map[string]MCPServer{
			"railyard_cocoindex": {
				Command: pythonPath,
				Args:    []string{scriptPath},
				Env: map[string]string{
					"COCOINDEX_DATABASE_URL":  cfg.CocoIndex.DatabaseURL,
					"COCOINDEX_ENGINE_ID":     engineID,
					"COCOINDEX_MAIN_TABLE":    mainTable,
					"COCOINDEX_OVERLAY_TABLE": overlayTable,
					"COCOINDEX_TRACK":         track,
					"COCOINDEX_WORKTREE":      workDir,
				},
			},
		},
	}

	data, err := json.MarshalIndent(mcpCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("overlay: marshal .mcp.json: %w", err)
	}

	mcpPath := filepath.Join(workDir, ".mcp.json")
	if err := os.WriteFile(mcpPath, data, 0644); err != nil {
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
