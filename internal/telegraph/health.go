package telegraph

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// HealthChecker provides HTTP health check endpoints for k8s probes.
// It uses atomic operations for lock-free concurrent access.
type HealthChecker struct {
	pollInterval time.Duration
	connected    atomic.Bool  // set true after adapter.Connect(), false on disconnect
	lastPollNano atomic.Int64 // unix nanoseconds of last poll cycle
}

// NewHealthChecker creates a HealthChecker with the given poll interval.
// The last poll time is initialized to now so the checker is not immediately stale.
func NewHealthChecker(pollInterval time.Duration) *HealthChecker {
	hc := &HealthChecker{
		pollInterval: pollInterval,
	}
	hc.lastPollNano.Store(time.Now().UnixNano())
	return hc
}

// SetConnected sets the adapter connected state.
func (h *HealthChecker) SetConnected(v bool) {
	h.connected.Store(v)
}

// SetLastPoll records the time of the latest poll cycle.
func (h *HealthChecker) SetLastPoll(t time.Time) {
	h.lastPollNano.Store(t.UnixNano())
}

// IsReady returns true if the adapter is connected and the last poll was
// within 3x the poll interval.
func (h *HealthChecker) IsReady() bool {
	if !h.connected.Load() {
		return false
	}
	lastPoll := time.Unix(0, h.lastPollNano.Load())
	return time.Since(lastPoll) < 3*h.pollInterval
}

// LivenessHandler always returns HTTP 200 ok.
func (h *HealthChecker) LivenessHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// ReadinessHandler returns HTTP 200 when ready, or HTTP 503 when not ready.
func (h *HealthChecker) ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	if !h.connected.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("not ready: adapter not connected"))
		return
	}
	lastPoll := time.Unix(0, h.lastPollNano.Load())
	if time.Since(lastPoll) >= 3*h.pollInterval {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("not ready: last poll too old"))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// StartHealthServer starts an HTTP server with /healthz and /readyz endpoints.
// It blocks until ctx is cancelled. The server listens on the given port.
func StartHealthServer(ctx context.Context, port int, hc *HealthChecker) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", hc.LivenessHandler)
	mux.HandleFunc("/readyz", hc.ReadinessHandler)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
