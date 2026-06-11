// Operator-triggered plugin restart (railyard-77h.13).
//
// Restart relaunches a single named plugin in a live host WITHOUT
// restarting the whole yard. It is the operator escape hatch for a wedged
// plugin, a binary replaced on disk, or a plugin the crash-budget
// supervisor permanently disabled. It is distinct from the supervisor's
// own crash-relaunch loop (supervise.go): a Restart is operator-initiated,
// so it RESETS the crash-budget window rather than counting toward it.
//
// IMPORTANT — config is NOT reloaded. The plugin config block comes from
// deps.Cfg, which is fixed for the host-process lifetime. Restart does NOT
// re-read railyard.yaml (that is a separate "reload" concern, deliberately
// out of scope for this bead). Restart DOES pick up a plugin binary that
// was replaced on disk since launch, because the relaunch re-execs the
// recorded path through the same go-plugin handshake the initial launch
// uses.
//
// Concurrency. Restart shares the host's mu / shutdownCh / lp.stopping /
// superviseDone machinery with Host.Stop and the supervisor. Two races are
// handled explicitly:
//
//   - Restart vs. Host.Stop: a Restart that observes shutdownCh closed
//     MUST NOT relaunch — exactly like the supervisor's pre-relaunch
//     shutdown poll. Restart checks isShuttingDown() before every launch.
//   - Restart vs. supervisor crash-relaunch: for a RUNNING plugin, Restart
//     marks lp.stopping=true under mu and then blocks on lp.superviseDone
//     before launching fresh. Setting stopping makes the supervisor read
//     the subprocess exit Restart triggers as planned (it walks away
//     without relaunching); waiting on superviseDone guarantees the old
//     supervisor goroutine is provably gone before a new one is spawned,
//     so there is never a window with two supervisors (and two
//     subprocesses) for one name.
package pluginhost

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// Restart relaunches the named plugin in place without restarting the
// yard. The plugin may be running, crash-budget-disabled, or init-failed;
// any other name is an error listing the known plugins.
//
// Behavior by prior state:
//   - running: gracefully stop the subprocess (cancel subscriptions,
//     drive PluginService.Stop with the per-plugin drain budget, then
//     terminate), reset the crash budget, and relaunch a fresh subprocess
//     through the same launch → Init (+Start if the host is started) path
//     the supervisor's relaunch uses.
//   - disabled: clear the disabled entry, then launch fresh.
//   - failed: clear the init-failure entry, then launch fresh.
//
// Restart returns only an error — the "old-state -> new-state" line the
// CLI prints is composed by the HTTP handler, which reads Status() before
// and after. On any launch failure the plugin is left out of the running
// set (recorded as an init-failure so Status() shows it as "failed") and
// the error is returned; the operator can retry.
//
// A Restart that loses the race with Host.Stop (shutdownCh closed) returns
// an error and does NOT relaunch.
func (h *Host) Restart(ctx context.Context, name string) error {
	// Fast shutdown guard: never start new subprocess work once Stop has
	// begun. Re-checked after the running-plugin teardown below, since the
	// teardown can take up to stopDrainTimeout and Stop may race in during
	// that window.
	if h.isShuttingDown() {
		return fmt.Errorf("pluginhost: cannot restart %q: host is shutting down", name)
	}

	// Snapshot the prior state and, for non-running states, clear the
	// registry entry under the lock. For the running state we DON'T remove
	// here — we must first mark stopping and tear the live subprocess down
	// in an ordered handoff with its supervisor.
	c, prevState, err := h.prepareRestart(name)
	if err != nil {
		return err
	}

	if prevState == StatusRunning {
		// Ordered teardown of the live subprocess + its supervisor. After
		// this returns the old supervisor is gone, the subprocess is dead,
		// the socket is cleaned up, the crash budget is reset, and the
		// registry entry is removed — so the fresh launch below cannot
		// collide with the old plugin's machinery.
		h.stopRunningForRestart(ctx, name)

		// Re-check shutdown: the teardown above can block for the drain
		// budget, and a Stop may have raced in. If so, do not relaunch.
		if h.isShuttingDown() {
			return fmt.Errorf("pluginhost: cannot restart %q: host began shutting down during teardown", name)
		}
	}

	// Launch fresh through the same supervisor-owned path the initial boot
	// and the crash-relaunch use. launchAndSuperviseForRestart resets the
	// crash budget (operator restart is not a crash) and re-drives Start
	// when the host is already started.
	if err := h.launchAndSuperviseForRestart(ctx, c); err != nil {
		return fmt.Errorf("pluginhost: restart %q: %w", name, err)
	}
	return nil
}

