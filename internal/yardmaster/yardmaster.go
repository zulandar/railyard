// Package yardmaster implements the supervisor agent.
package yardmaster

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/zulandar/railyard/internal/config"
)

// StartOpts holds parameters for starting the Yardmaster agent.
type StartOpts struct {
	ConfigPath string
	Config     *config.Config
}

// Start launches a Claude Code session with the yardmaster supervisor prompt.
// The Yardmaster monitors all engines across all tracks, merges branches,
// handles stalls, and manages cross-track dependencies.
func Start(opts StartOpts) error {
	if opts.Config == nil {
		return fmt.Errorf("yardmaster: config is required")
	}
	if len(opts.Config.Tracks) == 0 {
		return fmt.Errorf("yardmaster: at least one track must be configured")
	}

	prompt, err := RenderPrompt(opts.Config)
	if err != nil {
		return fmt.Errorf("yardmaster: render prompt: %w", err)
	}

	// Launch claude in autonomous mode (no human interaction in tmux pane)
	cmd := exec.Command("claude",
		"--dangerously-skip-permissions",
		"--system-prompt", prompt,
		"-p", "You are now running as the Yardmaster supervisor. Begin your monitoring loop: check engine health, completed cars, and blocked cars.",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("yardmaster: claude session: %w", err)
	}
	return nil
}

// BuildCommand constructs the exec.Cmd for the yardmaster Claude session.
// Exported for testing.
func BuildCommand(prompt string) *exec.Cmd {
	return exec.Command("claude",
		"--dangerously-skip-permissions",
		"--system-prompt", prompt,
		"-p", "You are now running as the Yardmaster supervisor. Begin your monitoring loop: check engine health, completed cars, and blocked cars.",
	)
}
