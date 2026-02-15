package main

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
	cmd.AddCommand(newBeadCmd())
	cmd.AddCommand(newEngineCmd())
	cmd.AddCommand(newCompleteCmd())
	cmd.AddCommand(newProgressCmd())
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

func main() {
	os.Exit(execute(newRootCmd()))
}
