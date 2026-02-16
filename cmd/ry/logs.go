package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

func newLogsCmd() *cobra.Command {
	var (
		configPath string
		engineID   string
		carID     string
		sessionID  string
		follow     bool
		lines      int
		raw        bool
	)

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "View agent log output",
		Long:  "Displays agent log entries from the agent_logs table. Supports filtering by engine, car, or session, and a --follow mode for tailing new entries.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd, configPath, logsOpts{
				engineID:  engineID,
				carID:    carID,
				sessionID: sessionID,
				follow:    follow,
				lines:     lines,
				raw:       raw,
			})
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&engineID, "engine", "", "filter by engine ID")
	cmd.Flags().StringVar(&carID, "car", "", "filter by car ID")
	cmd.Flags().StringVar(&sessionID, "session", "", "filter by session ID")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "tail mode — poll for new entries every 2s")
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "number of recent entries to show")
	cmd.Flags().BoolVar(&raw, "raw", false, "show full content instead of truncated preview")
	return cmd
}

type logsOpts struct {
	engineID  string
	carID    string
	sessionID string
	follow    bool
	lines     int
	raw       bool
}

func runLogs(cmd *cobra.Command, configPath string, opts logsOpts) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	query := buildLogsQuery(gormDB, opts)

	var entries []models.AgentLog
	if err := query.Order("id DESC").Limit(opts.lines).Find(&entries).Error; err != nil {
		return fmt.Errorf("query logs: %w", err)
	}

	// Reverse for chronological display.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	if len(entries) == 0 && !opts.follow {
		fmt.Fprintln(out, "No log entries found.")
		return nil
	}

	for _, e := range entries {
		printEntry(out, e, opts.raw)
	}

	if !opts.follow {
		return nil
	}

	// Follow mode: poll for new entries.
	var lastID uint
	if len(entries) > 0 {
		lastID = entries[len(entries)-1].ID
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
			var newEntries []models.AgentLog
			q := buildLogsQuery(gormDB, opts).Where("id > ?", lastID).Order("id ASC")
			if err := q.Find(&newEntries).Error; err != nil {
				fmt.Fprintf(out, "poll error: %v\n", err)
				continue
			}
			for _, e := range newEntries {
				printEntry(out, e, opts.raw)
				lastID = e.ID
			}
		}
	}
}

func buildLogsQuery(db *gorm.DB, opts logsOpts) *gorm.DB {
	q := db.Model(&models.AgentLog{})
	if opts.engineID != "" {
		q = q.Where("engine_id = ?", opts.engineID)
	}
	if opts.carID != "" {
		q = q.Where("car_id = ?", opts.carID)
	}
	if opts.sessionID != "" {
		q = q.Where("session_id = ?", opts.sessionID)
	}
	return q
}

func printEntry(out io.Writer, e models.AgentLog, raw bool) {
	if raw {
		ts := e.CreatedAt.Format("15:04:05")
		fmt.Fprintf(out, "--- [%s] %s %s %s ---\n", ts, shortID(e.EngineID), shortID(e.CarID), e.Direction)
		fmt.Fprintln(out, e.Content)
		return
	}

	ts := e.CreatedAt.Format("15:04:05")
	prefix := fmt.Sprintf("[%s] %s %s %s", ts, shortID(e.EngineID), shortID(e.CarID), e.Direction)

	lines := strings.Split(strings.TrimRight(e.Content, "\n"), "\n")
	for _, line := range lines {
		summary := formatStreamLine(line)
		if summary != "" {
			fmt.Fprintf(out, "%s | %s\n", prefix, summary)
		}
	}
}

// shortID returns the first 14 characters of an ID for compact display.
func shortID(id string) string {
	if len(id) <= 14 {
		return id
	}
	return id[:14]
}

// formatStreamLine parses a single stream-json line and returns a human-readable summary.
func formatStreamLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return truncate(line, 120)
	}

	typ, _ := obj["type"].(string)

	switch typ {
	case "assistant":
		return formatAssistantMessage(obj)
	case "user":
		return formatUserMessage(obj)
	case "result":
		return formatResult(obj)
	case "system":
		return "system"
	default:
		return fmt.Sprintf("%s: %s", typ, truncate(line, 100))
	}
}

func formatAssistantMessage(obj map[string]interface{}) string {
	msg, ok := obj["message"].(map[string]interface{})
	if !ok {
		return "assistant: ..."
	}
	content, ok := msg["content"].([]interface{})
	if !ok || len(content) == 0 {
		return "assistant: ..."
	}

	var parts []string
	for _, item := range content {
		block, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			text, _ := block["text"].(string)
			text = strings.ReplaceAll(text, "\n", " ")
			parts = append(parts, "assistant: "+truncate(text, 100))
		case "tool_use":
			name, _ := block["name"].(string)
			parts = append(parts, "tool_use: "+name)
		}
	}

	if len(parts) == 0 {
		return "assistant: ..."
	}
	return strings.Join(parts, " | ")
}

func formatUserMessage(obj map[string]interface{}) string {
	msg, ok := obj["message"].(map[string]interface{})
	if !ok {
		return "user: ..."
	}
	content, ok := msg["content"].([]interface{})
	if !ok || len(content) == 0 {
		return "user: ..."
	}

	block, ok := content[0].(map[string]interface{})
	if !ok {
		return "user: ..."
	}
	blockType, _ := block["type"].(string)
	if blockType == "tool_result" {
		return "tool_result"
	}
	return "user: ..."
}

func formatResult(obj map[string]interface{}) string {
	subtype, _ := obj["subtype"].(string)
	return fmt.Sprintf("result (%s)", subtype)
}
