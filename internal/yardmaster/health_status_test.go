package yardmaster

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/pluginhost"
)

// fakeStatusProvider satisfies StatusProvider for handler tests without
// constructing a real *pluginhost.Host.
//
// restartFn, when set, is invoked by Restart; the test can use it to flip
// the snapshot (simulating a revived plugin) and/or return an error.
type fakeStatusProvider struct {
	snap      pluginhost.Snapshot
	restartFn func(name string) error
}

func (f *fakeStatusProvider) Status() pluginhost.Snapshot { return f.snap }

func (f *fakeStatusProvider) Restart(_ context.Context, name string) error {
	if f.restartFn != nil {
		return f.restartFn(name)
	}
	return nil
}

// serveTestHealth binds on 127.0.0.1:0, keeps the listener open, and
// hands it to serveHealthOnListener. Returns the listener's URL base so
// the test can issue requests. Avoids the bind→Close→rebind port-grab
// race the earlier test had.
func serveTestHealth(t *testing.T, hs *HealthServer, provider StatusProvider) (urlBase string, cancel context.CancelFunc) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ctx, cancelFn := context.WithCancel(context.Background())
	go func() { _ = serveHealthOnListener(ctx, ln, hs, provider) }()
	return "http://" + addr, cancelFn
}

func TestHealthServerServesPluginsStatusJSON(t *testing.T) {
	hs := NewHealthServer(1 * time.Second)
	provider := &fakeStatusProvider{
		snap: pluginhost.Snapshot{
			Yardmaster: pluginhost.YardmasterInfo{Version: "test"},
			Plugins: []pluginhost.PluginStatus{
				{Name: "alpha", Status: pluginhost.StatusRunning, PID: 100},
			},
		},
	}

	urlBase, cancel := serveTestHealth(t, hs, provider)
	defer cancel()

	// Wait until the server is up.
	url := urlBase + "/plugins/status"
	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	var err error
	for time.Now().Before(deadline) {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET /plugins/status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}

	var got pluginhost.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Plugins) != 1 || got.Plugins[0].Name != "alpha" {
		t.Errorf("decoded snapshot = %+v", got)
	}
}

func TestHealthServerRejectsNonGetOnPluginsStatus(t *testing.T) {
	hs := NewHealthServer(1 * time.Second)
	provider := &fakeStatusProvider{}

	urlBase, cancel := serveTestHealth(t, hs, provider)
	defer cancel()

	url := urlBase + "/plugins/status"
	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	var err error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodPost, url, nil)
		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// TestHealthServerNilProviderJSONShape (railyard-k5z regression): when
// provider is nil the handler must emit "plugins":[] (not null) and must
// NOT leak a zero-time "booted_at".
func TestHealthServerNilProviderJSONShape(t *testing.T) {
	hs := NewHealthServer(1 * time.Second)

	urlBase, cancel := serveTestHealth(t, hs, nil)
	defer cancel()

	url := urlBase + "/plugins/status"
	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	var err error
	for time.Now().Before(deadline) {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bs := string(body)
	if strings.Contains(bs, `"plugins": null`) || strings.Contains(bs, `"plugins":null`) {
		t.Errorf("nil-provider must emit plugins:[], got:\n%s", bs)
	}
	if !strings.Contains(bs, `"plugins": []`) && !strings.Contains(bs, `"plugins":[]`) {
		t.Errorf("expected plugins:[] in response, got:\n%s", bs)
	}
	if strings.Contains(bs, "0001-01-01") {
		t.Errorf("zero-time leak in nil-provider response:\n%s", bs)
	}
}

// TestHealthServerRestartSuccess (railyard-77h.13) drives POST
// /plugins/restart through the real handler with a fake provider that
// flips a plugin from disabled to running, and asserts the JSON body
// reports "disabled -> running".
func TestHealthServerRestartSuccess(t *testing.T) {
	hs := NewHealthServer(1 * time.Second)
	provider := &fakeStatusProvider{
		snap: pluginhost.Snapshot{
			Plugins: []pluginhost.PluginStatus{
				{Name: "p1", Status: pluginhost.StatusDisabled},
			},
		},
	}
	// On restart, flip the snapshot to running so the post-restart Status()
	// reports the new state.
	provider.restartFn = func(string) error {
		provider.snap = pluginhost.Snapshot{
			Plugins: []pluginhost.PluginStatus{
				{Name: "p1", Status: pluginhost.StatusRunning},
			},
		}
		return nil
	}

	urlBase, cancel := serveTestHealth(t, hs, provider)
	defer cancel()

	url := urlBase + "/plugins/restart?name=p1"
	resp := pollPOST(t, url)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var got restartResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OldState != pluginhost.StatusDisabled || got.NewState != pluginhost.StatusRunning {
		t.Errorf("restart response = %+v, want disabled -> running", got)
	}
}

// TestHealthServerRestartUnknownName asserts the handler surfaces the
// provider's unknown-name error as a non-2xx with the message in the body.
func TestHealthServerRestartUnknownName(t *testing.T) {
	hs := NewHealthServer(1 * time.Second)
	provider := &fakeStatusProvider{}
	provider.restartFn = func(name string) error {
		return errUnknownTestPlugin(name)
	}

	urlBase, cancel := serveTestHealth(t, hs, provider)
	defer cancel()

	resp := pollPOST(t, urlBase+"/plugins/restart?name=ghost")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected non-200 for unknown plugin, got 200")
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ghost") {
		t.Errorf("error body missing plugin name: %s", body)
	}
}

