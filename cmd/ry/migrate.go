package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/models"
)

func newMigrateCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate to .railyard/ directory structure",
		Long: `Migrates an existing Railyard repo to the .railyard/ directory structure.

Steps performed:
  1. Check for running engines (warns and aborts if any are alive)
  2. Move engines/ to .railyard/engines/ (if engines/ exists)
  3. Prune and re-register git worktree entries
  4. Update .gitignore (remove engines/, add .railyard/)
  5. Create dispatch and yardmaster worktrees

Safe to run multiple times (idempotent).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrate(cmd.OutOrStdout(), configPath)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runMigrate(out io.Writer, configPath string) error {
	repoDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("migrate: get working directory: %w", err)
	}

	// Step 1: Check for running engines if config and DB are available.
	if err := checkRunningEngines(out, configPath); err != nil {
		return err
	}

	railyardDir := filepath.Join(repoDir, ".railyard")
	enginesDir := filepath.Join(railyardDir, "engines")
	oldEnginesDir := filepath.Join(repoDir, "engines")

	// Step 2: Create .railyard/ directory structure.
	fmt.Fprintln(out, "Creating .railyard/ directory structure...")
	if err := os.MkdirAll(enginesDir, 0755); err != nil {
		return fmt.Errorf("migrate: create .railyard/engines/: %w", err)
	}

	// Step 3: Move engines/ to .railyard/engines/ if old layout exists.
	if info, err := os.Stat(oldEnginesDir); err == nil && info.IsDir() {
		fmt.Fprintln(out, "Moving engines/ to .railyard/engines/...")
		entries, err := os.ReadDir(oldEnginesDir)
		if err != nil {
			return fmt.Errorf("migrate: read engines/: %w", err)
		}

		moved := 0
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "eng-") {
				continue
			}
			src := filepath.Join(oldEnginesDir, e.Name())
			dst := filepath.Join(enginesDir, e.Name())

			// Skip if already exists in destination.
			if _, err := os.Stat(dst); err == nil {
				fmt.Fprintf(out, "  %s already in .railyard/engines/, skipping\n", e.Name())
				continue
			}

			if err := os.Rename(src, dst); err != nil {
				fmt.Fprintf(out, "  Warning: could not move %s: %v\n", e.Name(), err)
				continue
			}
			moved++
			fmt.Fprintf(out, "  Moved %s\n", e.Name())
		}
		if moved > 0 {
			fmt.Fprintf(out, "Moved %d engine worktree(s)\n", moved)
		}

		// Remove old engines/ if empty.
		remaining, _ := os.ReadDir(oldEnginesDir)
		if len(remaining) == 0 {
			os.Remove(oldEnginesDir)
			fmt.Fprintln(out, "Removed empty engines/ directory")
		}
	} else {
		fmt.Fprintln(out, "No old engines/ directory found, skipping move")
	}

	// Step 4: Prune stale git worktree entries.
	fmt.Fprintln(out, "Pruning stale git worktree entries...")
	engine.CleanupWorktrees(repoDir)

	// Step 5: Update .gitignore.
	fmt.Fprintln(out, "Updating .gitignore...")
	if err := updateGitIgnoreForMigration(repoDir, out); err != nil {
		return err
	}

	// Step 6: Create dispatch and yardmaster worktrees.
	fmt.Fprintln(out, "Setting up dispatch worktree...")
	if _, err := engine.EnsureDispatchWorktree(repoDir); err != nil {
		fmt.Fprintf(out, "  Warning: %v\n", err)
	} else {
		fmt.Fprintln(out, "  Dispatch worktree ready")
	}

	fmt.Fprintln(out, "Setting up yardmaster worktree...")
	if _, err := engine.EnsureYardmasterWorktree(repoDir); err != nil {
		fmt.Fprintf(out, "  Warning: %v\n", err)
	} else {
		fmt.Fprintln(out, "  Yardmaster worktree ready")
	}

	fmt.Fprintln(out, "\nMigration complete.")
	return nil
}

// checkRunningEngines checks if any engines have a recent heartbeat and warns.
func checkRunningEngines(out io.Writer, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		// No config = no DB = can't check engines. Proceed silently.
		return nil
	}

	gormDB, err := db.Connect(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database)
	if err != nil {
		// Can't connect to DB — engines probably aren't running.
		return nil
	}

	var alive int64
	threshold := time.Now().Add(-30 * time.Second)
	gormDB.Model(&models.Engine{}).
		Where("status NOT IN ? AND last_activity > ?", []string{"dead"}, threshold).
		Count(&alive)

	if alive > 0 {
		return fmt.Errorf("migrate: %d engine(s) appear to be running (heartbeat within 30s). Stop all engines with 'ry stop' before migrating", alive)
	}

	return nil
}

// updateGitIgnoreForMigration ensures .railyard/ is in .gitignore and
// removes the old engines/ entry if present.
func updateGitIgnoreForMigration(repoDir string, out io.Writer) error {
	gitignorePath := filepath.Join(repoDir, ".gitignore")
	existing := readGitIgnoreEntries(gitignorePath)

	// Check what needs to change.
	hasRailyard := existing[".railyard/"]
	hasOldEngines := existing["engines/"]

	if hasRailyard && !hasOldEngines {
		fmt.Fprintln(out, "  .gitignore already up to date")
		return nil
	}

	if !hasRailyard {
		// Append .railyard/ entry.
		block := "\n# Railyard\n.railyard/\n"
		if err := appendToGitIgnore(gitignorePath, block); err != nil {
			return fmt.Errorf("migrate: update .gitignore: %w", err)
		}
		fmt.Fprintln(out, "  Added .railyard/ to .gitignore")
	}

	if hasOldEngines {
		if err := removeGitIgnoreEntry(gitignorePath, "engines/"); err != nil {
			fmt.Fprintf(out, "  Warning: could not remove engines/ from .gitignore: %v\n", err)
		} else {
			fmt.Fprintln(out, "  Removed engines/ from .gitignore")
		}
	}

	return nil
}

// removeGitIgnoreEntry removes a specific entry from .gitignore.
func removeGitIgnoreEntry(path, entry string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var result []string
	for _, line := range lines {
		if strings.TrimSpace(line) != entry {
			result = append(result, line)
		}
	}

	return os.WriteFile(path, []byte(strings.Join(result, "\n")), 0644)
}
