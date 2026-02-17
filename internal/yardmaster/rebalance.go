package yardmaster

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"gorm.io/gorm"
)

const (
	rebalanceCooldown = 5 * time.Minute
	trackCooldown     = 5 * time.Minute
	idleThreshold     = 2 * time.Minute
)

// rebalanceState tracks cooldowns across daemon cycles.
type rebalanceState struct {
	lastRebalanceAt time.Time
	lastTrackMoveAt map[string]time.Time
	tmux            orchestration.Tmux // nil → DefaultTmux
}

// trackMetrics holds per-track engine and work counts.
type trackMetrics struct {
	name       string
	liveCount  int
	idleCount  int
	readyWork  int
	maxSlots   int
	idleEngine *models.Engine // first eligible idle engine (for donor kill)
}

// rebalanceEngines checks for idle engines on tracks with no work and moves
// them to tracks with a backlog. At most 1 engine is moved per deficit track
// per cycle.
func rebalanceEngines(db *gorm.DB, cfg *config.Config, configPath string, state *rebalanceState, out io.Writer) error {
	now := time.Now()

	// Cooldown guard.
	if now.Sub(state.lastRebalanceAt) < rebalanceCooldown {
		return nil
	}

	// Gather metrics per track.
	metrics := make(map[string]*trackMetrics, len(cfg.Tracks))
	for _, t := range cfg.Tracks {
		m := &trackMetrics{name: t.Name, maxSlots: t.EngineSlots}

		// Live engines (non-dead, non-yardmaster).
		var liveEngines []models.Engine
		db.Where("track = ? AND status != ? AND id != ?", t.Name, engine.StatusDead, YardmasterID).
			Find(&liveEngines)
		m.liveCount = len(liveEngines)

		// Find idle engines eligible for rebalance (idle 2+ min, no current car).
		for i := range liveEngines {
			e := &liveEngines[i]
			if e.Status == engine.StatusIdle && e.CurrentCar == "" &&
				now.Sub(e.LastActivity) >= idleThreshold {
				m.idleCount++
				if m.idleEngine == nil {
					m.idleEngine = e
				}
			}
		}

		// Count ready work.
		ready, err := countReadyWork(db, t.Name)
		if err != nil {
			return fmt.Errorf("count ready work for track %s: %w", t.Name, err)
		}
		m.readyWork = ready

		metrics[t.Name] = m
	}

	// Classify tracks.
	type deficit struct {
		name    string
		backlog int // readyWork - liveCount
	}
	var surplusTracks []string
	var deficitTracks []deficit

	for name, m := range metrics {
		if m.idleCount > 0 && m.readyWork == 0 {
			surplusTracks = append(surplusTracks, name)
		}
		if m.readyWork > m.liveCount && m.liveCount < m.maxSlots {
			deficitTracks = append(deficitTracks, deficit{name: name, backlog: m.readyWork - m.liveCount})
		}
	}

	if len(surplusTracks) == 0 || len(deficitTracks) == 0 {
		return nil
	}

	// Sort deficit tracks by backlog descending (highest need first).
	sort.Slice(deficitTracks, func(i, j int) bool {
		return deficitTracks[i].backlog > deficitTracks[j].backlog
	})

	// Move 1 engine per deficit track per cycle.
	for _, dt := range deficitTracks {
		// Check per-track cooldown on receiver.
		if last, ok := state.lastTrackMoveAt[dt.name]; ok && now.Sub(last) < trackCooldown {
			continue
		}

		// Find a surplus donor not on per-track cooldown.
		var donor string
		for _, s := range surplusTracks {
			if last, ok := state.lastTrackMoveAt[s]; ok && now.Sub(last) < trackCooldown {
				continue
			}
			dm := metrics[s]
			// Keep at least 1 engine on donor if it has ready work.
			if dm.readyWork > 0 && dm.liveCount-1 < 1 {
				continue
			}
			donor = s
			break
		}
		if donor == "" {
			continue
		}

		err := rebalanceMove(db, cfg, configPath, donor, dt.name, state, out)
		if err != nil {
			fmt.Fprintf(out, "Rebalance %s → %s failed: %v\n", donor, dt.name, err)
			continue
		}

		fmt.Fprintf(out, "Rebalanced 1 engine: %s → %s\n", donor, dt.name)

		// Update cooldowns.
		state.lastTrackMoveAt[donor] = now
		state.lastTrackMoveAt[dt.name] = now

		// Remove donor from surplus if it has no more idle engines.
		dm := metrics[donor]
		dm.idleCount--
		dm.liveCount--
		if dm.idleCount <= 0 {
			// Remove from surplusTracks.
			for i, s := range surplusTracks {
				if s == donor {
					surplusTracks = append(surplusTracks[:i], surplusTracks[i+1:]...)
					break
				}
			}
		}

		if len(surplusTracks) == 0 {
			break
		}
	}

	state.lastRebalanceAt = now
	return nil
}

