package cli

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/pluginhost"
	"github.com/zulandar/railyard/pkg/plugin"
)

// bootFakePlugin is a minimal plugin.Plugin implementation used by the
// boot-summary tests. It does no work — the tests only need the host to
// remember the plugin's name so [pluginhost.Host.Names] can return it.
type bootFakePlugin struct {
	name string
}

func (p *bootFakePlugin) Name() string                                  { return p.name }
func (p *bootFakePlugin) Init(ctx context.Context, h plugin.Host) error { return nil }
func (p *bootFakePlugin) Start(ctx context.Context) error               { return nil }
func (p *bootFakePlugin) Stop(ctx context.Context) error                { return nil }

// TestPluginsListEmpty asserts the OSS-binary output: when no plugins are
// linked into the binary, the command prints the friendly fallback line
// rather than an empty table.
func TestPluginsListEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderPluginsList(&buf, nil); err != nil {
		t.Fatalf("renderPluginsList error: %v", err)
	}
	got := buf.String()
	want := "No plugins registered in this binary."
	if !strings.Contains(got, want) {
		t.Errorf("output = %q, want it to contain %q", got, want)
	}
}

// TestPluginsListWithEntries seeds two fake registry entries and asserts
// the rendered table includes the header row, both plugin names, and the
// build-time status / placeholder daemon and subscription cells.
//
// We feed the renderer directly rather than calling plugin.Register so
// the package-level registry stays clean for sibling tests (the registry
// is process-global and has no public reset).
func TestPluginsListWithEntries(t *testing.T) {
	var buf bytes.Buffer
	entries := []plugin.PluginEntry{
		{Name: "trainmaster", Factory: func() plugin.Plugin { return nil }},
		{Name: "audit-log", Factory: func() plugin.Plugin { return nil }},
	}
	if err := renderPluginsList(&buf, entries); err != nil {
		t.Fatalf("renderPluginsList error: %v", err)
	}
	got := buf.String()

	// Header row.
	if !strings.Contains(got, "NAME") || !strings.Contains(got, "STATUS") {
		t.Errorf("missing header row in output:\n%s", got)
	}
	if !strings.Contains(got, "DAEMONS") || !strings.Contains(got, "SUBSCRIPTIONS") {
		t.Errorf("missing daemons/subscriptions header in output:\n%s", got)
	}

	// Per-plugin rows. We don't lock in exact column widths (tabwriter
	// may vary with the longest name) — just assert the cells line up
	// per row.
	for _, name := range []string{"trainmaster", "audit-log"} {
		if !strings.Contains(got, name) {
			t.Errorf("missing plugin %q in output:\n%s", name, got)
		}
	}
	// "registered" status should appear at least once per plugin row.
	if c := strings.Count(got, "registered"); c < 2 {
		t.Errorf(`expected "registered" to appear at least twice (one per plugin), got %d:\n%s`, c, got)
	}

	// Daemons/subscriptions are unknown without IPC, so the renderer
	// emits "-" placeholders. We count occurrences loosely: with two
	// rows and two unknown columns per row we expect at least four "-"
	// substrings.
	if c := strings.Count(got, "-"); c < 4 {
		t.Errorf(`expected at least 4 "-" placeholder cells, got %d:\n%s`, c, got)
	}

	// Registration order must be preserved.
	tIdx := strings.Index(got, "trainmaster")
	aIdx := strings.Index(got, "audit-log")
	if tIdx < 0 || aIdx < 0 || tIdx > aIdx {
		t.Errorf("registration order not preserved: trainmaster idx=%d audit-log idx=%d", tIdx, aIdx)
	}
}

// TestPluginsListSubcommandWiring runs the cobra command end-to-end to
// ensure `ry plugins list` is reachable from the root command tree and
// emits the expected fallback when no plugins are linked. The OSS test
// binary registers zero plugins (sibling packages don't side-effect
// import any), so this test exercises the empty-case path through the
// real RunE.
func TestPluginsListSubcommandWiring(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("`ry plugins list` failed: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "No plugins registered") && !strings.Contains(got, "NAME") {
		t.Errorf("`ry plugins list` produced unexpected output:\n%s", got)
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

// TestLogBootSummaryNonEmpty registers two plugins on the host and asserts
// the boot summary line lists both names in registration order. The
// pluginhost-side lifecycle isn't invoked here — Names() reflects whatever
// is currently in the registered set, which for a pre-Init host is the
// raw registration order.
func TestLogBootSummaryNonEmpty(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	host := pluginhost.NewHost(pluginhost.Dependencies{})
	host.Register(&bootFakePlugin{name: "trainmaster"})
	host.Register(&bootFakePlugin{name: "audit-log"})

	logBootSummary(logger, host)

	got := buf.String()
	if !strings.Contains(got, "loaded plugins: trainmaster, audit-log") {
		t.Errorf("missing expected boot summary line:\n%s", got)
	}
}
