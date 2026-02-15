package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// ContextInput holds all data needed to render the context injection template.
type ContextInput struct {
	Bead          *models.Bead
	Track         *models.Track
	Config        *config.Config
	Progress      []models.BeadProgress
	Messages      []models.Message
	RecentCommits []string // pre-fetched "git log --oneline" lines
}

// RenderContext produces the full markdown prompt injected into engine sessions.
func RenderContext(input ContextInput) (string, error) {
	if input.Bead == nil {
		return "", fmt.Errorf("engine: bead is required")
	}
	if input.Track == nil {
		return "", fmt.Errorf("engine: track is required")
	}
	if input.Config == nil {
		return "", fmt.Errorf("engine: config is required")
	}

	var w strings.Builder
	writeHeader(&w, input.Track, input.Config)
	writeConventions(&w, input.Track)
	writeCurrentBead(&w, input.Bead)
	writeProgress(&w, input.Progress)
	writeMessages(&w, input.Messages)
	writeRecentCommits(&w, input.RecentCommits)
	writeInstructions(&w)
	return w.String(), nil
}

func writeHeader(w *strings.Builder, track *models.Track, cfg *config.Config) {
	fmt.Fprintf(w, "# You are an engine on track: %s\n", track.Name)
	fmt.Fprintf(w, "# Railyard owner: %s\n", cfg.Owner)
	fmt.Fprintf(w, "# Branch prefix: %s/%s/\n", cfg.BranchPrefix, track.Name)
	w.WriteString("\n")
}

func writeConventions(w *strings.Builder, track *models.Track) {
	w.WriteString("## Project Conventions\n")
	if track.SystemPrompt != "" {
		w.WriteString(track.SystemPrompt)
		w.WriteString("\n")
	}
	w.WriteString("\n")
	fmt.Fprintf(w, "Language: %s\n", track.Language)
	if conv := formatConventions(track.Conventions); conv != "" {
		w.WriteString(conv)
		w.WriteString("\n")
	}
	w.WriteString("\nIMPORTANT: You ONLY work on this project. Do not use patterns, languages,\n")
	w.WriteString("or frameworks from other projects. Follow the conventions above exactly.\n\n")
}

func writeCurrentBead(w *strings.Builder, bead *models.Bead) {
	w.WriteString("## Your Current Bead\n")
	fmt.Fprintf(w, "Bead: %s\n", bead.ID)
	fmt.Fprintf(w, "Title: %s\n", bead.Title)
	fmt.Fprintf(w, "Priority: %d\n", bead.Priority)
	fmt.Fprintf(w, "Branch: %s\n", bead.Branch)
	w.WriteString("\n### Description\n")
	w.WriteString(bead.Description)
	w.WriteString("\n\n### Design Notes\n")
	w.WriteString(bead.DesignNotes)
	w.WriteString("\n\n### Acceptance Criteria\n")
	w.WriteString(bead.Acceptance)
	w.WriteString("\n\n")
}

func writeProgress(w *strings.Builder, progress []models.BeadProgress) {
	if len(progress) == 0 {
		return
	}
	w.WriteString("## Previous Progress (if resuming)\n")
	for _, p := range progress {
		fmt.Fprintf(w, "### Cycle %d\n", p.Cycle)
		w.WriteString(p.Note)
		w.WriteString("\n")
		if p.FilesChanged != "" {
			fmt.Fprintf(w, "Files: %s\n", p.FilesChanged)
		}
		if p.CommitHash != "" {
			fmt.Fprintf(w, "Commit: %s\n", p.CommitHash)
		}
		w.WriteString("\n")
	}
}

func writeMessages(w *strings.Builder, messages []models.Message) {
	if len(messages) == 0 {
		return
	}
	w.WriteString("## Yardmaster Messages\n")
	for _, m := range messages {
		fmt.Fprintf(w, "### [%s] %s\n", m.Priority, m.Subject)
		fmt.Fprintf(w, "From: %s | %s\n", m.FromAgent, m.CreatedAt.Format("2006-01-02 15:04"))
		w.WriteString(m.Body)
		w.WriteString("\n\n")
	}
}

func writeRecentCommits(w *strings.Builder, commits []string) {
	if len(commits) == 0 {
		return
	}
	w.WriteString("## Recent Commits on Your Branch\n")
	for _, c := range commits {
		w.WriteString(c)
		w.WriteString("\n")
	}
	w.WriteString("\n")
}

func writeInstructions(w *strings.Builder) {
	w.WriteString("## When You're Done\n")
	w.WriteString("1. Run tests, ensure they pass\n")
	w.WriteString("2. Update bead status: `ry bead complete <bead-id> \"summary of what was done\"`\n")
	w.WriteString("3. The daemon will handle git push and /clear\n")
	w.WriteString("\n## If You're Stuck\n")
	w.WriteString("1. Update progress: `ry bead progress <bead-id> \"what you tried, what failed\"`\n")
	w.WriteString("2. Send message: `ry message send --from <engine-id> --to yardmaster --subject \"help\" --body \"need help with X\"`\n")
	w.WriteString("3. The Yardmaster will receive your message and may provide guidance\n")
	w.WriteString("\n## If You Need to Split Work\n")
	w.WriteString("1. Create child beads: `ry bead create --title \"sub-task\" --track <track> --parent <bead-id> --type task`\n")
	w.WriteString("2. Continue on the current bead, children will be picked up by other engines\n")
}

// formatConventions parses JSON conventions into bullet points.
func formatConventions(jsonStr string) string {
	if jsonStr == "" {
		return ""
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return ""
	}
	if len(data) == 0 {
		return ""
	}

	// Sort keys for deterministic output.
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		v := data[k]
		switch val := v.(type) {
		case string:
			fmt.Fprintf(&b, "- %s: %s\n", k, val)
		case float64:
			if val == float64(int(val)) {
				fmt.Fprintf(&b, "- %s: %d\n", k, int(val))
			} else {
				fmt.Fprintf(&b, "- %s: %g\n", k, val)
			}
		case bool:
			fmt.Fprintf(&b, "- %s: %t\n", k, val)
		default:
			// Nested values: marshal back to JSON.
			nested, err := json.Marshal(val)
			if err != nil {
				fmt.Fprintf(&b, "- %s: %v\n", k, val)
			} else {
				fmt.Fprintf(&b, "- %s: %s\n", k, string(nested))
			}
		}
	}
	return b.String()
}
