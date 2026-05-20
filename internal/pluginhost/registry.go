package pluginhost

import (
	"context"
	"log/slog"
	"time"

	"github.com/zulandar/railyard/pkg/plugin"
)

// stopDrainTimeout is the per-plugin Stop deadline (spec §4). A plugin
// whose Stop blocks longer than this is abandoned and core shutdown
// continues.
const stopDrainTimeout = 5 * time.Second

// Register adds a plugin to the host's lifecycle set. Intended to be
// called from cmd/ry boot once per entry in plugin.Registered(). Plugins
// are stored in registration order; that order is preserved by Init and
// Start and reversed by Stop.
//
// Register is not safe to call concurrently with Init/Start/Stop.
func (h *Host) Register(p plugin.Plugin) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.plugins = append(h.plugins, p)
}

// Names returns the names of every plugin currently in the lifecycle
// set, in registration order. Used by `ry plugins list` and the boot
// summary log line.
func (h *Host) Names() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.plugins))
	for _, p := range h.plugins {
		out = append(out, p.Name())
	}
	return out
}

// Init calls each plugin's Init in registration order. A plugin whose
// Init returns an error is logged at WARN and removed from the running
// set; other plugins and core continue (spec §4 failure isolation).
//
// Each plugin sees a per-plugin Host wrapper so [Host.Logger] returns a
// logger scoped to that plugin's name.
func (h *Host) Init(ctx context.Context) {
	h.mu.Lock()
	plugins := append([]plugin.Plugin(nil), h.plugins...)
	h.mu.Unlock()

	survivors := make([]plugin.Plugin, 0, len(plugins))
	for _, p := range plugins {
		name := p.Name()
		logger := slog.Default().With(slog.String("plugin", name))
		logger.Info("plugin init")
		if err := p.Init(ctx, h.hostFor(name)); err != nil {
			logger.Warn("init failed — skipped", slog.String("error", err.Error()))
			continue
		}
		survivors = append(survivors, p)
	}

	h.mu.Lock()
	h.plugins = survivors
	h.mu.Unlock()
}

// Start calls each surviving plugin's Start in registration order. A
// plugin whose Start errors is logged at WARN but kept in the set —
// failed Start is observable but doesn't unwind already-started plugins.
func (h *Host) Start(ctx context.Context) {
	h.mu.Lock()
	plugins := append([]plugin.Plugin(nil), h.plugins...)
	h.mu.Unlock()

	for _, p := range plugins {
		name := p.Name()
		logger := slog.Default().With(slog.String("plugin", name))
		if err := p.Start(ctx); err != nil {
			logger.Warn("start failed", slog.String("error", err.Error()))
			continue
		}
		logger.Info("plugin started")
	}
}

// Stop calls each plugin's Stop in reverse registration order. Each call
// is wrapped in a 5-second per-plugin context (spec §4). A plugin that
// ignores cancellation past the timeout is abandoned; the host returns
// and core shutdown continues regardless.
func (h *Host) Stop(parent context.Context) {
	h.mu.Lock()
	plugins := append([]plugin.Plugin(nil), h.plugins...)
	h.mu.Unlock()

	for i := len(plugins) - 1; i >= 0; i-- {
		p := plugins[i]
		name := p.Name()
		logger := slog.Default().With(slog.String("plugin", name))

		ctx, cancel := context.WithTimeout(parent, stopDrainTimeout)
		done := make(chan error, 1)
		go func() {
			done <- p.Stop(ctx)
		}()
		select {
		case err := <-done:
			if err != nil {
				logger.Warn("plugin stop returned error", slog.String("error", err.Error()))
			} else {
				logger.Info("plugin stopped")
			}
		case <-ctx.Done():
			logger.Warn("plugin stop drain timeout exceeded — abandoned",
				slog.Duration("timeout", stopDrainTimeout))
		}
		cancel()
	}
}
