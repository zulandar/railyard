package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

func TestMainIndexCodeSearchParams_NotConfigured(t *testing.T) {
	if MainIndexCodeSearchParams(nil) != nil {
		t.Error("nil config should yield nil params")
	}
	if MainIndexCodeSearchParams(&config.Config{}) != nil {
		t.Error("empty DatabaseURL should yield nil params so registration is gated off")
	}
}

func TestMainIndexCodeSearchParams_AllMainTablesNoOverlay(t *testing.T) {
	cfg := &config.Config{
		CocoIndex: config.CocoIndexConfig{
			DatabaseURL: "postgresql://x",
			VenvPath:    "cocoindex/.venv",
			ScriptsPath: "cocoindex",
		},
		Tracks: []config.TrackConfig{
			{Name: "backend"},
			{Name: "frontend"},
		},
	}
	p := MainIndexCodeSearchParams(cfg)
	if p == nil {
		t.Fatal("expected non-nil params when CocoIndex is configured")
	}
	if !strings.HasSuffix(p.PythonPath, filepath.Join("cocoindex/.venv", "bin", "python")) {
		t.Errorf("PythonPath = %q, want it to resolve the venv python", p.PythonPath)
	}
	if !strings.HasSuffix(p.ScriptPath, filepath.Join("cocoindex", "mcp_server.py")) {
		t.Errorf("ScriptPath = %q, want it to resolve mcp_server.py", p.ScriptPath)
	}
	mt := p.Env["COCOINDEX_MAIN_TABLE"]
	if !strings.Contains(mt, "main_backend_embeddings") || !strings.Contains(mt, "main_frontend_embeddings") {
		t.Errorf("COCOINDEX_MAIN_TABLE = %q, want all track main tables", mt)
	}
	if p.Env["COCOINDEX_DATABASE_URL"] != "postgresql://x" {
		t.Errorf("COCOINDEX_DATABASE_URL = %q", p.Env["COCOINDEX_DATABASE_URL"])
	}
	// Main-index profile searches main only — never an overlay or a single engine.
	if _, ok := p.Env["COCOINDEX_OVERLAY_TABLE"]; ok {
		t.Error("main-index profile must not set COCOINDEX_OVERLAY_TABLE")
	}
	if _, ok := p.Env["COCOINDEX_ENGINE_ID"]; ok {
		t.Error("main-index profile must not set COCOINDEX_ENGINE_ID")
	}
}

func TestEngineCodeSearchParams_NotConfigured(t *testing.T) {
	if EngineCodeSearchParams("/wd", "eng-1", "backend", nil) != nil {
		t.Error("nil config should yield nil params")
	}
	if EngineCodeSearchParams("/wd", "eng-1", "backend", &config.Config{}) != nil {
		t.Error("empty DatabaseURL should yield nil params so registration is gated off")
	}
}

func TestEngineCodeSearchParams_MainPlusOverlay(t *testing.T) {
	cfg := &config.Config{
		CocoIndex: config.CocoIndexConfig{
			DatabaseURL: "postgresql://x",
			VenvPath:    "cocoindex/.venv",
			ScriptsPath: "cocoindex",
		},
	}
	p := EngineCodeSearchParams("/work/tree", "eng-a1b2", "backend", cfg)
	if p == nil {
		t.Fatal("expected non-nil params when CocoIndex is configured")
	}
	if !strings.HasSuffix(p.PythonPath, filepath.Join("cocoindex/.venv", "bin", "python")) {
		t.Errorf("PythonPath = %q, want it to resolve the venv python", p.PythonPath)
	}
	if !strings.HasSuffix(p.ScriptPath, filepath.Join("cocoindex", "mcp_server.py")) {
		t.Errorf("ScriptPath = %q, want it to resolve mcp_server.py", p.ScriptPath)
	}
	// Engine searches its track's main table + this engine's overlay (mirrors
	// WriteMCPConfig), so branch-modified files win over the stale main index.
	if got := p.Env["COCOINDEX_MAIN_TABLE"]; got != "main_backend_embeddings" {
		t.Errorf("COCOINDEX_MAIN_TABLE = %q, want main_backend_embeddings", got)
	}
	if got := p.Env["COCOINDEX_OVERLAY_TABLE"]; got != OverlayTableName("eng-a1b2") {
		t.Errorf("COCOINDEX_OVERLAY_TABLE = %q, want %q", got, OverlayTableName("eng-a1b2"))
	}
	if got := p.Env["COCOINDEX_ENGINE_ID"]; got != "eng-a1b2" {
		t.Errorf("COCOINDEX_ENGINE_ID = %q, want eng-a1b2", got)
	}
	if got := p.Env["COCOINDEX_TRACK"]; got != "backend" {
		t.Errorf("COCOINDEX_TRACK = %q, want backend", got)
	}
	if got := p.Env["COCOINDEX_WORKTREE"]; got != "/work/tree" {
		t.Errorf("COCOINDEX_WORKTREE = %q, want /work/tree", got)
	}
	if got := p.Env["COCOINDEX_DATABASE_URL"]; got != "postgresql://x" {
		t.Errorf("COCOINDEX_DATABASE_URL = %q", got)
	}
}
