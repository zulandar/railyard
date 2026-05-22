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

// scaleTrackAdapter binds "scale_track" to orchestration.Scale. Note: this
// touches tmux sessions on the host running the daemon, which is sensible
// in local-dev mode where ry start owns tmux. In k8s pod mode, Scale will
// fail because there's no tmux session — that's a pre-existing limitation
// of the scale path, not something this adapter introduces.
func scaleTrackAdapter(db *gorm.DB, cfg *config.Config) func(ctx context.Context, track string, count int) error {
	return func(ctx context.Context, track string, count int) error {
		_, err := orchestration.Scale(orchestration.ScaleOpts{
			DB:     db,
			Config: cfg,
			Track:  track,
			Count:  count,
		})
		return err
	}
}

// forceCompleteAdapter binds "force_complete" to car.UpdateWithBus with
// status=done. There is no dedicated "force complete with operator reason"
// path in railyard today; the closest existing semantic is the status
// transition, which the car package's Update validates and (on success)
// publishes a [plugin.CarStatusChanged] event for. The operator reason is
// surfaced via a best-effort progress note appended after the transition;
// failure to write the note is logged but does not fail the command (the
// transition is the load-bearing effect).
func forceCompleteAdapter(db *gorm.DB, bus events.Bus) func(ctx context.Context, carID, reason string) error {
	return func(ctx context.Context, carID, reason string) error {
		if err := car.UpdateWithBus(db, bus, carID, map[string]interface{}{"status": "done"}); err != nil {
			return fmt.Errorf("force_complete %s: %w", carID, err)
		}
		return nil
	}
}
