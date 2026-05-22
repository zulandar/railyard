package cli

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/pluginhost"
)

// TestPluginsListSubcommandWiring runs the cobra command end-to-end to
// ensure `ry plugins list` is reachable from the root command tree and
// produces a non-empty render. We swap pluginsListDiscover for a
// stub so the test never hits the real plugins.d filesystem.
func TestPluginsListSubcommandWiring(t *testing.T) {
	withStubDiscover(t, func(cfg *config.Config) ([]pluginhost.PluginCandidate, error) {
		return []pluginhost.PluginCandidate{
			{
				Name:          "trainmaster",
				Path:          "/etc/railyard/plugins.d/trainmaster",
				Source:        "/etc/railyard/plugins.d",
				Executable:    true,
				Enabled:       true,
				AllowEvents:   []string{"*"},
				AllowCommands: []string{"dispatch.start", "dispatch.cancel"},
				SocketPath:    "/tmp/railyard-0/plugins/trainmaster.sock",
			},
		}, nil
	})
	withStubConfigLoad(t, func(string) (*config.Config, error) {
		return &config.Config{}, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("`ry plugins list` failed: %v", err)
	}
	got := buf.String()
	// Header columns.
	for _, want := range []string{"NAME", "ENABLED", "EXECUTABLE", "EVENTS", "COMMANDS", "PATH"} {
		if !strings.Contains(got, want) {
			t.Errorf("header missing column %q in output:\n%s", want, got)
		}
	}
	// Row fields.
	if !strings.Contains(got, "trainmaster") {
		t.Errorf("expected plugin name in output:\n%s", got)
	}
	// "1" event allow entry, "2" command allow entries (non-verbose
	// renders counts).
	if !strings.Contains(got, "/etc/railyard/plugins.d/trainmaster") {
		t.Errorf("expected path in output:\n%s", got)
	}
}

// TestPluginsListEmpty exercises the "no plugins discovered" path.
func TestPluginsListEmpty(t *testing.T) {
	withStubDiscover(t, func(cfg *config.Config) ([]pluginhost.PluginCandidate, error) {
		return nil, nil
	})
	withStubConfigLoad(t, func(string) (*config.Config, error) {
		return &config.Config{}, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("`ry plugins list` failed: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "no plugins found") {
		t.Errorf("expected friendly empty message; got:\n%s", got)
	}
}

// TestPluginsListVerbose checks that the -v flag expands the allow
// lists into their comma-separated contents instead of counts.
func TestPluginsListVerbose(t *testing.T) {
	withStubDiscover(t, func(cfg *config.Config) ([]pluginhost.PluginCandidate, error) {
		return []pluginhost.PluginCandidate{
			{
				Name:          "alpha",
				Path:          "/p/alpha",
				Executable:    true,
				Enabled:       true,
				AllowEvents:   []string{"Car.Created", "Engine.Started"},
				AllowCommands: []string{"alpha.*"},
			},
		}, nil
	})
	withStubConfigLoad(t, func(string) (*config.Config, error) {
		return &config.Config{}, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "list", "-v"})
	if err := root.Execute(); err != nil {
		t.Fatalf("`ry plugins list -v` failed: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "Car.Created,Engine.Started") {
		t.Errorf("verbose mode did not expand events:\n%s", got)
	}
	if !strings.Contains(got, "alpha.*") {
		t.Errorf("verbose mode did not expand commands:\n%s", got)
	}
}

// TestPluginsListWithRealDiscovery sets up a temp plugins dir with one
// fake "plugin" binary, points config at it via PluginsDir, and runs
// the command without stubbing DiscoverPlugins — exercising the
// internal/pluginhost integration end-to-end.
func TestPluginsListWithRealDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable bit semantics")
	}

	pluginsDir := t.TempDir()
	binPath := filepath.Join(pluginsDir, "hello")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("seeding fake plugin: %v", err)
	}

	withStubConfigLoad(t, func(string) (*config.Config, error) {
		return &config.Config{
			Plugins: config.PluginsConfig{
				Enabled:    []string{"hello"},
				PluginsDir: pluginsDir,
				Settings: map[string]config.PluginSettings{
					"hello": {
						Allow: config.AllowConfig{
							Events:   []string{"*"},
							Commands: []string{"hello.*", "hello.greet"},
						},
					},
				},
			},
		}, nil
	})
	// Do NOT stub pluginsListDiscover — exercise the real path.

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("`ry plugins list` failed: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "hello") {
		t.Errorf("real-discovery test missing plugin name 'hello':\n%s", got)
	}
	if !strings.Contains(got, binPath) {
		t.Errorf("real-discovery test missing binary path %q:\n%s", binPath, got)
	}
	// 1 event allow + 2 command allow entries in non-verbose form.
	if !strings.Contains(got, "yes") {
		t.Errorf("real-discovery test missing enabled/executable yes markers:\n%s", got)
	}
}

// TestPluginsParentCmdHelp ensures `ry plugins` with no subcommand prints
// help rather than erroring — matches the pattern other parent commands
// (e.g. `ry overlay`) follow.
func TestPluginsParentCmdHelp(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins"})
	if err := root.Execute(); err != nil {
		t.Fatalf("`ry plugins` failed: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "list") {
		t.Errorf("`ry plugins` help should list the `list` subcommand:\n%s", got)
	}
}

// TestLogBootSummaryEmpty captures slog output and asserts the
// "loaded plugins: (none)" line fires when the host has no surviving
// plugins. The OSS binary path is the load-bearing case for this branch.
func TestLogBootSummaryEmpty(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	host := pluginhost.NewHost(pluginhost.Dependencies{})

	logBootSummary(logger, host)

	got := buf.String()
	if !strings.Contains(got, "loaded plugins: (none)") {
		t.Errorf("missing empty-case boot summary line:\n%s", got)
	}
}

// TestLogBootSummaryNonEmpty exercised the boot summary line when the
// host had non-empty Names(). Under the subprocess plugin model the
// only way to populate Names() is to actually launch a subprocess
// plugin — coverage for that path lives in
// internal/pluginhost/launch_test.go where the host owns the lifecycle.
// Re-wiring this CLI-side smoke check to spin up a subprocess (so it
// keeps testing logBootSummary specifically) is tracked by bd issue
// railyard-bjp.
func TestLogBootSummaryNonEmpty(t *testing.T) {
	t.Skip("legacy in-process registration removed; tracked by bd issue railyard-bjp")
}

// withStubDiscover swaps pluginsListDiscover for the test and restores
// it via t.Cleanup.
func withStubDiscover(t *testing.T, fn func(*config.Config) ([]pluginhost.PluginCandidate, error)) {
	t.Helper()
	orig := pluginsListDiscover
	pluginsListDiscover = fn
	t.Cleanup(func() { pluginsListDiscover = orig })
}

// withStubConfigLoad swaps pluginsListLoadConfig for the test and
// restores it via t.Cleanup.
func withStubConfigLoad(t *testing.T, fn func(string) (*config.Config, error)) {
	t.Helper()
	orig := pluginsListLoadConfig
	pluginsListLoadConfig = fn
	t.Cleanup(func() { pluginsListLoadConfig = orig })
}
