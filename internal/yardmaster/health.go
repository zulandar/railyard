package yardmaster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/pluginhost"
	"gorm.io/gorm"
)

// StatusProvider is the contract the HTTP server uses to satisfy
// GET /plugins/status AND POST /plugins/restart. It is satisfied by
// *pluginhost.Host via the Status() and Restart() methods. The
// indirection keeps the yardmaster package from importing pluginhost
// beyond the type-level dependency on pluginhost.Snapshot.
//
// Restart relaunches a single named plugin in the live host without
// restarting the yard (railyard-77h.13). It returns the prior state so
// the handler can render an "old-state -> new-state" line; an error for an
// unknown name or a failed relaunch.
type StatusProvider interface {
	Status() pluginhost.Snapshot
	Restart(ctx context.Context, name string) error
}

// Compile-time assertion that *pluginhost.Host satisfies StatusProvider.
// Without this, a signature drift on Host.Status() / Host.Restart() would
// only break the CLI build at the assignment site (pkg/cli/yardmaster.go).
// This catches it at the package that defines the interface.
var _ StatusProvider = (*pluginhost.Host)(nil)

// HealthServer provides HTTP health check endpoints for k8s probes.
type HealthServer struct {
	pollInterval time.Duration
	mu           sync.RWMutex
	lastPoll     time.Time
}

// NewHealthServer creates a HealthServer with the given poll interval.
func NewHealthServer(pollInterval time.Duration) *HealthServer {
	return &HealthServer{
		pollInterval: pollInterval,
		lastPoll:     time.Now(),
	}
}

// RecordPoll records the time of the latest daemon poll.
func (h *HealthServer) RecordPoll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastPoll = time.Now()
}

// IsReady returns true if the last poll was within 2x the poll interval.
func (h *HealthServer) IsReady() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return time.Since(h.lastPoll) < 2*h.pollInterval
}

// StartHealthServer starts an HTTP server with /healthz, /readyz, and
// /plugins/status endpoints. It blocks until ctx is cancelled. The server
// listens on the given port. provider may be nil, in which case
// /plugins/status returns an empty Snapshot.
func StartHealthServer(ctx context.Context, port int, hs *HealthServer, provider StatusProvider) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	return serveHealthOnListener(ctx, ln, hs, provider)
}

// serveHealthOnListener serves the health endpoints on the supplied
// listener. Factored out of StartHealthServer so tests can bind on :0,
// keep the listener open, and pass it in — no port-grab race between
// the test's Close() and the server's rebind.
func serveHealthOnListener(ctx context.Context, ln net.Listener, hs *HealthServer, provider StatusProvider) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if hs.IsReady() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("not ready: last poll too old"))
		}
	})
	mux.HandleFunc("/plugins/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Always emit a well-formed Snapshot with a non-nil Plugins slice
		// so jq scripts that range over .plugins[] don't error against
		// the OSS binary (or any deployment running without a plugin
		// host wired in).
		snap := pluginhost.Snapshot{Plugins: []pluginhost.PluginStatus{}}
		if provider != nil {
			snap = provider.Status()
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(snap); err != nil {
			// Body header already written; just log.
			slog.Default().Error("plugins/status: encode", "err", err)
		}
	})

	// POST /plugins/restart?name=<name> relaunches a single plugin in the
	// running host without restarting the yard (railyard-77h.13).
	mux.HandleFunc("/plugins/restart", makeRestartHandler(provider))

	srv := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// makeRestartHandler builds the POST /plugins/restart handler. This is the
// only MUTATING route on the health server, which binds 0.0.0.0 so k8s
// probes can reach /healthz and /readyz; restart is therefore gated to
// loopback callers (railyard-uv8.5) — the ry CLI talks to it over
// 127.0.0.1. Without the gate, anyone with network reach could restart
// plugins, revive a deliberately crash-disabled plugin (restart resets the
// crash budget), or restart-loop one. Extracted as a named function so the
// loopback gate is unit-testable. The response JSON carries the old and new
// state strings so the CLI can render "old -> new"; failures return a
// non-2xx with a JSON {"error": "..."} body.
func makeRestartHandler(provider StatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Loopback-only: refuse any caller that is not on the local
		// loopback interface. The listener binds all interfaces for the
		// read-only probes, but this mutation must not be network-reachable.
		if !isLoopbackRemoteAddr(r.RemoteAddr) {
			writeRestartError(w, http.StatusForbidden,
				"plugins/restart is restricted to loopback callers")
			return
		}
		name := r.URL.Query().Get("name")
		if name == "" {
			writeRestartError(w, http.StatusBadRequest, "missing required query param: name")
			return
		}
		if provider == nil {
			writeRestartError(w, http.StatusServiceUnavailable, "no plugin host is wired into this process")
			return
		}

		// Capture the prior state from the snapshot so the response can
		// report "old -> new". A name absent from the snapshot reads as
		// "unknown" — Restart itself returns the authoritative unknown-name
		// error below, so this is only for display.
		oldState := pluginStateFromSnapshot(provider.Status(), name)

		if err := provider.Restart(r.Context(), name); err != nil {
			writeRestartError(w, restartErrorStatus(err), err.Error())
			return
		}

		newState := pluginStateFromSnapshot(provider.Status(), name)
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(restartResponse{Name: name, OldState: oldState, NewState: newState}); err != nil {
			slog.Default().Error("plugins/restart: encode", "err", err)
		}
	}
}

