package orchestration

import (
	"fmt"
	"sort"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// AssignTracks distributes N engines across tracks proportional to engine_slots.
// Each active track gets at least 1 engine if totalEngines >= number of tracks.
// Returns map[trackName]engineCount.
func AssignTracks(cfg *config.Config, totalEngines int) map[string]int {
	if cfg == nil || len(cfg.Tracks) == 0 || totalEngines <= 0 {
		return map[string]int{}
	}

	result := make(map[string]int)
	tracks := cfg.Tracks

	// If fewer engines than tracks, assign to tracks with most slots.
	if totalEngines < len(tracks) {
		// Sort by engine_slots descending.
		sorted := make([]config.TrackConfig, len(tracks))
		copy(sorted, tracks)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].EngineSlots > sorted[j].EngineSlots
		})
		for i := 0; i < totalEngines; i++ {
			result[sorted[i].Name] = 1
		}
		return result
	}

	// Proportional distribution with floor of 1 per track.
	totalSlots := 0
	for _, t := range tracks {
		totalSlots += t.EngineSlots
	}
	if totalSlots == 0 {
		totalSlots = len(tracks)
	}

	// First pass: assign floor of proportional share (min 1).
	assigned := 0
	for _, t := range tracks {
		share := (t.EngineSlots * totalEngines) / totalSlots
		if share < 1 {
			share = 1
		}
		result[t.Name] = share
		assigned += share
	}

	// Distribute remaining engines to tracks with largest fractional remainder.
	remaining := totalEngines - assigned
	if remaining > 0 {
		type remainder struct {
			name string
			frac float64
		}
		var rems []remainder
		for _, t := range tracks {
			exact := float64(t.EngineSlots) * float64(totalEngines) / float64(totalSlots)
			frac := exact - float64(result[t.Name])
			rems = append(rems, remainder{name: t.Name, frac: frac})
		}
		sort.Slice(rems, func(i, j int) bool {
			return rems[i].frac > rems[j].frac
		})
		for i := 0; i < remaining && i < len(rems); i++ {
			result[rems[i].name]++
		}
	}

	// Handle over-assignment (can happen with floor of 1 per track).
	totalAssigned := 0
	for _, c := range result {
		totalAssigned += c
	}
	if totalAssigned > totalEngines {
		// Remove excess from tracks with most engines (LIFO).
		type entry struct {
			name  string
			count int
		}
		var entries []entry
		for name, count := range result {
			entries = append(entries, entry{name, count})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].count > entries[j].count
		})
		excess := totalAssigned - totalEngines
		for i := 0; excess > 0 && i < len(entries); i++ {
			if entries[i].count > 1 {
				reduce := entries[i].count - 1
				if reduce > excess {
					reduce = excess
				}
				result[entries[i].name] -= reduce
				excess -= reduce
			}
		}
	}

	return result
}

// ScaleOpts configures the ry engine scale command.
type ScaleOpts struct {
	DB         *gorm.DB
	Config     *config.Config
	ConfigPath string
	Track      string
	Count      int
	Tmux       Tmux // defaults to DefaultTmux if nil
}

// ScaleResult holds the outcome of a scale operation.
type ScaleResult struct {
	Track           string
	Previous        int
	Current         int
	SessionsCreated []string
	SessionsKilled  []string
}

// Scale adjusts the engine count for a track.
func Scale(opts ScaleOpts) (*ScaleResult, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("orchestration: database connection is required")
	}
	if opts.Config == nil {
		return nil, fmt.Errorf("orchestration: config is required")
	}
	if opts.Track == "" {
		return nil, fmt.Errorf("orchestration: track is required")
	}
	if opts.Count < 0 {
		return nil, fmt.Errorf("orchestration: count must be non-negative")
	}
	if opts.Tmux == nil {
		opts.Tmux = DefaultTmux
	}

	owner := opts.Config.Owner

	// Validate track exists.
	var maxSlots int
	found := false
	for _, t := range opts.Config.Tracks {
		if t.Name == opts.Track {
			maxSlots = t.EngineSlots
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("orchestration: track %q not found in config", opts.Track)
	}
	if opts.Count > maxSlots {
		return nil, fmt.Errorf("orchestration: count %d exceeds max engine_slots %d for track %q", opts.Count, maxSlots, opts.Track)
	}

	// Check that at least the yardmaster session is running.
	ymSession := YardmasterSession(owner)
	if !opts.Tmux.SessionExists(ymSession) {
		return nil, fmt.Errorf("orchestration: no railyard session running")
	}

	// Count current live engines for this track.
	var currentEngines []models.Engine
	opts.DB.Where("track = ? AND status != ?", opts.Track, "dead").Find(&currentEngines)
	currentCount := len(currentEngines)

	result := &ScaleResult{
		Track:    opts.Track,
		Previous: currentCount,
		Current:  opts.Count,
	}

	delta := opts.Count - currentCount
	if delta == 0 {
		return result, nil
	}

	if delta > 0 {
		// Scale up: find next available engine index and create new sessions.
		nextIdx := nextEngineIndex(opts.Tmux, owner)
		for i := 0; i < delta; i++ {
			engSession := EngineSession(owner, nextIdx)
			nextIdx++

			if err := opts.Tmux.CreateSession(engSession); err != nil {
				return result, fmt.Errorf("orchestration: create engine session: %w", err)
			}
			engineCmd := fmt.Sprintf("ry engine start --config %s --track %s", opts.ConfigPath, opts.Track)
			if err := opts.Tmux.SendKeys(engSession, engineCmd); err != nil {
				return result, fmt.Errorf("orchestration: start engine on %s: %w", opts.Track, err)
			}
			result.SessionsCreated = append(result.SessionsCreated, engSession)
		}
	} else {
		// Scale down: drain newest engines first (LIFO by StartedAt).
		sort.Slice(currentEngines, func(i, j int) bool {
			return currentEngines[i].StartedAt.After(currentEngines[j].StartedAt)
		})
		toRemove := -delta
		for i := 0; i < toRemove && i < len(currentEngines); i++ {
			eng := currentEngines[i]
			// Mark as dead.
			opts.DB.Model(&models.Engine{}).Where("id = ?", eng.ID).
				Update("status", "dead")
			result.SessionsKilled = append(result.SessionsKilled, eng.ID)
		}
	}

	return result, nil
}

