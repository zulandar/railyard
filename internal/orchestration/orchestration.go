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
	Telegraph  bool // include telegraph pane in main session
	Tmux       Tmux // defaults to DefaultTmux if nil
}

// StartResult holds the result of starting the railyard.
type StartResult struct {
	Session        string
	YardmasterPane string
	TelegraphPane  string // set when Telegraph=true
	EnginePanes    []EnginePane
}

// EnginePane maps a tmux pane to a track assignment.
type EnginePane struct {
	PaneID string
	Track  string
}

// Start creates a tmux session with yardmaster, engines, and optionally
// telegraph. Dispatch is NOT auto-launched; use 'ry dispatch' separately.
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

	// Ensure .claude/settings.json has the permissions engines need.
	if err := EnsureClaudeSettings(opts.ConfigPath); err != nil {
		return nil, err
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

	// Create main session (yardmaster + engines + optional telegraph).
	if err := opts.Tmux.CreateSession(SessionName); err != nil {
		return nil, err
	}

	result := &StartResult{
		Session: SessionName,
	}

	// Pane 0 of main session: yardmaster.
	mainPanes, err := opts.Tmux.ListPanes(SessionName)
	if err != nil {
		_ = opts.Tmux.KillSession(SessionName)
		return nil, fmt.Errorf("orchestration: list main panes: %w", err)
	}
	result.YardmasterPane = mainPanes[0]
	ymCmd := fmt.Sprintf("ry yardmaster --config %s", opts.ConfigPath)
	if err := opts.Tmux.SendKeys(result.YardmasterPane, ymCmd); err != nil {
		_ = opts.Tmux.KillSession(SessionName)
		return nil, fmt.Errorf("orchestration: start yardmaster: %w", err)
	}

	// Optional telegraph pane.
	if opts.Telegraph {
		tgPane, err := opts.Tmux.NewPane(SessionName)
		if err != nil {
			_ = opts.Tmux.KillSession(SessionName)
			return nil, fmt.Errorf("orchestration: create telegraph pane: %w", err)
		}
		tgCmd := fmt.Sprintf("ry telegraph start --config %s", opts.ConfigPath)
		if err := opts.Tmux.SendKeys(tgPane, tgCmd); err != nil {
			_ = opts.Tmux.KillSession(SessionName)
			return nil, fmt.Errorf("orchestration: start telegraph: %w", err)
		}
		result.TelegraphPane = tgPane
	}

	// Engine panes in main session.
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

	// Tile the main session layout for visibility.
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

	mainRunning := opts.Tmux.SessionExists(SessionName)
	dispatchRunning := opts.Tmux.SessionExists(DispatchSessionName)
	if !mainRunning && !dispatchRunning {
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

	// Step 3: Send C-c to all panes in both sessions.
	if mainRunning {
		panes, err := opts.Tmux.ListPanes(SessionName)
		if err == nil {
			for _, p := range panes {
				_ = opts.Tmux.SendSignal(p, "C-c")
			}
		}
	}
	if dispatchRunning {
		panes, err := opts.Tmux.ListPanes(DispatchSessionName)
		if err == nil {
			for _, p := range panes {
				_ = opts.Tmux.SendSignal(p, "C-c")
			}
		}
	}
	// Brief pause for processes to exit.
	time.Sleep(2 * time.Second)

	// Step 4: Kill both tmux sessions.
	if mainRunning {
		if err := opts.Tmux.KillSession(SessionName); err != nil {
			return err
		}
	}
	if dispatchRunning {
		if err := opts.Tmux.KillSession(DispatchSessionName); err != nil {
			return err
		}
	}

	// Step 5: Mark all non-dead engines as dead.
	opts.DB.Model(&models.Engine{}).
		Where("status != ?", "dead").
		Updates(map[string]interface{}{"status": "dead"})

	return nil
}

// StatusInfo holds dashboard information.
type StatusInfo struct {
	SessionRunning    bool
	DispatchRunning   bool
	Engines           []EngineInfo
	TrackSummary      []TrackSummary
	MessageDepth      int64
	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalTokens       int64
}

// EngineInfo holds per-engine dashboard data.
type EngineInfo struct {
	ID           string
	Track        string
	Status       string
	CurrentCar   string
	LastActivity time.Time
	Uptime       time.Duration
}

// TrackSummary holds per-track car counts.
type TrackSummary struct {
	Track        string
	Open         int64
	Ready        int64
	InProgress   int64
	Done         int64
	Blocked      int64
	MergeFailed  int64
	BaseBranches []string // unique base branches for active cars on this track
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
		SessionRunning:  tmux.SessionExists(SessionName),
		DispatchRunning: tmux.SessionExists(DispatchSessionName),
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
			CurrentCar:   e.CurrentCar,
			LastActivity: e.LastActivity,
			Uptime:       now.Sub(e.StartedAt),
		})
	}

	// Gather track summaries.
	var tracks []models.Track
	db.Where("active = ?", true).Find(&tracks)

	for _, t := range tracks {
		ts := TrackSummary{Track: t.Name}
		db.Model(&models.Car{}).Where("track = ? AND status = ?", t.Name, "open").Count(&ts.Open)
		db.Model(&models.Car{}).Where("track = ? AND status = ?", t.Name, "in_progress").Count(&ts.InProgress)
		db.Model(&models.Car{}).Where("track = ? AND status = ?", t.Name, "done").Count(&ts.Done)
		db.Model(&models.Car{}).Where("track = ? AND status = ?", t.Name, "blocked").Count(&ts.Blocked)
		db.Model(&models.Car{}).Where("track = ? AND status = ?", t.Name, "merge-failed").Count(&ts.MergeFailed)
		// Ready = open with no unresolved blockers.
		var ready int64
		db.Model(&models.Car{}).
			Where("track = ? AND status = ? AND (assignee = ? OR assignee IS NULL)", t.Name, "open", "").
			Where("id NOT IN (?)",
				db.Model(&models.CarDep{}).
					Select("car_id").
					Joins("JOIN cars ON cars.id = car_deps.blocked_by").
					Where("cars.status NOT IN ?", models.ResolvedBlockerStatuses),
			).Count(&ready)
		ts.Ready = ready

		// Collect unique base branches for active (non-done/merged/cancelled) cars.
		var bases []string
		db.Model(&models.Car{}).
			Where("track = ? AND status NOT IN ?", t.Name, []string{"done", "merged", "cancelled"}).
			Distinct("base_branch").Pluck("base_branch", &bases)
		seen := map[string]bool{}
		for _, b := range bases {
			if b == "" {
				b = "main"
			}
			if !seen[b] {
				ts.BaseBranches = append(ts.BaseBranches, b)
				seen[b] = true
			}
		}

		info.TrackSummary = append(info.TrackSummary, ts)
	}

	// Message queue depth (unacknowledged, non-broadcast).
	db.Model(&models.Message{}).
		Where("acknowledged = ? AND to_agent != ?", false, "broadcast").
		Count(&info.MessageDepth)

	// Aggregate token usage across all stdout logs.
	var tokenRow struct {
		InputTokens  int64
		OutputTokens int64
		TotalTokens  int64
	}
	db.Model(&models.AgentLog{}).
		Select("COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens, COALESCE(SUM(token_count),0) as total_tokens").
		Where("direction = ?", "out").
		Scan(&tokenRow)
	info.TotalInputTokens = tokenRow.InputTokens
	info.TotalOutputTokens = tokenRow.OutputTokens
	info.TotalTokens = tokenRow.TotalTokens

	return info, nil
}

