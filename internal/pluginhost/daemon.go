package pluginhost

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"

	"github.com/zulandar/railyard/pkg/plugin"
)

// The daemon drain budget is not declared as a constant in this file —
// instead it is whatever remains of the per-plugin Stop budget
// (registry.go:stopDrainTimeout, 5s). When host.Stop runs it first
// cancels every daemon for the plugin, then calls Plugin.Stop
// concurrently with a single shared 5s budget. Both the plugin's own
// goroutines AND its daemons must drain inside that shared budget,
// otherwise they are abandoned. Spec §4 (per-plugin Stop drain = 5s)
// and spec §8 (per-daemon drain = 5s) are interpreted together: 5s is
// the combined per-plugin budget, not stacked per-daemon. Plugins
// remain unblocked by misbehaving daemons because the host fans out
// daemon cancellation eagerly and joinDaemons honors ctx.Done from the
// outer 5s deadline.

// daemonRestartBudget is the lifetime panic budget per daemon, NOT per
// consecutive-panic streak. After this many panics the daemon is
// permanently disabled for the lifetime of the host. Spec §8: "restarts
// daemon up to 3 times total".
//
// This differs from the event bus's subscription disable semantics
// (internal/events/bus.go uses a consecutive-panic counter that resets on
// success). The daemon counter is monotonic.
const daemonRestartBudget = 3

// daemonState tracks a single registered daemon for its lifetime. Each
// daemon owns a per-plugin context derived from the host's root daemon
// context; cancelling the context signals the daemon goroutine to exit.
//
// done is closed by the wrapper goroutine once the wrapper finishes —
// either because the daemon returned cleanly, the daemon exhausted its
// panic budget, or the wrapper observed ctx.Done and waited the daemon
// out (or abandoned it). It is the join handle used by Host.Stop.
type daemonState struct {
	pluginName string
	name       string
	fn         plugin.DaemonFunc
	logger     *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	// panics is the lifetime panic count for this daemon. Read and written
	// only by the wrapper goroutine, no synchronization required.
	panics int
}

