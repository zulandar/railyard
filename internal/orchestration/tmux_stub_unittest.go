//go:build unittest

package orchestration

// RealTmux is a no-op stub used during unit testing (build tag: unittest).
// The real implementation is in tmux_real.go.
type RealTmux struct{}

func (RealTmux) SessionExists(name string) bool          { return false }
func (RealTmux) CreateSession(name string) error          { return nil }
func (RealTmux) NewPane(session string) (string, error)   { return "", nil }
func (RealTmux) SendKeys(paneID, keys string) error       { return nil }
func (RealTmux) SendSignal(paneID, signal string) error   { return nil }
func (RealTmux) KillPane(paneID string) error             { return nil }
func (RealTmux) KillSession(name string) error            { return nil }
func (RealTmux) ListPanes(session string) ([]string, error) { return nil, nil }
func (RealTmux) TileLayout(session string) error          { return nil }
