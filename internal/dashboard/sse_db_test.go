package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// startSSEServer starts a test HTTP server with a real SQLite DB wired to the
// SSE handler. It returns the base URL and a cleanup function.
func startSSEServer(t *testing.T, db *gorm.DB) (string, func()) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/health", func(c *gin.Context) { c.Status(200) })
	router.GET("/api/events", handleSSE(db))

	// Bind to port 0 to get an OS-assigned free port.
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	srv := &http.Server{Handler: router}
	go srv.Serve(listener)

	// Wait for server to be ready using a health endpoint (not SSE).
	baseURL := fmt.Sprintf("http://localhost:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	return baseURL, func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
	}
}

// readSSEEvent reads SSE lines from a scanner until it finds a complete event
// (terminated by a blank line). Returns the event name and data string.
func readSSEEvent(scanner *bufio.Scanner) (eventName, eventData string, ok bool) {
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// End of event block.
			if eventName != "" || eventData != "" {
				return eventName, eventData, true
			}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			eventName = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			eventData = strings.TrimPrefix(line, "data: ")
		}
	}
	return eventName, eventData, false
}

func TestSSE_EscalationDetection(t *testing.T) {
	db := testDB(t)
	baseURL, cleanup := startSSEServer(t, db)
	defer cleanup()

	// Connect to SSE endpoint with a dedicated client (no connection pooling).
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/events", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Use a channel to collect events from a background reader goroutine.
	type sseEvent struct {
		name string
		data string
	}
	events := make(chan sseEvent, 10)

	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for {
			name, data, ok := readSSEEvent(scanner)
			if !ok {
				close(events)
				return
			}
			events <- sseEvent{name: name, data: data}
		}
	}()

	// Read the "connected" event first.
	select {
	case evt := <-events:
		if evt.name != "connected" {
			t.Fatalf("first event = %q, want 'connected'", evt.name)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for connected event")
	}

	// Insert an escalation message AFTER connecting so lastSeenID won't include it.
	db.Create(&models.Message{
		FromAgent:    "test-agent",
		ToAgent:      "human",
		Subject:      "Need human review",
		Body:         "Something requires attention",
		Priority:     "urgent",
		CarID:        "car-42",
		Acknowledged: false,
	})

	// Wait for the escalation event (poll interval is 3s, allow up to 13s for ~4 polls).
	deadline := time.After(13 * time.Second)
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before receiving escalation")
			}
			if evt.name == "escalation" {
				// Verify data contains the subject.
				var parsed escalationEvent
				if err := json.Unmarshal([]byte(evt.data), &parsed); err != nil {
					t.Fatalf("unmarshal escalation data: %v", err)
				}
				if parsed.Subject != "Need human review" {
					t.Errorf("subject = %q, want 'Need human review'", parsed.Subject)
				}
				if parsed.From != "test-agent" {
					t.Errorf("from = %q, want 'test-agent'", parsed.From)
				}
				if parsed.CarID != "car-42" {
					t.Errorf("car_id = %q, want 'car-42'", parsed.CarID)
				}
				if parsed.Priority != "urgent" {
					t.Errorf("priority = %q, want 'urgent'", parsed.Priority)
				}
				if parsed.Count < 1 {
					t.Errorf("count = %d, want >= 1", parsed.Count)
				}
				return // success
			}
			// Skip heartbeat or other events.
		case <-deadline:
			t.Fatal("timeout waiting for escalation event (13s)")
		}
	}
}

func TestSSE_HeartbeatSent(t *testing.T) {
	db := testDB(t)
	baseURL, cleanup := startSSEServer(t, db)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/events", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()

	type sseEvent struct {
		name string
		data string
	}
	events := make(chan sseEvent, 10)

	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for {
			name, data, ok := readSSEEvent(scanner)
			if !ok {
				close(events)
				return
			}
			events <- sseEvent{name: name, data: data}
		}
	}()

	// Wait for a heartbeat event (should arrive within ~15s).
	deadline := time.After(18 * time.Second)
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before receiving heartbeat")
			}
			if evt.name == "heartbeat" {
				// Verify heartbeat contains a timestamp.
				var parsed map[string]string
				if err := json.Unmarshal([]byte(evt.data), &parsed); err != nil {
					t.Fatalf("unmarshal heartbeat data: %v", err)
				}
				ts, ok := parsed["timestamp"]
				if !ok || ts == "" {
					t.Error("heartbeat missing timestamp field")
				}
				// Verify timestamp is valid RFC3339.
				if _, err := time.Parse(time.RFC3339, ts); err != nil {
					t.Errorf("timestamp %q is not valid RFC3339: %v", ts, err)
				}
				return // success
			}
			// Skip other events.
		case <-deadline:
			t.Fatal("timeout waiting for heartbeat event (18s)")
		}
	}
}

func TestSSE_ContextCancellation(t *testing.T) {
	db := testDB(t)
	baseURL, cleanup := startSSEServer(t, db)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/events", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()

	// Read the "connected" event to confirm connection is established.
	scanner := bufio.NewScanner(resp.Body)
	name, _, ok := readSSEEvent(scanner)
	if !ok {
		t.Fatal("failed to read connected event")
	}
	if name != "connected" {
		t.Fatalf("first event = %q, want 'connected'", name)
	}

	// Cancel the request context.
	cancel()

	// The handler should return and the connection should close.
	// Attempting to read should fail/return false within a reasonable time.
	done := make(chan struct{})
	go func() {
		// This should return quickly once the connection is closed.
		for scanner.Scan() {
			// Drain any buffered data.
		}
		close(done)
	}()

	select {
	case <-done:
		// Connection closed cleanly.
	case <-time.After(5 * time.Second):
		t.Fatal("SSE handler did not close connection within 5s after context cancellation")
	}
}
