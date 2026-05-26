package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/pluginhost"
)

func withStubStatusFetch(t *testing.T, fn func(ctx context.Context, url string, timeout time.Duration) (*pluginhost.Snapshot, error)) {
	t.Helper()
	orig := pluginsStatusFetch
	pluginsStatusFetch = fn
	t.Cleanup(func() { pluginsStatusFetch = orig })
}

func TestPluginsStatusTableOutput(t *testing.T) {
	withStubConfigLoad(t, func(string) (*config.Config, error) {
		return &config.Config{Yardmaster: config.YardmasterConfig{HealthPort: 8081}}, nil
	})
	withStubStatusFetch(t, func(ctx context.Context, url string, timeout time.Duration) (*pluginhost.Snapshot, error) {
		if !strings.Contains(url, "8081") {
			t.Errorf("expected default URL to use HealthPort 8081, got %q", url)
		}
		return &pluginhost.Snapshot{
			Plugins: []pluginhost.PluginStatus{
				{Name: "trainmaster", Status: pluginhost.StatusRunning, PID: 42, SubscriptionCount: 2, CommandCount: 1, LastActivity: time.Now().Add(-30 * time.Second), Path: "/etc/railyard/plugins.d/trainmaster"},
				{Name: "broken", Status: pluginhost.StatusFailed, Error: "init: boom"},
			},
		}, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("`ry plugins status` failed: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"NAME", "STATUS", "RESTARTS", "SUBS", "CMDS", "LAST-ACTIVITY", "PID", "PATH", "ERROR"} {
		if !strings.Contains(got, want) {
			t.Errorf("header missing column %q in output:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "trainmaster") || !strings.Contains(got, "running") {
		t.Errorf("missing running row:\n%s", got)
	}
	if !strings.Contains(got, "broken") || !strings.Contains(got, "failed") {
		t.Errorf("missing failed row:\n%s", got)
	}
}

// TestPluginsStatusErrorColumn asserts the rendered table surfaces
// PluginStatus.Error for non-running plugins so operators can diagnose
// failed/disabled rows without falling back to --json. See railyard-kag.
func TestPluginsStatusErrorColumn(t *testing.T) {
	withStubConfigLoad(t, func(string) (*config.Config, error) {
		return &config.Config{Yardmaster: config.YardmasterConfig{HealthPort: 8081}}, nil
	})
	longErr := strings.Repeat("x", 200)
	withStubStatusFetch(t, func(ctx context.Context, url string, timeout time.Duration) (*pluginhost.Snapshot, error) {
		return &pluginhost.Snapshot{
			Plugins: []pluginhost.PluginStatus{
				{Name: "good", Status: pluginhost.StatusRunning, PID: 42},
				{Name: "broken", Status: pluginhost.StatusFailed, Error: "init: handshake failed"},
				{Name: "noisy", Status: pluginhost.StatusFailed, Error: longErr},
			},
		}, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()

	if !strings.Contains(got, "ERROR") {
		t.Errorf("expected ERROR column header in output:\n%s", got)
	}

	lines := strings.Split(got, "\n")
	var goodLine, brokenLine, noisyLine string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(strings.TrimSpace(l), "good "):
			goodLine = l
		case strings.HasPrefix(strings.TrimSpace(l), "broken "):
			brokenLine = l
		case strings.HasPrefix(strings.TrimSpace(l), "noisy "):
			noisyLine = l
		}
	}
	if brokenLine == "" {
		t.Fatalf("missing 'broken' row in output:\n%s", got)
	}
	if !strings.Contains(brokenLine, "init: handshake failed") {
		t.Errorf("expected error text on 'broken' row, got:\n%s", brokenLine)
	}
	if goodLine != "" && strings.Contains(goodLine, "init:") {
		t.Errorf("running row should not carry an error, got:\n%s", goodLine)
	}
	if noisyLine == "" {
		t.Fatalf("missing 'noisy' row in output:\n%s", got)
	}
	if strings.Contains(noisyLine, longErr) {
		t.Errorf("expected long error to be truncated; got full string in:\n%s", noisyLine)
	}
	if !strings.Contains(noisyLine, "…") && !strings.Contains(noisyLine, "...") {
		t.Errorf("expected truncation ellipsis on long error, got:\n%s", noisyLine)
	}
}

func TestPluginsStatusJSONOutput(t *testing.T) {
	withStubConfigLoad(t, func(string) (*config.Config, error) {
		return &config.Config{Yardmaster: config.YardmasterConfig{HealthPort: 8081}}, nil
	})
	withStubStatusFetch(t, func(ctx context.Context, url string, timeout time.Duration) (*pluginhost.Snapshot, error) {
		return &pluginhost.Snapshot{
			Plugins: []pluginhost.PluginStatus{{Name: "trainmaster", Status: pluginhost.StatusRunning}},
		}, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "status", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"name": "trainmaster"`) && !strings.Contains(got, `"name":"trainmaster"`) {
		t.Errorf("expected JSON output, got:\n%s", got)
	}
}

func TestPluginsStatusConnectionRefusedHint(t *testing.T) {
	withStubConfigLoad(t, func(string) (*config.Config, error) {
		return &config.Config{Yardmaster: config.YardmasterConfig{HealthPort: 8081}}, nil
	})
	withStubStatusFetch(t, func(ctx context.Context, url string, timeout time.Duration) (*pluginhost.Snapshot, error) {
		return nil, errors.New("dial tcp 127.0.0.1:8081: connect: connection refused")
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "status"})
	err := root.Execute()
	if err == nil {
		t.Fatalf("expected non-nil error for connection refused")
	}
	combined := buf.String() + err.Error()
	if !strings.Contains(combined, "yardmaster not reachable") {
		t.Errorf("expected reachability hint in error/output, got:\n%s", combined)
	}
	if !strings.Contains(combined, "ry plugins list") {
		t.Errorf("expected hint to mention `ry plugins list`, got:\n%s", combined)
	}
}

func TestPluginsStatusUrlOverride(t *testing.T) {
	withStubConfigLoad(t, func(string) (*config.Config, error) {
		return &config.Config{}, nil
	})
	called := ""
	withStubStatusFetch(t, func(ctx context.Context, url string, timeout time.Duration) (*pluginhost.Snapshot, error) {
		called = url
		return &pluginhost.Snapshot{}, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "status", "--url", "http://example.invalid:9999/plugins/status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if called != "http://example.invalid:9999/plugins/status" {
		t.Errorf("fetch called with %q", called)
	}
}
