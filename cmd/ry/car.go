package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/car"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"gorm.io/gorm"
)

func newCarCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "car",
		Short: "Car management commands",
	}

	cmd.AddCommand(newCarCreateCmd())
	cmd.AddCommand(newCarListCmd())
	cmd.AddCommand(newCarShowCmd())
	cmd.AddCommand(newCarUpdateCmd())
	cmd.AddCommand(newCarDepCmd())
	cmd.AddCommand(newCarReadyCmd())
	cmd.AddCommand(newCarChildrenCmd())
	cmd.AddCommand(newCarPublishCmd())
	return cmd
}

func newCarCreateCmd() *cobra.Command {
	var (
		configPath  string
		title       string
		track       string
		carType    string
		priority    int
		description string
		acceptance  string
		design      string
		parentID    string
		skipTests   bool
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new car",
		Long:  "Creates a new car (work item) in the Railyard database with an auto-generated ID.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCarCreate(cmd, configPath, car.CreateOpts{
				Title:       title,
				Track:       track,
				Type:        carType,
				Priority:    priority,
				Description: description,
				Acceptance:  acceptance,
				DesignNotes: design,
				ParentID:    parentID,
				SkipTests:   skipTests,
			})
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&title, "title", "", "car title (required)")
	cmd.Flags().StringVar(&track, "track", "", "track name (required if no parent with track)")
	cmd.Flags().StringVar(&carType, "type", "task", "car type (task, epic, bug, spike)")
	cmd.Flags().IntVar(&priority, "priority", 2, "priority (0=critical → 4=backlog)")
	cmd.Flags().StringVar(&description, "description", "", "detailed description")
	cmd.Flags().StringVar(&acceptance, "acceptance", "", "acceptance criteria")
	cmd.Flags().StringVar(&design, "design", "", "design notes")
	cmd.Flags().StringVar(&parentID, "parent", "", "parent epic car ID")
	cmd.Flags().BoolVar(&skipTests, "skip-tests", false, "skip test gate during merge")
	cmd.MarkFlagRequired("title")
	return cmd
}

func runCarCreate(cmd *cobra.Command, configPath string, opts car.CreateOpts) error {
	cfg, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}
	opts.BranchPrefix = cfg.BranchPrefix

	b, err := car.Create(gormDB, opts)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Created car %s\n", b.ID)
	fmt.Fprintf(out, "Branch: %s\n", b.Branch)
	if b.ParentID != nil {
		fmt.Fprintf(out, "Parent: %s\n", *b.ParentID)
	}
	return nil
}

func newCarListCmd() *cobra.Command {
	var (
		configPath string
		track      string
		status     string
		carType   string
		assignee   string
		showTokens bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cars",
		Long:  "Lists cars with optional filters. Output is formatted as a table.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCarList(cmd, configPath, car.ListFilters{
				Track:    track,
				Status:   status,
				Type:     carType,
				Assignee: assignee,
			}, showTokens)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&track, "track", "", "filter by track")
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&carType, "type", "", "filter by type")
	cmd.Flags().StringVar(&assignee, "assignee", "", "filter by assignee")
	cmd.Flags().BoolVar(&showTokens, "tokens", false, "show token usage column")
	return cmd
}

func runCarList(cmd *cobra.Command, configPath string, filters car.ListFilters, showTokens bool) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	cars, err := car.List(gormDB, filters)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(cars) == 0 {
		fmt.Fprintln(out, "No cars found.")
		return nil
	}

	// Build token map if --tokens flag is set.
	var tokenMap map[string]car.TokenSummary
	if showTokens {
		ids := make([]string, len(cars))
		for i, b := range cars {
			ids[i] = b.ID
		}
		tokenMap, err = car.CarTokenMap(gormDB, ids)
		if err != nil {
			return err
		}
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if showTokens {
		fmt.Fprintln(w, "ID\tTITLE\tSTATUS\tTRACK\tPRI\tASSIGNEE\tTOKENS")
	} else {
		fmt.Fprintln(w, "ID\tTITLE\tSTATUS\tTRACK\tPRI\tASSIGNEE")
	}
	for _, b := range cars {
		a := b.Assignee
		if a == "" {
			a = "-"
		}
		if showTokens {
			tokens := "-"
			if ts, ok := tokenMap[b.ID]; ok && ts.TotalTokens > 0 {
				tokens = formatTokenCount(ts.TotalTokens)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
				b.ID, truncate(b.Title, 40), b.Status, b.Track, b.Priority, a, tokens)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
				b.ID, truncate(b.Title, 40), b.Status, b.Track, b.Priority, a)
		}
	}
	w.Flush()
	return nil
}

func newCarShowCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show car details",
		Long:  "Displays full details of a car including description, acceptance criteria, design notes, progress, and dependencies.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCarShow(cmd, configPath, args[0])
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runCarShow(cmd *cobra.Command, configPath, id string) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	b, err := car.Get(gormDB, id)
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
		summary, err := car.ChildrenSummary(gormDB, b.ID)
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
			fmt.Fprintf(out, "  %s %s %s\n", d.CarID, d.DepType, d.BlockedBy)
		}
	}

	if len(b.Progress) > 0 {
		fmt.Fprintln(out, "\nProgress:")
		for _, p := range b.Progress {
			fmt.Fprintf(out, "  [%s] cycle=%d engine=%s: %s\n",
				p.CreatedAt.Format("2006-01-02 15:04"), p.Cycle, p.EngineID, p.Note)
		}
	}

	// Token usage section.
	tokenSummary, err := car.GetTokenUsage(gormDB, b.ID)
	if err == nil && tokenSummary.TotalTokens > 0 {
		fmt.Fprintln(out, "\nToken Usage:")
		fmt.Fprintf(out, "  Input:     %s\n", formatTokenCount(tokenSummary.InputTokens))
		fmt.Fprintf(out, "  Output:    %s\n", formatTokenCount(tokenSummary.OutputTokens))
		fmt.Fprintf(out, "  Total:     %s\n", formatTokenCount(tokenSummary.TotalTokens))
		if tokenSummary.Model != "" {
			fmt.Fprintf(out, "  Model:     %s\n", tokenSummary.Model)
			cost := estimateCost(tokenSummary.Model, tokenSummary.InputTokens, tokenSummary.OutputTokens)
			fmt.Fprintf(out, "  Est. Cost: $%.2f\n", cost)
		}
	}

	return nil
}