// restartErrorStatus maps a Host.Restart error to an HTTP status code
// (railyard-uv8.8): only genuine client errors are 4xx. An unknown plugin
// name is a bad request (400); a restart already in progress is a conflict
// (409); the host shutting down is transient (503); everything else — a
// failed relaunch (launch/Init/Start) — is a server error (500).
func restartErrorStatus(err error) int {
	switch {
	case errors.Is(err, pluginhost.ErrPluginNotFound):
		return http.StatusBadRequest
	case errors.Is(err, pluginhost.ErrRestartInProgress):
		return http.StatusConflict
	case errors.Is(err, pluginhost.ErrHostShuttingDown):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// isLoopbackRemoteAddr reports whether an http.Request RemoteAddr
// ("host:port") resolves to the loopback interface. A malformed or
// non-loopback address returns false (deny) (railyard-uv8.5).
func isLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// restartResponse is the JSON body returned by POST /plugins/restart on
// success (railyard-77h.13). OldState/NewState are the plugin's snapshot
// status before and after the relaunch, so the CLI can render
// "old -> new". A revived plugin reads e.g. "disabled -> running".
type restartResponse struct {
	Name     string `json:"name"`
	OldState string `json:"old_state"`
	NewState string `json:"new_state"`
}

// restartErrorBody is the JSON shape for a /plugins/restart failure.
type restartErrorBody struct {
	Error string `json:"error"`
}

// writeRestartError writes a JSON {"error": msg} body with the given
// status code.
func writeRestartError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(restartErrorBody{Error: msg}); err != nil {
		slog.Default().Error("plugins/restart: encode error body", "err", err)
	}
}

// pluginStateFromSnapshot returns the status string for `name` in snap, or
// "unknown" if the snapshot has no row for it.
func pluginStateFromSnapshot(snap pluginhost.Snapshot, name string) string {
	for _, p := range snap.Plugins {
		if p.Name == name {
			return p.Status
		}
	}
	return "unknown"
}

// DefaultStaleThreshold is the default time after which an engine is considered stale.
const DefaultStaleThreshold = 60 * time.Second

// CheckEngineHealth returns engines where last_activity is older than threshold
// and status is not "dead".
func CheckEngineHealth(db *gorm.DB, threshold time.Duration) ([]models.Engine, error) {
	if db == nil {
		return nil, fmt.Errorf("yardmaster: db is required")
	}
	if threshold <= 0 {
		return nil, fmt.Errorf("yardmaster: threshold must be positive")
	}

	cutoff := time.Now().Add(-threshold)
	var engines []models.Engine
	if err := db.Where("last_activity < ? AND status != ?", cutoff, "dead").
		Find(&engines).Error; err != nil {
		return nil, fmt.Errorf("yardmaster: check engine health: %w", err)
	}
	return engines, nil
}

// StaleEngines is a convenience wrapper using the default 60s threshold.
func StaleEngines(db *gorm.DB) ([]models.Engine, error) {
	return CheckEngineHealth(db, DefaultStaleThreshold)
}

// ReassignCar releases a car from a stalled/dead engine so it can be reclaimed.
// It sets the car status to "open", clears the assignee, marks the old engine
// as dead, writes a progress note, and sends a broadcast notification.
//
// The release is conditional: the car must still be assigned to fromEngineID
// and in an active status (claimed/in_progress). If it moved on — completed,
// merged, or already reassigned to another engine — the car is left untouched,
// no note or broadcast is written, and ReassignCar returns (false, nil); the
// engine is still marked dead either way, since the caller established its
// staleness (railyard-h2v).
func ReassignCar(db *gorm.DB, carID, fromEngineID, reason string) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("yardmaster: db is required")
	}
	if carID == "" {
		return false, fmt.Errorf("yardmaster: carID is required")
	}
	if fromEngineID == "" {
		return false, fmt.Errorf("yardmaster: fromEngineID is required")
	}

	reassigned := false
	err := db.Transaction(func(tx *gorm.DB) error {
		// Release the car only if this engine still actively holds it.
		result := tx.Model(&models.Car{}).
			Where("id = ? AND assignee = ? AND status IN ?", carID, fromEngineID, []string{"claimed", "in_progress"}).
			Updates(map[string]interface{}{
				"status":   "open",
				"assignee": "",
			})
		if result.Error != nil {
			return fmt.Errorf("yardmaster: release car %s: %w", carID, result.Error)
		}
		reassigned = result.RowsAffected > 0

		// Mark the engine as dead and clear its current car. This happens even
		// when the car moved on: the engine itself is still stale.
		if err := tx.Model(&models.Engine{}).Where("id = ?", fromEngineID).Updates(map[string]interface{}{
			"status":      "dead",
			"current_car": "",
		}).Error; err != nil {
			return fmt.Errorf("yardmaster: mark engine %s dead: %w", fromEngineID, err)
		}

		if !reassigned {
			return nil
		}

		// Write progress note.
		note := fmt.Sprintf("Reassigned from engine %s: %s", fromEngineID, reason)
		if err := tx.Create(&models.CarProgress{
			CarID:        carID,
			EngineID:     fromEngineID,
			Note:         note,
			FilesChanged: "[]",
			CreatedAt:    time.Now(),
		}).Error; err != nil {
			return fmt.Errorf("yardmaster: progress note for car %s: %w", carID, err)
		}

		// Send broadcast notification.
		if _, err := messaging.Send(tx, "yardmaster", "broadcast", "reassignment",
			fmt.Sprintf("Car %s reassigned from stalled engine %s", carID, fromEngineID),
			messaging.SendOpts{CarID: carID, Priority: "urgent"},
		); err != nil {
			return fmt.Errorf("yardmaster: broadcast reassignment for car %s: %w", carID, err)
		}

		return nil
	})
	if err != nil {
		return false, err
	}
	return reassigned, nil
}
