package telegraph

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

// TestLivenessAlwaysReturns200 verifies LivenessHandler always returns 200.
func TestLivenessAlwaysReturns200(t *testing.T) {
	port := freePort(t)
	hc := NewHealthChecker(30 * time.Second)
	// connected=false, poll stale — liveness should still be 200
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartHealthServer(ctx, port, hc)
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

// TestLivenessAlwaysReturns200_WhenNotConnected verifies liveness is 200 even when not connected.
func TestLivenessAlwaysReturns200_WhenNotConnected(t *testing.T) {
	port := freePort(t)
	hc := NewHealthChecker(30 * time.Second)
	hc.SetConnected(false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartHealthServer(ctx, port, hc)
	waitForServer(t, port)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("liveness status = %d, want 200 even when not connected", resp.StatusCode)
	}
}

// TestReadiness503_WhenNotConnected verifies readiness returns 503 when connected=false.
func TestReadiness503_WhenNotConnected(t *testing.T) {
	port := freePort(t)
	hc := NewHealthChecker(30 * time.Second)
	hc.SetConnected(false)
	hc.SetLastPoll(time.Now()) // poll is fresh
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartHealthServer(ctx, port, hc)
	waitForServer(t, port)

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

// TestReadiness503_WhenPollStale verifies readiness returns 503 when poll is stale.
func TestReadiness503_WhenPollStale(t *testing.T) {
	port := freePort(t)
	hc := NewHealthChecker(1 * time.Millisecond)
	hc.SetConnected(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartHealthServer(ctx, port, hc)
	waitForServer(t, port)

	// Wait for poll to become stale (> 3x pollInterval)
	time.Sleep(10 * time.Millisecond)

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

// TestReadiness200_WhenConnectedAndFreshPoll verifies readiness returns 200 when healthy.
func TestReadiness200_WhenConnectedAndFreshPoll(t *testing.T) {
	port := freePort(t)
	hc := NewHealthChecker(30 * time.Second)
	hc.SetConnected(true)
	hc.SetLastPoll(time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartHealthServer(ctx, port, hc)
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

// TestSetLastPoll_ResetsStaleTimer verifies SetLastPoll restores readiness after stale.
func TestSetLastPoll_ResetsStaleTimer(t *testing.T) {
	hc := NewHealthChecker(1 * time.Millisecond)
	hc.SetConnected(true)

	// Let poll go stale
	time.Sleep(10 * time.Millisecond)
	if hc.IsReady() {
		t.Error("expected not ready (stale poll)")
	}

	// Reset poll time
	hc.SetLastPoll(time.Now())
	if !hc.IsReady() {
		t.Error("expected ready after SetLastPoll")
	}
}

// TestStartHealthServer_ShutdownOnCancel verifies the server shuts down cleanly.
func TestStartHealthServer_ShutdownOnCancel(t *testing.T) {
	port := freePort(t)
	hc := NewHealthChecker(30 * time.Second)
	hc.SetConnected(true)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- StartHealthServer(ctx, port, hc)
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

// TestIsReady_UnitTests verifies IsReady logic without HTTP.
func TestIsReady_NotConnected(t *testing.T) {
	hc := NewHealthChecker(30 * time.Second)
	hc.SetConnected(false)
	hc.SetLastPoll(time.Now())
	if hc.IsReady() {
		t.Error("expected not ready when not connected")
	}
}

func TestIsReady_ConnectedAndFresh(t *testing.T) {
	hc := NewHealthChecker(30 * time.Second)
	hc.SetConnected(true)
	hc.SetLastPoll(time.Now())
	if !hc.IsReady() {
		t.Error("expected ready when connected and fresh poll")
	}
}

func TestIsReady_ConnectedButStale(t *testing.T) {
	hc := NewHealthChecker(1 * time.Millisecond)
	hc.SetConnected(true)
	time.Sleep(10 * time.Millisecond)
	if hc.IsReady() {
		t.Error("expected not ready when poll is stale")
	}
}
