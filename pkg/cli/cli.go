package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version info set via ldflags at build time.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ry",
		Short: "Railyard — multi-agent AI orchestration",
		Long:  "Railyard coordinates coding agents across local machines and cloud VMs.",
	}

	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newDBCmd())
	cmd.AddCommand(newCarCmd())
	cmd.AddCommand(newEngineCmd())
	cmd.AddCommand(newCompleteCmd())
	cmd.AddCommand(newProgressCmd())
	cmd.AddCommand(newMessageCmd())
	cmd.AddCommand(newInboxCmd())
	cmd.AddCommand(newDispatchCmd())
	cmd.AddCommand(newYardmasterCmd())
	cmd.AddCommand(newSwitchCmd())
	cmd.AddCommand(newStartCmd())
	cmd.AddCommand(newStopCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newLogsCmd())
	cmd.AddCommand(newWatchCmd())
	cmd.AddCommand(newDoctorCmd())
	cmd.AddCommand(newDashboardCmd())
	cmd.AddCommand(newCocoIndexCmd())
	cmd.AddCommand(newOverlayCmd())
	cmd.AddCommand(newGitIgnoreCmd())
	cmd.AddCommand(newMigrateCmd())
	cmd.AddCommand(newTelegraphCmd())
	cmd.AddCommand(newBullCmd())
	cmd.AddCommand(newInspectCmd())
	cmd.AddCommand(newInitCmd())
	cmd.AddCommand(newPluginsCmd())
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "ry %s (commit: %s, built: %s)\n", Version, Commit, Date)
		},
	}
}

func execute(cmd *cobra.Command) int {
	if err := cmd.Execute(); err != nil {
		return 1
	}
	return 0
}

// Run executes the railyard CLI and exits the process with the
// appropriate status code. It mirrors what cmd/ry/main.go did before the
// pkg/cli extraction.
//
// Enterprise binaries that side-effect import plugins call this from
// their own main() function:
//
//	package main
//
//	import (
//	    _ "github.com/your-org/railyard-enterprise/plugins/<name>"
//	    "github.com/zulandar/railyard/pkg/cli"
//	)
//
//	func main() { cli.Run() }
func Run() {
	os.Exit(execute(newRootCmd()))
}
