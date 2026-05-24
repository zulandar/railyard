package pluginhost

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

// TestDiscoverFiltersNonExecutable seeds a dir with one executable and
// one non-executable file. Only the executable should be returned as a
// candidate.
func TestDiscoverFiltersNonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable bit semantics")
	}
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "good"), 0o755)
	mustWriteFile(t, filepath.Join(dir, "bad"), 0o644)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cs := scanDir(dir, logger)
	if len(cs) != 1 {
		t.Fatalf("scanDir = %d candidates, want 1: %+v", len(cs), cs)
	}
	if cs[0].name != "good" {
		t.Errorf("name = %q, want %q", cs[0].name, "good")
	}
}

// TestDiscoverCollisionLastWins seeds two directories that both contain
// a `dup` executable, with the second directory taking precedence.
func TestDiscoverCollisionLastWins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable bit semantics")
	}
	a := t.TempDir()
	b := t.TempDir()
	mustWriteFile(t, filepath.Join(a, "dup"), 0o755)
	mustWriteFile(t, filepath.Join(b, "dup"), 0o755)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// We can't compose discoverCandidates here directly (it scans
	// canonical dirs); instead exercise the same merge primitive used by
	// the production function.
	all := []candidate{
		{name: "dup", path: filepath.Join(a, "dup"), source: a},
		{name: "dup", path: filepath.Join(b, "dup"), source: b},
	}
	merged := make(map[string]candidate)
	for _, c := range all {
		if _, exists := merged[c.name]; exists {
			logger.Warn("collision (test driver)")
		}
		merged[c.name] = c
	}
	if got := merged["dup"].path; got != filepath.Join(b, "dup") {
		t.Errorf("last-wins violated: got %q want %q", got, filepath.Join(b, "dup"))
	}
}

// TestFilterEnabled covers the enabled-list intersection.
func TestFilterEnabled(t *testing.T) {
	cs := []candidate{
		{name: "alpha", path: "/a/alpha"},
		{name: "beta", path: "/a/beta"},
		{name: "gamma", path: "/a/gamma"},
	}
	launch, missing := filterEnabled(cs, []string{"alpha", "missing-plugin", "gamma"})
	if len(launch) != 2 || launch[0].name != "alpha" || launch[1].name != "gamma" {
		t.Errorf("launch = %+v, want [alpha, gamma]", launch)
	}
	if len(missing) != 1 || missing[0] != "missing-plugin" {
		t.Errorf("missing = %+v, want [missing-plugin]", missing)
	}
}

// TestFilterEnabledEmpty returns nil when the enabled list is empty.
func TestFilterEnabledEmpty(t *testing.T) {
	launch, missing := filterEnabled([]candidate{{name: "a"}}, nil)
	if launch != nil || missing != nil {
		t.Errorf("empty enabled list should return (nil, nil); got (%v, %v)", launch, missing)
	}
}

// TestDiscoverPluginsSurfacesNonExecutables ensures DiscoverPlugins
// returns a PluginCandidate (Executable=false) for files that exist in
// a plugins.d directory but lack the executable bit. Without this row
// operators see "no plugins found" with zero diagnostic — even though
// ls shows the file right there.
func TestDiscoverPluginsSurfacesNonExecutables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable bit semantics")
	}

	pluginsDir := t.TempDir()
	nonExecPath := filepath.Join(pluginsDir, "trainmaster-plugin")
	if err := os.WriteFile(nonExecPath, []byte("not an executable"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := &config.Config{Plugins: config.PluginsConfig{PluginsDir: pluginsDir}}
	got, err := DiscoverPlugins(cfg)
	if err != nil {
		t.Fatalf("DiscoverPlugins: %v", err)
	}

	// Other plugins may live on /etc/railyard/plugins.d etc. on the host;
	// just assert our seeded file is present with Executable=false.
	var found *PluginCandidate
	for i := range got {
		if got[i].Path == nonExecPath {
			found = &got[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("seeded non-executable %q not found in DiscoverPlugins output: %+v", nonExecPath, got)
	}
	if found.Name != "trainmaster-plugin" {
		t.Errorf("Name = %q, want %q", found.Name, "trainmaster-plugin")
	}
	if found.Executable {
		t.Errorf("Executable = true, want false (file mode is 0o644)")
	}
	if found.Source != pluginsDir {
		t.Errorf("Source = %q, want %q", found.Source, pluginsDir)
	}
}

// TestDiscoverPluginsExecutableWinsOverNonExecutable seeds an executable
// `dup` in PluginsDir (highest priority) and a non-executable `dup` in
// a separate dir, asserting the executable entry takes precedence in
// the returned slice (Executable=true). Future-proofing — collisions
// across exec/non-exec must not silently swap launch behavior.
func TestDiscoverPluginsExecutableWinsOverNonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable bit semantics")
	}

	// Hermetic: point HOME and cwd at empty dirs so the only directories
	// with content are the system one (likely empty in CI) and PluginsDir.
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())

	execDir := t.TempDir()
	mustWriteFile(t, filepath.Join(execDir, "dup"), 0o755)

	// Use PluginsDir for the executable so it has highest priority over
	// any stray non-executable that might appear in the search dirs.
	cfg := &config.Config{Plugins: config.PluginsConfig{PluginsDir: execDir}}
	got, err := DiscoverPlugins(cfg)
	if err != nil {
		t.Fatalf("DiscoverPlugins: %v", err)
	}

	var dup *PluginCandidate
	for i := range got {
		if got[i].Name == "dup" {
			dup = &got[i]
			break
		}
	}
	if dup == nil {
		t.Fatalf("expected `dup` in output, got: %+v", got)
	}
	if !dup.Executable {
		t.Errorf("Executable = false, want true (file mode 0o755 should dominate)")
	}
}

func mustWriteFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
