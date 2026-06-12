package cli

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/zulandar/railyard/internal/car"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/dashboard"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"github.com/zulandar/railyard/internal/pluginhost"
	"github.com/zulandar/railyard/internal/yardmaster"
	"github.com/zulandar/railyard/pkg/plugin"
	"gorm.io/gorm"
)

// logBootSummary emits a single INFO log line describing which plugins
// survived Init and reached Start. The format mirrors spec §4 boot
// observability:
//
//	loaded plugins: trainmaster, audit-log
//
// When no plugins are running (either none registered or every Init
// failed) the line reads "loaded plugins: (none)". Plugin names are
// reported in registration order — the same order Init/Start walked.
//
// This is intentionally a single line, not one-per-plugin: per-plugin
// lifecycle logs (init / started / stopped) are emitted by
// internal/pluginhost. The boot summary is a complementary one-glance
// check that operators see at startup.
func logBootSummary(logger *slog.Logger, host *pluginhost.Host) {
	if logger == nil {
		logger = slog.Default()
	}
	names := host.Names()
	if len(names) == 0 {
		logger.Info("loaded plugins: (none)")
		return
	}
	logger.Info(
		"loaded plugins: "+strings.Join(names, ", "),
		slog.Any("plugins", names),
	)
}

// buildPluginHost constructs the plugin host. Under the subprocess plugin
// model (railyard-fll.3) the host discovers plugin binaries on disk and
// launches each enabled binary as a subprocess during [pluginhost.Host.Init].
// There is no in-process registry walk anymore; the returned host has NOT
// yet launched any plugins. Callers are expected to call host.Init,
// host.Start, and host.Stop around the surrounding subsystems.
//
// The OSS railyard binary ships no plugin binaries in its image, so when
// railyard.yaml's `plugins.enabled` list is empty (the OSS default) this
// helper produces a host that owns no subprocesses — Init/Start/Stop
// become effective no-ops.
func buildPluginHost(cfg *config.Config, db *gorm.DB, bus events.Bus) *pluginhost.Host {
	deps := pluginhost.Dependencies{
		Cfg:             cfg,
		DB:              db,
		Bus:             bus,
		RailyardVersion: resolveRailyardVersion(),
		BuildCommit:     Commit,
		BuildTime:       parseBuildDate(Date),

		PauseYardFn:     pauseYardAdapter(db, bus),
		ResumeYardFn:    resumeYardAdapter(db, bus),
		ReassignCarFn:   reassignCarAdapter(db),
		ScaleTrackFn:    scaleTrackAdapter(db, cfg),
		ForceCompleteFn: forceCompleteAdapter(db, bus),
	}

	return pluginhost.NewHost(deps)
}

// resolveRailyardVersion returns the ldflags-supplied Version if it isn't
// the default "dev" placeholder, otherwise it falls back to the module
// version from runtime/debug.ReadBuildInfo. Empty when neither is set.
func resolveRailyardVersion() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return Version
}

