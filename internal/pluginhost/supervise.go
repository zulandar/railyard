// Subprocess supervision for the gRPC plugin host (railyard-fll.6).
//
// The supervisor is the long-lived guardian of one launched plugin. It
// owns the per-plugin restart loop: when the subprocess exits without a
// planned shutdown, it consults the sliding-window [crashBudget],
// applies [backoffSchedule], and relaunches via the same one-shot
// [Host.launchPluginOnce] path used at boot. On budget exhaustion it
// flips the plugin to `disabled`, emits a single ERROR with the crash
// history, tears down the socket, and removes the plugin from the
// active registry.
//
// Stop ordering is critical: the host's shutdownCh, closed by
// [Host.Stop], gates the restart loop AND short-circuits the backoff
// sleep. Stop also marks each launchedPlugin.stopping=true before
// signalling shutdown — once `stopping` is true, the supervisor
// interprets the subprocess exit as planned and walks away without
// touching the budget. The combined `stopping` + `shutdownCh` design
// means a race between Stop and a crash-restart cannot leave the host
// with a half-relaunched plugin to clean up.
package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// supervisorExitPoll is the interval the supervisor uses to poll
// goplugin.Client.Exited(). go-plugin does not expose a done channel,
// so we poll; 50ms is responsive enough that the spec's "<1s relaunch"
// budget is comfortably met (250ms backoff + ≤50ms detect ≈ 300ms total).
const supervisorExitPoll = 50 * time.Millisecond

// startSupervisor spawns the long-lived supervisor goroutine for `c`
// and the freshly-launched `lp`. It records lp in the host registry
// FIRST so subsequent host operations (Names, lookupPluginByName,
// Subscribe) see it; on permanent-disable the supervisor removes it
// back out.
//
// startSupervisor expects:
//   - lp's initial launch + Init have already succeeded (the caller is
//     [Host.initOne]). The supervisor only kicks in if and when the
//     subprocess later exits unexpectedly.
//   - lp is NOT yet in h.launched. The supervisor adds it under the
//     host lock before spawning the goroutine — keeps initOne simple
//     and ensures the registry-insertion and the goroutine-spawn are
//     adjacent.
//
// The supervisor goroutine joins through h.supervisorWG; [Host.Stop]
// blocks on the wait group after closing h.shutdownCh, so a relaunch
// attempt cannot race shutdown.
func (h *Host) startSupervisor(ctx context.Context, c candidate, lp *launchedPlugin) {
	h.mu.Lock()
	lp.budget = newCrashBudget(h.clock)
	lp.superviseDone = make(chan struct{})
	h.launched[lp.name] = lp
	h.mu.Unlock()

	h.supervisorWG.Add(1)
	go h.supervise(ctx, c, lp)
}

