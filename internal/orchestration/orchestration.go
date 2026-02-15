package orchestration

import (
	"fmt"
	"strings"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// StartOpts configures the ry start command.
type StartOpts struct {
	Config     *config.Config
	ConfigPath string
	DB         *gorm.DB
	Engines    int  // 0 = sum of track engine_slots
	Tmux       Tmux // defaults to DefaultTmux if nil
}

// StartResult holds the result of starting the railyard.
type StartResult struct {
	Session        string
	DispatchPane   string
	YardmasterPane string
	EnginePanes    []EnginePane
}

// EnginePane maps a tmux pane to a track assignment.
type EnginePane struct {
	PaneID string
	Track  string
}

// Start creates a tmux session and launches all components.
func Start(opts StartOpts) (*StartResult, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("orchestration: config is required")
	}
	if opts.ConfigPath == "" {
		return nil, fmt.Errorf("orchestration: config path is required")
	}
	if opts.DB == nil {
		return nil, fmt.Errorf("orchestration: database connection is required")
	}
	if len(opts.Config.Tracks) == 0 {
		return nil, fmt.Errorf("orchestration: at least one track must be configured")
	}
	if opts.Tmux == nil {
		opts.Tmux = DefaultTmux
	}

	// Check if already running.
	if opts.Tmux.SessionExists(SessionName) {
		return nil, fmt.Errorf("orchestration: railyard session already running (use 'ry stop' first)")
	}

	// Determine engine count.
	totalEngines := opts.Engines
	if totalEngines <= 0 {
		for _, t := range opts.Config.Tracks {
			totalEngines += t.EngineSlots
		}
	}
	if totalEngines <= 0 {
		totalEngines = 1
	}

	// Assign tracks to engines.
	assignment := AssignTracks(opts.Config, totalEngines)

	// Create tmux session (first pane is dispatch).
	if err := opts.Tmux.CreateSession(SessionName); err != nil {
		return nil, err
	}

	result := &StartResult{Session: SessionName}

	// Pane 0 (initial pane): dispatch.
	panes, err := opts.Tmux.ListPanes(SessionName)
	if err != nil {
		_ = opts.Tmux.KillSession(SessionName)
		return nil, fmt.Errorf("orchestration: list initial panes: %w", err)
	}
	result.DispatchPane = panes[0]
	dispatchCmd := fmt.Sprintf("ry dispatch --config %s", opts.ConfigPath)
	if err := opts.Tmux.SendKeys(result.DispatchPane, dispatchCmd); err != nil {
		_ = opts.Tmux.KillSession(SessionName)
		return nil, fmt.Errorf("orchestration: start dispatch: %w", err)
	}

	// Pane 1: yardmaster.
	ymPane, err := opts.Tmux.NewPane(SessionName)
	if err != nil {
		_ = opts.Tmux.KillSession(SessionName)
		return nil, fmt.Errorf("orchestration: create yardmaster pane: %w", err)
	}
	result.YardmasterPane = ymPane
	ymCmd := fmt.Sprintf("ry yardmaster --config %s", opts.ConfigPath)
	if err := opts.Tmux.SendKeys(ymPane, ymCmd); err != nil {
		_ = opts.Tmux.KillSession(SessionName)
		return nil, fmt.Errorf("orchestration: start yardmaster: %w", err)
	}

	// Panes 2..N+1: engines.
	for trackName, count := range assignment {
		for i := 0; i < count; i++ {
			pane, err := opts.Tmux.NewPane(SessionName)
			if err != nil {
				_ = opts.Tmux.KillSession(SessionName)
				return nil, fmt.Errorf("orchestration: create engine pane: %w", err)
			}
			engineCmd := fmt.Sprintf("ry engine start --config %s --track %s", opts.ConfigPath, trackName)
			if err := opts.Tmux.SendKeys(pane, engineCmd); err != nil {
				_ = opts.Tmux.KillSession(SessionName)
				return nil, fmt.Errorf("orchestration: start engine on %s: %w", trackName, err)
			}
			result.EnginePanes = append(result.EnginePanes, EnginePane{PaneID: pane, Track: trackName})
		}
	}

	// Tile the layout for visibility.
	_ = opts.Tmux.TileLayout(SessionName)

	return result, nil
}

// StopOpts configures the ry stop command.
type StopOpts struct {
	DB      *gorm.DB
	Timeout time.Duration // max wait for graceful drain (default 60s)
	Tmux    Tmux          // defaults to DefaultTmux if nil
}

