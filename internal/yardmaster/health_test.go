package yardmaster

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCheckEngineHealth_NilDB(t *testing.T) {
	_, err := CheckEngineHealth(nil, 60*time.Second)
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestCheckEngineHealth_ZeroThreshold(t *testing.T) {
	// With nil db, db check happens first.
	_, err := CheckEngineHealth(nil, 0)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckEngineHealth_NegativeThreshold(t *testing.T) {
	_, err := CheckEngineHealth(nil, -1*time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStaleEngines_UsesDefault(t *testing.T) {
	// Just verify it calls CheckEngineHealth with nil db (returns error).
	_, err := StaleEngines(nil)
	if err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestDefaultStaleThreshold(t *testing.T) {
	if DefaultStaleThreshold != 60*time.Second {
		t.Errorf("DefaultStaleThreshold = %v, want 60s", DefaultStaleThreshold)
	}
}

func TestReassignCar_NilDB(t *testing.T) {
	err := ReassignCar(nil, "car-001", "eng-001", "stalled")
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestReassignCar_EmptyCarID(t *testing.T) {
	// nil db check comes first, then carID check.
	err := ReassignCar(nil, "", "eng-001", "stalled")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReassignCar_EmptyEngineID(t *testing.T) {
	// nil db check comes first, then field checks.
	err := ReassignCar(nil, "car-001", "", "stalled")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- HealthServer tests ---

func TestNewHealthServer(t *testing.T) {
	hs := NewHealthServer(30 * time.Second)
	if hs == nil {
		t.Fatal("expected non-nil HealthServer")
	}
	if !hs.IsReady() {
		t.Error("new HealthServer should be ready immediately")
	}
}

func TestHealthServer_IsReady_WithinInterval(t *testing.T) {
	hs := NewHealthServer(30 * time.Second)
	hs.RecordPoll()
	if !hs.IsReady() {
		t.Error("expected ready after RecordPoll")
	}
}

func TestHealthServer_NotReady_AfterTimeout(t *testing.T) {
	hs := NewHealthServer(1 * time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if hs.IsReady() {
		t.Error("expected not ready after timeout")
	}
}

func TestHealthServer_RecordPoll_ResetsReadiness(t *testing.T) {
	hs := NewHealthServer(1 * time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if hs.IsReady() {
		t.Error("expected not ready before RecordPoll")
	}
	hs.RecordPoll()
	if !hs.IsReady() {
		t.Error("expected ready after RecordPoll")
	}
}

// freePort returns an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestStartHealthServer_Healthz(t *testing.T) {
	port := freePort(t)
	hs := NewHealthServer(30 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartHealthServer(ctx, port, hs)
	// Wait for the server to start.
	waitForServer(t, port)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestStartHealthServer_Readyz_Ready(t *testing.T) {
	port := freePort(t)
	hs := NewHealthServer(30 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartHealthServer(ctx, port, hs)
	waitForServer(t, port)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", port))
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestStartHealthServer_Readyz_NotReady(t *testing.T) {
	port := freePort(t)
	hs := NewHealthServer(1 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartHealthServer(ctx, port, hs)
	waitForServer(t, port)

	// Wait for the poll interval to expire.
	time.Sleep(5 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", port))
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "not ready") {
		t.Errorf("body = %q, want contains 'not ready'", body)
	}
}

func TestStartHealthServer_ShutdownOnCancel(t *testing.T) {
	port := freePort(t)
	hs := NewHealthServer(30 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- StartHealthServer(ctx, port, hs)
	}()
	waitForServer(t, port)

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5 seconds")
	}
}

// waitForServer polls until the server is accepting connections or times out.
func waitForServer(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server on port %d did not start in time", port)
}
