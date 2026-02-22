package telegraph

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"gorm.io/gorm"
)

// Daemon is the main telegraph process. It connects to a chat platform via
// an Adapter, pumps inbound messages to a handler, and posts Railyard events
// to configured channels.
type Daemon struct {
	db             *gorm.DB
	cfg            *config.Config
	adapter        Adapter
	spawner        ProcessSpawner
	statusProvider StatusProvider
	out            io.Writer
}

// DaemonOpts holds parameters for creating a new Daemon.
type DaemonOpts struct {
	DB             *gorm.DB
	Config         *config.Config
	Adapter        Adapter
	Spawner        ProcessSpawner // optional; enables dispatch sessions
	StatusProvider StatusProvider // optional; defaults to orchestration-based
	Out            io.Writer      // defaults to os.Stdout
}

// NewDaemon creates a Daemon with the given options.
func NewDaemon(opts DaemonOpts) (*Daemon, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("telegraph: db is required")
	}
	if opts.Config == nil {
		return nil, fmt.Errorf("telegraph: config is required")
	}
	if opts.Adapter == nil {
		return nil, fmt.Errorf("telegraph: adapter is required")
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	if opts.Spawner == nil {
		fmt.Fprintf(out, "telegraph: no spawner configured; dispatch sessions disabled\n")
	}
	return &Daemon{
		db:             opts.DB,
		cfg:            opts.Config,
		adapter:        opts.Adapter,
		spawner:        opts.Spawner,
		statusProvider: opts.StatusProvider,
		out:            out,
	}, nil
}

// noopSpawner returns an error on Spawn — used when no real spawner is configured.
type noopSpawner struct{}

func (noopSpawner) Spawn(ctx context.Context, prompt string) (Process, error) {
	return nil, fmt.Errorf("telegraph: dispatch sessions not available (no spawner configured)")
}

// Run starts the telegraph daemon. It connects the adapter, builds all
// subsystems (Router, Watcher, digest scheduler), and blocks until the
// context is cancelled. On shutdown it closes the adapter gracefully.
func (d *Daemon) Run(ctx context.Context) error {
	fmt.Fprintf(d.out, "Telegraph connecting...\n")
	if err := d.adapter.Connect(ctx); err != nil {
		return fmt.Errorf("telegraph: connect: %w", err)
	}

	// Extract bot user ID if the adapter supports it.
	var botUserID string
	if bui, ok := d.adapter.(BotUserIDer); ok {
		botUserID = bui.BotUserID()
	}

	// Resolve spawner — use noopSpawner if none configured.
	spawner := d.spawner
	if spawner == nil {
		spawner = noopSpawner{}
	}

	// Resolve status provider.
	sp := d.statusProvider
	if sp == nil {
		sp = &defaultStatusProvider{db: d.db, tmux: nil}
	}

	// Build CommandHandler.
	cmdHandler, err := NewCommandHandler(CommandHandlerOpts{
		DB:             d.db,
		StatusProvider: sp,
	})
	if err != nil {
		d.adapter.Close()
		return fmt.Errorf("telegraph: build command handler: %w", err)
	}

	// Build SessionManager.
	hbTimeout := time.Duration(d.cfg.Telegraph.DispatchLock.HeartbeatTimeoutSec) * time.Second
	sessionMgr, err := NewSessionManager(SessionManagerOpts{
		DB:               d.db,
		Adapter:          d.adapter,
		Spawner:          spawner,
		HeartbeatTimeout: hbTimeout,
	})
	if err != nil {
		d.adapter.Close()
		return fmt.Errorf("telegraph: build session manager: %w", err)
	}

	// Build Router.
	router, err := NewRouter(RouterOpts{
		SessionMgr: sessionMgr,
		CmdHandler: cmdHandler,
		Adapter:    d.adapter,
		BotUserID:  botUserID,
		Out:        d.out,
	})
	if err != nil {
		d.adapter.Close()
		return fmt.Errorf("telegraph: build router: %w", err)
	}

	// Start listening for inbound messages.
	inbound, err := d.adapter.Listen(ctx)
	if err != nil {
		d.adapter.Close()
		return fmt.Errorf("telegraph: listen: %w", err)
	}

	// Build and start Watcher.
	pollInterval := time.Duration(d.cfg.Telegraph.Events.PollIntervalSec) * time.Second
	watcher, err := NewWatcher(WatcherOpts{
		DB:             d.db,
		StatusProvider: sp,
		PollInterval:   pollInterval,
	})
	if err != nil {
		d.adapter.Close()
		return fmt.Errorf("telegraph: build watcher: %w", err)
	}
	eventsCh := watcher.Run(ctx)

	// Start event dispatcher goroutine.
	go d.dispatchEvents(ctx, eventsCh)

	// Start digest scheduler goroutine.
	go d.runDigestScheduler(ctx, watcher)

	fmt.Fprintf(d.out, "Telegraph online\n")

	// Post online status.
	if err := d.adapter.Send(ctx, OutboundMessage{
		Text: "Telegraph online",
	}); err != nil {
		log.Printf("telegraph: send online message: %v", err)
	}

	// Main event loop: pump inbound messages until context is cancelled.
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(d.out, "Telegraph shutting down...\n")
			d.sendShutdown()
			if err := d.adapter.Close(); err != nil {
				log.Printf("telegraph: close adapter: %v", err)
			}
			fmt.Fprintf(d.out, "Telegraph stopped\n")
			return nil

		case msg, ok := <-inbound:
			if !ok {
				// Adapter closed the channel.
				fmt.Fprintf(d.out, "Telegraph inbound channel closed\n")
				return nil
			}
			router.Handle(ctx, msg)
		}
	}
}

