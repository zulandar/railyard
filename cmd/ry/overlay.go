package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/engine"
)

func newOverlayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "overlay",
		Short: "Manage CocoIndex per-engine overlay indexes",
	}

	cmd.AddCommand(newOverlayBuildCmd())
	cmd.AddCommand(newOverlayStatusCmd())
	cmd.AddCommand(newOverlayCleanupCmd())
	cmd.AddCommand(newOverlayGCCmd())
	return cmd
}

// ── ry overlay build ────────────────────────────────────────────────────────

func newOverlayBuildCmd() *cobra.Command {
	var (
		configPath string
		engineID   string
		track      string
		workDir    string
	)

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build overlay index for an engine's changed files",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if cfg.CocoIndex.DatabaseURL == "" {
				return fmt.Errorf("cocoindex.database_url not configured in %s", configPath)
			}
			if !cfg.CocoIndex.Overlay.Enabled {
				fmt.Fprintln(cmd.OutOrStdout(), "Overlay indexing is disabled in config")
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Building overlay for engine %s (track: %s)...\n", engineID, track)

			table, err := engine.BuildOverlay(workDir, engineID, track, cfg)
			if err != nil {
				return fmt.Errorf("build overlay: %w", err)
			}

			fmt.Fprintf(out, "Overlay built: %s\n", table)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&engineID, "engine", "", "engine ID (e.g. eng-a1b2c3d4)")
	cmd.Flags().StringVar(&track, "track", "", "track name (e.g. backend)")
	cmd.Flags().StringVar(&workDir, "workdir", ".", "path to engine's git worktree")
	cmd.MarkFlagRequired("engine")
	cmd.MarkFlagRequired("track")
	return cmd
}

// ── ry overlay status ───────────────────────────────────────────────────────

func newOverlayStatusCmd() *cobra.Command {
	var (
		configPath string
		engineID   string
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show overlay status for an engine or all engines",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if cfg.CocoIndex.DatabaseURL == "" {
				return fmt.Errorf("cocoindex.database_url not configured in %s", configPath)
			}

			out := cmd.OutOrStdout()

			if engineID != "" {
				return showOverlayStatus(out, engineID, cfg)
			}

			// No --engine flag: list all overlays by querying overlay_meta.
			return showAllOverlayStatus(out, cfg)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&engineID, "engine", "", "engine ID (optional; omit to show all)")
	return cmd
}

// overlayStatusResult matches the JSON output from overlay.py status.
type overlayStatusResult struct {
	EngineID      string          `json:"engine_id"`
	Track         string          `json:"track"`
	Branch        string          `json:"branch"`
	LastCommit    string          `json:"last_commit"`
	FilesIndexed  int             `json:"files_indexed"`
	ChunksIndexed int             `json:"chunks_indexed"`
	DeletedFiles  json.RawMessage `json:"deleted_files"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
	Status        string          `json:"status"`
}

func showOverlayStatus(out io.Writer, engineID string, cfg *config.Config) error {
	pythonPath := filepath.Join(cfg.CocoIndex.VenvPath, "bin", "python")
	scriptPath := filepath.Join(cfg.CocoIndex.ScriptsPath, "overlay.py")

	cmd := exec.Command(pythonPath, scriptPath, "status",
		"--engine-id", engineID,
		"--database-url", cfg.CocoIndex.DatabaseURL,
	)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("overlay status: %s: %w", strings.TrimSpace(string(raw)), err)
	}

	var result overlayStatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		// Couldn't parse — just print raw output.
		fmt.Fprint(out, string(raw))
		return nil
	}

	if result.Status == "not_found" {
		fmt.Fprintf(out, "No overlay found for engine %s\n", engineID)
		return nil
	}

	printOverlayStatus(out, result)
	return nil
}

func showAllOverlayStatus(out io.Writer, cfg *config.Config) error {
	pythonPath := filepath.Join(cfg.CocoIndex.VenvPath, "bin", "python")
	scriptPath := filepath.Join(cfg.CocoIndex.ScriptsPath, "overlay.py")

	// Query overlay_meta for all engines via a small inline Python script.
	script := fmt.Sprintf(`
import psycopg2, json, sys
conn = psycopg2.connect(%q)
try:
    with conn.cursor() as cur:
        cur.execute("SELECT engine_id, track, branch, last_commit, files_indexed, chunks_indexed, deleted_files, created_at, updated_at FROM overlay_meta ORDER BY updated_at DESC")
        rows = cur.fetchall()
finally:
    conn.close()
results = []
for r in rows:
    results.append({"engine_id": r[0], "track": r[1], "branch": r[2], "last_commit": r[3], "files_indexed": r[4], "chunks_indexed": r[5], "deleted_files": json.loads(r[6]) if r[6] else [], "created_at": str(r[7]), "updated_at": str(r[8]), "status": "ok"})
print(json.dumps(results))
`, cfg.CocoIndex.DatabaseURL)

	cmd := exec.Command(pythonPath, "-c", script)
	_ = scriptPath // used only for single-engine queries
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("overlay status: %s: %w", strings.TrimSpace(string(raw)), err)
	}

	var results []overlayStatusResult
	if err := json.Unmarshal(raw, &results); err != nil {
		fmt.Fprint(out, string(raw))
		return nil
	}

	if len(results) == 0 {
		fmt.Fprintln(out, "No overlay indexes found")
		return nil
	}

	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(out, "")
		}
		printOverlayStatus(out, r)
	}
	return nil
}

func printOverlayStatus(out io.Writer, r overlayStatusResult) {
	fmt.Fprintf(out, "Engine:    %s\n", r.EngineID)
	fmt.Fprintf(out, "Track:     %s\n", r.Track)
	fmt.Fprintf(out, "Branch:    %s\n", r.Branch)
	fmt.Fprintf(out, "Commit:    %s\n", r.LastCommit)
	fmt.Fprintf(out, "Files:     %d\n", r.FilesIndexed)
	fmt.Fprintf(out, "Chunks:    %d\n", r.ChunksIndexed)
	fmt.Fprintf(out, "Updated:   %s\n", r.UpdatedAt)
}

// ── ry overlay cleanup ──────────────────────────────────────────────────────

func newOverlayCleanupCmd() *cobra.Command {
	var (
		configPath string
		engineID   string
	)

	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Drop overlay table and metadata for an engine",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if cfg.CocoIndex.DatabaseURL == "" {
				return fmt.Errorf("cocoindex.database_url not configured in %s", configPath)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Cleaning up overlay for engine %s...\n", engineID)

			if err := engine.CleanupOverlay(engineID, cfg); err != nil {
				return fmt.Errorf("cleanup overlay: %w", err)
			}

			fmt.Fprintf(out, "Overlay cleaned up for engine %s\n", engineID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&engineID, "engine", "", "engine ID (e.g. eng-a1b2c3d4)")
	cmd.MarkFlagRequired("engine")
	return cmd
}

// ── ry overlay gc ───────────────────────────────────────────────────────────

func newOverlayGCCmd() *cobra.Command {
	var (
		configPath string
		dryRun     bool
	)

	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Clean up orphaned overlays for engines that no longer exist",
		Long: `Cross-references overlay_meta in pgvector with the engines table in Dolt.
Any overlay whose engine_id doesn't correspond to an active engine gets cleaned up.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if cfg.CocoIndex.DatabaseURL == "" {
				return fmt.Errorf("cocoindex.database_url not configured in %s", configPath)
			}

			return runOverlayGC(cmd, cfg, dryRun)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list orphaned overlays without removing them")
	return cmd
}

