package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/orchestration"
)

func newStatusCmd() *cobra.Command {
	var (
		configPath string
		watch      bool
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Railyard status dashboard",
		Long:  "Displays the Railyard status dashboard: engine status, bead counts per track, and message queue depth. Use --watch for auto-refresh.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, configPath, watch)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().BoolVar(&watch, "watch", false, "auto-refresh every 5 seconds")
	return cmd
}

func runStatus(cmd *cobra.Command, configPath string, watch bool) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	for {
		info, err := orchestration.Status(gormDB, nil)
		if err != nil {
			return err
		}

		if watch {
			// Clear screen.
			fmt.Fprint(out, "\033[2J\033[H")
		}

		fmt.Fprint(out, orchestration.FormatStatus(info))

		if !watch {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
}
