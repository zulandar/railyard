package cli

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/pluginhost"
)

// TestPluginsListSubcommandWiring runs the cobra command end-to-end to
// ensure `ry plugins list` is reachable from the root command tree and
// emits the placeholder message until railyard-hqe rewires it to live
// launched-plugin introspection.
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
	if !strings.Contains(got, "railyard-hqe") {
		t.Errorf("`ry plugins list` did not print the railyard-hqe placeholder:\n%s", got)
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
