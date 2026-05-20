package pluginhost

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/pkg/plugin"
)

// activeCarStatuses is the set of non-terminal car statuses included in
// Snapshot.Cars.Active. See spec §7.2 — terminal statuses (done, merged,
// cancelled) are intentionally excluded; transitions into them are
// signalled by the corresponding bus events.
var activeCarStatuses = []string{"open", "ready", "claimed", "in_progress", "blocked"}

// Snapshot assembles the current operational state in a single read
// transaction. Per spec §7.2 the snapshot performs no SQL aggregation
// beyond per-row scans — counters are tallied in Go.
func (h *Host) Snapshot(ctx context.Context) (*plugin.Snapshot, error) {
	if h.deps.DB == nil {
		return nil, errors.New("pluginhost: snapshot requires a non-nil DB")
	}

	snap := &plugin.Snapshot{
		Timestamp: time.Now().UTC(),
		Cars: plugin.CarsSnap{
			Counts: make(map[string]int),
		},
		Stats: plugin.SnapStats{
			EngineCountsByStatus: make(map[string]int),
		},
	}

	err := h.deps.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Engines — full list, mapped 1:1 to EngineSnap.
		var engines []models.Engine
		if err := tx.Find(&engines).Error; err != nil {
			return err
		}
		snap.Engines = make([]plugin.EngineSnap, 0, len(engines))
		enginesByTrack := make(map[string][]string)
		for _, e := range engines {
			snap.Engines = append(snap.Engines, plugin.EngineSnap{
				ID:           e.ID,
				Track:        e.Track,
				Status:       e.Status,
				CurrentCar:   e.CurrentCar,
				LastActivity: e.LastActivity,
			})
			snap.Stats.EngineCountsByStatus[e.Status]++
			enginesByTrack[e.Track] = append(enginesByTrack[e.Track], e.ID)
		}

		// Tracks — joined with the engine list above for ActiveEngines.
		var tracks []models.Track
		if err := tx.Find(&tracks).Error; err != nil {
			return err
		}
		snap.Tracks = make([]plugin.TrackSnap, 0, len(tracks))
		for _, t := range tracks {
			snap.Tracks = append(snap.Tracks, plugin.TrackSnap{
				Name:          t.Name,
				Language:      t.Language,
				Slots:         t.EngineSlots,
				ActiveEngines: enginesByTrack[t.Name],
			})
		}

		// Cars — fetch active set in full, then tally every car's status
		// (terminal included) via a lightweight per-row scan.
		var activeCars []models.Car
		if err := tx.Where("status IN ?", activeCarStatuses).Find(&activeCars).Error; err != nil {
			return err
		}
		snap.Cars.Active = make([]plugin.CarSummary, 0, len(activeCars))
		for _, c := range activeCars {
			snap.Cars.Active = append(snap.Cars.Active, plugin.CarSummary{
				ID:          c.ID,
				Title:       c.Title,
				Track:       c.Track,
				Status:      c.Status,
				Type:        c.Type,
				Priority:    c.Priority,
				Assignee:    c.Assignee,
				Branch:      c.Branch,
				RequestedBy: c.RequestedBy,
				CreatedAt:   c.CreatedAt,
				ClaimedAt:   c.ClaimedAt,
			})
		}

		// Tally counts across every car (terminal + non-terminal). We
		// scan only the status column so this stays cheap as state grows.
		var statusRows []struct {
			Status string
		}
		if err := tx.Model(&models.Car{}).Select("status").Find(&statusRows).Error; err != nil {
			return err
		}
		for _, row := range statusRows {
			snap.Cars.Counts[row.Status]++
		}

		// Yardmaster — no first-class model yet, so report the static
		// "running" status the spec calls out. A future bead can replace
		// this once a yardmaster status row exists.
		snap.Yardmaster = plugin.YardmasterSnap{
			Status: "running",
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}