// FormatStatus renders StatusInfo as a human-readable dashboard string.
func FormatStatus(info *StatusInfo) string {
	var b strings.Builder

	if info.SessionRunning && info.DispatchRunning {
		b.WriteString("Railyard: RUNNING\n")
	} else if info.SessionRunning || info.DispatchRunning {
		b.WriteString("Railyard: PARTIAL\n")
	} else {
		b.WriteString("Railyard: STOPPED\n")
	}
	b.WriteString("\n")

	// Engine table.
	b.WriteString("ENGINES\n")
	b.WriteString(fmt.Sprintf("%-14s %-12s %-10s %-14s %-20s %s\n",
		"ID", "TRACK", "STATUS", "CURRENT CAR", "LAST ACTIVITY", "UPTIME"))
	for _, e := range info.Engines {
		car := e.CurrentCar
		if car == "" {
			car = "-"
		}
		b.WriteString(fmt.Sprintf("%-14s %-12s %-10s %-14s %-20s %s\n",
			e.ID, e.Track, e.Status, car,
			e.LastActivity.Format("15:04:05"),
			formatDuration(e.Uptime)))
	}
	if len(info.Engines) == 0 {
		b.WriteString("  (no active engines)\n")
	}
	b.WriteString("\n")

	// Track summary.
	b.WriteString("TRACKS\n")
	multiBase := hasMultipleBases(info.TrackSummary)
	if multiBase {
		b.WriteString(fmt.Sprintf("%-12s %-12s %6s %6s %6s %6s %6s %8s\n",
			"TRACK", "BASE", "OPEN", "READY", "ACTIVE", "DONE", "BLOCKED", "MRG-FAIL"))
		for _, t := range info.TrackSummary {
			base := strings.Join(t.BaseBranches, ",")
			if base == "" {
				base = "main"
			}
			b.WriteString(fmt.Sprintf("%-12s %-12s %6d %6d %6d %6d %6d %8d\n",
				t.Track, base, t.Open, t.Ready, t.InProgress, t.Done, t.Blocked, t.MergeFailed))
		}
	} else {
		b.WriteString(fmt.Sprintf("%-12s %6s %6s %6s %6s %6s %8s\n",
			"TRACK", "OPEN", "READY", "ACTIVE", "DONE", "BLOCKED", "MRG-FAIL"))
		for _, t := range info.TrackSummary {
			b.WriteString(fmt.Sprintf("%-12s %6d %6d %6d %6d %6d %8d\n",
				t.Track, t.Open, t.Ready, t.InProgress, t.Done, t.Blocked, t.MergeFailed))
		}
	}
	if len(info.TrackSummary) == 0 {
		b.WriteString("  (no active tracks)\n")
	}
	b.WriteString("\n")

	// Message depth.
	b.WriteString(fmt.Sprintf("Message queue: %d unacknowledged\n", info.MessageDepth))

	// Token usage.
	if info.TotalTokens > 0 {
		b.WriteString("\nTOKENS\n")
		b.WriteString(fmt.Sprintf("  Input:     %s\n", formatTokens(info.TotalInputTokens)))
		b.WriteString(fmt.Sprintf("  Output:    %s\n", formatTokens(info.TotalOutputTokens)))
		b.WriteString(fmt.Sprintf("  Total:     %s\n", formatTokens(info.TotalTokens)))
	}

	return b.String()
}

// hasMultipleBases returns true when any track has more than one base branch,
// or different tracks target different base branches.
func hasMultipleBases(tracks []TrackSummary) bool {
	seen := map[string]bool{}
	for _, t := range tracks {
		if len(t.BaseBranches) > 1 {
			return true
		}
		for _, b := range t.BaseBranches {
			seen[b] = true
		}
	}
	// Multiple distinct base branches across all tracks.
	return len(seen) > 1
}

// formatTokens formats an int64 with comma separators.
func formatTokens(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	remainder := len(s) % 3
	if remainder > 0 {
		b.WriteString(s[:remainder])
	}
	for i := remainder; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
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
