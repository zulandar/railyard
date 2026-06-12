package yardmaster

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zulandar/railyard/internal/pluginhost"
)

// TestRestartErrorStatus maps Host.Restart error categories to HTTP status
// codes (railyard-uv8.8): only genuine client errors are 4xx; server-side
// relaunch failures are 5xx.
func TestRestartErrorStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"unknown plugin", fmt.Errorf("pluginhost: unknown plugin %q: %w", "x", pluginhost.ErrPluginNotFound), http.StatusBadRequest},
		{"in progress", fmt.Errorf("wrap: %w", pluginhost.ErrRestartInProgress), http.StatusConflict},
		{"shutting down", fmt.Errorf("wrap: %w", pluginhost.ErrHostShuttingDown), http.StatusServiceUnavailable},
		{"relaunch failure", errors.New("pluginhost: restart \"x\": Init RPC: boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		if got := restartErrorStatus(c.err); got != c.want {
			t.Errorf("%s: restartErrorStatus = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestRestartHandlerRelaunchFailureIs5xx proves a server-side relaunch
// failure surfaces as 5xx, not 400 (railyard-uv8.8).
func TestRestartHandlerRelaunchFailureIs5xx(t *testing.T) {
	provider := &fakeStatusProvider{restartFn: func(string) error {
		return errors.New("pluginhost: restart \"p1\": Start RPC: subprocess died")
	}}
	h := makeRestartHandler(provider)

	req := httptest.NewRequest(http.MethodPost, "/plugins/restart?name=p1", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a relaunch failure", rec.Code)
	}
}

// TestIsLoopbackRemoteAddr covers the host-extraction + loopback check used
// to gate the mutating /plugins/restart route (railyard-uv8.5).
func TestIsLoopbackRemoteAddr(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:5000":   true,
		"[::1]:5000":       true,
		"10.0.0.5:5000":    false,
		"192.168.1.10:443": false,
		"203.0.113.7:8080": false,
		"":                 false,
		"garbage":          false,
	}
	for addr, want := range cases {
		if got := isLoopbackRemoteAddr(addr); got != want {
			t.Errorf("isLoopbackRemoteAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}

// TestRestartHandlerRejectsNonLoopback proves the restart route refuses a
// caller that is not on the loopback interface — the health server binds
// 0.0.0.0 for k8s probes, and restart is a privileged mutation that must
// not be reachable from the network (railyard-uv8.5).
func TestRestartHandlerRejectsNonLoopback(t *testing.T) {
	called := false
	provider := &fakeStatusProvider{restartFn: func(string) error { called = true; return nil }}
	h := makeRestartHandler(provider)

	req := httptest.NewRequest(http.MethodPost, "/plugins/restart?name=p1", nil)
	req.RemoteAddr = "10.0.0.5:40000" // a remote, non-loopback caller
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for non-loopback caller", rec.Code)
	}
	if called {
		t.Error("Restart must not be invoked for a non-loopback caller")
	}
}

// TestRestartHandlerAllowsLoopback proves the local CLI path (127.0.0.1 /
// ::1) still works after the loopback gate.
func TestRestartHandlerAllowsLoopback(t *testing.T) {
	provider := &fakeStatusProvider{}
	called := false
	provider.restartFn = func(string) error { called = true; return nil }
	h := makeRestartHandler(provider)

	for _, addr := range []string{"127.0.0.1:5000", "[::1]:5000"} {
		called = false
		req := httptest.NewRequest(http.MethodPost, "/plugins/restart?name=p1", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("addr %s: status = %d, want 200", addr, rec.Code)
		}
		if !called {
			t.Errorf("addr %s: Restart should be invoked for a loopback caller", addr)
		}
	}
}
