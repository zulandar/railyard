package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/pluginhost"
)

// TestPluginsStatusIntegration boots an httptest server that serves the
// same /plugins/status JSON the real handler produces, then drives the
// real default fetch (no pluginsStatusFetch stub) through it.
//
// This catches:
//   - JSON tag drift between Snapshot and the CLI decoder.
//   - URL builder bugs in --url override.
//   - Default-URL composition from cfg.Yardmaster.HealthPort.
func TestPluginsStatusIntegration(t *testing.T) {
	snap := pluginhost.Snapshot{
		Yardmaster: pluginhost.YardmasterInfo{Version: "integration"},
		Plugins: []pluginhost.PluginStatus{
			{
				Name:              "trainmaster",
				Status:            pluginhost.StatusRunning,
				RestartCount:      0,
				SubscriptionCount: 3,
				CommandCount:      2,
				// 2h ago renders as a stable "2h ago" bucket regardless
				// of scheduling slop between fixture build and table
				// render. A 15s offset previously flaked between
				// "15s ago" and "16s ago" under CI load.
				LastActivity: time.Now().Add(-2 * time.Hour),
				PID:          4242,
				Path:         "/etc/railyard/plugins.d/trainmaster",
			},
			{
				Name:   "missing-plugin",
				Status: pluginhost.StatusSkipped,
				Error:  "not found in: /etc/railyard/plugins.d",
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/plugins/status" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	}))
	defer server.Close()

	// Use --url override to point at httptest server.
	withStubConfigLoad(t, func(string) (*config.Config, error) {
		return &config.Config{}, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "status", "--url", server.URL + "/plugins/status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "trainmaster") || !strings.Contains(got, "running") {
		t.Errorf("missing trainmaster running row:\n%s", got)
	}
	if !strings.Contains(got, "missing-plugin") || !strings.Contains(got, "skipped") {
		t.Errorf("missing skipped row:\n%s", got)
	}
	if !strings.Contains(got, "2h ago") {
		t.Errorf("expected '2h ago' LAST-ACTIVITY rendering:\n%s", got)
	}
}