// TestHealthServerRestartMissingName asserts a missing ?name returns 400.
func TestHealthServerRestartMissingName(t *testing.T) {
	hs := NewHealthServer(1 * time.Second)
	provider := &fakeStatusProvider{}

	urlBase, cancel := serveTestHealth(t, hs, provider)
	defer cancel()

	resp := pollPOST(t, urlBase+"/plugins/restart")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for missing name", resp.StatusCode)
	}
}

// TestHealthServerRestartRejectsGet asserts GET is not allowed on the
// restart endpoint (it is a mutating action).
func TestHealthServerRestartRejectsGet(t *testing.T) {
	hs := NewHealthServer(1 * time.Second)
	provider := &fakeStatusProvider{}

	urlBase, cancel := serveTestHealth(t, hs, provider)
	defer cancel()

	url := urlBase + "/plugins/restart?name=p1"
	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	var err error
	for time.Now().Before(deadline) {
		resp, err = http.Get(url) //nolint:bodyclose // closed below
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for GET on restart", resp.StatusCode)
	}
}

// pollPOST issues a POST to url, retrying until the test server is up.
func pollPOST(t *testing.T, url string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	var err error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodPost, url, nil)
		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			return resp
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("POST %s: %v", url, err)
	return nil
}

// errUnknownTestPlugin mimics the host's unknown-name error shape for the
// handler test (the real message comes from pluginhost).
func errUnknownTestPlugin(name string) error {
	return &unknownPluginError{name: name}
}

type unknownPluginError struct{ name string }

func (e *unknownPluginError) Error() string {
	return "pluginhost: unknown plugin \"" + e.name + "\"; known plugins: (none)"
}

// TestSnapshotSkippedRowOmitsLastActivity asserts the wire format for
// skipped plugins does NOT include a zero-time last_activity field —
// omitzero on PluginStatus.LastActivity drops it when zero.
func TestSnapshotSkippedRowOmitsLastActivity(t *testing.T) {
	hs := NewHealthServer(1 * time.Second)
	provider := &fakeStatusProvider{
		snap: pluginhost.Snapshot{
			Plugins: []pluginhost.PluginStatus{
				{Name: "ghost", Status: pluginhost.StatusSkipped, Error: "not found in: /etc"},
			},
		},
	}

	urlBase, cancel := serveTestHealth(t, hs, provider)
	defer cancel()

	url := urlBase + "/plugins/status"
	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	var err error
	for time.Now().Before(deadline) {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bs := string(body)
	if strings.Contains(bs, "0001-01-01") {
		t.Errorf("skipped row leaked zero-time last_activity:\n%s", bs)
	}
}
