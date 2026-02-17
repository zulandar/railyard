package dashboard

import (
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// EngineRow holds engine data for display.
type EngineRow struct {
	ID           string
	Track        string
	Status       string
	CurrentCar   string
	LastActivity time.Time
	StartedAt    time.Time
}

// EngineSummary returns all non-yardmaster engines.
func EngineSummary(db *gorm.DB) ([]EngineRow, error) {
	var engines []models.Engine
	deadCutoff := time.Now().Add(-1 * time.Hour)
	if err := db.Where("(role != ? OR role IS NULL) AND NOT (status = ? AND last_activity < ?)", "yardmaster", "dead", deadCutoff).
		Order("track ASC, id ASC").Find(&engines).Error; err != nil {
		return nil, err
	}

	rows := make([]EngineRow, len(engines))
	for i, e := range engines {
		rows[i] = EngineRow{
			ID:           e.ID,
			Track:        e.Track,
			Status:       e.Status,
			CurrentCar:   e.CurrentCar,
			LastActivity: e.LastActivity,
			StartedAt:    e.StartedAt,
		}
	}
	return rows, nil
}

// TrackStatusCount holds car counts by status for a single track.
type TrackStatusCount struct {
	Track       string
	Draft       int
	Open        int
	Claimed     int
	InProgress  int
	Done        int
	Merged      int
	Blocked     int
	Total       int
}

// TrackSummary returns per-track car counts grouped by status.
func TrackSummary(db *gorm.DB) ([]TrackStatusCount, error) {
	type row struct {
		Track  string
		Status string
		Count  int
	}
	var rows []row
	if err := db.Model(&models.Car{}).
		Select("track, status, count(*) as count").
		Where("type != ?", "epic").
		Group("track, status").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	// Aggregate by track.
	trackMap := make(map[string]*TrackStatusCount)
	for _, r := range rows {
		tc, ok := trackMap[r.Track]
		if !ok {
			tc = &TrackStatusCount{Track: r.Track}
			trackMap[r.Track] = tc
		}
		tc.Total += r.Count
		switch r.Status {
		case "draft":
			tc.Draft += r.Count
		case "open", "ready":
			tc.Open += r.Count
		case "claimed":
			tc.Claimed += r.Count
		case "in_progress":
			tc.InProgress += r.Count
		case "done":
			tc.Done += r.Count
		case "merged":
			tc.Merged += r.Count
		case "blocked":
			tc.Blocked += r.Count
		}
	}

	result := make([]TrackStatusCount, 0, len(trackMap))
	for _, tc := range trackMap {
		result = append(result, *tc)
	}
	return result, nil
}

// MessageQueueDepth returns the count of unacknowledged non-broadcast messages.
func MessageQueueDepth(db *gorm.DB) (int64, error) {
	var count int64
	if err := db.Model(&models.Message{}).
		Where("acknowledged = ? AND to_agent != ?", false, "broadcast").
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// CarRow holds car data for display in the list view.
type CarRow struct {
	ID        string
	Title     string
	Status    string
	Type      string
	Track     string
	Priority  int
	Assignee  string
	CreatedAt time.Time
}

// CarListResult holds the car list plus metadata for filter dropdowns.
type CarListResult struct {
	Cars     []CarRow
	Tracks   []string
	Statuses []string
	Types    []string
}

// CarList returns cars matching filters, plus distinct values for filter dropdowns.
func CarList(db *gorm.DB, track, status, carType, parentID string) CarListResult {
	if db == nil {
		return CarListResult{Cars: []CarRow{}}
	}

	q := db.Model(&models.Car{})
	if track != "" {
		q = q.Where("track = ?", track)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if carType != "" {
		q = q.Where("type = ?", carType)
	}
	if parentID != "" {
		q = q.Where("parent_id = ?", parentID)
	}

	var cars []models.Car
	q.Order("priority ASC, created_at ASC").Find(&cars)

	rows := make([]CarRow, len(cars))
	for i, c := range cars {
		rows[i] = CarRow{
			ID:        c.ID,
			Title:     c.Title,
			Status:    c.Status,
			Type:      c.Type,
			Track:     c.Track,
			Priority:  c.Priority,
			Assignee:  c.Assignee,
			CreatedAt: c.CreatedAt,
		}
	}

	// Distinct values for filter dropdowns.
	var tracks []string
	db.Model(&models.Car{}).Distinct("track").Order("track ASC").Pluck("track", &tracks)
	var statuses []string
	db.Model(&models.Car{}).Distinct("status").Order("status ASC").Pluck("status", &statuses)
	var types []string
	db.Model(&models.Car{}).Distinct("type").Order("type ASC").Pluck("type", &types)

	return CarListResult{
		Cars:     rows,
		Tracks:   tracks,
		Statuses: statuses,
		Types:    types,
	}
}

// DepRow holds a dependency link for display.
type DepRow struct {
	CarID  string
	Title  string
	Status string
}

// ChildRow holds a child car for display.
type ChildRow struct {
	ID       string
	Title    string
	Status   string
	Type     string
	Priority int
	Assignee string
}

// ProgressRow holds a progress note for display.
type ProgressRow struct {
	Cycle        int
	EngineID     string
	Note         string
	FilesChanged string
	CommitHash   string
	CreatedAt    time.Time
}

// CarDetail holds full car detail data for the detail view.
type CarDetail struct {
	ID          string
	Title       string
	Description string
	Type        string
	Status      string
	Priority    int
	Track       string
	Branch      string
	Assignee    string
	DesignNotes string
	Acceptance  string
	SkipTests   bool
	ParentID    string
	ParentTitle string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ClaimedAt   *time.Time
	CompletedAt *time.Time

	Children []ChildRow
	BlockedBy []DepRow
	Blocks    []DepRow
	Progress  []ProgressRow
}

// GetCarDetail returns full car detail data for the detail page.
func GetCarDetail(db *gorm.DB, id string) (*CarDetail, error) {
	if db == nil {
		return nil, fmt.Errorf("no database connection")
	}

	var c models.Car
	if err := db.Preload("Deps").Preload("Progress", func(tx *gorm.DB) *gorm.DB {
		return tx.Order("created_at ASC")
	}).Where("id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}

	detail := &CarDetail{
		ID:          c.ID,
		Title:       c.Title,
		Description: c.Description,
		Type:        c.Type,
		Status:      c.Status,
		Priority:    c.Priority,
		Track:       c.Track,
		Branch:      c.Branch,
		Assignee:    c.Assignee,
		DesignNotes: c.DesignNotes,
		Acceptance:  c.Acceptance,
		SkipTests:   c.SkipTests,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
		ClaimedAt:   c.ClaimedAt,
		CompletedAt: c.CompletedAt,
	}

	// Parent info.
	if c.ParentID != nil && *c.ParentID != "" {
		detail.ParentID = *c.ParentID
		var parent models.Car
		if err := db.Select("id, title").Where("id = ?", *c.ParentID).First(&parent).Error; err == nil {
			detail.ParentTitle = parent.Title
		}
	}

	// Children (if epic).
	if c.Type == "epic" {
		var children []models.Car
		db.Where("parent_id = ?", c.ID).Order("priority ASC, created_at ASC").Find(&children)
		detail.Children = make([]ChildRow, len(children))
		for i, ch := range children {
			detail.Children[i] = ChildRow{
				ID:       ch.ID,
				Title:    ch.Title,
				Status:   ch.Status,
				Type:     ch.Type,
				Priority: ch.Priority,
				Assignee: ch.Assignee,
			}
		}
	}

	// Blocked by (dependencies).
	for _, dep := range c.Deps {
		var blocker models.Car
		if err := db.Select("id, title, status").Where("id = ?", dep.BlockedBy).First(&blocker).Error; err == nil {
			detail.BlockedBy = append(detail.BlockedBy, DepRow{
				CarID:  blocker.ID,
				Title:  blocker.Title,
				Status: blocker.Status,
			})
		}
	}

	// Blocks (reverse deps — what does this car block?).
	var reverseDeps []models.CarDep
	db.Where("blocked_by = ?", c.ID).Find(&reverseDeps)
	for _, dep := range reverseDeps {
		var blocked models.Car
		if err := db.Select("id, title, status").Where("id = ?", dep.CarID).First(&blocked).Error; err == nil {
			detail.Blocks = append(detail.Blocks, DepRow{
				CarID:  blocked.ID,
				Title:  blocked.Title,
				Status: blocked.Status,
			})
		}
	}

	// Progress notes.
	detail.Progress = make([]ProgressRow, len(c.Progress))
	for i, p := range c.Progress {
		detail.Progress[i] = ProgressRow{
			Cycle:        p.Cycle,
			EngineID:     p.EngineID,
			Note:         p.Note,
			FilesChanged: p.FilesChanged,
			CommitHash:   p.CommitHash,
			CreatedAt:    p.CreatedAt,
		}
	}

	return detail, nil
}

// EngineDetail holds full engine data for the detail view.
type EngineDetail struct {
	ID            string
	Track         string
	Status        string
	Role          string
	SessionID     string
	CurrentCar    string
	CurrentTitle  string
	CurrentStatus string
	LastActivity  time.Time
	StartedAt     time.Time
	Uptime        string
}

// ActivityRow holds a progress note for engine activity display.
type ActivityRow struct {
	CarID        string
	CarTitle     string
	Cycle        int
	Note         string
	FilesChanged string
	CommitHash   string
	CreatedAt    time.Time
}

// GetEngineDetail returns full engine data for the detail page.
func GetEngineDetail(db *gorm.DB, id string) (*EngineDetail, error) {
	if db == nil {
		return nil, fmt.Errorf("no database connection")
	}

	var e models.Engine
	if err := db.Where("id = ?", id).First(&e).Error; err != nil {
		return nil, err
	}

	detail := &EngineDetail{
		ID:           e.ID,
		Track:        e.Track,
		Status:       e.Status,
		Role:         e.Role,
		SessionID:    e.SessionID,
		CurrentCar:   e.CurrentCar,
		LastActivity: e.LastActivity,
		StartedAt:    e.StartedAt,
	}

	// Compute uptime.
	if !e.StartedAt.IsZero() {
		detail.Uptime = formatDuration(time.Since(e.StartedAt))
	}

	// Get current car title/status if assigned.
	if e.CurrentCar != "" {
		var car models.Car
		if err := db.Select("id, title, status").Where("id = ?", e.CurrentCar).First(&car).Error; err == nil {
			detail.CurrentTitle = car.Title
			detail.CurrentStatus = car.Status
		}
	}

	return detail, nil
}

// GetEngineActivity returns recent progress notes from this engine.
func GetEngineActivity(db *gorm.DB, engineID string) []ActivityRow {
	if db == nil {
		return []ActivityRow{}
	}

	var notes []models.CarProgress
	db.Where("engine_id = ?", engineID).
		Order("created_at DESC").
		Limit(50).
		Find(&notes)

	rows := make([]ActivityRow, len(notes))
	for i, n := range notes {
		rows[i] = ActivityRow{
			CarID:        n.CarID,
			Cycle:        n.Cycle,
			Note:         n.Note,
			FilesChanged: n.FilesChanged,
			CommitHash:   n.CommitHash,
			CreatedAt:    n.CreatedAt,
		}
		// Look up car title.
		var car models.Car
		if err := db.Select("title").Where("id = ?", n.CarID).First(&car).Error; err == nil {
			rows[i].CarTitle = car.Title
		}
	}
	return rows
}

// formatDuration formats a duration as a human-readable string like "2h 15m".
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h >= 24 {
		days := h / 24
		h = h % 24
		return fmt.Sprintf("%dd %dh", days, h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// Escalation holds a recent escalation message for display.
type Escalation struct {
	ID        uint
	FromAgent string
	CarID     string
	Subject   string
	Body      string
	Priority  string
	CreatedAt time.Time
}

// RecentEscalations returns unacknowledged messages sent to "human".
func RecentEscalations(db *gorm.DB) ([]Escalation, error) {
	if db == nil {
		return []Escalation{}, nil
	}
	var msgs []models.Message
	if err := db.Where("to_agent = ? AND acknowledged = ?", "human", false).
		Order("created_at DESC").
		Limit(20).
		Find(&msgs).Error; err != nil {
		return nil, err
	}

	result := make([]Escalation, len(msgs))
	for i, m := range msgs {
		result[i] = Escalation{
			ID:        m.ID,
			FromAgent: m.FromAgent,
			CarID:     m.CarID,
			Subject:   m.Subject,
			Body:      m.Body,
			Priority:  m.Priority,
			CreatedAt: m.CreatedAt,
		}
	}
	return result, nil
}

// MessageFilters holds optional filters for the messages list.
type MessageFilters struct {
	Agent    string
	Priority string
	Unacked  bool
}

// MessageRow holds a message for display in the list.
type MessageRow struct {
	ID           uint
	FromAgent    string
	ToAgent      string
	Subject      string
	Body         string
	CarID        string
	Priority     string
	Acknowledged bool
	CreatedAt    time.Time
}

// MessageListResult holds the message list plus metadata for filter dropdowns.
type MessageListResult struct {
	Messages   []MessageRow
	Agents     []string
	Priorities []string
}

// ListMessages returns messages matching filters, newest first.
func ListMessages(db *gorm.DB, filters MessageFilters) MessageListResult {
	if db == nil {
		return MessageListResult{Messages: []MessageRow{}}
	}

	q := db.Model(&models.Message{})
	if filters.Agent != "" {
		q = q.Where("from_agent = ? OR to_agent = ?", filters.Agent, filters.Agent)
	}
	if filters.Priority != "" {
		q = q.Where("priority = ?", filters.Priority)
	}
	if filters.Unacked {
		q = q.Where("acknowledged = ?", false)
	}

	var msgs []models.Message
	q.Order("created_at DESC").Limit(200).Find(&msgs)

	rows := make([]MessageRow, len(msgs))
	for i, m := range msgs {
		rows[i] = MessageRow{
			ID:           m.ID,
			FromAgent:    m.FromAgent,
			ToAgent:      m.ToAgent,
			Subject:      m.Subject,
			Body:         m.Body,
			CarID:        m.CarID,
			Priority:     m.Priority,
			Acknowledged: m.Acknowledged,
			CreatedAt:    m.CreatedAt,
		}
	}

	// Distinct values for filter dropdowns.
	var agents []string
	db.Model(&models.Message{}).Distinct("from_agent").Order("from_agent ASC").Pluck("from_agent", &agents)
	var priorities []string
	db.Model(&models.Message{}).Distinct("priority").Order("priority ASC").Pluck("priority", &priorities)

	return MessageListResult{
		Messages:   rows,
		Agents:     agents,
		Priorities: priorities,
	}
}

// PendingEscalationCount returns the number of unacked messages to "human".
func PendingEscalationCount(db *gorm.DB) int64 {
	if db == nil {
		return 0
	}
	var count int64
	db.Model(&models.Message{}).
		Where("to_agent = ? AND acknowledged = ?", "human", false).
		Count(&count)
	return count
}