// Stop gracefully shuts down the railyard.
func Stop(opts StopOpts) error {
	if opts.DB == nil {
		return fmt.Errorf("orchestration: database connection is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	if opts.Tmux == nil {
		opts.Tmux = DefaultTmux
	}

	if !opts.Tmux.SessionExists(SessionName) {
		return fmt.Errorf("orchestration: no railyard session running")
	}

	// Step 1: Send drain broadcast.
	if _, err := messaging.Send(opts.DB, "orchestrator", "broadcast", "drain", "Railyard shutting down. Complete current work and exit gracefully.", messaging.SendOpts{}); err != nil {
		// Non-fatal — continue with shutdown.
		_ = err
	}

	// Step 2: Wait for working engines to finish (up to timeout).
	deadline := time.Now().Add(opts.Timeout)
	for time.Now().Before(deadline) {
		var working int64
		opts.DB.Model(&models.Engine{}).Where("status = ?", "working").Count(&working)
		if working == 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Step 3: Send C-c to all panes.
	panes, err := opts.Tmux.ListPanes(SessionName)
	if err == nil {
		for _, p := range panes {
			_ = opts.Tmux.SendSignal(p, "C-c")
		}
		// Brief pause for processes to exit.
		time.Sleep(2 * time.Second)
	}

	// Step 4: Kill the tmux session.
	if err := opts.Tmux.KillSession(SessionName); err != nil {
		return err
	}

	// Step 5: Mark all non-dead engines as dead.
	opts.DB.Model(&models.Engine{}).
		Where("status != ?", "dead").
		Updates(map[string]interface{}{"status": "dead"})

	return nil
}

// StatusInfo holds dashboard information.
type StatusInfo struct {
	SessionRunning bool
	Engines        []EngineInfo
	TrackSummary   []TrackSummary
	MessageDepth   int64
}

// EngineInfo holds per-engine dashboard data.
type EngineInfo struct {
	ID           string
	Track        string
	Status       string
	CurrentBead  string
	LastActivity time.Time
	Uptime       time.Duration
}

// TrackSummary holds per-track bead counts.
type TrackSummary struct {
	Track      string
	Open       int64
	Ready      int64
	InProgress int64
	Done       int64
	Blocked    int64
}

// Status gathers dashboard information.
func Status(db *gorm.DB, tmux Tmux) (*StatusInfo, error) {
	if db == nil {
		return nil, fmt.Errorf("orchestration: database connection is required")
	}
	if tmux == nil {
		tmux = DefaultTmux
	}

	info := &StatusInfo{
		SessionRunning: tmux.SessionExists(SessionName),
	}

	// Gather engine info.
	var engines []models.Engine
	db.Where("status != ?", "dead").Order("track, id").Find(&engines)

	now := time.Now()
	for _, e := range engines {
		info.Engines = append(info.Engines, EngineInfo{
			ID:           e.ID,
			Track:        e.Track,
			Status:       e.Status,
			CurrentBead:  e.CurrentBead,
			LastActivity: e.LastActivity,
			Uptime:       now.Sub(e.StartedAt),
		})
	}

	// Gather track summaries.
	var tracks []models.Track
	db.Where("active = ?", true).Find(&tracks)

	for _, t := range tracks {
		ts := TrackSummary{Track: t.Name}
		db.Model(&models.Bead{}).Where("track = ? AND status = ?", t.Name, "open").Count(&ts.Open)
		db.Model(&models.Bead{}).Where("track = ? AND status = ?", t.Name, "in_progress").Count(&ts.InProgress)
		db.Model(&models.Bead{}).Where("track = ? AND status = ?", t.Name, "done").Count(&ts.Done)
		db.Model(&models.Bead{}).Where("track = ? AND status = ?", t.Name, "blocked").Count(&ts.Blocked)
		// Ready = open with no unresolved blockers (simplified: count open with no deps or all deps done).
		var ready int64
		db.Model(&models.Bead{}).
			Where("track = ? AND status = ? AND assignee = ?", t.Name, "open", "").
			Where("id NOT IN (?)",
				db.Model(&models.BeadDep{}).
					Select("bead_id").
					Joins("JOIN beads ON beads.id = bead_deps.blocked_by AND beads.status != 'done'"),
			).Count(&ready)
		ts.Ready = ready
		info.TrackSummary = append(info.TrackSummary, ts)
	}

	// Message queue depth (unacknowledged, non-broadcast).
	db.Model(&models.Message{}).
		Where("acknowledged = ? AND to_agent != ?", false, "broadcast").
		Count(&info.MessageDepth)

	return info, nil
}

// FormatStatus renders StatusInfo as a human-readable dashboard string.
func FormatStatus(info *StatusInfo) string {
	var b strings.Builder

	if info.SessionRunning {
		b.WriteString("Railyard: RUNNING\n")
	} else {
		b.WriteString("Railyard: STOPPED\n")
	}
	b.WriteString("\n")

	// Engine table.
	b.WriteString("ENGINES\n")
	b.WriteString(fmt.Sprintf("%-14s %-12s %-10s %-14s %-20s %s\n",
		"ID", "TRACK", "STATUS", "CURRENT BEAD", "LAST ACTIVITY", "UPTIME"))
	for _, e := range info.Engines {
		bead := e.CurrentBead
		if bead == "" {
			bead = "-"
		}
		b.WriteString(fmt.Sprintf("%-14s %-12s %-10s %-14s %-20s %s\n",
			e.ID, e.Track, e.Status, bead,
			e.LastActivity.Format("15:04:05"),
			formatDuration(e.Uptime)))
	}
	if len(info.Engines) == 0 {
		b.WriteString("  (no active engines)\n")
	}
	b.WriteString("\n")

	// Track summary.
	b.WriteString("TRACKS\n")
	b.WriteString(fmt.Sprintf("%-12s %6s %6s %6s %6s %6s\n",
		"TRACK", "OPEN", "READY", "ACTIVE", "DONE", "BLOCKED"))
	for _, t := range info.TrackSummary {
		b.WriteString(fmt.Sprintf("%-12s %6d %6d %6d %6d %6d\n",
			t.Track, t.Open, t.Ready, t.InProgress, t.Done, t.Blocked))
	}
	if len(info.TrackSummary) == 0 {
		b.WriteString("  (no active tracks)\n")
	}
	b.WriteString("\n")

	// Message depth.
	b.WriteString(fmt.Sprintf("Message queue: %d unacknowledged\n", info.MessageDepth))

	return b.String()
}

// formatDuration formats a duration as "Xh Ym" or "Ym Zs".
func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}
