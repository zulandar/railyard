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
	Car           *models.Car
	Track         *models.Track
	Config        *config.Config
	Progress      []models.CarProgress
	Messages      []models.Message
	RecentCommits []string // pre-fetched "git log --oneline" lines
	EngineID      string   // engine identifier, used for co-author trailer
}

// RenderContext produces the full markdown prompt injected into engine sessions.
func RenderContext(input ContextInput) (string, error) {
	if input.Car == nil {
		return "", fmt.Errorf("engine: car is required")
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
	writeCurrentCar(&w, input.Car)
	writeProgress(&w, input.Progress)
	writeMessages(&w, input.Messages)
	writeRecentCommits(&w, input.RecentCommits)
	writeInstructions(&w, input.EngineID)
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

func writeCurrentCar(w *strings.Builder, car *models.Car) {
	w.WriteString("## Your Current Car\n")
	fmt.Fprintf(w, "Car: %s\n", car.ID)
	fmt.Fprintf(w, "Title: %s\n", car.Title)
	fmt.Fprintf(w, "Priority: %d\n", car.Priority)
	fmt.Fprintf(w, "Branch: %s\n", car.Branch)
	w.WriteString("\n### Description\n")
	w.WriteString(car.Description)
	w.WriteString("\n\n### Design Notes\n")
	w.WriteString(car.DesignNotes)
	w.WriteString("\n\n### Acceptance Criteria\n")
	w.WriteString(car.Acceptance)
	w.WriteString("\n\n")
}

func writeProgress(w *strings.Builder, progress []models.CarProgress) {
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

func writeInstructions(w *strings.Builder, engineID string) {
	// Co-author trailer instruction.
	if engineID != "" {
		w.WriteString("## Git Commit Attribution\n")
		w.WriteString("You MUST append the following Co-Authored-By trailer to EVERY commit message:\n")
		w.WriteString("```\n")
		fmt.Fprintf(w, "Co-Authored-By: Railyard Engine %s <railyard-engine@noreply>\n", engineID)
		w.WriteString("```\n")
		w.WriteString("This identifies which engine produced the work. Do not omit this.\n\n")
	}

	w.WriteString("## When You're Done\n")
	w.WriteString("1. Run tests, ensure they pass\n")
	w.WriteString("2. Mark the car complete by running this command:\n")
	w.WriteString("```\n")
	w.WriteString("ry complete <car-id> \"summary of what was done\"\n")
	w.WriteString("```\n")
	w.WriteString("3. The daemon will handle git push and /clear\n")
	w.WriteString("\n**IMPORTANT**: Use the `ry complete` command above — do NOT send a message to the Yardmaster to report completion. Messages are for help requests only.\n")
	w.WriteString("\n## If You're Stuck\n")
	w.WriteString("1. Update progress: `ry car progress <car-id> \"what you tried, what failed\"`\n")
	w.WriteString("2. Send message: `ry message send --from <engine-id> --to yardmaster --subject \"help\" --body \"need help with X\"`\n")
	w.WriteString("3. The Yardmaster will receive your message and may provide guidance\n")
	w.WriteString("\n## If You Need to Split Work\n")
	w.WriteString("1. Create child cars: `ry car create --title \"sub-task\" --track <track> --parent <car-id> --type task`\n")
	w.WriteString("2. Continue on the current car, children will be picked up by other engines\n")

	w.WriteString("\n## If You Discover a Bug\n")
	w.WriteString("If you find a bug or issue **outside** your car's scope (code you didn't write, ")
	w.WriteString("a different module, a broken dependency, a security issue, or a previously completed car ")
	w.WriteString("whose acceptance criteria weren't met), file a bug car:\n")
	w.WriteString("```\n")
	w.WriteString("ry car create --title \"Bug: <short description>\" --track <track> --type bug --priority 1 --description \"<what is broken, where, and how to reproduce>\" --acceptance \"<what 'fixed' looks like>\"\n")
	w.WriteString("```\n")
	w.WriteString("Then notify the Yardmaster:\n")
	w.WriteString("```\n")
	w.WriteString("ry message send --from <engine-id> --to yardmaster --subject \"bug-filed\" --car-id <new-bug-car-id> --body \"Found bug while working on <your-car-id>: <brief summary>\"\n")
	w.WriteString("```\n")
	w.WriteString("**Scope rule**: Fix issues that are **inside** your car's scope directly — don't file bugs for your own work. ")
	w.WriteString("Only file bugs for problems that belong to a different car or track.\n")
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
