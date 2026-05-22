package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/pkg/plugin"
)

// newPluginsCmd returns the `ry plugins` parent command. It currently
// has one subcommand, `ry plugins list`. Running `ry plugins` with no
// subcommand prints help — keeping a parent command makes future
// subcommands (e.g. `ry plugins status`, which would query a running
// yardmaster over IPC) a one-line addition without rewriting wiring.
func newPluginsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "Inspect plugins compiled into this binary",
		Long: "Inspect plugins compiled into this binary. " +
			"Use `ry plugins list` to see which plugins were linked at build time.",
	}
	cmd.AddCommand(newPluginsListCmd())
	return cmd
}

// newPluginsListCmd returns the `ry plugins list` subcommand.
//
// Under the subprocess plugin model (railyard-fll.3) plugins are launched
// out-of-process and the host's launched-plugin registry is the source of
// truth. From a one-shot CLI invocation we do NOT have a live host
// instance to query (the running yardmaster owns it), so for now we fall
// back to the legacy [plugin.Registered] view — which on the OSS binary
// is always empty, producing the friendly "No plugins registered" line.
//
// Wiring this command to a live launched-plugin list (so it shows the
// names, status, and socket paths of the currently-running subprocesses)
// is tracked by bd issue railyard-hqe.
func newPluginsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List plugins available to this binary",
		Long: "List plugins available to this binary. " +
			"Under the subprocess plugin model the live list is owned by a " +
			"running yardmaster; this fallback shows the legacy in-process " +
			"registry (empty on the OSS binary). See bd issue railyard-hqe " +
			"for the rewire to launched-plugin introspection.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Legacy registry view; the OSS binary registers zero plugins
			// so this always prints "No plugins registered in this binary."
			// railyard-hqe tracks the rewire to live launched-plugin info.
			//nolint:staticcheck // SA1019: tracked by bd issue railyard-hqe
			return renderPluginsList(cmd.OutOrStdout(), plugin.Registered())
		},
	}
}

// renderPluginsList writes the plugin table to out using the supplied
// registry snapshot. Splitting the renderer from the [plugin.Registered]
// global lookup keeps the function unit-testable: tests can feed in
// arbitrary entries (including the empty slice) without polluting the
// package-level registry, which has no public reset.
//
// Note: plugin.PluginEntry is deprecated in the subprocess plugin model
// (railyard-fll.2); this renderer keeps using it as the legacy bootstrap
// path until the host rewrite in railyard-fll.3.
//
//nolint:staticcheck // SA1019: legacy registry view retained until railyard-fll.3
func renderPluginsList(out io.Writer, entries []plugin.PluginEntry) error {
	if len(entries) == 0 {
		fmt.Fprintln(out, "No plugins registered in this binary.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tDAEMONS\tSUBSCRIPTIONS")
	for _, e := range entries {
		// Status is always "registered" for the build-time view; the
		// daemon/subscription columns render "-" because we cannot query
		// the running yardmaster from this binary's process. The schema
		// is stable so a future `ry plugins status` subcommand can fill
		// the remaining cells in without breaking consumers that grep
		// this output.
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Name, "registered", "-", "-")
	}
	return w.Flush()
}
