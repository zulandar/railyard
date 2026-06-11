package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/pluginhost"
)

// newPluginsCmd returns the `ry plugins` parent command. It currently
// has one subcommand, `ry plugins list`. Running `ry plugins` with no
// subcommand prints help — keeping a parent command makes future
// subcommands (e.g. `ry plugins status`, which would query a running
// yardmaster over IPC) a one-line addition without rewriting wiring.
func newPluginsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "Inspect plugins available to this binary",
		Long: "Inspect plugins available to this binary. " +
			"Use `ry plugins list` to see plugins discovered in the plugins.d directories " +
			"and their enabled-in-config status. " +
			"Use `ry plugins status` to query a running yardmaster for live runtime state. " +
			"Use `ry plugins restart <name>` to relaunch a single plugin in a running yardmaster.",
	}
	cmd.AddCommand(newPluginsListCmd())
	cmd.AddCommand(newPluginsStatusCmd())
	cmd.AddCommand(newPluginsRestartCmd())
	return cmd
}

// pluginsListDiscover is the indirection seam used by tests to stub out
// pluginhost.DiscoverPlugins without exercising the real plugins.d scan.
var pluginsListDiscover = pluginhost.DiscoverPlugins

// pluginsListLoadConfig is the indirection seam used by tests to stub
// out config.Load. Tests can swap it for a closure that returns a
// hand-built *config.Config without round-tripping through YAML.
var pluginsListLoadConfig = config.Load

// newPluginsListCmd returns the `ry plugins list` subcommand.
//
// Under the subprocess plugin model plugins are launched by a running
// railyard host; the CLI runs as a one-shot, so there is no live host to
// query. Instead this command reports the "would-be-launched if
// railyard started right now" state: every plugin binary found in the
// configured plugins.d directories, plus per-row enabled-in-config and
// allow-block summary information.
//
// This is intentionally NOT a runtime-status command. A future
// `ry plugins status` (or `ry status plugins`) command will speak IPC
// to a running yardmaster for actual subprocess state — that is a
// separate feature.
func newPluginsListCmd() *cobra.Command {
	var (
		configPath string
		verbose    bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List plugins discovered on disk and their configured state",
		Long: "List plugins discovered on disk and their configured state.\n\n" +
			"Sources the data from a static scan of the plugins.d directories the host " +
			"would scan on startup, intersected with railyard.yaml's plugins.enabled list " +
			"and per-plugin allow blocks. Does NOT query a running railyard for live " +
			"subprocess state — that is a future `ry plugins status` command.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginsList(cmd, configPath, verbose)
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "expand allow-list contents instead of counts")
	return cmd
}

// runPluginsList loads the config, runs the read-only discovery, and
// renders a tab-aligned table to cmd.OutOrStdout(). Missing config is
// not fatal — operators may inspect plugins.d before completing yard
// setup; a load failure falls back to a pure on-disk scan with a
// warning to stderr.
func runPluginsList(cmd *cobra.Command, configPath string, verbose bool) error {
	cfg, cfgErr := pluginsListLoadConfig(configPath)
	if cfgErr != nil {
		// Non-fatal: continue with a nil config so the command still
		// shows what is on disk. The warning goes to stderr so tooling
		// piping stdout to less/grep still gets clean table output.
		fmt.Fprintf(cmd.ErrOrStderr(), "plugins list: %v (continuing with on-disk scan only)\n", cfgErr)
	}

	candidates, err := pluginsListDiscover(cfg)
	if err != nil {
		return fmt.Errorf("plugins list: discover: %w", err)
	}

	out := cmd.OutOrStdout()
	if len(candidates) == 0 {
		fmt.Fprintln(out, "no plugins found in /etc/railyard/plugins.d, ~/.railyard/plugins, ./plugins")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if verbose {
		fmt.Fprintln(w, "NAME\tENABLED\tEXECUTABLE\tEVENTS\tCOMMANDS\tPATH")
	} else {
		fmt.Fprintln(w, "NAME\tENABLED\tEXECUTABLE\tEVENTS\tCOMMANDS\tPATH")
	}
	for _, c := range candidates {
		path := c.Path
		if path == "" {
			path = "(not found on disk)"
		}
		var events, commands string
		if verbose {
			events = formatAllowEntries(c.AllowEvents)
			commands = formatAllowEntries(c.AllowCommands)
		} else {
			events = fmt.Sprintf("%d", len(c.AllowEvents))
			commands = fmt.Sprintf("%d", len(c.AllowCommands))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			c.Name,
			yesNo(c.Enabled),
			yesNo(c.Executable),
			events,
			commands,
			path,
		)
	}
	return w.Flush()
}

// yesNo renders a bool as the human-friendly "yes"/"no" the other `ry`
// list commands favour.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// formatAllowEntries renders a (possibly empty) allow-list slice as a
// comma-separated string. Empty slices print "-" so the column stays
// visually consistent — empty cells in tabwriter output read as
// alignment bugs.
func formatAllowEntries(entries []string) string {
	if len(entries) == 0 {
		return "-"
	}
	return strings.Join(entries, ",")
}
