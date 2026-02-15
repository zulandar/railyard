package main

import (
	"fmt"
	"strings"
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
	cmd.AddCommand(newBeadDepCmd())
	cmd.AddCommand(newBeadReadyCmd())
	cmd.AddCommand(newBeadChildrenCmd())
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
		parentID    string
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
				ParentID:    parentID,
			})
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&title, "title", "", "bead title (required)")
	cmd.Flags().StringVar(&track, "track", "", "track name (required if no parent with track)")
	cmd.Flags().StringVar(&beadType, "type", "task", "bead type (task, epic, bug, spike)")
	cmd.Flags().IntVar(&priority, "priority", 2, "priority (0=critical → 4=backlog)")
	cmd.Flags().StringVar(&description, "description", "", "detailed description")
	cmd.Flags().StringVar(&acceptance, "acceptance", "", "acceptance criteria")
	cmd.Flags().StringVar(&design, "design", "", "design notes")
	cmd.Flags().StringVar(&parentID, "parent", "", "parent epic bead ID")
	cmd.MarkFlagRequired("title")
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
	if b.ParentID != nil {
		fmt.Fprintf(out, "Parent: %s\n", *b.ParentID)
	}
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
	if b.Type == "epic" {
		summary, err := bead.ChildrenSummary(gormDB, b.ID)
		if err == nil {
			total := 0
			parts := []string{}
			for _, sc := range summary {
				total += sc.Count
				parts = append(parts, fmt.Sprintf("%d %s", sc.Count, sc.Status))
			}
			if total > 0 {
				fmt.Fprintf(out, "Children:    %d (%s)\n", total, strings.Join(parts, ", "))
			}
		}
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

func newBeadDepCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dep",
		Short: "Manage bead dependencies",
	}

	cmd.AddCommand(newBeadDepAddCmd())
	cmd.AddCommand(newBeadDepListCmd())
	cmd.AddCommand(newBeadDepRemoveCmd())
	return cmd
}

func newBeadDepAddCmd() *cobra.Command {
	var (
		configPath string
		blockedBy  string
		depType    string
	)

	cmd := &cobra.Command{
		Use:   "add <bead-id>",
		Short: "Add a dependency",
		Long:  "Creates a blocking dependency: the bead is blocked by the specified blocker.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}
			if err := bead.AddDep(gormDB, args[0], blockedBy, depType); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added dependency: %s blocked by %s\n", args[0], blockedBy)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&blockedBy, "blocked-by", "", "bead ID that blocks this bead (required)")
	cmd.Flags().StringVar(&depType, "type", "blocks", "dependency type")
	cmd.MarkFlagRequired("blocked-by")
	return cmd
}

func newBeadDepListCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "list <bead-id>",
		Short: "List bead dependencies",
		Long:  "Shows what blocks this bead and what this bead blocks.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}
			blockers, dependents, err := bead.ListDeps(gormDB, args[0])
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if len(blockers) == 0 && len(dependents) == 0 {
				fmt.Fprintf(out, "No dependencies for %s\n", args[0])
				return nil
			}

			if len(blockers) > 0 {
				w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(out, "Blocked by:")
				fmt.Fprintln(w, "  BLOCKER\tTYPE")
				for _, b := range blockers {
					fmt.Fprintf(w, "  %s\t%s\n", b.BlockedBy, b.DepType)
				}
				w.Flush()
			}

			if len(dependents) > 0 {
				w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(out, "Blocks:")
				fmt.Fprintln(w, "  DEPENDENT\tTYPE")
				for _, d := range dependents {
					fmt.Fprintf(w, "  %s\t%s\n", d.BeadID, d.DepType)
				}
				w.Flush()
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func newBeadDepRemoveCmd() *cobra.Command {
	var (
		configPath string
		blockedBy  string
	)

	cmd := &cobra.Command{
		Use:   "remove <bead-id>",
		Short: "Remove a dependency",
		Long:  "Removes a blocking dependency between two beads.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}
			if err := bead.RemoveDep(gormDB, args[0], blockedBy); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed dependency: %s blocked by %s\n", args[0], blockedBy)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&blockedBy, "blocked-by", "", "bead ID to remove as blocker (required)")
	cmd.MarkFlagRequired("blocked-by")
	return cmd
}

func newBeadReadyCmd() *cobra.Command {
	var (
		configPath string
		track      string
	)

	cmd := &cobra.Command{
		Use:   "ready",
		Short: "List ready beads",
		Long:  "Lists beads that are ready for work: status=open, unassigned, and all blockers resolved.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}

			beads, err := bead.ReadyBeads(gormDB, track)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if len(beads) == 0 {
				fmt.Fprintln(out, "No ready beads.")
				return nil
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTITLE\tTRACK\tPRI")
			for _, b := range beads {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\n",
					b.ID, truncate(b.Title, 40), b.Track, b.Priority)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&track, "track", "", "filter by track")
	return cmd
}

func newBeadChildrenCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "children <parent-id>",
		Short: "List children of an epic",
		Long:  "Lists all child beads of an epic, with a status summary.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBeadChildren(cmd, configPath, args[0])
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runBeadChildren(cmd *cobra.Command, configPath, parentID string) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	children, err := bead.GetChildren(gormDB, parentID)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(children) == 0 {
		fmt.Fprintf(out, "No children for %s\n", parentID)
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTITLE\tSTATUS\tTRACK\tPRI")
	for _, b := range children {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n",
			b.ID, truncate(b.Title, 40), b.Status, b.Track, b.Priority)
	}
	w.Flush()

	// Print status summary.
	summary, err := bead.ChildrenSummary(gormDB, parentID)
	if err != nil {
		return err
	}
	parts := []string{}
	for _, sc := range summary {
		parts = append(parts, fmt.Sprintf("%d %s", sc.Count, sc.Status))
	}
	fmt.Fprintf(out, "\nSummary: %s\n", strings.Join(parts, ", "))
	return nil
}

// truncate shortens a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
