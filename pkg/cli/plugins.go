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
// The list is sourced from [plugin.Registered], which is the package-level
// registry populated at init() time by side-effect imports in custom
// (e.g. enterprise) binaries. The OSS `ry` binary registers zero plugins,
// in which case the command prints "No plugins registered in this binary."
//
// Status / daemon / subscription columns reflect the build-time view only.
// Live runtime status (a plugin currently running and how many daemons
// and subscriptions it has registered) would require IPC into a running
// yardmaster, which is intentionally not part of this command — that is
// a future bead. Status always reads "registered"; the daemon and
// subscription columns always render "-" so the output schema stays
// stable once a future `ry plugins status` subcommand fills them in.
func newPluginsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List plugins compiled into this binary",
		Long: "List plugins compiled into this binary. " +
			"This is a build-time view: status is always \"registered\" and the " +
			"daemons / subscriptions columns are unknown without IPC into a " +
			"running yardmaster.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// plugin.Registered / plugin.PluginEntry are deprecated under
			// the subprocess plugin model (railyard-fll.2). The `ry plugins`
			// view continues to use them until the host rewrite in
			// railyard-fll.3 surfaces an equivalent introspection path.
			//nolint:staticcheck // SA1019: legacy registry view retained until railyard-fll.3
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
