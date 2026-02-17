package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

func newWatchCmd() *cobra.Command {
	var (
		configPath string
		agent      string
		all        bool
	)

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Stream messages in real-time",
		Long:  "Polls for new messages and displays them as they arrive. Defaults to watching the \"human\" agent inbox. Use --all to watch all messages.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWatch(cmd, configPath, agent, all)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&agent, "agent", "human", "agent inbox to watch")
	cmd.Flags().BoolVar(&all, "all", false, "watch all messages regardless of recipient")
	return cmd
}

func runWatch(cmd *cobra.Command, configPath, agent string, all bool) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	if all {
		fmt.Fprintln(out, "Watching all messages... (Ctrl+C to stop)")
	} else {
		fmt.Fprintf(out, "Watching messages for %q... (Ctrl+C to stop)\n", agent)
	}

	// Show recent messages first.
	var recent []models.Message
	q := buildWatchQuery(gormDB, agent, all)
	if err := q.Order("id DESC").Limit(10).Find(&recent).Error; err != nil {
		return fmt.Errorf("query messages: %w", err)
	}

	// Reverse for chronological display.
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}

	for _, m := range recent {
		printWatchMessage(out, m)
	}

	// Follow mode: poll for new messages.
	var lastID uint
	if len(recent) > 0 {
		lastID = recent[len(recent)-1].ID
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			var newMsgs []models.Message
			nq := buildWatchQuery(gormDB, agent, all).Where("id > ?", lastID).Order("id ASC")
			if err := nq.Find(&newMsgs).Error; err != nil {
				fmt.Fprintf(out, "poll error: %v\n", err)
				continue
			}
			for _, m := range newMsgs {
				printWatchMessage(out, m)
				lastID = m.ID
			}
		}
	}
}

func buildWatchQuery(db *gorm.DB, agent string, all bool) *gorm.DB {
	q := db.Model(&models.Message{})
	if !all {
		q = q.Where("to_agent = ?", agent)
	}
	return q
}

func printWatchMessage(out io.Writer, m models.Message) {
	ts := m.CreatedAt.Format("15:04:05")
	prefix := ""
	if m.Priority == "urgent" {
		prefix = "[URGENT] "
	}
	fmt.Fprintf(out, "[%s] %s→%s %s%s: %s\n", ts, m.FromAgent, m.ToAgent, prefix, m.Subject, truncate(m.Body, 200))
}
