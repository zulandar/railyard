package cli

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

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
			info, ok := debug.ReadBuildInfo()
			v, c, d := resolveVersion(Version, Commit, Date, info, ok)
			fmt.Fprintf(cmd.OutOrStdout(), "ry %s (commit: %s, built: %s)\n", v, c, d)
		},
	}
}

// resolveVersion fills in version/commit/date from Go's build info when the
// ldflags-injected values are still at their defaults. Ldflags always win —
// release tarballs see no behavior change. The fallback path is what makes
// `go install ...@latest` and locally-built binaries report something useful.
func resolveVersion(version, commit, date string, info *debug.BuildInfo, infoOK bool) (string, string, string) {
	if !infoOK {
		return version, commit, date
	}

	versionFromBuildInfo := false
	if version == "dev" && info.Main.Version != "" {
		version = info.Main.Version
		versionFromBuildInfo = true
	}

	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if commit == "none" {
				if len(s.Value) >= 8 {
					commit = s.Value[:8]
				} else {
					commit = s.Value
				}
			}
		case "vcs.time":
			if date == "unknown" {
				date = s.Value
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	// Only mark dirty when version came from build info — ldflag-injected
	// values from a release build should display verbatim. Skip if the
	// pseudo-version already carries "+dirty" build metadata to avoid
	// double-tagging (e.g. "v0.9.10-...+dirty-dirty").
	if dirty && versionFromBuildInfo && !strings.Contains(version, "+dirty") {
		version += "-dirty"
	}
	return version, commit, date
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
