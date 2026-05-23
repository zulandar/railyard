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
type fakeStatusProvider struct{ snap pluginhost.Snapshot }

func (f *fakeStatusProvider) Status() pluginhost.Snapshot { return f.snap }

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