// dispatchEvents reads detected events from the watcher channel, filters
// them by config toggles, formats them, and sends to the chat platform.
func (d *Daemon) dispatchEvents(ctx context.Context, eventsCh <-chan DetectedEvent) {
	evtCfg := d.cfg.Telegraph.Events
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-eventsCh:
			if !ok {
				return
			}
			d.handleDetectedEvent(ctx, event, evtCfg)
		}
	}
}

// handleDetectedEvent processes a single detected event: applies config
// filters, formats, and sends via the adapter.
func (d *Daemon) handleDetectedEvent(ctx context.Context, event DetectedEvent, evtCfg config.EventsConfig) {
	var formatted FormattedEvent

	switch event.Type {
	case EventCarStatusChange:
		if !evtCfg.CarLifecycle {
			return
		}
		formatted = FormatCarEvent(event)
	case EventEngineStalled:
		if !evtCfg.EngineStalls {
			return
		}
		formatted = FormatStallEvent(event)
	case EventEscalation:
		if !evtCfg.Escalations {
			return
		}
		formatted = FormatEscalation(event)
	case EventPulse, EventDailyDigest, EventWeeklyDigest:
		// Pulse and digest events are not gated by event toggles.
		formatted = FormattedEvent{
			Title:    event.Title,
			Body:     event.Body,
			Severity: "info",
			Color:    ColorInfo,
		}
	default:
		return
	}

	if err := d.adapter.Send(ctx, OutboundMessage{
		Events: []FormattedEvent{formatted},
	}); err != nil {
		log.Printf("telegraph: send event %s: %v", event.Type, err)
	}
}

// runDigestScheduler manages cron-based daily and weekly digest timers.
// It returns immediately if neither digest is enabled.
func (d *Daemon) runDigestScheduler(ctx context.Context, watcher *Watcher) {
	dailyCfg := d.cfg.Telegraph.Digest.Daily
	weeklyCfg := d.cfg.Telegraph.Digest.Weekly

	if !dailyCfg.Enabled && !weeklyCfg.Enabled {
		return
	}

	var dailyTimer, weeklyTimer *time.Timer
	if dailyCfg.Enabled && dailyCfg.Cron != "" {
		if d := nextCronDuration(dailyCfg.Cron); d > 0 {
			dailyTimer = time.NewTimer(d)
		}
	}
	if weeklyCfg.Enabled && weeklyCfg.Cron != "" {
		if d := nextCronDuration(weeklyCfg.Cron); d > 0 {
			weeklyTimer = time.NewTimer(d)
		}
	}

	defer func() {
		if dailyTimer != nil {
			dailyTimer.Stop()
		}
		if weeklyTimer != nil {
			weeklyTimer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timerChan(dailyTimer):
			d.fireDigest(ctx, watcher, "daily")
			if d := nextCronDuration(dailyCfg.Cron); d > 0 {
				dailyTimer.Reset(d)
			}
		case <-timerChan(weeklyTimer):
			d.fireDigest(ctx, watcher, "weekly")
			if d := nextCronDuration(weeklyCfg.Cron); d > 0 {
				weeklyTimer.Reset(d)
			}
		}
	}
}

// fireDigest builds and sends a single digest (daily or weekly).
func (d *Daemon) fireDigest(ctx context.Context, watcher *Watcher, kind string) {
	var event *DetectedEvent
	var err error

	switch kind {
	case "daily":
		event, err = watcher.BuildDailyDigest()
	case "weekly":
		event, err = watcher.BuildWeeklyDigest()
	}
	if err != nil {
		log.Printf("telegraph: %s digest: %v", kind, err)
		return
	}
	if event == nil {
		// No activity — suppress digest.
		return
	}

	formatted := FormattedEvent{
		Title:    event.Title,
		Body:     event.Body,
		Severity: "info",
		Color:    ColorInfo,
	}
	if err := d.adapter.Send(ctx, OutboundMessage{
		Events: []FormattedEvent{formatted},
	}); err != nil {
		log.Printf("telegraph: send %s digest: %v", kind, err)
	}
}

// timerChan returns the timer's channel, or nil if the timer is nil.
// A nil channel blocks forever in select, which is the desired behavior
// when a digest type is not enabled.
func timerChan(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// sendShutdown posts a shutdown message to the adapter (best-effort).
func (d *Daemon) sendShutdown() {
	ctx := context.Background()
	if err := d.adapter.Send(ctx, OutboundMessage{
		Text: "Telegraph shutting down",
	}); err != nil {
		log.Printf("telegraph: send shutdown message: %v", err)
	}
}
