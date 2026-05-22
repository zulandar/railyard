package yardmaster

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/pluginhost"
)

// fakeStatusProvider satisfies StatusProvider for handler tests without
// constructing a real *pluginhost.Host.
type fakeStatusProvider struct{ snap pluginhost.Snapshot }

func (f *fakeStatusProvider) Status() pluginhost.Snapshot { return f.snap }

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

	// Bind on :0 to grab any free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	port := mustPort(t, addr)
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- StartHealthServer(ctx, port, hs, provider) }()

	// Wait until the server is up.
	url := "http://" + addr + "/plugins/status"
	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
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

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := mustPort(t, ln.Addr().String())
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = StartHealthServer(ctx, port, hs, provider) }()

	url := "http://" + addr + "/plugins/status"
	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
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

func mustPort(t *testing.T, addr string) int {
	t.Helper()
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host:port: %v", err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("parse port %q: %v", p, err)
	}
	return n
}
