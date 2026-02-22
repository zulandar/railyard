package telegraph

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"gorm.io/gorm"
)

// StatusProvider abstracts the orchestration.Status() call for testability.
type StatusProvider interface {
	Status() (*orchestration.StatusInfo, error)
}

// defaultStatusProvider calls orchestration.Status() directly.
type defaultStatusProvider struct {
	db   *gorm.DB
	tmux orchestration.Tmux
}

func (p *defaultStatusProvider) Status() (*orchestration.StatusInfo, error) {
	return orchestration.Status(p.db, p.tmux)
}

// Default watcher intervals.
const (
	DefaultPollInterval  = 15 * time.Second
	DefaultPulseInterval = 30 * time.Minute
)

// EventType identifies the kind of event detected by the watcher.
type EventType string

const (
	EventCarStatusChange EventType = "car_status_change"
	EventEngineStalled   EventType = "engine_stalled"
	EventEscalation      EventType = "escalation"
	EventPulse           EventType = "pulse"
)

// DetectedEvent is a raw event detected by the watcher before formatting.
type DetectedEvent struct {
	Type      EventType
	Timestamp time.Time

	// Car events
	CarID     string
	OldStatus string
	NewStatus string
	Track     string
	Title     string // car title

	// Stall events
	EngineID   string
	CurrentCar string

	// Escalation events
	MessageID uint
	FromAgent string
	Subject   string
	Body      string
	Priority  string
}

// carSnapshot holds the last-known status of each car for change detection.
type carSnapshot struct {
	Status string
	Track  string
	Title  string
}

// pulseDigest holds a snapshot of orchestration status for comparison.
type pulseDigest struct {
	TotalActive  int64
	TotalReady   int64
	TotalDone    int64
	TotalBlocked int64
	EngineCount  int
	Working      int
}

// Watcher polls Dolt for car lifecycle changes, engine stalls, and
// escalation messages. It emits DetectedEvents to a channel for
// formatting and delivery.
type Watcher struct {
	db             *gorm.DB
	statusProvider StatusProvider
	pollInterval   time.Duration
	pulseInterval  time.Duration

	mu          sync.Mutex
	snapshot    map[string]carSnapshot // carID -> last-known state
	seeded      bool                   // true after first poll (baseline established)
	lastDigest  *pulseDigest           // last emitted pulse for comparison
	lastPulseAt time.Time              // when the last pulse was emitted
}

// WatcherOpts holds parameters for creating a Watcher.
type WatcherOpts struct {
	DB             *gorm.DB
	StatusProvider StatusProvider // defaults to orchestration.Status()
	PollInterval   time.Duration  // defaults to DefaultPollInterval
	PulseInterval  time.Duration  // defaults to DefaultPulseInterval
}

// NewWatcher creates a Watcher.
func NewWatcher(opts WatcherOpts) (*Watcher, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("telegraph: watcher: db is required")
	}
	poll := opts.PollInterval
	if poll <= 0 {
		poll = DefaultPollInterval
	}
	pulse := opts.PulseInterval
	if pulse <= 0 {
		pulse = DefaultPulseInterval
	}
	sp := opts.StatusProvider
	if sp == nil {
		sp = &defaultStatusProvider{db: opts.DB, tmux: nil}
	}
	return &Watcher{
		db:             opts.DB,
		statusProvider: sp,
		pollInterval:   poll,
		pulseInterval:  pulse,
		snapshot:       make(map[string]carSnapshot),
	}, nil
}

// Poll runs one detection cycle: checks for car status changes, stalled
// engines, and escalation messages. Returns all detected events.
func (w *Watcher) Poll(ctx context.Context) ([]DetectedEvent, error) {
	var allEvents []DetectedEvent

	carEvents, err := w.detectCarEvents()
	if err != nil {
		return nil, fmt.Errorf("telegraph: watcher: car events: %w", err)
	}
	allEvents = append(allEvents, carEvents...)

	stallEvents, err := w.detectStalls()
	if err != nil {
		return nil, fmt.Errorf("telegraph: watcher: stall events: %w", err)
	}
	allEvents = append(allEvents, stallEvents...)

	escalations, err := w.detectEscalations()
	if err != nil {
		return nil, fmt.Errorf("telegraph: watcher: escalation events: %w", err)
	}
	allEvents = append(allEvents, escalations...)

	return allEvents, nil
}