// rebalanceMove kills one idle engine on the donor track and scales up the
// receiver track by 1.
func rebalanceMove(db *gorm.DB, cfg *config.Config, configPath, donorTrack, receiverTrack string, state *rebalanceState, out io.Writer) error {
	dm := findIdleEngine(db, donorTrack)
	if dm == nil {
		return fmt.Errorf("no idle engine on donor track %s", donorTrack)
	}

	// Kill the idle engine on the donor track.
	if err := db.Model(&models.Engine{}).Where("id = ?", dm.ID).
		Update("status", engine.StatusDead).Error; err != nil {
		return fmt.Errorf("mark engine %s dead: %w", dm.ID, err)
	}

	// Count current live engines on receiver to determine target count.
	var currentCount int64
	db.Model(&models.Engine{}).
		Where("track = ? AND status != ? AND id != ?", receiverTrack, engine.StatusDead, YardmasterID).
		Count(&currentCount)

	tmux := state.tmux
	if tmux == nil {
		tmux = orchestration.DefaultTmux
	}

	_, err := orchestration.Scale(orchestration.ScaleOpts{
		DB:         db,
		Config:     cfg,
		ConfigPath: configPath,
		Track:      receiverTrack,
		Count:      int(currentCount) + 1,
		Tmux:       tmux,
	})
	if err != nil {
		return fmt.Errorf("scale up %s: %w", receiverTrack, err)
	}

	return nil
}

// findIdleEngine returns the first engine on a track that is idle with no
// current car and has been idle for at least idleThreshold.
func findIdleEngine(db *gorm.DB, track string) *models.Engine {
	var eng models.Engine
	cutoff := time.Now().Add(-idleThreshold)
	result := db.Where("track = ? AND status = ? AND current_car = ? AND last_activity <= ? AND id != ?",
		track, engine.StatusIdle, "", cutoff, YardmasterID).
		Order("last_activity ASC").
		Limit(1).
		Find(&eng)
	if result.Error != nil || result.RowsAffected == 0 {
		return nil
	}
	return &eng
}

// countReadyWork counts open, unblocked, unassigned, non-epic cars on a track.
// This mirrors the query logic from engine.ClaimCar.
func countReadyWork(db *gorm.DB, track string) (int, error) {
	// Subquery: car IDs that have at least one non-done, non-cancelled blocker.
	blockedSub := db.Table("car_deps").
		Select("car_deps.car_id").
		Joins("JOIN cars blocker ON car_deps.blocked_by = blocker.id").
		Where("blocker.status NOT IN ?", []string{"done", "cancelled"})

	var count int64
	err := db.Model(&models.Car{}).
		Where("status = ? AND (assignee = ? OR assignee IS NULL) AND track = ? AND type != ?",
			"open", "", track, "epic").
		Where("id NOT IN (?)", blockedSub).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	return int(count), nil
}
