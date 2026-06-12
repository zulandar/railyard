package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
)

// TestPluginsRestartCmd_RendersOldToNew exercises the CLI through the
// stubbed pluginsRestartPost seam: a successful restart prints
// "name: old -> new" to stdout.
func TestPluginsRestartCmd_RendersOldToNew(t *testing.T) {
	orig := pluginsRestartPost
	pluginsRestartPost = func(_ context.Context, _, name string, _ time.Duration) (*restartResult, error) {
		return &restartResult{Name: name, OldState: "disabled", NewState: "running"}, nil
	}
	t.Cleanup(func() { pluginsRestartPost = orig })

	withStubConfigLoad(t, func(string) (*config.Config, error) { return &config.Config{}, nil })

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "restart", "trainmaster", "--url", "http://example/plugins/restart"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "trainmaster: disabled -> running") {
		t.Errorf("output missing 'trainmaster: disabled -> running':\n%s", got)
	}
}

// TestPluginsRestartCmd_SurfacesError asserts an error from the POST (e.g.
// unknown name) is surfaced non-zero with the message on stderr.
func TestPluginsRestartCmd_SurfacesError(t *testing.T) {
	orig := pluginsRestartPost
	pluginsRestartPost = func(_ context.Context, _, name string, _ time.Duration) (*restartResult, error) {
		return nil, &cliTestError{msg: "unknown plugin \"" + name + "\""}
	}
	t.Cleanup(func() { pluginsRestartPost = orig })

	withStubConfigLoad(t, func(string) (*config.Config, error) { return &config.Config{}, nil })

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "restart", "ghost", "--url", "http://example/plugins/restart"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected non-nil error for unknown plugin")
	}
	if !strings.Contains(buf.String(), "ghost") {
		t.Errorf("stderr missing plugin name:\n%s", buf.String())
	}
}

type cliTestError struct{ msg string }

func (e *cliTestError) Error() string { return e.msg }

// TestPluginsRestartIntegration boots an httptest server that mimics the
// yardmaster POST /plugins/restart handler and drives the REAL default
// POST (no pluginsRestartPost stub) through it end-to-end. This catches:
//   - name query-param composition in defaultPluginsRestartPost,
//   - JSON tag drift between the handler's restartResponse and the CLI's
//     restartResult,
//   - the non-2xx {"error": ...} decode path.
func TestPluginsRestartIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/plugins/restart" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := r.URL.Query().Get("name")
		if name == "unknown-plugin" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "pluginhost: unknown plugin \"unknown-plugin\""})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"name":      name,
			"old_state": "running",
			"new_state": "running",
		})
	}))
	defer server.Close()

	withStubConfigLoad(t, func(string) (*config.Config, error) { return &config.Config{}, nil })

	// Happy path: real POST against the httptest server.
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "restart", "trainmaster", "--url", server.URL + "/plugins/restart"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute happy path: %v", err)
	}
	if !strings.Contains(buf.String(), "trainmaster: running -> running") {
		t.Errorf("happy-path output unexpected:\n%s", buf.String())
	}

	// Error path: unknown plugin -> non-2xx decoded into a clean message.
	root2 := newRootCmd()
	var buf2 bytes.Buffer
	root2.SetOut(&buf2)
	root2.SetErr(&buf2)
	root2.SetArgs([]string{"plugins", "restart", "unknown-plugin", "--url", server.URL + "/plugins/restart"})
	if err := root2.Execute(); err == nil {
		t.Fatal("expected error for unknown-plugin")
	}
	if !strings.Contains(buf2.String(), "unknown plugin") {
		t.Errorf("error-path output missing decoded message:\n%s", buf2.String())
	}
}
