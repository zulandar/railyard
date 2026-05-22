package cli

import (
	"fmt"

	"github.com/spf13/cobra"
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
			"Use `ry plugins list` to see which plugins are currently launched.",
	}
	cmd.AddCommand(newPluginsListCmd())
	return cmd
}

// newPluginsListCmd returns the `ry plugins list` subcommand.
//
// Under the subprocess plugin model (railyard-fll.3) plugins are launched
// out-of-process and the host's launched-plugin registry is the source of
// truth. From a one-shot CLI invocation we do NOT have a live host
// instance to query (the running yardmaster owns it).
//
// Wiring this command to the live launched-plugin list (so it shows the
// names, status, and socket paths of the currently-running subprocesses)
// is tracked by bd issue railyard-hqe. Until that lands, the command
// prints a placeholder message.
func newPluginsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List plugins launched by the running yardmaster",
		Long: "List plugins launched by the running yardmaster. " +
			"Live launched-plugin introspection is not yet wired up from a " +
			"one-shot CLI invocation; see bd issue railyard-hqe for the rewire.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "plugins introspection not yet implemented (railyard-hqe)")
			return nil
		},
	}
}
