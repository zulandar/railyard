// Optional plugin health probing for the gRPC plugin host
// (railyard-77h.12).
//
// The host supervises the plugin PROCESS (crash budget, relaunch — see
// supervise.go / crashbudget.go) but that only tells operators whether
// the subprocess is ALIVE, not whether it is FUNCTIONAL. A connector with
// dead remote credentials looks healthy to the supervisor forever.
//
// This file adds a best-effort functional probe: a single poller
// goroutine polls every running plugin's optional PluginService.Health
// RPC on a configurable interval (cfg.plugins.health_interval_sec,
// default 30s). The result is stored on each launchedPlugin under
// [Host.mu] and surfaced by [Host.Status] in the HEALTH column.
//
// Backward compatibility: a plugin built before this RPC — or one that
// simply does not implement pkg/plugin.HealthReporter — returns
// codes.Unimplemented from its adapter. The host records that as "n/a"
// (healthValueNA), NOT an error. Any other RPC error or a timeout (the
// host uses a 2s deadline per probe) maps to "degraded" with the error
// text so operators can see what went wrong.
//
// Lifecycle: the poller goroutine is started from [Host.Start], joins
// through [Host.supervisorWG], and exits when [Host.shutdownCh] closes —
// the same join/stop discipline the per-plugin supervisors use, so
// [Host.Stop] cleanly reaps it with no goroutine leak.
package pluginhost

import (
	"context"
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// healthProbeTimeout is the per-plugin deadline for a single Health RPC.
// A probe that does not return within this window is treated as a
// degraded result with a timeout message — the host never blocks the
// poll loop on a wedged plugin.
const healthProbeTimeout = 2 * time.Second

// Health verdict strings stored on launchedPlugin.healthValue and
// surfaced verbatim in the Status() snapshot / `ry plugins status` table.
// Strings (not iota) because they cross the JSON boundary.
const (
	healthValueOK       = "ok"
	healthValueDegraded = "degraded"
	healthValueFailing  = "failing"
	// healthValueNA marks a plugin that does not implement HealthReporter
	// (its Health RPC returned codes.Unimplemented). This is the
	// backward-compatible path — NOT an error condition.
	healthValueNA = "n/a"
)

// healthPollLoop is the single long-lived goroutine that polls every
// running plugin's Health RPC on `interval`. It performs an immediate
// first poll on entry, then ticks until h.shutdownCh closes.
//
// The caller MUST h.supervisorWG.Add(1) before launching this goroutine
// (mirroring startSupervisor) so [Host.Stop]'s supervisorWG.Wait() joins
// it. A non-positive interval falls back to the config default so the
// loop never busy-spins on a misconfigured value.
func (h *Host) healthPollLoop(ctx context.Context, interval time.Duration) {
	defer h.supervisorWG.Done()

	if interval <= 0 {
		interval = time.Duration(defaultHealthIntervalSec()) * time.Second
	}

	// Immediate first poll so operators see health shortly after boot
	// rather than after one full interval. Honor shutdown racing the
	// very first tick.
	if h.isShuttingDown() {
		return
	}
	h.pollHealthOnce(ctx)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-h.shutdownCh:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			h.pollHealthOnce(ctx)
		}
	}
}

// pollHealthOnce probes every currently-running plugin's Health RPC once
// and records the result on each launchedPlugin under h.mu.
//
// It snapshots the launched set under the lock, releases the lock to make
// the (potentially blocking) RPCs, then re-acquires the lock per result
// to store it — so a slow plugin never serializes Status() readers or the
// supervisor hot path against the lock.
func (h *Host) pollHealthOnce(ctx context.Context) {
	type target struct {
		name string
		rpc  protov1.PluginServiceClient
	}
	h.mu.RLock()
	targets := make([]target, 0, len(h.launched))
	for name, lp := range h.launched {
		if lp.pluginRPC == nil {
			continue
		}
		targets = append(targets, target{name: name, rpc: lp.pluginRPC})
	}
	h.mu.RUnlock()

	// Deterministic order keeps test assertions and logs stable; the set
	// is small so the sort cost is negligible.
	sort.Slice(targets, func(i, j int) bool { return targets[i].name < targets[j].name })

	for _, tg := range targets {
		value, message := h.probeOne(ctx, tg.rpc)
		now := h.clock()
		h.mu.Lock()
		if lp, ok := h.launched[tg.name]; ok {
			lp.healthValue = value
			lp.healthMessage = message
			lp.healthCheckedAt = now
		}
		h.mu.Unlock()
	}
}

// probeOne makes a single Health RPC against rpc with a bounded deadline
// and maps the outcome to a (value, message) pair:
//
//   - success      -> mapped state value + the plugin's own message
//   - Unimplemented -> healthValueNA, empty message (NOT an error)
//   - any other err -> healthValueDegraded, the error text as the message
//
// A nil response on a nil error is defensively treated as degraded.
func (h *Host) probeOne(parent context.Context, rpc protov1.PluginServiceClient) (value, message string) {
	ctx, cancel := context.WithTimeout(parent, healthProbeTimeout)
	defer cancel()

	resp, err := rpc.Health(ctx, &protov1.HealthRequest{})
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			return healthValueNA, ""
		}
		return healthValueDegraded, err.Error()
	}
	if resp == nil {
		return healthValueDegraded, "plugin returned an empty health response"
	}
	return protoHealthToValue(resp.GetState()), resp.GetMessage()
}

// protoHealthToValue maps the wire enum onto the stored verdict string.
// An unspecified/unknown state is recorded as degraded — a HealthReporter
// that returns the zero value is misbehaving, and degraded is the safe
// operator-visible verdict.
func protoHealthToValue(s protov1.HealthState) string {
	switch s {
	case protov1.HealthState_HEALTH_STATE_OK:
		return healthValueOK
	case protov1.HealthState_HEALTH_STATE_DEGRADED:
		return healthValueDegraded
	case protov1.HealthState_HEALTH_STATE_FAILING:
		return healthValueFailing
	default:
		return healthValueDegraded
	}
}

// defaultHealthIntervalSec is the host-side mirror of the config default
// (config.defaultHealthIntervalSec, intentionally not exported there).
// Used only as the loop's last-resort fallback; the configured value is
// resolved via cfg.Plugins.HealthInterval() before the loop is started.
func defaultHealthIntervalSec() int { return 30 }
