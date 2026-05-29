package engine

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/config"
)

// CocoIndexPaths resolves the venv python interpreter and the mcp_server.py
// script (run in one-shot `query` mode by the native loop's codesearch tool).
// Shared by every cocoindex caller so path resolution stays in one place.
func CocoIndexPaths(cfg *config.Config) (pythonPath, scriptPath string) {
	pythonPath, _ = filepath.Abs(filepath.Join(cfg.CocoIndex.VenvPath, "bin", "python"))
	scriptPath, _ = filepath.Abs(filepath.Join(cfg.CocoIndex.ScriptsPath, "mcp_server.py"))
	return pythonPath, scriptPath
}

// MainIndexCocoIndexEnv builds the COCOINDEX_* env for the "main index" profile:
// every track's main table joined, no overlay. This is what dispatch, telegraph,
// bull (triage) and inspect (review) all search — they operate against the main
// branch, not a single engine's modified files. Shared by WriteDispatchMCPConfig
// and MainIndexCodeSearchParams so their table targeting cannot drift.
func MainIndexCocoIndexEnv(cfg *config.Config) map[string]string {
	var tables []string
	for _, t := range cfg.Tracks {
		tables = append(tables, fmt.Sprintf("main_%s_embeddings", t.Name))
	}
	return map[string]string{
		"COCOINDEX_DATABASE_URL": cfg.CocoIndex.DatabaseURL,
		"COCOINDEX_MAIN_TABLE":   strings.Join(tables, ","),
	}
}

// MainIndexCodeSearchParams builds the native agent loop's codesearch params for
// the main-index profile (all track main tables, no overlay) — the same table
// targeting WriteDispatchMCPConfig writes for the claude CLI path. Used by
// dispatch, telegraph chat-dispatch, bull triage and inspect review. Returns nil
// when CocoIndex is not configured (DatabaseURL == "") so callers gate
// codesearch-tool registration on a non-nil result.
func MainIndexCodeSearchParams(cfg *config.Config) *agentloop.CodeSearchParams {
	if cfg == nil || cfg.CocoIndex.DatabaseURL == "" {
		return nil
	}
	pythonPath, scriptPath := CocoIndexPaths(cfg)
	return &agentloop.CodeSearchParams{
		PythonPath: pythonPath,
		ScriptPath: scriptPath,
		Env:        MainIndexCocoIndexEnv(cfg),
	}
}

// engineCocoIndexEnv builds the COCOINDEX_* env for the engine profile: the
// track's main table plus this engine's per-engine overlay table (so the
// engine's branch-modified files win over the stale main index). Shared by the
// .mcp.json writer (WriteMCPConfig) and EngineCodeSearchParams so their table
// targeting cannot drift apart.
func engineCocoIndexEnv(workDir, engineID, track string, cfg *config.Config) map[string]string {
	return map[string]string{
		"COCOINDEX_DATABASE_URL":  cfg.CocoIndex.DatabaseURL,
		"COCOINDEX_ENGINE_ID":     engineID,
		"COCOINDEX_MAIN_TABLE":    fmt.Sprintf("main_%s_embeddings", track),
		"COCOINDEX_OVERLAY_TABLE": OverlayTableName(engineID),
		"COCOINDEX_TRACK":         track,
		"COCOINDEX_WORKTREE":      workDir,
	}
}

// EngineCodeSearchParams builds the native agent loop's codesearch params for
// the engine profile (main_<track>_embeddings + the per-engine overlay table) —
// the same table targeting WriteMCPConfig writes for the claude CLI path. It
// returns nil when CocoIndex is not configured (DatabaseURL == "") so callers
// gate codesearch-tool registration on a non-nil result.
func EngineCodeSearchParams(workDir, engineID, track string, cfg *config.Config) *agentloop.CodeSearchParams {
	if cfg == nil || cfg.CocoIndex.DatabaseURL == "" {
		return nil
	}
	pythonPath, scriptPath := CocoIndexPaths(cfg)
	return &agentloop.CodeSearchParams{
		PythonPath: pythonPath,
		ScriptPath: scriptPath,
		Env:        engineCocoIndexEnv(workDir, engineID, track, cfg),
	}
}