func runOverlayGC(cmd *cobra.Command, cfg *config.Config, dryRun bool) error {
	out := cmd.OutOrStdout()

	// Step 1: Get all engine IDs from overlay_meta in pgvector.
	overlayEngines, err := getOverlayEngineIDs(cfg)
	if err != nil {
		return fmt.Errorf("query overlay_meta: %w", err)
	}

	if len(overlayEngines) == 0 {
		fmt.Fprintln(out, "No overlays found — nothing to GC")
		return nil
	}

	// Step 2: Get active engine IDs from Dolt.
	activeEngines, err := getActiveEngineIDs(cfg)
	if err != nil {
		return fmt.Errorf("query engines table: %w", err)
	}

	// Step 3: Find orphans.
	activeSet := make(map[string]bool, len(activeEngines))
	for _, id := range activeEngines {
		activeSet[id] = true
	}

	var orphans []string
	for _, id := range overlayEngines {
		if !activeSet[id] {
			orphans = append(orphans, id)
		}
	}

	if len(orphans) == 0 {
		fmt.Fprintf(out, "All %d overlay(s) belong to active engines — nothing to GC\n", len(overlayEngines))
		return nil
	}

	fmt.Fprintf(out, "Found %d orphaned overlay(s):\n", len(orphans))
	for _, id := range orphans {
		fmt.Fprintf(out, "  %s\n", id)
	}

	if dryRun {
		fmt.Fprintln(out, "\nDry run — no changes made")
		return nil
	}

	// Step 4: Clean up orphans.
	cleaned := 0
	for _, id := range orphans {
		if err := engine.CleanupOverlay(id, cfg); err != nil {
			fmt.Fprintf(out, "  warning: failed to clean %s: %v\n", id, err)
			continue
		}
		cleaned++
	}

	fmt.Fprintf(out, "\nCleaned up %d/%d orphaned overlay(s)\n", cleaned, len(orphans))
	return nil
}

// getOverlayEngineIDs queries pgvector overlay_meta for all engine IDs.
func getOverlayEngineIDs(cfg *config.Config) ([]string, error) {
	pythonPath := filepath.Join(cfg.CocoIndex.VenvPath, "bin", "python")

	script := fmt.Sprintf(`
import psycopg2, json
conn = psycopg2.connect(%q)
try:
    with conn.cursor() as cur:
        cur.execute("SELECT DISTINCT engine_id FROM overlay_meta")
        rows = cur.fetchall()
finally:
    conn.close()
print(json.dumps([r[0] for r in rows]))
`, cfg.CocoIndex.DatabaseURL)

	cmd := exec.Command(pythonPath, "-c", script)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.TrimSpace(string(raw)), err)
	}

	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, fmt.Errorf("parse engine IDs: %w", err)
	}
	return ids, nil
}

// getActiveEngineIDs queries the Dolt engines table for active engine IDs.
func getActiveEngineIDs(cfg *config.Config) ([]string, error) {
	gormDB, err := db.ConnectAdmin(cfg.Dolt.Host, cfg.Dolt.Port)
	if err != nil {
		return nil, fmt.Errorf("connect to Dolt: %w", err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}
	defer sqlDB.Close()

	// Use the database for this owner.
	if err := gormDB.Exec(fmt.Sprintf("USE `%s`", cfg.Dolt.Database)).Error; err != nil {
		return nil, fmt.Errorf("use database: %w", err)
	}

	var ids []string
	if err := gormDB.Raw("SELECT id FROM engines WHERE status IN ('idle', 'working', 'starting')").Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("query engines: %w", err)
	}
	return ids, nil
}
