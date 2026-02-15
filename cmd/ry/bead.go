package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/bead"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"gorm.io/gorm"
)

func newBeadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bead",
		Short: "Bead management commands",
	}

	cmd.AddCommand(newBeadCreateCmd())
	cmd.AddCommand(newBeadListCmd())
	cmd.AddCommand(newBeadShowCmd())
	cmd.AddCommand(newBeadUpdateCmd())
	return cmd
}

func newBeadCreateCmd() *cobra.Command {
	var (
		configPath  string
		title       string
		track       string
		beadType    string
		priority    int
		description string
		acceptance  string
		design      string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new bead",
		Long:  "Creates a new bead (work item) in the Railyard database with an auto-generated ID.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBeadCreate(cmd, configPath, bead.CreateOpts{
				Title:       title,
				Track:       track,
				Type:        beadType,
				Priority:    priority,
				Description: description,
				Acceptance:  acceptance,
				DesignNotes: design,
			})
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&title, "title", "", "bead title (required)")
	cmd.Flags().StringVar(&track, "track", "", "track name (required)")
	cmd.Flags().StringVar(&beadType, "type", "task", "bead type (task, epic, bug, spike)")
	cmd.Flags().IntVar(&priority, "priority", 2, "priority (0=critical → 4=backlog)")
	cmd.Flags().StringVar(&description, "description", "", "detailed description")
	cmd.Flags().StringVar(&acceptance, "acceptance", "", "acceptance criteria")
	cmd.Flags().StringVar(&design, "design", "", "design notes")
	cmd.MarkFlagRequired("title")
	cmd.MarkFlagRequired("track")
	return cmd
}

func runBeadCreate(cmd *cobra.Command, configPath string, opts bead.CreateOpts) error {
	cfg, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}
	opts.BranchPrefix = cfg.BranchPrefix

	b, err := bead.Create(gormDB, opts)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Created bead %s\n", b.ID)
	fmt.Fprintf(out, "Branch: %s\n", b.Branch)
	return nil
}

func newBeadListCmd() *cobra.Command {
	var (
		configPath string
		track      string
		status     string
		beadType   string
		assignee   string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List beads",
		Long:  "Lists beads with optional filters. Output is formatted as a table.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBeadList(cmd, configPath, bead.ListFilters{
				Track:    track,
				Status:   status,
				Type:     beadType,
				Assignee: assignee,
			})
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&track, "track", "", "filter by track")
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&beadType, "type", "", "filter by type")
	cmd.Flags().StringVar(&assignee, "assignee", "", "filter by assignee")
	return cmd
}

func runBeadList(cmd *cobra.Command, configPath string, filters bead.ListFilters) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	beads, err := bead.List(gormDB, filters)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(beads) == 0 {
		fmt.Fprintln(out, "No beads found.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTITLE\tSTATUS\tTRACK\tPRI\tASSIGNEE")
	for _, b := range beads {
		a := b.Assignee
		if a == "" {
			a = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
			b.ID, truncate(b.Title, 40), b.Status, b.Track, b.Priority, a)
	}
	w.Flush()
	return nil
}

func newBeadShowCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show bead details",
		Long:  "Displays full details of a bead including description, acceptance criteria, design notes, progress, and dependencies.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBeadShow(cmd, configPath, args[0])
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runBeadShow(cmd *cobra.Command, configPath, id string) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	b, err := bead.Get(gormDB, id)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "ID:          %s\n", b.ID)
	fmt.Fprintf(out, "Title:       %s\n", b.Title)
	fmt.Fprintf(out, "Status:      %s\n", b.Status)
	fmt.Fprintf(out, "Type:        %s\n", b.Type)
	fmt.Fprintf(out, "Priority:    %d\n", b.Priority)
	fmt.Fprintf(out, "Track:       %s\n", b.Track)
	fmt.Fprintf(out, "Branch:      %s\n", b.Branch)
	if b.Assignee != "" {
		fmt.Fprintf(out, "Assignee:    %s\n", b.Assignee)
	}
	if b.ParentID != nil {
		fmt.Fprintf(out, "Parent:      %s\n", *b.ParentID)
	}
	fmt.Fprintf(out, "Created:     %s\n", b.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(out, "Updated:     %s\n", b.UpdatedAt.Format("2006-01-02 15:04:05"))
	if b.ClaimedAt != nil {
		fmt.Fprintf(out, "Claimed:     %s\n", b.ClaimedAt.Format("2006-01-02 15:04:05"))
	}
	if b.CompletedAt != nil {
		fmt.Fprintf(out, "Completed:   %s\n", b.CompletedAt.Format("2006-01-02 15:04:05"))
	}

	if b.Description != "" {
		fmt.Fprintf(out, "\nDescription:\n%s\n", b.Description)
	}
	if b.Acceptance != "" {
		fmt.Fprintf(out, "\nAcceptance:\n%s\n", b.Acceptance)
	}
	if b.DesignNotes != "" {
		fmt.Fprintf(out, "\nDesign Notes:\n%s\n", b.DesignNotes)
	}

	if len(b.Deps) > 0 {
		fmt.Fprintln(out, "\nDependencies:")
		for _, d := range b.Deps {
			fmt.Fprintf(out, "  %s %s %s\n", d.BeadID, d.DepType, d.BlockedBy)
		}
	}

	if len(b.Progress) > 0 {
		fmt.Fprintln(out, "\nProgress:")
		for _, p := range b.Progress {
			fmt.Fprintf(out, "  [%s] cycle=%d engine=%s: %s\n",
				p.CreatedAt.Format("2006-01-02 15:04"), p.Cycle, p.EngineID, p.Note)
		}
	}

	return nil
}

func newBeadUpdateCmd() *cobra.Command {
	var (
		configPath  string
		status      string
		assignee    string
		priority    int
		description string
		acceptance  string
		design      string
	)

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a bead",
		Long:  "Updates bead fields. Status transitions are validated.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			updates := make(map[string]interface{})

			if cmd.Flags().Changed("status") {
				updates["status"] = status
			}
			if cmd.Flags().Changed("assignee") {
				updates["assignee"] = assignee
			}
			if cmd.Flags().Changed("priority") {
				updates["priority"] = priority
			}
			if cmd.Flags().Changed("description") {
				updates["description"] = description
			}
			if cmd.Flags().Changed("acceptance") {
				updates["acceptance"] = acceptance
			}
			if cmd.Flags().Changed("design") {
				updates["design_notes"] = design
			}

			if len(updates) == 0 {
				return fmt.Errorf("no fields to update; use --status, --assignee, --priority, --description, --acceptance, or --design")
			}

			return runBeadUpdate(cmd, configPath, args[0], updates)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&status, "status", "", "new status")
	cmd.Flags().StringVar(&assignee, "assignee", "", "assign to engine")
	cmd.Flags().IntVar(&priority, "priority", 0, "new priority")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringVar(&acceptance, "acceptance", "", "new acceptance criteria")
	cmd.Flags().StringVar(&design, "design", "", "new design notes")
	return cmd
}

func runBeadUpdate(cmd *cobra.Command, configPath, id string, updates map[string]interface{}) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	if err := bead.Update(gormDB, id, updates); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Updated bead %s\n", id)
	return nil
}

// connectFromConfig loads config and returns a GORM DB connection.
func connectFromConfig(configPath string) (*config.Config, *gorm.DB, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	gormDB, err := db.Connect(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to %s: %w", cfg.Dolt.Database, err)
	}

	return cfg, gormDB, nil
}

// truncate shortens a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
