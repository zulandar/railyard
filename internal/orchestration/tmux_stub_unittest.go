//go:build unittest

package orchestration

// RealTmux is a no-op stub used during unit testing (build tag: unittest).
// The real implementation is in tmux_real.go.
type RealTmux struct{}

func (RealTmux) SessionExists(name string) bool                { return false }
func (RealTmux) CreateSession(name string) error               { return nil }
func (RealTmux) SendKeys(session, keys string) error           { return nil }
func (RealTmux) SendSignal(session, signal string) error       { return nil }
func (RealTmux) KillSession(name string) error                 { return nil }
func (RealTmux) ListSessions(prefix string) ([]string, error)  { return nil, nil }
