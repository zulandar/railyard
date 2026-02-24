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

func (RealTmux) SendKeys(session, keys string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", session, keys, "Enter")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("send keys to %q: %s: %w", session, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (RealTmux) SendSignal(session, signal string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", session, signal)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("send signal to %q: %s: %w", session, strings.TrimSpace(string(out)), err)
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

// ListSessions returns all tmux session names matching the given prefix.
func (RealTmux) ListSessions(prefix string) ([]string, error) {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// tmux returns error when server is not running (no sessions).
		if strings.Contains(string(out), "no server running") || strings.Contains(string(out), "no sessions") {
			return nil, nil
		}
		return nil, fmt.Errorf("list tmux sessions: %s: %w", strings.TrimSpace(string(out)), err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var sessions []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" && strings.HasPrefix(l, prefix) {
			sessions = append(sessions, l)
		}
	}
	return sessions, nil
}
