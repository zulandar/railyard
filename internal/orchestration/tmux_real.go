//go:build !unittest

package orchestration

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RealTmux is the production implementation that calls the real tmux binary.
type RealTmux struct{}

func (RealTmux) SessionExists(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", name)
	return cmd.Run() == nil
}

func (RealTmux) CreateSession(name string) error {
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name, "-x", "200", "-y", "50")
	// Unset TMUX so this works when invoked from inside an existing tmux session.
	cmd.Env = envWithoutTMUX()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create tmux session %q: %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// envWithoutTMUX returns the current environment with the TMUX variable removed,
// allowing tmux new-session to work when called from inside an existing session.
func envWithoutTMUX() []string {
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TMUX=") {
			env = append(env, e)
		}
	}
	return env
}

func (RealTmux) NewPane(session string) (string, error) {
	cmd := exec.Command("tmux", "split-window", "-t", session, "-d", "-P", "-F", "#{pane_id}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("new tmux pane in %q: %s: %w", session, strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (RealTmux) SendKeys(paneID, keys string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", paneID, keys, "Enter")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("send keys to %q: %s: %w", paneID, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (RealTmux) SendSignal(paneID, signal string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", paneID, signal)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("send signal to %q: %s: %w", paneID, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (RealTmux) KillPane(paneID string) error {
	cmd := exec.Command("tmux", "kill-pane", "-t", paneID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kill pane %q: %s: %w", paneID, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (RealTmux) KillSession(name string) error {
	cmd := exec.Command("tmux", "kill-session", "-t", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kill tmux session %q: %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (RealTmux) ListPanes(session string) ([]string, error) {
	cmd := exec.Command("tmux", "list-panes", "-t", session, "-F", "#{pane_id}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list panes in %q: %s: %w", session, strings.TrimSpace(string(out)), err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var panes []string
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			panes = append(panes, l)
		}
	}
	return panes, nil
}

func (RealTmux) TileLayout(session string) error {
	cmd := exec.Command("tmux", "select-layout", "-t", session, "tiled")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tile layout for %q: %s: %w", session, strings.TrimSpace(string(out)), err)
	}
	return nil
}