// runDaemonFor is the *Host implementation invoked by pluginView.RunDaemon.
// pluginName is the registered plugin's name; daemonName is the label
// passed to RunDaemon by the plugin. fn is the plugin-supplied daemon
// body.
//
// Each call spawns one wrapper goroutine that:
//   - logs lifecycle transitions with plugin/daemon attributes,
//   - recovers panics, increments the lifetime panic counter, and
//     restarts the daemon up to daemonRestartBudget total panics, after
//     which the daemon is permanently disabled,
//   - exits when its per-plugin context is cancelled, after which Host.Stop
//     joins on the daemon's done channel.
//
// The daemon's context is a child of h.daemonCtx (lazily initialised on
// first call), so a single Host-wide cancel can fan out to every daemon —
// useful for catastrophic shutdown paths even if Stop is never called.
func (h *Host) runDaemonFor(pluginName, daemonName string, fn plugin.DaemonFunc) {
	if fn == nil {
		slog.Default().Warn("pluginhost: RunDaemon called with nil fn",
			slog.String("plugin", pluginName),
			slog.String("daemon", daemonName),
		)
		return
	}

	h.mu.Lock()
	if h.daemonCtx == nil {
		h.daemonCtx, h.daemonCancel = context.WithCancel(context.Background())
	}
	ctx, cancel := context.WithCancel(h.daemonCtx)
	state := &daemonState{
		pluginName: pluginName,
		name:       daemonName,
		fn:         fn,
		logger: slog.Default().With(
			slog.String("plugin", pluginName),
			slog.String("daemon", daemonName),
		),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	if h.daemons == nil {
		h.daemons = make(map[string][]*daemonState)
	}
	h.daemons[pluginName] = append(h.daemons[pluginName], state)
	h.mu.Unlock()

	go h.superviseDaemon(state)
}

// superviseDaemon is the long-lived wrapper goroutine for a single
// daemon. It loops, invoking fn(ctx) with panic recovery, until either:
//   - the context is cancelled (orderly shutdown), or
//   - fn returns nil (orderly completion), or
//   - the daemon's lifetime panic count reaches daemonRestartBudget
//     (permanent disable).
//
// A non-nil return from fn is logged at WARN and counted as a restart
// trigger without consuming panic budget; the supervisor immediately
// re-invokes fn so daemons that return errors can be retried without
// the harsher consecutive-panic accounting. Spec §8 only specifies the
// panic restart bound; error-restart behavior is implementation choice
// — we keep retrying on error until ctx is cancelled.
//
// We always invoke fn at least once, even if ctx is already cancelled by
// the time the goroutine is scheduled. This matches plugin author
// expectations (the daemon body runs, observes ctx, returns) and keeps
// the test surface deterministic: a freshly-registered daemon whose
// context gets cancelled before the goroutine runs still gets a chance
// to execute its cleanup path.
func (h *Host) superviseDaemon(s *daemonState) {
	defer close(s.done)
	for {
		stop := h.invokeDaemonOnce(s)
		if stop {
			return
		}
		// Don't restart-loop if ctx is already cancelled — the daemon
		// already had its run. Without this check a daemon that returns
		// nil but is supposed to keep running could spin if we restarted
		// it unconditionally on ctx-cancelled returns; the check inside
		// invokeDaemonOnce handles the error case but not a clean nil
		// return. (Clean returns set stop=true above, so this is
		// defense in depth.)
		if s.ctx.Err() != nil {
			return
		}
	}
}

// invokeDaemonOnce runs fn(ctx) once with panic recovery. Returns true
// when the supervisor should stop looping (clean return, ctx cancelled,
// or panic budget exhausted); returns false to request a restart.
func (h *Host) invokeDaemonOnce(s *daemonState) (stop bool) {
	defer func() {
		if r := recover(); r != nil {
			s.panics++
			s.logger.Error(fmt.Sprintf(
				"plugin daemon panicked: plugin=%s daemon=%s panic=%v\n%s",
				s.pluginName, s.name, r, debug.Stack(),
			))
			if s.panics >= daemonRestartBudget {
				s.logger.Error("daemon disabled after 3 consecutive panics",
					slog.Int("panics", s.panics),
				)
				stop = true
				return
			}
			stop = false
		}
	}()

	err := s.fn(s.ctx)
	if err != nil {
		s.logger.Warn("daemon exited with error", slog.String("error", err.Error()))
		// If the context is cancelled we treat that as a clean stop;
		// otherwise we retry. Plugins that want hard-fail-on-error
		// should not return errors — return nil and exit on ctx instead.
		if s.ctx.Err() != nil {
			return true
		}
		return false
	}
	// Clean exit: stop supervising regardless of ctx state.
	return true
}

// cancelDaemons cancels every daemon belonging to the named plugin and
// returns the slice of daemonStates so the caller can join on their done
// channels. The plugin's entry is removed from h.daemons so subsequent
// Stop calls (or re-registrations) start fresh.
//
// Returns nil when the plugin has no registered daemons.
func (h *Host) cancelDaemons(pluginName string) []*daemonState {
	h.mu.Lock()
	defer h.mu.Unlock()
	states := h.daemons[pluginName]
	if len(states) == 0 {
		return nil
	}
	delete(h.daemons, pluginName)
	for _, s := range states {
		s.cancel()
	}
	return states
}

// joinDaemons waits for the supplied daemon goroutines to finish or until
// the deadline expires (signalled via the parent context). Daemons that
// have not finished by the deadline are abandoned with a WARN log.
//
// Each daemon's join is independent — slow daemon A does not block fast
// daemon B's drain — but the overall wait is bounded by the parent
// context's deadline so the caller's drain budget stays intact.
func joinDaemons(parent context.Context, states []*daemonState) {
	if len(states) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, s := range states {
		wg.Add(1)
		go func(s *daemonState) {
			defer wg.Done()
			select {
			case <-s.done:
				// Daemon honored cancellation in time.
			case <-parent.Done():
				// plugin= / daemon= attrs are already attached to s.logger
				// (see runDaemonFor) — including them in the message would
				// duplicate them at the structured-attr level. Keep the
				// message terse; downstream log views surface the attrs.
				s.logger.Warn("daemon abandoned (drain timeout exceeded)")
			}
		}(s)
	}
	// Bound the join by the parent context. wg.Wait blocks until every
	// goroutine returns, which they all do as soon as parent is cancelled
	// (since each goroutine selects on parent.Done()).
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-parent.Done():
		// Wait for the wg goroutines to observe the cancellation. They
		// each select on parent.Done so this completes promptly.
		<-done
	}
}