// supervise is the per-plugin restart loop. It blocks on subprocess
// exit, distinguishes planned vs unplanned terminations, and either
// relaunches (with backoff) or marks the plugin permanently disabled.
//
// The function returns — and closes lp.superviseDone — on any of:
//   - host shutdown (h.shutdownCh closed),
//   - plugin permanent-disable (crash budget exceeded),
//   - context cancellation propagating to the relaunch path.
//
// Note: this function is NOT responsible for the *initial* launch.
// initOne does that and only invokes startSupervisor on success.
func (h *Host) supervise(ctx context.Context, c candidate, lp *launchedPlugin) {
	defer h.supervisorWG.Done()
	defer close(lp.superviseDone)

	for {
		// Block until the current subprocess exits OR host shutdown
		// fires. We rely on go-plugin's Exited() polled at
		// supervisorExitPoll. If shutdown wins, we walk away — Stop is
		// responsible for the actual subprocess kill.
		if !h.waitForExitOrShutdown(lp) {
			// shutdown closed; no relaunch.
			return
		}

		// Planned shutdown path: stopping was set by Stop before it
		// invoked client.Kill. Walk away without touching the budget.
		if h.isPluginStopping(lp) {
			return
		}

		// Unplanned exit: record the crash. The reason string is best
		// effort — go-plugin does not surface the subprocess exit
		// code, so we can only attest "exited unexpectedly". Lane D's
		// SDK convention is that a panic-recovery returns exit code 1
		// via os.Exit(1); we record that as the canonical reason.
		// Writes to lp.lastExitReason MUST take h.mu — Status() reads
		// it under the lock to surface the Error column for disabled
		// rows.
		h.mu.Lock()
		lp.lastExitReason = "subprocess exited unexpectedly"
		h.mu.Unlock()
		count, exceeded := lp.budget.recordCrash()
		lp.consecutiveCrashes++

		if exceeded {
			h.handlePermanentDisable(lp, count)
			return
		}

		lp.logger.Warn(
			fmt.Sprintf("plugin %s: subprocess exited unexpectedly — restarting (crash %d/%d in window)",
				c.name, count, crashBudgetThreshold-1),
			slog.Int("crashes_in_window", count),
			slog.Int("consecutive_crashes", lp.consecutiveCrashes),
		)

		// Tear down the dead subprocess's socket + go-plugin client
		// state before relaunching.
		lp.client.Kill()
		removeSocket(lp.socketPath)

		// Sleep with shutdown short-circuit. The sleep index is the
		// consecutive-crash count MINUS ONE (we have just incremented
		// it) — so the first crash sleeps 250ms, the second 500ms, etc.
		delay := backoffSchedule(lp.consecutiveCrashes - 1)
		if !h.backoffSleep(delay, h.shutdownCh) {
			// shutdown short-circuited the sleep.
			return
		}

		// One more shutdown poll BEFORE handing back to the launch
		// path — the relaunch is uninterruptible once it starts (it
		// owns a fresh go-plugin handshake), so if a Stop fired
		// during the backoff we must abort here and avoid leaking a
		// freshly-spawned subprocess that the host will never tear
		// down.
		if h.isShuttingDown() {
			return
		}

		// Relaunch. The new attempt replaces lp in-place — go-plugin
		// state, pid, socket path all change. On relaunch failure we
		// count the failure as a crash (it leaves the plugin
		// down just as a runtime exit would) and continue the loop.
		if err := h.relaunch(ctx, c, lp); err != nil {
			// A failed sha256 pin (railyard-77h.15) on relaunch is the
			// CRITICAL re-verify case: the on-disk binary changed since
			// first boot. This is NOT a transient crash — looping would
			// just keep refusing the same swapped binary. Permanently
			// disable immediately with the distinct integrity-mismatch
			// reason (verifyBinaryPin already WARN-logged both hashes).
			var integ *integrityMismatchError
			if errors.As(err, &integ) {
				h.handleIntegrityDisable(lp)
				return
			}
			lp.logger.Warn(
				fmt.Sprintf("plugin %s: relaunch attempt failed: %v", c.name, err),
				slog.String("error", err.Error()),
			)
			h.mu.Lock()
			lp.lastExitReason = "relaunch failed: " + err.Error()
			h.mu.Unlock()
			// Loop iterates again — recordCrash and possibly
			// permanent-disable handled at the top.
			continue
		}

		// Successful relaunch: reset the consecutive-crash counter so
		// the next crash backs off from 250ms again. We do NOT reset
		// the sliding window (per the brief).
		lp.consecutiveCrashes = 0

		// Bump restartCount and lastActivity together in a single
		// critical section. We do NOT call h.bumpActivity here because
		// that helper only updates lastActivity — restartCount needs to
		// be incremented in the same lock acquisition to stay
		// consistent with any concurrent Status() reader.
		h.mu.Lock()
		if relaunchLP, ok := h.launched[c.name]; ok {
			relaunchLP.restartCount++
			relaunchLP.lastActivity = h.clock()
		}
		h.mu.Unlock()

		// If shutdown fired DURING relaunch, tear the brand-new
		// subprocess down rather than leaving the loop and trusting
		// the next iteration's shutdown check (which would leak the
		// process).
		if h.isShuttingDown() {
			lp.client.Kill()
			removeSocket(lp.socketPath)
			return
		}
	}
}

