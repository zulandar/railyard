package engine

import (
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/pkg/plugin"
	"gorm.io/gorm"
)

// publish is a nil-safe convenience: if bus is nil the call is a no-op.
// Callers ignore publish errors — the bus has no return value, and a slow
// subscriber must never block the engine's lifecycle paths.
func publish(bus events.Bus, topic string, payload any) {
	if bus == nil {
		return
	}
	bus.Publish(topic, payload)
}

// RegisterWithBus is the bus-aware variant of [Register]. It performs the
// same registration and, on success, publishes an [plugin.EngineStarted]
// event after the engines row is durably committed. Passing a nil bus is
// equivalent to calling [Register] directly — the existing Register
// function is kept untouched so non-bus callers (currently cmd/ry) require
// no changes in this wave.
//
// See spec §6.3 (railyard-3q8.4.2).
func RegisterWithBus(db *gorm.DB, opts RegisterOpts, bus events.Bus) (*models.Engine, error) {
	eng, err := Register(db, opts)
	if err != nil {
		return nil, err
	}
	publish(bus, string(plugin.EngineStarted), plugin.EngineStartedEvent{
		EngineID: eng.ID,
		Track:    eng.Track,
	})
	return eng, nil
}

// DeregisterWithBus is the bus-aware variant of [Deregister]. It performs
// the deregistration and, on success, publishes an [plugin.EngineStopped]
// event. Per the plugin design spec, the event fires exactly once per
// engine lifecycle — only on the clean shutdown path where Deregister
// succeeds. Failed deregisters do not emit the event.
func DeregisterWithBus(db *gorm.DB, engineID string, bus events.Bus) error {
	if err := Deregister(db, engineID); err != nil {
		return err
	}
	publish(bus, string(plugin.EngineStopped), plugin.EngineStoppedEvent{
		EngineID: engineID,
	})
	return nil
}

// HandleStallWithBus is the bus-aware variant of [HandleStall]. It runs
// the stall transition (engine -> stalled, car -> blocked, yardmaster
// message) and, on success, publishes [plugin.EngineStalled]. The
// LastActivityUnix payload field is read from the engine row after the
// transition commits so subscribers see consistent state.
//
// If the LastActivity read fails (rare; only on DB errors after the
// transaction already committed) the publish falls back to a zero
// timestamp so the event still fires — the EngineID alone is enough for
// most subscribers to react.
func HandleStallWithBus(db *gorm.DB, engineID, carID string, reason StallReason, repoDir, branch string, bus events.Bus) error {
	if err := HandleStall(db, engineID, carID, reason, repoDir, branch); err != nil {
		return err
	}

	var lastActivityUnix int64
	if bus != nil {
		var eng models.Engine
		if err := db.Select("last_activity").Where("id = ?", engineID).First(&eng).Error; err == nil {
			lastActivityUnix = eng.LastActivity.Unix()
		}
	}

	publish(bus, string(plugin.EngineStalled), plugin.EngineStalledEvent{
		EngineID:         engineID,
		LastActivityUnix: lastActivityUnix,
	})
	return nil
}