// Run starts the watcher loop. It polls on the configured interval and
// sends detected events to the returned channel. The channel is closed
// when the context is cancelled. Pulse digests fire on a separate interval.
func (w *Watcher) Run(ctx context.Context) <-chan DetectedEvent {
	ch := make(chan DetectedEvent, 64)
	go func() {
		defer close(ch)
		pollTicker := time.NewTicker(w.pollInterval)
		defer pollTicker.Stop()
		pulseTicker := time.NewTicker(w.pulseInterval)
		defer pulseTicker.Stop()

		emit := func(events []DetectedEvent) {
			for _, e := range events {
				select {
				case ch <- e:
				case <-ctx.Done():
					return
				}
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-pollTicker.C:
				events, err := w.Poll(ctx)
				if err != nil {
					continue
				}
				emit(events)
			case <-pulseTicker.C:
				if pulse, err := w.BuildPulse(); err == nil && pulse != nil {
					select {
					case ch <- *pulse:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return ch
}

// detectCarEvents compares current car statuses against the in-memory
// snapshot and emits events for any changes. On the first call it seeds
// the snapshot without emitting events (to avoid a burst of false
// positives on startup).
func (w *Watcher) detectCarEvents() ([]DetectedEvent, error) {
	var cars []models.Car
	if err := w.db.Select("id, status, track, title").Find(&cars).Error; err != nil {
		return nil, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	var events []DetectedEvent
	currentIDs := make(map[string]bool, len(cars))

	for _, c := range cars {
		currentIDs[c.ID] = true
		old, exists := w.snapshot[c.ID]
		if !exists {
			// New car — record it. Only emit if we've already seeded.
			w.snapshot[c.ID] = carSnapshot{Status: c.Status, Track: c.Track, Title: c.Title}
			if w.seeded {
				events = append(events, DetectedEvent{
					Type:      EventCarStatusChange,
					Timestamp: time.Now(),
					CarID:     c.ID,
					OldStatus: "",
					NewStatus: c.Status,
					Track:     c.Track,
					Title:     c.Title,
				})
			}
			continue
		}
		if old.Status != c.Status {
			events = append(events, DetectedEvent{
				Type:      EventCarStatusChange,
				Timestamp: time.Now(),
				CarID:     c.ID,
				OldStatus: old.Status,
				NewStatus: c.Status,
				Track:     c.Track,
				Title:     c.Title,
			})
			w.snapshot[c.ID] = carSnapshot{Status: c.Status, Track: c.Track, Title: c.Title}
		}
	}

	// Detect deleted cars (present in snapshot but missing from DB).
	if w.seeded {
		for id := range w.snapshot {
			if !currentIDs[id] {
				delete(w.snapshot, id)
			}
		}
	}

	if !w.seeded {
		w.seeded = true
	}

	return events, nil
}

// detectStalls finds engines with status='stalled'.
func (w *Watcher) detectStalls() ([]DetectedEvent, error) {
	var engines []models.Engine
	if err := w.db.Where("status = ?", "stalled").Find(&engines).Error; err != nil {
		return nil, err
	}

	events := make([]DetectedEvent, 0, len(engines))
	for _, e := range engines {
		events = append(events, DetectedEvent{
			Type:       EventEngineStalled,
			Timestamp:  time.Now(),
			EngineID:   e.ID,
			Track:      e.Track,
			CurrentCar: e.CurrentCar,
		})
	}
	return events, nil
}

// detectEscalations finds unacknowledged messages sent to "human" or
// "telegraph" and marks them as acknowledged after pickup.
func (w *Watcher) detectEscalations() ([]DetectedEvent, error) {
	var msgs []models.Message
	if err := w.db.Where("to_agent IN ? AND acknowledged = ?",
		[]string{"human", "telegraph"}, false).
		Order("created_at ASC").
		Find(&msgs).Error; err != nil {
		return nil, err
	}

	if len(msgs) == 0 {
		return nil, nil
	}

	events := make([]DetectedEvent, 0, len(msgs))
	ids := make([]uint, 0, len(msgs))
	for _, m := range msgs {
		events = append(events, DetectedEvent{
			Type:      EventEscalation,
			Timestamp: m.CreatedAt,
			MessageID: m.ID,
			FromAgent: m.FromAgent,
			CarID:     m.CarID,
			Subject:   m.Subject,
			Body:      m.Body,
			Priority:  m.Priority,
		})
		ids = append(ids, m.ID)
	}

	// Mark acknowledged so they aren't picked up again.
	if err := w.db.Model(&models.Message{}).
		Where("id IN ?", ids).
		Update("acknowledged", true).Error; err != nil {
		return nil, fmt.Errorf("acknowledge escalations: %w", err)
	}

	return events, nil
}

// BuildPulse creates a pulse digest event from the current orchestration
// status. Returns nil (suppressed) when:
//   - totalActive==0 AND totalReady==0 (no active work), OR
//   - nothing changed since the last digest.
func (w *Watcher) BuildPulse() (*DetectedEvent, error) {
	info, err := w.statusProvider.Status()
	if err != nil {
		return nil, fmt.Errorf("telegraph: watcher: pulse status: %w", err)
	}

	current := buildDigest(info)

	w.mu.Lock()
	defer w.mu.Unlock()

	// Suppress: no active work and no ready work.
	if current.TotalActive == 0 && current.TotalReady == 0 {
		return nil, nil
	}

	// Suppress: nothing changed since last digest.
	if w.lastDigest != nil && *w.lastDigest == current {
		return nil, nil
	}

	w.lastDigest = &current
	w.lastPulseAt = time.Now()

	formatted := FormatPulse(info)
	return &DetectedEvent{
		Type:      EventPulse,
		Timestamp: time.Now(),
		Title:     formatted.Title,
		Body:      formatted.Body,
	}, nil
}

// buildDigest creates a pulseDigest from orchestration StatusInfo.
func buildDigest(info *orchestration.StatusInfo) pulseDigest {
	d := pulseDigest{
		EngineCount: len(info.Engines),
	}
	for _, e := range info.Engines {
		if e.Status == "working" {
			d.Working++
		}
	}
	for _, ts := range info.TrackSummary {
		d.TotalActive += ts.InProgress
		d.TotalReady += ts.Ready
		d.TotalDone += ts.Done
		d.TotalBlocked += ts.Blocked
	}
	return d
}

// LastPulseAt returns when the last pulse digest was emitted.
func (w *Watcher) LastPulseAt() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastPulseAt
}

// Snapshot returns a copy of the current car snapshot (for testing).
func (w *Watcher) Snapshot() map[string]carSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := make(map[string]carSnapshot, len(w.snapshot))
	for k, v := range w.snapshot {
		cp[k] = v
	}
	return cp
}

// Seeded returns whether the watcher has completed its initial snapshot.
func (w *Watcher) Seeded() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seeded
}