// prepareRestart resolves the named plugin's prior state and returns the
// candidate to relaunch. For a DISABLED or FAILED plugin it clears the
// corresponding registry entry under the lock (so the subsequent launch is
// the single live record for that name). For a RUNNING plugin it leaves
// the launched entry in place — stopRunningForRestart handles the ordered
// teardown. For an unknown name it returns an error listing the known
// plugins.
//
// Holds h.mu for the duration of the state read + clear.
func (h *Host) prepareRestart(name string) (candidate, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if lp, ok := h.launched[name]; ok {
		return candidate{name: lp.name, path: lp.path}, StatusRunning, nil
	}
	if dp, ok := h.disabled[name]; ok {
		path := dp.path
		delete(h.disabled, name)
		return candidate{name: name, path: path}, StatusDisabled, nil
	}
	if f, ok := h.initFailures[name]; ok {
		path := f.path
		delete(h.initFailures, name)
		return candidate{name: name, path: path}, StatusFailed, nil
	}

	return candidate{}, "", fmt.Errorf(
		"pluginhost: unknown plugin %q; known plugins: %s",
		name, h.knownPluginNamesLocked())
}

// knownPluginNamesLocked returns every plugin name the host is aware of
// (running, disabled, init-failed, or skipped), sorted and de-duplicated,
// for the unknown-name error. Caller MUST hold h.mu.
func (h *Host) knownPluginNamesLocked() string {
	seen := make(map[string]struct{})
	for n := range h.launched {
		seen[n] = struct{}{}
	}
	for n := range h.disabled {
		seen[n] = struct{}{}
	}
	for n := range h.initFailures {
		seen[n] = struct{}{}
	}
	for _, s := range h.skipped {
		seen[s.name] = struct{}{}
	}
	if len(seen) == 0 {
		return "(none)"
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// stopRunningForRestart performs the ordered graceful teardown of a
// currently-running plugin ahead of an operator relaunch. It mirrors the
// per-plugin half of Host.Stop, with one critical addition: it marks
// lp.stopping=true and waits for the plugin's supervisor goroutine to exit
// (lp.superviseDone) BEFORE returning, so the relaunch never races the old
// supervisor.
//
// Sequence:
//  1. Mark lp.stopping=true under mu, then wait on lp.superviseDone. The
//     supervisor, on its next exit observation, reads stopping=true and
//     walks away without relaunching. (markStoppingAndAwaitSupervisor.)
//  2. Cancel outstanding Subscribe streams.
//  3. Drive PluginService.Stop with the stopDrainTimeout drain budget.
//  4. SIGTERM → wait → SIGKILL via terminateSubprocess.
//  5. Reset the crash budget (operator restart, not a crash).
//  6. Remove the socket and the launched registry entry.
//
// Note on ordering vs. Host.Stop: Host.Stop kills the subprocess and THEN
// joins supervisorWG. Here we must join the supervisor FIRST (so it sees
// stopping and never relaunches), then kill — otherwise a supervisor that
// observed the exit before we set stopping could relaunch into a process
// we are about to discard. markStoppingAndAwaitSupervisor sets stopping
// before waiting, and the subprocess is still alive at that point, so the
// supervisor stays parked in waitForExitOrShutdown until WE kill it — at
// which point stopping is already true.
func (h *Host) stopRunningForRestart(parent context.Context, name string) {
	lp := h.lookupPluginByName(name)
	if lp == nil {
		return
	}

	// (1) Mark stopping + cancel subscriptions, then kill the subprocess so
	// the supervisor's waitForExitOrShutdown observes the exit and takes
	// the planned-shutdown branch (stopping=true). We mark stopping FIRST,
	// then terminate, then await superviseDone — guaranteeing the
	// supervisor reads stopping before it can act on the exit.
	h.markStopping(lp)
	h.cancelPluginSubscriptions(lp)

	// (2) Best-effort graceful Stop RPC with the drain budget, same as
	// Host.Stop's per-plugin path.
	ctx, cancel := context.WithTimeout(parent, stopDrainTimeout)
	done := make(chan error, 1)
	go func() {
		_, err := lp.pluginRPC.Stop(ctx, &protov1.StopRequest{DrainTimeoutMs: stopDrainTimeout.Milliseconds()})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			lp.logger.Warn("plugin "+name+": Stop returned error (restart)",
				slog.String("error", err.Error()))
		} else {
			lp.logger.Info("plugin " + name + ": stopped (restart)")
		}
	case <-ctx.Done():
		lp.logger.Warn("plugin "+name+": Stop drain timeout exceeded (restart) — abandoned",
			slog.Duration("timeout", stopDrainTimeout))
	}
	cancel()

	// (3) SIGTERM → wait → SIGKILL. This is the exit the supervisor is
	// parked waiting for.
	h.terminateSubprocess(lp)

	// (4) Now that the subprocess is dead, block until the supervisor
	// goroutine has fully exited. Because stopping is already true, the
	// supervisor takes the planned-shutdown branch and closes
	// superviseDone without relaunching.
	h.awaitSupervisor(lp)

	// (5) Reset the crash budget so the operator restart does not count
	// toward the disable threshold.
	if lp.budget != nil {
		lp.budget.reset()
	}

	// (6) Drop the socket + registry entry. The fresh launch below
	// re-inserts a new launchedPlugin under the same name.
	removeSocket(lp.socketPath)
	h.removeLaunched(name)
}

// markStopping sets lp.stopping=true under the host lock. Mirrors the
// per-plugin marking Host.Stop does so the supervisor reads a concurrent
// subprocess exit as planned.
func (h *Host) markStopping(lp *launchedPlugin) {
	h.mu.Lock()
	lp.stopping = true
	h.mu.Unlock()
}

// awaitSupervisor blocks until lp's supervisor goroutine has closed
// lp.superviseDone (i.e. fully exited). Nil-safe for the brief window
// before a supervisor is spawned.
func (h *Host) awaitSupervisor(lp *launchedPlugin) {
	h.mu.RLock()
	done := lp.superviseDone
	h.mu.RUnlock()
	if done == nil {
		return
	}
	<-done
}

// markStoppingAndAwaitSupervisor sets lp.stopping=true and then blocks on
// lp.superviseDone. It is the ordered handoff the race tests exercise
// directly: stopping must be visible to the supervisor before this returns,
// and it returns only after the supervisor has exited. Production callers
// reach the same two primitives through stopRunningForRestart (which
// interleaves the subprocess kill between them).
func (h *Host) markStoppingAndAwaitSupervisor(lp *launchedPlugin) {
	h.markStopping(lp)
	h.awaitSupervisor(lp)
}

// launchAndSuperviseForRestart performs a single fresh launch of c through
// launchPluginOnce, drives PluginService.Init, re-Starts the plugin if the
// host is already started, registers command ownership, spawns a fresh
// supervisor (which installs a brand-new crash budget), and bumps activity.
// It is the operator-restart analogue of initOne, minus the discovery walk
// — the candidate is reconstructed from the prior registry entry's path.
//
// On any failure the subprocess is killed and the socket cleaned up, the
// init-failure is recorded (so Status() shows the plugin as failed), and
// the error is returned.
func (h *Host) launchAndSuperviseForRestart(ctx context.Context, c candidate) error {
	if h.isShuttingDown() {
		return fmt.Errorf("host is shutting down")
	}

	logger := slog.Default().With(slog.String("plugin", c.name))
	logger.Info("plugin " + c.name + ": operator restart")

	lp, err := h.launchPluginOnce(ctx, c, logger)
	if err != nil {
		h.recordInitFailure(c, err)
		return fmt.Errorf("launch: %w", err)
	}

	allow := h.resolveAllowList(c.name)
	lp.allow = allow

	resp, err := lp.pluginRPC.Init(ctx, &protov1.InitRequest{
		PluginName:           c.name,
		Capabilities:         &protov1.Capabilities{},
		SupportedEventTopics: canonicalEventTopics(),
	})
	if err != nil {
		lp.client.Kill()
		removeSocket(lp.socketPath)
		h.recordInitFailure(c, err)
		return fmt.Errorf("Init RPC: %w", err)
	}

	// Intersect advertised caps with the allow-list, mirroring initOne.
	allowedEvents, _ := filterAllowedEvents(append([]string(nil), resp.AllowedEvents...), allow)
	allowedCmds, _ := filterAllowedCommands(append([]string(nil), resp.AllowedCommands...), allow)
	lp.capabilities = pluginCapabilities{
		subscribeEvents: allowedEvents,
		provideCommands: allowedCmds,
	}
	lp.sdkVersion = resp.SdkVersion

	specByName := make(map[string]*protov1.CommandSchema, len(resp.CommandSpecs))
	for _, cs := range resp.CommandSpecs {
		if cs == nil || cs.Name == "" {
			continue
		}
		specByName[cs.Name] = cs
	}

	// Register command ownership BEFORE spawning the supervisor (matches
	// initOne's ordering so a crash-restart race cannot leave commands
	// unowned). In the same critical section, clear any stale disabled /
	// init-failure entry for this name: prepareRestart already cleared
	// those for the disabled/failed paths, but a RUNNING plugin can be
	// concurrently permanent-disabled by its supervisor during the
	// teardown window (between prepareRestart releasing the lock and
	// stopRunningForRestart marking stopping). Dropping the stale entries
	// here guarantees Status() shows exactly one row for the freshly
	// launched plugin.
	h.mu.Lock()
	delete(h.disabled, c.name)
	delete(h.initFailures, c.name)
	for _, cmd := range allowedCmds {
		if cmd == "" {
			continue
		}
		if _, taken := h.allowed[cmd]; taken {
			logger.Warn("pluginhost: plugin command conflicts with core allow-list — ignoring",
				slog.String("command", cmd))
			continue
		}
		if existing, taken := h.pluginCmds[cmd]; taken {
			logger.Warn("pluginhost: plugin command name collision — keeping first registration",
				slog.String("command", cmd),
				slog.String("first_plugin", existing),
				slog.String("second_plugin", lp.name))
			continue
		}
		h.pluginCmds[cmd] = lp.name
		if spec, ok := specByName[cmd]; ok && len(spec.Args) > 0 {
			h.pluginCmdSpecs[cmd] = spec
		}
	}
	h.mu.Unlock()

	// If the host is already started, drive Start so the relaunched plugin
	// is fully live (mirrors the supervisor's relaunch + initOne→Start).
	h.mu.RLock()
	hostStarted := h.started
	h.mu.RUnlock()
	if hostStarted {
		if _, err := lp.pluginRPC.Start(ctx, &protov1.StartRequest{}); err != nil {
			lp.client.Kill()
			removeSocket(lp.socketPath)
			h.recordInitFailure(c, err)
			return fmt.Errorf("Start RPC: %w", err)
		}
		lp.logger.Info("plugin " + c.name + ": started (restart)")
	}

	// Hand to a fresh supervisor. startSupervisor installs a NEW crash
	// budget (newCrashBudget) and a fresh superviseDone — the operator
	// restart therefore begins with a clean budget window, satisfying the
	// "reset crash budget" requirement for the disabled/failed revival
	// paths (the running path also reset the old budget in
	// stopRunningForRestart, belt-and-suspenders).
	h.startSupervisor(ctx, c, lp)

	// A restart is observable activity.
	h.bumpActivity(c.name)
	return nil
}

// recordInitFailure stores an init-failure entry for c so Status() reports
// the plugin as "failed" after a failed restart attempt.
func (h *Host) recordInitFailure(c candidate, err error) {
	h.mu.Lock()
	h.initFailures[c.name] = initFailure{
		name:     c.name,
		path:     c.path,
		err:      err.Error(),
		failedAt: h.clock(),
	}
	h.mu.Unlock()
}
