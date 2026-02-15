// Package dispatch implements the planner agent.
package dispatch

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/zulandar/railyard/internal/config"
)

// StartOpts holds parameters for starting the Dispatch agent.
type StartOpts struct {
	ConfigPath string
	Config     *config.Config
}

// Start launches a Claude Code session with the dispatch planner prompt.
// It's interactive — the user types feature requests and Dispatch creates beads.
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

	// Launch claude in autonomous mode (no human interaction in tmux pane)
	cmd := exec.Command("claude",
		"--dangerously-skip-permissions",
		"--system-prompt", prompt,
		"-p", "You are now running as the Dispatch planner. Monitor for incoming feature requests and decompose them into beads. Check status with: ry bead list && ry bead ready",
	)
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
		"--system-prompt", prompt,
		"-p", "You are now running as the Dispatch planner. Monitor for incoming feature requests and decompose them into beads. Check status with: ry bead list && ry bead ready",
	)
}