func newCarUpdateCmd() *cobra.Command {
	var (
		configPath  string
		status      string
		assignee    string
		priority    int
		description string
		acceptance  string
		design      string
		skipTests   bool
	)

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a car",
		Long:  "Updates car fields. Status transitions are validated.",
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
			if cmd.Flags().Changed("skip-tests") {
				updates["skip_tests"] = skipTests
			}

			if len(updates) == 0 {
				return fmt.Errorf("no fields to update; use --status, --assignee, --priority, --description, --acceptance, --design, or --skip-tests")
			}

			return runCarUpdate(cmd, configPath, args[0], updates)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&status, "status", "", "new status")
	cmd.Flags().StringVar(&assignee, "assignee", "", "assign to engine")
	cmd.Flags().IntVar(&priority, "priority", 0, "new priority")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringVar(&acceptance, "acceptance", "", "new acceptance criteria")
	cmd.Flags().StringVar(&design, "design", "", "new design notes")
	cmd.Flags().BoolVar(&skipTests, "skip-tests", false, "skip test gate during merge")
	return cmd
}

func runCarUpdate(cmd *cobra.Command, configPath, id string, updates map[string]interface{}) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	if err := car.Update(gormDB, id, updates); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Updated car %s\n", id)
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

func newCarDepCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dep",
		Short: "Manage car dependencies",
	}

	cmd.AddCommand(newCarDepAddCmd())
	cmd.AddCommand(newCarDepListCmd())
	cmd.AddCommand(newCarDepRemoveCmd())
	return cmd
}

func newCarDepAddCmd() *cobra.Command {
	var (
		configPath string
		blockedBy  string
		depType    string
	)

	cmd := &cobra.Command{
		Use:   "add <car-id>",
		Short: "Add a dependency",
		Long:  "Creates a blocking dependency: the car is blocked by the specified blocker.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}
			if err := car.AddDep(gormDB, args[0], blockedBy, depType); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added dependency: %s blocked by %s\n", args[0], blockedBy)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&blockedBy, "blocked-by", "", "car ID that blocks this car (required)")
	cmd.Flags().StringVar(&depType, "type", "blocks", "dependency type")
	cmd.MarkFlagRequired("blocked-by")
	return cmd
}

func newCarDepListCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "list <car-id>",
		Short: "List car dependencies",
		Long:  "Shows what blocks this car and what this car blocks.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}
			blockers, dependents, err := car.ListDeps(gormDB, args[0])
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
					fmt.Fprintf(w, "  %s\t%s\n", d.CarID, d.DepType)
				}
				w.Flush()
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func newCarDepRemoveCmd() *cobra.Command {
	var (
		configPath string
		blockedBy  string
	)

	cmd := &cobra.Command{
		Use:   "remove <car-id>",
		Short: "Remove a dependency",
		Long:  "Removes a blocking dependency between two cars.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}
			if err := car.RemoveDep(gormDB, args[0], blockedBy); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed dependency: %s blocked by %s\n", args[0], blockedBy)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&blockedBy, "blocked-by", "", "car ID to remove as blocker (required)")
	cmd.MarkFlagRequired("blocked-by")
	return cmd
}

func newCarReadyCmd() *cobra.Command {
	var (
		configPath string
		track      string
	)

	cmd := &cobra.Command{
		Use:   "ready",
		Short: "List ready cars",
		Long:  "Lists cars that are ready for work: status=open, unassigned, and all blockers resolved.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}

			cars, err := car.ReadyCars(gormDB, track)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if len(cars) == 0 {
				fmt.Fprintln(out, "No ready cars.")
				return nil
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTITLE\tTRACK\tPRI")
			for _, b := range cars {
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

func newCarChildrenCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "children <parent-id>",
		Short: "List children of an epic",
		Long:  "Lists all child cars of an epic, with a status summary.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCarChildren(cmd, configPath, args[0])
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runCarChildren(cmd *cobra.Command, configPath, parentID string) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	children, err := car.GetChildren(gormDB, parentID)
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
	summary, err := car.ChildrenSummary(gormDB, parentID)
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

func newCarPublishCmd() *cobra.Command {
	var (
		configPath string
		recursive  bool
	)

	cmd := &cobra.Command{
		Use:   "publish <id>",
		Short: "Publish a draft car (transition draft → open)",
		Long: `Publishes a car by transitioning it from draft to open status.
With --recursive, also publishes all draft children (useful for epics).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}

			count, err := car.Publish(gormDB, args[0], recursive)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if count == 0 {
				fmt.Fprintf(out, "No draft cars to publish for %s\n", args[0])
			} else {
				fmt.Fprintf(out, "Published %d car(s) starting from %s\n", count, args[0])
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "also publish all draft children (for epics)")
	return cmd
}

// truncate shortens a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