// waitForExitOrShutdown polls until lp.client.Exited() returns true OR
// h.shutdownCh closes. Returns true on subprocess exit, false on
// shutdown.
//
// Polling is acceptable here: the alternative is a goroutine that
// blocks on cmd.Wait, but go-plugin owns the *exec.Cmd and does its
// own Wait internally — we'd need a shim that synchronizes with the
// library's wait, which complicates the test surface. A 50ms poll is
// well under the spec's "<1s relaunch" SLA.
func (h *Host) waitForExitOrShutdown(lp *launchedPlugin) bool {
	t := time.NewTicker(supervisorExitPoll)
	defer t.Stop()
	for {
		select {
		case <-h.shutdownCh:
			return false
		case <-t.C:
			if lp.client.Exited() {
				return true
			}
		}
	}
}

// isPluginStopping reports whether [Host.Stop] (or the planned
// per-plugin Stop path) has flagged lp as stopping. Read under the
// host lock — keeps the supervisor's view consistent with Stop's
// write.
func (h *Host) isPluginStopping(lp *launchedPlugin) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return lp.stopping
}

// isShuttingDown reports whether [Host.Stop] has closed the shutdown
// channel. Non-blocking — used by the supervisor as a fast-path check
// at race-prone boundaries (around relaunch) where we want to abort
// rather than start new subprocess work.
func (h *Host) isShuttingDown() bool {
	select {
	case <-h.shutdownCh:
		return true
	default:
		return false
	}
}

// handlePermanentDisable performs the budget-exceeded teardown: emits
// the structured ERROR log, kills the lingering subprocess (in case it
// is still draining), removes the socket file, snapshots the plugin's
// final state into h.disabled, and drops it from the active registry.
// [Host.Status] surfaces the snapshot as the "disabled" row.
func (h *Host) handlePermanentDisable(lp *launchedPlugin, finalCount int) {
	firstAt := lp.budget.firstCrash()

	lp.logger.Error(
		fmt.Sprintf(
			"plugin permanently disabled after crash budget exceeded; restart railyard to retry. plugin=%s crashes_in_window=%d first_crash_at=%s last_exit_reason=%q",
			lp.name, finalCount, firstAt.Format(time.RFC3339Nano), lp.lastExitReason,
		),
		slog.String("plugin", lp.name),
		slog.Int("crashes_in_window", finalCount),
		slog.String("first_crash_at", firstAt.Format(time.RFC3339Nano)),
		slog.String("last_exit_reason", lp.lastExitReason),
	)

	// Cancel any outstanding Subscribe streams — they would otherwise
	// hang trying to talk to a process that's about to disappear.
	h.cancelPluginSubscriptions(lp)

	// Best-effort cleanup. go-plugin's Kill() on an already-exited
	// process is a no-op; that's fine.
	lp.client.Kill()
	removeSocket(lp.socketPath)

	h.markPermanentlyDisabled(lp)
}

// handleIntegrityDisable is the relaunch-path teardown when a supervisor
// relaunch is refused because the on-disk binary's sha256 no longer matches
// the configured pin (railyard-77h.15). Unlike handlePermanentDisable it is
// NOT a crash-budget exhaustion — the binary changed under us — so it logs
// the integrity framing (verifyBinaryPin already WARN-logged both hashes)
// and records the disabled snapshot with the integrity-mismatch reason. The
// dead subprocess is already gone (the crash that triggered this relaunch),
// but we kill + clean its socket defensively, exactly as the crash-budget
// path does.
func (h *Host) handleIntegrityDisable(lp *launchedPlugin) {
	h.mu.Lock()
	lp.lastExitReason = integrityMismatchReason
	h.mu.Unlock()

	lp.logger.Error(
		fmt.Sprintf(
			"plugin permanently disabled: binary sha256 no longer matches the configured pin on relaunch; restart railyard after restoring the pinned binary. plugin=%s reason=%s",
			lp.name, integrityMismatchReason,
		),
		slog.String("plugin", lp.name),
		slog.String("reason", integrityMismatchReason),
	)

	h.cancelPluginSubscriptions(lp)
	lp.client.Kill()
	removeSocket(lp.socketPath)
	h.markPermanentlyDisabled(lp)
}