// parseBuildDate parses the ldflags-supplied Date variable. The default
// "unknown" placeholder yields a zero time.Time, as do unparseable values.
// Accepted formats: RFC3339 and "2006-01-02T15:04:05Z" (the format goreleaser
// emits). Best-effort; the field is documented as optional in the spec.
func parseBuildDate(s string) time.Time {
	if s == "" || s == "unknown" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// pauseYardAdapter binds the pluginhost "pause_yard" command to the
// existing dashboard.SetYardPaused write. The bus argument is captured so
// the dashboard's pause/resume event semantics (currently emitted only by
// the HTTP handler) can also fire when a plugin pauses the yard.
func pauseYardAdapter(db *gorm.DB, bus events.Bus) func(ctx context.Context, reason string) error {
	return func(ctx context.Context, reason string) error {
		if err := dashboard.SetYardPaused(db, true, reason); err != nil {
			return err
		}
		if bus != nil {
			bus.Publish(string(plugin.YardPaused), plugin.YardPausedEvent{Reason: reason})
		}
		return nil
	}
}

// resumeYardAdapter binds "resume_yard" to dashboard.SetYardPaused(false).
func resumeYardAdapter(db *gorm.DB, bus events.Bus) func(ctx context.Context, reason string) error {
	return func(ctx context.Context, reason string) error {
		if err := dashboard.SetYardPaused(db, false, ""); err != nil {
			return err
		}
		if bus != nil {
			bus.Publish(string(plugin.YardResumed), plugin.YardResumedEvent{Reason: reason})
		}
		return nil
	}
}

// reassignCarAdapter binds "reassign_car" to yardmaster.ReassignCar. The
// existing function signature already matches everything except the operator
// reason, which the allow-list does not expose; we pass a fixed
// "plugin-dispatched" reason so the progress note is still self-describing.
func reassignCarAdapter(db *gorm.DB) func(ctx context.Context, carID, fromEngine string) error {
	return func(ctx context.Context, carID, fromEngine string) error {
		return yardmaster.ReassignCar(db, carID, fromEngine, "plugin-dispatched")
	}
}

// scaleFunc is the orchestration.Scale signature, factored out so tests can
// stub the tmux-based scale path without owning a real tmux session.
type scaleFunc func(opts orchestration.ScaleOpts) (*orchestration.ScaleResult, error)

// scaleTrackAdapter binds "scale_track" to the right scaling path for the
// deployment mode.
//
//   - Local (tmux) mode — railyard.yaml has no `kubernetes:` section — drives
//     orchestration.Scale, which creates/kills per-engine tmux sessions. This
//     is the path `ry start` owns.
//   - Kubernetes mode — railyard.yaml carries a `kubernetes.namespace` — scales
//     the track's engine Deployment (`<release>-engine-<track>`) replicas via
//     orchestration.ScaleK8sReplicas. The tmux path is skipped here because a
//     pod has no tmux session, so orchestration.Scale would only ever return
//     "no railyard session running".
//
// The OSS build wires no in-cluster kube client, so the scaler passed here is
// nil; in that case ScaleK8sReplicas degrades to a logged no-op (see
// orchestration.ScaleK8sReplicas). A future in-cluster build can inject a
// real client-go-backed K8sScaler at buildPluginHost without touching this
// adapter.
func scaleTrackAdapter(db *gorm.DB, cfg *config.Config) func(ctx context.Context, track string, count int) error {
	return scaleTrackAdapterWithScaler(db, cfg, nil, orchestration.Scale)
}

// scaleTrackAdapterWithScaler is the injectable core of scaleTrackAdapter.
// scaler may be nil (OSS default → logged no-op in k8s mode); tmuxScale
// defaults to orchestration.Scale when nil.
func scaleTrackAdapterWithScaler(db *gorm.DB, cfg *config.Config, scaler orchestration.K8sScaler, tmuxScale scaleFunc) func(ctx context.Context, track string, count int) error {
	if tmuxScale == nil {
		tmuxScale = orchestration.Scale
	}
	return func(ctx context.Context, track string, count int) error {
		// Kubernetes mode: manage engine Deployment replicas; the tmux path
		// does not apply inside a pod.
		if cfg != nil && cfg.Kubernetes.Namespace != "" {
			return orchestration.ScaleK8sReplicas(ctx, orchestration.K8sScaleOpts{
				Config: cfg,
				Scaler: scaler,
				Track:  track,
				Count:  count,
			})
		}
		// Local mode: tmux-session scaling. K8s pod-replica management is a
		// no-op here (logged inside ScaleK8sReplicas for observability).
		_ = orchestration.ScaleK8sReplicas(ctx, orchestration.K8sScaleOpts{
			Config: cfg,
			Scaler: scaler,
			Track:  track,
			Count:  count,
		})
		_, err := tmuxScale(orchestration.ScaleOpts{
			DB:     db,
			Config: cfg,
			Track:  track,
			Count:  count,
		})
		return err
	}
}

// forceCompleteAdapter binds "force_complete" to a direct status
// transition (status=done) paired with a [models.CarProgress] audit
// row. The audit row's EngineID is the literal marker
// "<plugin-dispatched>" so operators can grep the progress log for
// plugin-initiated completions.
//
// Atomicity & ordering. The status flip and the progress-note insert
// run inside one db.Transaction so a failure to persist the audit row
// rolls back the status update — there must never be a
// force-completed car without a matching reason on file. The status
// transition log line and the [plugin.CarStatusChanged] publish both
// fire AFTER the outer transaction commits. This keeps subscribers
// and on-call operators from observing transitions that the rollback
// would have erased; the previous design routed the publish/log
// through [car.UpdateWithBus] which ran inside the inner tx and could
// emit a phantom event when the audit-row insert later failed.
//
// Reason required. An empty reason is rejected at the adapter (with
// a "reason required" error) because the allow-list's string-arg
// validator only checks the kind, not emptiness. A blank Note would
// silently violate the "never a force-complete without a reason on
// file" invariant the audit row exists to enforce.
func forceCompleteAdapter(db *gorm.DB, bus events.Bus) func(ctx context.Context, carID, reason string) error {
	return func(ctx context.Context, carID, reason string) error {
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("force_complete %s: reason required", carID)
		}
		var oldStatus string
		err := db.Transaction(func(tx *gorm.DB) error {
			var c models.Car
			if err := tx.Where("id = ?", carID).First(&c).Error; err != nil {
				return fmt.Errorf("load car %s: %w", carID, err)
			}
			oldStatus = c.Status
			if !car.IsValidTransition(c.Status, "done") {
				valid := car.ValidTransitions[c.Status]
				return fmt.Errorf("car: invalid status transition from %q to %q; valid transitions: %v", c.Status, "done", valid)
			}
			now := time.Now()
			updates := map[string]interface{}{
				"status":       "done",
				"completed_at": now,
			}
			if err := tx.Model(&models.Car{}).Where("id = ?", carID).Updates(updates).Error; err != nil {
				return fmt.Errorf("update car %s: %w", carID, err)
			}
			note := &models.CarProgress{
				CarID:        carID,
				EngineID:     "<plugin-dispatched>",
				Note:         reason,
				FilesChanged: "[]",
				CreatedAt:    now,
			}
			if err := tx.Create(note).Error; err != nil {
				return fmt.Errorf("write progress note: %w", err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("force_complete %s: %w", carID, err)
		}
		// Publish and log only after the commit lands so a rolled-back
		// transaction can never leak a phantom CarStatusChanged event
		// or a misleading "status transition" log line.
		slog.Info("car: status transition", "car", carID, "from", oldStatus, "to", "done", "via", "force_complete")
		if bus != nil {
			bus.Publish(string(plugin.CarStatusChanged), plugin.CarStatusChangedEvent{
				CarID:     carID,
				OldStatus: oldStatus,
				NewStatus: "done",
			})
		}
		return nil
	}
}
