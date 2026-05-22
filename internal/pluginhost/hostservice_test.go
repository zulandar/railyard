package pluginhost

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/pkg/plugin"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// TestHostServiceYardInfo confirms the wire response mirrors what
// Host.YardInfo() returns.
func TestHostServiceYardInfo(t *testing.T) {
	host := NewHost(Dependencies{
		Cfg: &config.Config{
			Owner:   "alice",
			Project: "yard",
			Repo:    "https://example.com/repo",
		},
		RailyardVersion: "v1.2.3",
		BuildCommit:     "abc123",
		BuildTime:       time.Unix(1700000000, 0).UTC(),
	})
	hs := newHostService(host, "p1")
	resp, err := hs.YardInfo(context.Background(), &protov1.YardInfoRequest{})
	if err != nil {
		t.Fatalf("YardInfo: %v", err)
	}
	if resp.Owner != "alice" {
		t.Errorf("Owner = %q", resp.Owner)
	}
	if resp.Project != "yard" {
		t.Errorf("Project = %q", resp.Project)
	}
	if resp.RailyardVersion != "v1.2.3" {
		t.Errorf("RailyardVersion = %q", resp.RailyardVersion)
	}
	if resp.BuildTime == nil || resp.BuildTime.AsTime().Unix() != 1700000000 {
		t.Errorf("BuildTime = %v", resp.BuildTime)
	}
}

// TestHostServiceConfig returns the named plugin's YAML block when
// present.
func TestHostServiceConfig(t *testing.T) {
	// Mimic what config.Parse does: stash an unknown top-level node.
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("hello:\n  greeting: hola\n"), &n); err != nil {
		t.Fatalf("unmarshal seed: %v", err)
	}
	// n is a DocumentNode whose first content is a mapping with one key
	// "hello"; we want the inner value node (the mapping that contains
	// `greeting`).
	cfg := &config.Config{
		PluginConfigs: map[string]yaml.Node{
			"hello": *n.Content[0].Content[1],
		},
	}
	host := NewHost(Dependencies{Cfg: cfg})
	hs := newHostService(host, "hello")

	resp, err := hs.Config(context.Background(), &protov1.ConfigRequest{Name: "hello"})
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if !resp.Present {
		t.Fatal("expected Present=true")
	}
	if len(resp.ConfigYaml) == 0 {
		t.Fatal("expected non-empty config_yaml")
	}

	// Missing key returns present=false.
	missing, err := hs.Config(context.Background(), &protov1.ConfigRequest{Name: "absent"})
	if err != nil {
		t.Fatalf("Config(absent): %v", err)
	}
	if missing.Present {
		t.Error("expected Present=false for absent key")
	}
}

// TestHostServiceDispatchCommandCore routes a core allow-list command
// through HostService.DispatchCommand.
func TestHostServiceDispatchCommandCore(t *testing.T) {
	var seen string
	host := NewHost(Dependencies{
		Cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: []string{"p1"},
				Settings: map[string]config.PluginSettings{
					"p1": {Allow: config.AllowConfig{Commands: []string{"*"}}},
				},
			},
		},
		PauseYardFn: func(_ context.Context, reason string) error {
			seen = reason
			return nil
		},
	})
	hs := newHostService(host, "p1")
	resp, err := hs.DispatchCommand(context.Background(), &protov1.DispatchCommandRequest{
		Name: "pause_yard",
	})
	if err != nil {
		t.Fatalf("DispatchCommand: %v", err)
	}
	if !resp.Success {
		t.Errorf("Success = false, want true; Error=%q", resp.Error)
	}
	_ = seen // reason is best-effort
}

// TestHostServiceDispatchCommandUnknown returns an in-band Error rather
// than a gRPC error. The plugin must hold the allow-list cap for the
// command name — otherwise we'd PermissionDenied before reaching the
// "unknown command" branch.
func TestHostServiceDispatchCommandUnknown(t *testing.T) {
	host := NewHost(Dependencies{
		Cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: []string{"p1"},
				Settings: map[string]config.PluginSettings{
					"p1": {Allow: config.AllowConfig{Commands: []string{"*"}}},
				},
			},
		},
	})
	hs := newHostService(host, "p1")
	resp, err := hs.DispatchCommand(context.Background(), &protov1.DispatchCommandRequest{
		Name: "nope",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("Success = true, want false")
	}
	if resp.Error == "" {
		t.Error("expected non-empty Error")
	}
}

// TestHostServiceDispatchCommandDeniedByAllowList confirms the
// allow-list check fires BEFORE routing. A plugin with no allow-list
// entries (or an entry that doesn't cover the name) gets
// PermissionDenied.
func TestHostServiceDispatchCommandDeniedByAllowList(t *testing.T) {
	host := NewHost(Dependencies{
		// No PauseYardFn — but we don't expect to reach it; the
		// allow-list check should refuse the call first.
		Cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: []string{"p1"},
				// No Settings entry → strict default → everything denied.
			},
		},
	})
	hs := newHostService(host, "p1")
	_, err := hs.DispatchCommand(context.Background(), &protov1.DispatchCommandRequest{
		Name: "pause_yard",
	})
	if err == nil {
		t.Fatal("expected PermissionDenied error, got nil")
	}
	if got, want := status.Code(err), codes.PermissionDenied; got != want {
		t.Errorf("status code = %v, want %v", got, want)
	}
}

// TestHostServiceDispatchCommandPrefixWildcard confirms a "ns.*" wildcard
// in the allow-list permits commands under that namespace.
func TestHostServiceDispatchCommandPrefixWildcard(t *testing.T) {
	host := NewHost(Dependencies{
		Cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: []string{"p1"},
				Settings: map[string]config.PluginSettings{
					"p1": {Allow: config.AllowConfig{Commands: []string{"foo.*"}}},
				},
			},
		},
	})
	hs := newHostService(host, "p1")
	// foo.bar passes the allow-list, then routes — but no handler →
	// in-band "command not allowed: foo.bar" response (not gRPC error).
	resp, err := hs.DispatchCommand(context.Background(), &protov1.DispatchCommandRequest{
		Name: "foo.bar",
	})
	if err != nil {
		t.Fatalf("unexpected gRPC error: %v", err)
	}
	if resp == nil || resp.Success {
		t.Errorf("unexpected response: %+v", resp)
	}
	// Different namespace is refused at the allow-list step.
	_, err = hs.DispatchCommand(context.Background(), &protov1.DispatchCommandRequest{
		Name: "other.cmd",
	})
	if err == nil {
		t.Fatal("expected PermissionDenied error, got nil")
	}
	if got, want := status.Code(err), codes.PermissionDenied; got != want {
		t.Errorf("status code = %v, want %v", got, want)
	}
}

// TestHostServiceLog forwards a record to the host logger without error.
// We do not capture slog output here — the verbose handler test lives
// in lifecycle_log_test.go's legacy suite (railyard-bjp will rebuild it
// for the subprocess world).
func TestHostServiceLog(t *testing.T) {
	host := NewHost(Dependencies{})
	hs := newHostService(host, "p1")
	_, err := hs.Log(context.Background(), &protov1.LogRequest{
		Level:   int32(0), // INFO
		Message: "hello world",
		Attrs:   map[string]string{"k": "v"},
	})
	if err != nil {
		t.Errorf("Log: %v", err)
	}
}

// suppress unused import warnings when the plugin package isn't referenced.
var _ = plugin.CarCreated