// markPermanentlyDisabled snapshots lp into h.disabled, removes it from
// h.launched, and clears any command ownership the plugin held — all in
// a single critical section so Status() readers observe exactly one of
// the running/disabled rows. Factored out of handlePermanentDisable for
// direct unit testability (the rest of handlePermanentDisable touches
// go-plugin's client.Kill and the socket file, which need a real
// subprocess to exercise).
func (h *Host) markPermanentlyDisabled(lp *launchedPlugin) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.disabled[lp.name] = &disabledPlugin{
		name:           lp.name,
		path:           lp.path,
		pid:            lp.pid,
		restartCount:   lp.restartCount,
		lastActivity:   lp.lastActivity,
		lastExitReason: lp.lastExitReason,
		commandCount:   len(lp.capabilities.provideCommands),
	}
	delete(h.launched, lp.name)
	for cmd, owner := range h.pluginCmds {
		if owner == lp.name {
			delete(h.pluginCmds, cmd)
		}
	}
}

// relaunch is the supervisor's path for re-establishing the
// subprocess after a crash. It walks the same launch → Init → Start
// sequence the initial boot follows; the resulting plugin replaces lp's
// fields in-place (so any external pointers held by Subscribe / lookups
// remain valid).
//
// Start is only invoked if the host has already passed its top-level
// Start barrier — relaunching during Init phase should not pre-start
// the plugin ahead of its siblings.
func (h *Host) relaunch(ctx context.Context, c candidate, lp *launchedPlugin) error {
	fresh, err := h.launchPluginOnce(ctx, c, lp.logger)
	if err != nil {
		return fmt.Errorf("launch: %w", err)
	}

	// Re-run Init. Capabilities are re-advertised; we trust the
	// allow-list intersection already cached on lp.allow.
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := fresh.pluginRPC.Init(initCtx, &protov1.InitRequest{
		PluginName:   c.name,
		Capabilities: &protov1.Capabilities{},
	}); err != nil {
		fresh.client.Kill()
		removeSocket(fresh.socketPath)
		return fmt.Errorf("Init RPC after relaunch: %w", err)
	}

	// If the host has already been Started, re-Start the plugin.
	// Otherwise leave it Init-only — host.Start will pick it up in
	// the normal order.
	h.mu.RLock()
	hostStarted := h.started
	h.mu.RUnlock()
	if hostStarted {
		startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := fresh.pluginRPC.Start(startCtx, &protov1.StartRequest{})
		startCancel()
		if err != nil {
			fresh.client.Kill()
			removeSocket(fresh.socketPath)
			return fmt.Errorf("Start RPC after relaunch: %w", err)
		}
	}

	// Swap the new go-plugin handles into the supervisor's lp, under
	// the host lock so concurrent lookups see a consistent view. We
	// preserve lp.budget, lp.superviseDone, lp.allow, lp.capabilities,
	// lp.stopping, and lp.subMu/subCancels — those are supervisor state
	// that must outlive the dead subprocess.
	h.mu.Lock()
	lp.client = fresh.client
	lp.pluginRPC = fresh.pluginRPC
	lp.pid = fresh.pid
	lp.socketPath = fresh.socketPath
	h.mu.Unlock()

	lp.logger.Info(
		fmt.Sprintf("plugin %s: relaunched (pid=%d)", c.name, fresh.pid),
		slog.Int("pid", fresh.pid),
	)
	return nil
}
