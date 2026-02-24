package orchestration

import "fmt"

// Legacy session names kept for Stop() backwards compatibility.
const legacySessionName = "railyard"
const legacyDispatchSessionName = "railyard-dispatch"

// SessionPrefix returns the prefix for all tmux sessions belonging to this owner.
// Format: railyard_OWNER
func SessionPrefix(owner string) string {
	return fmt.Sprintf("railyard_%s", owner)
}

// YardmasterSession returns the tmux session name for the yardmaster.
// Format: railyard_OWNER_yardmaster
func YardmasterSession(owner string) string {
	return fmt.Sprintf("railyard_%s_yardmaster", owner)
}

// TelegraphSession returns the tmux session name for telegraph.
// Format: railyard_OWNER_telegraph
func TelegraphSession(owner string) string {
	return fmt.Sprintf("railyard_%s_telegraph", owner)
}

// EngineSession returns the tmux session name for an engine instance.
// Format: railyard_OWNER_engNNN (zero-padded to 3 digits)
func EngineSession(owner string, index int) string {
	return fmt.Sprintf("railyard_%s_eng%03d", owner, index)
}

// DispatchSession returns the tmux session name for dispatch.
// Format: railyard_OWNER_dispatch
func DispatchSession(owner string) string {
	return fmt.Sprintf("railyard_%s_dispatch", owner)
}

// Tmux abstracts tmux operations for testability.
type Tmux interface {
	SessionExists(name string) bool
	CreateSession(name string) error
	SendKeys(session, keys string) error
	SendSignal(session, signal string) error
	KillSession(name string) error
	ListSessions(prefix string) ([]string, error)
}

// DefaultTmux is the default tmux implementation used by the package.
// Set to RealTmux{} in tmux_real.go (excluded from test builds via build tag).
var DefaultTmux Tmux = RealTmux{}
