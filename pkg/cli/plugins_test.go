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
	for _, want := range []string{"NAME", "ENABLED", "EXECUTABLE", "PINNED", "EVENTS", "COMMANDS", "PATH"} {
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

// TestPluginsListPinnedColumn asserts the `ry plugins list` PINNED column
// reflects whether a sha256 pin is configured (railyard-77h.15). Operators
// use it to audit which enabled plugins are integrity-pinned.
func TestPluginsListPinnedColumn(t *testing.T) {
	withStubDiscover(t, func(cfg *config.Config) ([]pluginhost.PluginCandidate, error) {
		return []pluginhost.PluginCandidate{
			{Name: "pinned-one", Path: "/p/pinned-one", Executable: true, Enabled: true, Pinned: true},
			{Name: "unpinned-two", Path: "/p/unpinned-two", Executable: true, Enabled: true, Pinned: false},
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
	if !strings.Contains(got, "PINNED") {
		t.Errorf("header missing PINNED column:\n%s", got)
	}
	// Locate each row and verify the PINNED column reads yes/no.
	// Layout: NAME ENABLED EXECUTABLE PINNED EVENTS COMMANDS PATH
	for _, tc := range []struct {
		name    string
		wantCol string
	}{
		{"pinned-one", "yes"},
		{"unpinned-two", "no"},
	} {
		var row string
		for _, line := range strings.Split(got, "\n") {
			if strings.Contains(line, tc.name) {
				row = line
				break
			}
		}
		if row == "" {
			t.Fatalf("no row for %q in output:\n%s", tc.name, got)
		}
		fields := strings.Fields(row)
		if len(fields) < 4 || fields[3] != tc.wantCol {
			t.Errorf("PINNED column for %q = %q, want %q in row: %s", tc.name, fieldOr(fields, 3), tc.wantCol, row)
		}
	}
}

// fieldOr returns fields[i] or "" if out of range — keeps the assertion
// above from panicking on a malformed row.
func fieldOr(fields []string, i int) string {
	if i < len(fields) {
		return fields[i]
	}
	return ""
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

// TestPluginsListSurfacesNonExecutables seeds a plugins dir with a file
// that lacks the exec bit, then runs `ry plugins list` end-to-end and
// asserts the file appears with EXECUTABLE=no. This is the user-facing
// acceptance criterion from railyard-4px — operators who drop a binary
// without +x must see a diagnostic row, not "no plugins found".
func TestPluginsListSurfacesNonExecutables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable bit semantics")
	}

	pluginsDir := t.TempDir()
	nonExecPath := filepath.Join(pluginsDir, "trainmaster-plugin")
	if err := os.WriteFile(nonExecPath, []byte("not an executable"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	withStubConfigLoad(t, func(string) (*config.Config, error) {
		return &config.Config{
			Plugins: config.PluginsConfig{PluginsDir: pluginsDir},
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
	if !strings.Contains(got, "trainmaster-plugin") {
		t.Errorf("expected plugin name in output:\n%s", got)
	}
	if !strings.Contains(got, nonExecPath) {
		t.Errorf("expected path %q in output:\n%s", nonExecPath, got)
	}
	// Locate the row for our seeded plugin and confirm EXECUTABLE column
	// reads "no". Scan line-by-line so other rows (system plugins.d) can't
	// cause a false positive on "no" appearing elsewhere.
	var row string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "trainmaster-plugin") && strings.Contains(line, nonExecPath) {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("no row found for seeded plugin in output:\n%s", got)
	}
	// Row layout: NAME ENABLED EXECUTABLE EVENTS COMMANDS PATH
	// The 3rd whitespace-separated field should be "no".
	fields := strings.Fields(row)
	if len(fields) < 3 || fields[2] != "no" {
		t.Errorf("EXECUTABLE column = %q, want %q in row: %s", fields[2], "no", row)
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
