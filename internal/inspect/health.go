package inspect

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

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

// registerHealthHandlers adds /healthz and /readyz to the mux.
func registerHealthHandlers(mux *http.ServeMux, hs *HealthServer) {
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
}

// StartHealthServer starts an HTTP server with /healthz and /readyz endpoints.
func StartHealthServer(ctx context.Context, port int, hs *HealthServer) error {
	mux := http.NewServeMux()
	registerHealthHandlers(mux, hs)

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
