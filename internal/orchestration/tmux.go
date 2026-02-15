package orchestration

const SessionName = "railyard"

// Tmux abstracts tmux operations for testability.
type Tmux interface {
	SessionExists(name string) bool
	CreateSession(name string) error
	NewPane(session string) (string, error)
	SendKeys(paneID, keys string) error
	SendSignal(paneID, signal string) error
	KillPane(paneID string) error
	KillSession(name string) error
	ListPanes(session string) ([]string, error)
	TileLayout(session string) error
}

// DefaultTmux is the default tmux implementation used by the package.
// Set to RealTmux{} in tmux_real.go (excluded from test builds via build tag).
var DefaultTmux Tmux = RealTmux{}
