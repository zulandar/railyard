// Package dispatch implements the planner agent.
package dispatch

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/engine"
)

// StartOpts holds parameters for starting the Dispatch agent.
type StartOpts struct {
	ConfigPath string
	Config     *config.Config
	RepoDir    string // primary repo directory (for worktree creation)
}

// Start launches a Claude Code session with the dispatch planner prompt.
// It's interactive — the user types feature requests and Dispatch creates cars.
func Start(opts StartOpts) error {
	if opts.Config == nil {
		return fmt.Errorf("dispatch: config is required")
	}
	if len(opts.Config.Tracks) == 0 {
		return fmt.Errorf("dispatch: at least one track must be configured")
	}

	prompt, err := RenderPrompt(opts.Config)
	if err != nil {
		return fmt.Errorf("dispatch: render prompt: %w", err)
	}

	// Set up the dispatch worktree so the dispatcher operates in isolation
	// from the user's primary repo. Falls back to cwd if worktree fails.
	workDir := opts.RepoDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	if wtDir, err := engine.EnsureDispatchWorktree(workDir); err != nil {
		log.Printf("dispatch: worktree setup warning: %v (using repo dir)", err)
	} else {
		// Sync worktree to the primary repo's current branch.
		branch := engine.DetectBaseBranch(workDir, opts.Config.DefaultBranch)
		if err := engine.SyncWorktreeToBranch(wtDir, branch); err != nil {
			log.Printf("dispatch: worktree sync warning: %v", err)
		}
		workDir = wtDir
	}

	// Write cocoindex MCP config so the dispatcher can search the codebase.
	// Non-fatal: dispatcher works without it, just no semantic search.
	if opts.Config.CocoIndex.DatabaseURL != "" {
		if err := WriteDispatchMCPConfig(workDir, opts.Config); err != nil {
			log.Printf("dispatch: write MCP config warning: %v", err)
		}
	}

	// Launch claude interactively — user attaches to tmux pane and converses.
	cmd := exec.Command("claude",
		"--dangerously-skip-permissions",
		"--append-system-prompt", prompt,
	)
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dispatch: claude session: %w", err)
	}
	return nil
}

// BuildCommand constructs the exec.Cmd for the dispatch Claude session.
// Exported for testing.
func BuildCommand(prompt string) *exec.Cmd {
	return exec.Command("claude",
		"--dangerously-skip-permissions",
		"--append-system-prompt", prompt,
	)
}
