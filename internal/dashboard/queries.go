package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"
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
	Provider     string
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
			Provider:     e.Provider,
			LastActivity: e.LastActivity,
			StartedAt:    e.StartedAt,
		}
	}
	return rows, nil
}

// TrackStatusCount holds car counts by status for a single track.
type TrackStatusCount struct {
	Track      string
	Draft      int
	Open       int
	Claimed    int
	InProgress int
	Done       int
	Merged     int
	Blocked    int
	Total      int
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
	ID          string
	Title       string
	Status      string
	Type        string
	Track       string
	Priority    int
	Assignee    string
	CreatedAt   time.Time
	TotalTokens int64
	TotalCycles int
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
	ids := make([]string, len(cars))
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
		ids[i] = c.ID
	}

	// Batch-fetch token usage per car.
	if len(ids) > 0 {
		type tokenRow struct {
			CarID       string `gorm:"column:car_id"`
			TotalTokens int64  `gorm:"column:total_tokens"`
		}
		var tokenRows []tokenRow
		db.Model(&models.AgentLog{}).
			Select("car_id, COALESCE(SUM(token_count),0) as total_tokens").
			Where("car_id IN ? AND direction = ?", ids, "out").
			Group("car_id").
			Scan(&tokenRows)
		tokenMap := make(map[string]int64, len(tokenRows))
		for _, tr := range tokenRows {
			tokenMap[tr.CarID] = tr.TotalTokens
		}
		for i := range rows {
			rows[i].TotalTokens = tokenMap[rows[i].ID]
		}
	}

	// Batch-fetch cycle counts per car.
	if len(ids) > 0 {
		type cycleRow struct {
			CarID       string `gorm:"column:car_id"`
			TotalCycles int    `gorm:"column:total_cycles"`
		}
		var cycleRows []cycleRow
		db.Model(&models.CarProgress{}).
			Select("car_id, COUNT(*) as total_cycles").
			Where("car_id IN ?", ids).
			Group("car_id").
			Scan(&cycleRows)
		cycleMap := make(map[string]int, len(cycleRows))
		for _, cr := range cycleRows {
			cycleMap[cr.CarID] = cr.TotalCycles
		}
		for i := range rows {
			rows[i].TotalCycles = cycleMap[rows[i].ID]
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

// CycleDetailRow holds a single cycle detail for template rendering.
type CycleDetailRow struct {
	Cycle        int
	EngineID     string
	Duration     string
	DurationSec  float64
	FilesChanged int
	Note         string
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

	Children  []ChildRow
	BlockedBy []DepRow
	Blocks    []DepRow
	Progress  []ProgressRow

	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	TokenModel   string
	EstCost      float64

	TotalCycles       int
	AvgCycleDuration  string
	TotalFilesChanged int
	CycleStalled      bool
	CycleDetails      []CycleDetailRow
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

	// Token usage.
	var tokenSummary struct {
		InputTokens  int64 `gorm:"column:input_tokens"`
		OutputTokens int64 `gorm:"column:output_tokens"`
		TotalTokens  int64 `gorm:"column:total_tokens"`
	}
	if err := db.Model(&models.AgentLog{}).
		Select("COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens, COALESCE(SUM(token_count),0) as total_tokens").
		Where("car_id = ? AND direction = ?", id, "out").
		Scan(&tokenSummary).Error; err == nil {
		detail.InputTokens = tokenSummary.InputTokens
		detail.OutputTokens = tokenSummary.OutputTokens
		detail.TotalTokens = tokenSummary.TotalTokens
	}
	// Most recent model for cost estimation.
	if detail.TotalTokens > 0 {
		var logEntry models.AgentLog
		if err := db.Where("car_id = ? AND direction = ? AND model != ?", id, "out", "").
			Order("created_at DESC").First(&logEntry).Error; err == nil {
			detail.TokenModel = logEntry.Model
			detail.EstCost = estimateTokenCost(logEntry.Model, detail.InputTokens, detail.OutputTokens)
		}
	}

	// Cycle metrics.
	var progressRows []models.CarProgress
	db.Where("car_id = ?", id).Order("cycle ASC").Find(&progressRows)
	if len(progressRows) > 0 {
		detail.TotalCycles = len(progressRows)
		detail.CycleStalled = len(progressRows) > 5

		// Calculate durations and file counts.
		var totalDuration float64
		var totalFiles int
		engineSet := make(map[string]bool)
		for i, p := range progressRows {
			var dur float64
			if i > 0 {
				dur = p.CreatedAt.Sub(progressRows[i-1].CreatedAt).Seconds()
				totalDuration += dur
			}
			fc := countJSONArray(p.FilesChanged)
			totalFiles += fc
			engineSet[p.EngineID] = true

			durStr := "\u2014"
			if i > 0 {
				durStr = formatCycleDuration(dur)
			}
			detail.CycleDetails = append(detail.CycleDetails, CycleDetailRow{
				Cycle:        p.Cycle,
				EngineID:     p.EngineID,
				Duration:     durStr,
				DurationSec:  dur,
				FilesChanged: fc,
				Note:         p.Note,
				CommitHash:   p.CommitHash,
				CreatedAt:    p.CreatedAt,
			})
		}
		detail.TotalFilesChanged = totalFiles
		if detail.TotalCycles > 1 {
			avg := totalDuration / float64(detail.TotalCycles-1)
			detail.AvgCycleDuration = formatCycleDuration(avg)
		}
	}

	return detail, nil
}

// GraphNode holds a car node for the dependency graph.
type GraphNode struct {
	CarID  string
	Title  string
	Status string
	Type   string
	Depth  int
	IsRoot bool
}

// GraphEdge holds a directed edge from blocker to blocked car.
type GraphEdge struct {
	From string
	To   string
}

// DepGraphResult holds the dependency graph data for rendering.
type DepGraphResult struct {
	Nodes []GraphNode
	Edges []GraphEdge
	Tree  []DepTreeNode
}

// DepTreeNode holds a node in the rendered tree structure.
type DepTreeNode struct {
	CarID    string
	Title    string
	Status   string
	Indent   int
	IsLast   bool
	Prefix   string
	Relation string // "blocks" or "blocked by"
}

// DependencyGraph builds a dependency tree for a car, walking both up
// (what blocks this car) and down (what this car blocks). Max depth is 3.
func DependencyGraph(db *gorm.DB, carID string) DepGraphResult {
	if db == nil {
		return DepGraphResult{}
	}

	nodes := make(map[string]GraphNode)
	var edges []GraphEdge

	// Load the root car.
	var rootCar models.Car
	if err := db.Select("id, title, status, type").Where("id = ?", carID).First(&rootCar).Error; err != nil {
		return DepGraphResult{}
	}
	nodes[carID] = GraphNode{CarID: carID, Title: rootCar.Title, Status: rootCar.Status, Type: rootCar.Type, Depth: 0, IsRoot: true}

	// Walk "blocked by" (upstream) — what must complete before this car.
	walkDeps(db, carID, 1, 3, "up", nodes, &edges)
	// Walk "blocks" (downstream) — what this car blocks.
	walkDeps(db, carID, 1, 3, "down", nodes, &edges)

	// Build tree representation.
	var tree []DepTreeNode
	tree = append(tree, DepTreeNode{CarID: carID, Title: rootCar.Title, Status: rootCar.Status, Indent: 0})

	// Add "blocked by" items.
	var blockedBy []models.CarDep
	db.Where("car_id = ?", carID).Find(&blockedBy)
	for i, dep := range blockedBy {
		isLast := i == len(blockedBy)-1
		if n, ok := nodes[dep.BlockedBy]; ok {
			tree = append(tree, DepTreeNode{
				CarID: n.CarID, Title: n.Title, Status: n.Status,
				Indent: 1, IsLast: isLast, Relation: "blocks this",
			})
			addSubTree(db, dep.BlockedBy, 2, 3, "up", nodes, &tree)
		}
	}

	// Add "blocks" items.
	var blocks []models.CarDep
	db.Where("blocked_by = ?", carID).Find(&blocks)
	for i, dep := range blocks {
		isLast := i == len(blocks)-1
		if n, ok := nodes[dep.CarID]; ok {
			tree = append(tree, DepTreeNode{
				CarID: n.CarID, Title: n.Title, Status: n.Status,
				Indent: 1, IsLast: isLast, Relation: "blocked by this",
			})
			addSubTree(db, dep.CarID, 2, 3, "down", nodes, &tree)
		}
	}

	nodeSlice := make([]GraphNode, 0, len(nodes))
	for _, n := range nodes {
		nodeSlice = append(nodeSlice, n)
	}

	return DepGraphResult{Nodes: nodeSlice, Edges: edges, Tree: tree}
}

// walkDeps recursively walks the dependency graph in the given direction.
func walkDeps(db *gorm.DB, carID string, depth, maxDepth int, direction string, nodes map[string]GraphNode, edges *[]GraphEdge) {
	if depth > maxDepth {
		return
	}

	var deps []models.CarDep
	if direction == "up" {
		db.Where("car_id = ?", carID).Find(&deps)
		for _, dep := range deps {
			*edges = append(*edges, GraphEdge{From: dep.BlockedBy, To: carID})
			if _, seen := nodes[dep.BlockedBy]; !seen {
				var car models.Car
				if err := db.Select("id, title, status, type").Where("id = ?", dep.BlockedBy).First(&car).Error; err == nil {
					nodes[dep.BlockedBy] = GraphNode{CarID: car.ID, Title: car.Title, Status: car.Status, Type: car.Type, Depth: depth}
					walkDeps(db, dep.BlockedBy, depth+1, maxDepth, direction, nodes, edges)
				}
			}
		}
	} else {
		db.Where("blocked_by = ?", carID).Find(&deps)
		for _, dep := range deps {
			*edges = append(*edges, GraphEdge{From: carID, To: dep.CarID})
			if _, seen := nodes[dep.CarID]; !seen {
				var car models.Car
				if err := db.Select("id, title, status, type").Where("id = ?", dep.CarID).First(&car).Error; err == nil {
					nodes[dep.CarID] = GraphNode{CarID: car.ID, Title: car.Title, Status: car.Status, Type: car.Type, Depth: depth}
					walkDeps(db, dep.CarID, depth+1, maxDepth, direction, nodes, edges)
				}
			}
		}
	}
}

// addSubTree recursively adds children to the tree.
func addSubTree(db *gorm.DB, carID string, depth, maxDepth int, direction string, nodes map[string]GraphNode, tree *[]DepTreeNode) {
	if depth > maxDepth {
		return
	}

	var deps []models.CarDep
	if direction == "up" {
		db.Where("car_id = ?", carID).Find(&deps)
	} else {
		db.Where("blocked_by = ?", carID).Find(&deps)
	}

	for i, dep := range deps {
		targetID := dep.BlockedBy
		if direction == "down" {
			targetID = dep.CarID
		}
		isLast := i == len(deps)-1
		if n, ok := nodes[targetID]; ok {
			rel := "blocks this"
			if direction == "down" {
				rel = "blocked by this"
			}
			*tree = append(*tree, DepTreeNode{
				CarID: n.CarID, Title: n.Title, Status: n.Status,
				Indent: depth, IsLast: isLast, Relation: rel,
			})
			addSubTree(db, targetID, depth+1, maxDepth, direction, nodes, tree)
		}
	}
}

// EngineDetail holds full engine data for the detail view.
type EngineDetail struct {
	ID            string
	Track         string
	Status        string
	Role          string
	SessionID     string
	Provider      string
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
		Provider:     e.Provider,
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

// formatCycleDuration formats seconds as a human-readable duration string.
func formatCycleDuration(seconds float64) string {
	if seconds <= 0 {
		return "\u2014"
	}
	totalSec := int(seconds)
	if totalSec < 60 {
		return fmt.Sprintf("%ds", totalSec)
	}
	min := totalSec / 60
	sec := totalSec % 60
	if min < 60 {
		return fmt.Sprintf("%dm %ds", min, sec)
	}
	h := min / 60
	min = min % 60
	return fmt.Sprintf("%dh %dm", h, min)
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

// DashboardStats holds aggregate numbers for the summary stats bar.
type DashboardStats struct {
	ActiveEngines  int
	OpenCars       int
	InProgressCars int
	BlockedCars    int
	CompletedToday int64
	TotalTokens    int64
}

// CompletedToday returns the count of cars completed (status=done) since midnight today.
func CompletedToday(db *gorm.DB) int64 {
	if db == nil {
		return 0
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var count int64
	db.Model(&models.Car{}).
		Where("status = ? AND completed_at >= ?", "done", midnight).
		Count(&count)
	return count
}

// TotalTokenUsage returns the sum of output tokens across all agent logs.
func TotalTokenUsage(db *gorm.DB) int64 {
	if db == nil {
		return 0
	}
	var total int64
	db.Model(&models.AgentLog{}).
		Select("COALESCE(SUM(output_tokens), 0)").
		Scan(&total)
	return total
}

// ComputeStats builds DashboardStats from already-fetched engines/tracks plus DB queries.
func ComputeStats(engines []EngineRow, tracks []TrackStatusCount, db *gorm.DB) DashboardStats {
	var s DashboardStats
	for _, e := range engines {
		if e.Status != "dead" && e.Status != "stopped" {
			s.ActiveEngines++
		}
	}
	for _, tc := range tracks {
		s.OpenCars += tc.Open
		s.InProgressCars += tc.InProgress
		s.BlockedCars += tc.Blocked
	}
	s.CompletedToday = CompletedToday(db)
	s.TotalTokens = TotalTokenUsage(db)
	return s
}

// LogRow holds an agent log entry for display.
type LogRow struct {
	ID           uint
	EngineID     string
	SessionID    string
	CarID        string
	Direction    string
	Content      string
	TokenCount   int
	InputTokens  int
	OutputTokens int
	Model        string
	LatencyMs    int
	CreatedAt    time.Time
}

// AgentLogFilters holds optional filters for the logs list.
type AgentLogFilters struct {
	EngineID  string
	CarID     string
	Direction string
}

// AgentLogListResult holds the log list plus metadata for filter dropdowns.
type AgentLogListResult struct {
	Logs       []LogRow
	Engines    []string
	Cars       []string
	Directions []string
}

// AgentLogList returns the latest 200 agent log entries matching filters.
func AgentLogList(db *gorm.DB, filters AgentLogFilters) AgentLogListResult {
	if db == nil {
		return AgentLogListResult{Logs: []LogRow{}}
	}

	q := db.Model(&models.AgentLog{})
	if filters.EngineID != "" {
		q = q.Where("engine_id = ?", filters.EngineID)
	}
	if filters.CarID != "" {
		q = q.Where("car_id = ?", filters.CarID)
	}
	if filters.Direction != "" {
		q = q.Where("direction = ?", filters.Direction)
	}

	var logs []models.AgentLog
	q.Order("created_at DESC").Limit(200).Find(&logs)

	rows := make([]LogRow, len(logs))
	for i, l := range logs {
		content := l.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		rows[i] = LogRow{
			ID:           l.ID,
			EngineID:     l.EngineID,
			SessionID:    l.SessionID,
			CarID:        l.CarID,
			Direction:    l.Direction,
			Content:      content,
			TokenCount:   l.TokenCount,
			InputTokens:  l.InputTokens,
			OutputTokens: l.OutputTokens,
			Model:        l.Model,
			LatencyMs:    l.LatencyMs,
			CreatedAt:    l.CreatedAt,
		}
	}

	// Distinct values for filter dropdowns.
	var engines []string
	db.Model(&models.AgentLog{}).Distinct("engine_id").Order("engine_id ASC").Pluck("engine_id", &engines)
	var cars []string
	db.Model(&models.AgentLog{}).Distinct("car_id").Where("car_id != ''").Order("car_id ASC").Pluck("car_id", &cars)
	var directions []string
	db.Model(&models.AgentLog{}).Distinct("direction").Order("direction ASC").Pluck("direction", &directions)

	return AgentLogListResult{
		Logs:       rows,
		Engines:    engines,
		Cars:       cars,
		Directions: directions,
	}
}

// TokenUsageRow holds aggregated token data for one engine.
type TokenUsageRow struct {
	EngineID     string
	Track        string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	Model        string
}

// TokenUsageResult holds per-engine token data plus overall totals.
type TokenUsageResult struct {
	ByEngine    []TokenUsageRow
	TotalInput  int64
	TotalOutput int64
	TotalAll    int64
}

// TokenUsageSummary returns token usage aggregated by engine, plus overall totals.
func TokenUsageSummary(db *gorm.DB) TokenUsageResult {
	if db == nil {
		return TokenUsageResult{ByEngine: []TokenUsageRow{}}
	}

	type row struct {
		EngineID     string `gorm:"column:engine_id"`
		InputTokens  int64  `gorm:"column:input_tokens"`
		OutputTokens int64  `gorm:"column:output_tokens"`
		TotalTokens  int64  `gorm:"column:total_tokens"`
	}

	var rows []row
	db.Model(&models.AgentLog{}).
		Select("engine_id, COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens, COALESCE(SUM(token_count),0) as total_tokens").
		Where("direction = ?", "out").
		Group("engine_id").
		Order("total_tokens DESC").
		Scan(&rows)

	result := TokenUsageResult{
		ByEngine: make([]TokenUsageRow, len(rows)),
	}

	for i, r := range rows {
		result.ByEngine[i] = TokenUsageRow{
			EngineID:     r.EngineID,
			InputTokens:  r.InputTokens,
			OutputTokens: r.OutputTokens,
			TotalTokens:  r.TotalTokens,
		}
		result.TotalInput += r.InputTokens
		result.TotalOutput += r.OutputTokens
		result.TotalAll += r.TotalTokens

		// Look up engine track and most recent model.
		var eng models.Engine
		if err := db.Select("track").Where("id = ?", r.EngineID).First(&eng).Error; err == nil {
			result.ByEngine[i].Track = eng.Track
		}
		var log models.AgentLog
		if err := db.Where("engine_id = ? AND direction = ? AND model != ?", r.EngineID, "out", "").
			Order("created_at DESC").First(&log).Error; err == nil {
			result.ByEngine[i].Model = log.Model
		}
	}

	return result
}

// CycleUsageResult holds aggregate cycle stats for the logs/stats page.
type CycleUsageResult struct {
	TotalCycles    int64
	AvgPerCar      float64
	StalledCars    int64
	ByEngine       []EngineCycleRow
}

// EngineCycleRow holds per-engine cycle counts.
type EngineCycleRow struct {
	EngineID    string
	TotalCycles int64
}

// CycleUsageSummary returns aggregate cycle stats.
func CycleUsageSummary(db *gorm.DB) CycleUsageResult {
	if db == nil {
		return CycleUsageResult{ByEngine: []EngineCycleRow{}}
	}

	var totalCycles int64
	db.Model(&models.CarProgress{}).Count(&totalCycles)

	var carCount int64
	db.Model(&models.CarProgress{}).Distinct("car_id").Count(&carCount)

	var avgPerCar float64
	if carCount > 0 {
		avgPerCar = float64(totalCycles) / float64(carCount)
	}

	// Count stalled cars (more than 5 cycles).
	type stalledRow struct {
		CarID string
		Cnt   int64
	}
	var stalledRows []stalledRow
	db.Model(&models.CarProgress{}).
		Select("car_id, COUNT(*) as cnt").
		Group("car_id").
		Having("COUNT(*) > ?", 5).
		Scan(&stalledRows)
	stalledCars := int64(len(stalledRows))

	// Per-engine breakdown.
	type engineRow struct {
		EngineID    string `gorm:"column:engine_id"`
		TotalCycles int64  `gorm:"column:total_cycles"`
	}
	var engineRows []engineRow
	db.Model(&models.CarProgress{}).
		Select("engine_id, COUNT(*) as total_cycles").
		Group("engine_id").
		Order("total_cycles DESC").
		Scan(&engineRows)

	byEngine := make([]EngineCycleRow, len(engineRows))
	for i, r := range engineRows {
		byEngine[i] = EngineCycleRow{
			EngineID:    r.EngineID,
			TotalCycles: r.TotalCycles,
		}
	}

	return CycleUsageResult{
		TotalCycles: totalCycles,
		AvgPerCar:   avgPerCar,
		StalledCars: stalledCars,
		ByEngine:    byEngine,
	}
}

// SessionRow holds session data for display in the list view.
type SessionRow struct {
	ID               uint
	Source           string
	UserName         string
	Status           string
	CarsCreatedCount int
	Duration         string
	CreatedAt        time.Time
}

// SessionFilters holds optional filters for the session list.
type SessionFilters struct {
	Source   string
	Status   string
	UserName string
}

// SessionListResult holds the session list plus metadata for filter dropdowns.
type SessionListResult struct {
	Sessions []SessionRow
	Sources  []string
	Statuses []string
	Users    []string
}

// SessionList returns sessions matching filters, newest first.
func SessionList(db *gorm.DB, filters SessionFilters) SessionListResult {
	if db == nil {
		return SessionListResult{Sessions: []SessionRow{}}
	}

	q := db.Model(&models.DispatchSession{})
	if filters.Source != "" {
		q = q.Where("source = ?", filters.Source)
	}
	if filters.Status != "" {
		q = q.Where("status = ?", filters.Status)
	}
	if filters.UserName != "" {
		q = q.Where("user_name = ?", filters.UserName)
	}

	var sessions []models.DispatchSession
	q.Order("created_at DESC").Limit(200).Find(&sessions)

	rows := make([]SessionRow, len(sessions))
	for i, s := range sessions {
		rows[i] = SessionRow{
			ID:               s.ID,
			Source:           s.Source,
			UserName:         s.UserName,
			Status:           s.Status,
			CarsCreatedCount: countJSONArray(s.CarsCreated),
			CreatedAt:        s.CreatedAt,
		}
		if s.CompletedAt != nil {
			rows[i].Duration = formatDuration(s.CompletedAt.Sub(s.CreatedAt))
		} else if s.Status == "active" {
			rows[i].Duration = formatDuration(time.Since(s.CreatedAt))
		} else {
			rows[i].Duration = "\u2014"
		}
	}

	// Distinct values for filter dropdowns.
	var sources []string
	db.Model(&models.DispatchSession{}).Distinct("source").Order("source ASC").Pluck("source", &sources)
	var statuses []string
	db.Model(&models.DispatchSession{}).Distinct("status").Order("status ASC").Pluck("status", &statuses)
	var users []string
	db.Model(&models.DispatchSession{}).Distinct("user_name").Order("user_name ASC").Pluck("user_name", &users)

	return SessionListResult{
		Sessions: rows,
		Sources:  sources,
		Statuses: statuses,
		Users:    users,
	}
}

// countJSONArray returns the number of elements in a JSON array string.
func countJSONArray(jsonStr string) int {
	if jsonStr == "" || jsonStr == "null" || jsonStr == "[]" {
		return 0
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &arr); err != nil {
		return 0
	}
	return len(arr)
}

// SessionDetailData holds the full session data for the detail page.
type SessionDetailData struct {
	ID               uint
	Source           string
	UserName         string
	PlatformThreadID string
	ChannelID        string
	Status           string
	CarsCreated      []string
	LastHeartbeat    time.Time
	CreatedAt        time.Time
	CompletedAt      *time.Time
	Duration         string
	Conversations    []ConversationRow
}

// ConversationRow holds a single conversation message for display.
type ConversationRow struct {
	ID        uint
	Sequence  int
	Role      string
	UserName  string
	Content   string
	CreatedAt time.Time
}

// GetSessionDetail returns full session data including conversations.
func GetSessionDetail(db *gorm.DB, id string) (*SessionDetailData, error) {
	if db == nil {
		return nil, fmt.Errorf("no database connection")
	}

	var s models.DispatchSession
	if err := db.Preload("Conversations", func(tx *gorm.DB) *gorm.DB {
		return tx.Order("sequence ASC")
	}).Where("id = ?", id).First(&s).Error; err != nil {
		return nil, err
	}

	var cars []string
	if s.CarsCreated != "" && s.CarsCreated != "null" {
		json.Unmarshal([]byte(s.CarsCreated), &cars)
	}

	detail := &SessionDetailData{
		ID:               s.ID,
		Source:           s.Source,
		UserName:         s.UserName,
		PlatformThreadID: s.PlatformThreadID,
		ChannelID:        s.ChannelID,
		Status:           s.Status,
		CarsCreated:      cars,
		LastHeartbeat:    s.LastHeartbeat,
		CreatedAt:        s.CreatedAt,
		CompletedAt:      s.CompletedAt,
	}

	if s.CompletedAt != nil {
		detail.Duration = formatDuration(s.CompletedAt.Sub(s.CreatedAt))
	} else if s.Status == "active" {
		detail.Duration = formatDuration(time.Since(s.CreatedAt))
	} else {
		detail.Duration = "\u2014"
	}

	detail.Conversations = make([]ConversationRow, len(s.Conversations))
	for i, c := range s.Conversations {
		detail.Conversations[i] = ConversationRow{
			ID:        c.ID,
			Sequence:  c.Sequence,
			Role:      c.Role,
			UserName:  c.UserName,
			Content:   c.Content,
			CreatedAt: c.CreatedAt,
		}
	}

	return detail, nil
}

// ActiveSessionCount returns the count of sessions with status='active'.
func ActiveSessionCount(db *gorm.DB) int64 {
	if db == nil {
		return 0
	}
	var count int64
	db.Model(&models.DispatchSession{}).
		Where("status = ?", "active").
		Count(&count)
	return count
}

// YardmasterInfo holds yardmaster engine data for the status card.
type YardmasterInfo struct {
	ID           string
	Status       string
	LastActivity time.Time
	StartedAt    time.Time
	Uptime       string
	CurrentCar   string
}

// YardmasterStatus returns the yardmaster engine status, or nil if not registered.
func YardmasterStatus(db *gorm.DB) *YardmasterInfo {
	if db == nil {
		return nil
	}

	var eng models.Engine
	if err := db.Where("role = ?", "yardmaster").First(&eng).Error; err != nil {
		return nil
	}

	info := &YardmasterInfo{
		ID:           eng.ID,
		Status:       eng.Status,
		LastActivity: eng.LastActivity,
		StartedAt:    eng.StartedAt,
		CurrentCar:   eng.CurrentCar,
	}
	if !eng.StartedAt.IsZero() {
		info.Uptime = formatDuration(time.Since(eng.StartedAt))
	}
	return info
}

// estimateTokenCost estimates the USD cost for the given model and token counts.
func estimateTokenCost(model string, inputTokens, outputTokens int64) float64 {
	var inputRate, outputRate float64 // per million tokens
	switch {
	case strings.HasPrefix(model, "claude-opus"):
		inputRate = 15.0
		outputRate = 75.0
	case strings.HasPrefix(model, "claude-sonnet"):
		inputRate = 3.0
		outputRate = 15.0
	case strings.HasPrefix(model, "claude-haiku"):
		inputRate = 0.80
		outputRate = 4.0
	default:
		inputRate = 3.0
		outputRate = 15.0
	}
	return float64(inputTokens)/1_000_000*inputRate + float64(outputTokens)/1_000_000*outputRate
}