// nextEngineIndex finds the next available engine session index by scanning
// existing sessions with the railyard_OWNER_eng prefix.
func nextEngineIndex(tmux Tmux, owner string) int {
	prefix := fmt.Sprintf("railyard_%s_eng", owner)
	sessions, err := tmux.ListSessions(prefix)
	if err != nil || len(sessions) == 0 {
		return 0
	}
	maxIdx := -1
	for _, s := range sessions {
		// Extract the NNN suffix from railyard_OWNER_engNNN.
		suffix := s[len(prefix):]
		var idx int
		if _, err := fmt.Sscanf(suffix, "%d", &idx); err == nil {
			if idx > maxIdx {
				maxIdx = idx
			}
		}
	}
	return maxIdx + 1
}

// EngineListOpts configures ry engine list.
type EngineListOpts struct {
	DB     *gorm.DB
	Track  string
	Status string
}

// ListEngines returns filtered engine information.
func ListEngines(opts EngineListOpts) ([]EngineInfo, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("orchestration: database connection is required")
	}

	query := opts.DB.Model(&models.Engine{})
	if opts.Track != "" {
		query = query.Where("track = ?", opts.Track)
	}
	if opts.Status != "" {
		query = query.Where("status = ?", opts.Status)
	} else {
		query = query.Where("status != ?", "dead")
	}

	var engines []models.Engine
	if err := query.Order("track, started_at").Find(&engines).Error; err != nil {
		return nil, fmt.Errorf("orchestration: list engines: %w", err)
	}

	now := time.Now()
	var infos []EngineInfo
	for _, e := range engines {
		infos = append(infos, EngineInfo{
			ID:           e.ID,
			Track:        e.Track,
			Status:       e.Status,
			Provider:     e.Provider,
			CurrentCar:   e.CurrentCar,
			LastActivity: e.LastActivity,
			Uptime:       now.Sub(e.StartedAt),
		})
	}
	return infos, nil
}

// RestartEngine kills an engine's process and launches a replacement in a new session.
func RestartEngine(db *gorm.DB, cfg *config.Config, configPath, engineID string, tmux Tmux) error {
	if db == nil {
		return fmt.Errorf("orchestration: database connection is required")
	}
	if engineID == "" {
		return fmt.Errorf("orchestration: engine ID is required")
	}
	if cfg == nil {
		return fmt.Errorf("orchestration: config is required")
	}
	if tmux == nil {
		tmux = DefaultTmux
	}

	owner := cfg.Owner
	ymSession := YardmasterSession(owner)
	if !tmux.SessionExists(ymSession) {
		return fmt.Errorf("orchestration: no railyard session running")
	}

	// Get engine info.
	var eng models.Engine
	if err := db.Where("id = ?", engineID).First(&eng).Error; err != nil {
		return fmt.Errorf("orchestration: engine %q not found", engineID)
	}

	// Mark old engine as dead.
	db.Model(&models.Engine{}).Where("id = ?", engineID).
		Update("status", "dead")

	// Create new session with same track.
	nextIdx := nextEngineIndex(tmux, owner)
	engSession := EngineSession(owner, nextIdx)
	if err := tmux.CreateSession(engSession); err != nil {
		return fmt.Errorf("orchestration: create replacement session: %w", err)
	}
	engineCmd := fmt.Sprintf("ry engine start --config %s --track %s", configPath, eng.Track)
	if err := tmux.SendKeys(engSession, engineCmd); err != nil {
		return fmt.Errorf("orchestration: start replacement engine on %s: %w", eng.Track, err)
	}

	return nil
}
