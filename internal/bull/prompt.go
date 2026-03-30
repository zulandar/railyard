package bull

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// TriageResult holds the structured output from AI triage of a GitHub issue.
type TriageResult struct {
	Classification string   `json:"classification"`
	Priority       int      `json:"priority"`
	Track          string   `json:"track"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Acceptance     string   `json:"acceptance"`
	DesignNotes    string   `json:"design_notes,omitempty"`
	Dependencies   []string `json:"dependencies,omitempty"`
	RejectReason   string   `json:"reject_reason,omitempty"`
}

// TrackInfo carries metadata about a track for use in prompt generation.
type TrackInfo struct {
	Name         string
	Language     string
	FilePatterns []string
	Conventions  []string
}

// CommentContext holds a single GitHub issue comment for triage prompt generation.
type CommentContext struct {
	Author string
	Body   string
	Date   string
}

// IssueContext provides the input data for building a triage prompt.
type IssueContext struct {
	Number   int
	Title    string
	Body     string
	Labels   []string
	Author   string
	Comments []CommentContext
}

// BuildTriagePrompt generates the AI prompt for triaging a GitHub issue.
// mode is "standard" or "full". tracks lists the available tracks with metadata.
// codeContext contains optional semantic search results for additional context.
func BuildTriagePrompt(issue IssueContext, mode string, tracks []TrackInfo, codeContext string) string {
	var b strings.Builder

	b.WriteString("# Bull — Issue Triage Agent\n\n")
	b.WriteString("You are Bull, the issue triage agent for Railyard. ")
	b.WriteString("Analyze the following GitHub issue and produce a structured triage result.\n\n")

	// Issue context
	b.WriteString("## Issue\n\n")
	b.WriteString(fmt.Sprintf("- **Number**: #%d\n", issue.Number))
	b.WriteString(fmt.Sprintf("- **Title**: %s\n", issue.Title))
	b.WriteString(fmt.Sprintf("- **Author**: %s\n", issue.Author))
	if len(issue.Labels) > 0 {
		b.WriteString(fmt.Sprintf("- **Labels**: %s\n", strings.Join(issue.Labels, ", ")))
	}
	b.WriteString(fmt.Sprintf("\n### Body\n\n%s\n\n", issue.Body))

	// Discussion (issue comments)
	if len(issue.Comments) > 0 {
		b.WriteString("### Discussion\n\n")
		for _, c := range issue.Comments {
			fmt.Fprintf(&b, "**@%s** (%s):\n%s\n\n", c.Author, c.Date, c.Body)
		}
	}

	// Available tracks
	b.WriteString("## Available Tracks\n\n")
	for _, track := range tracks {
		b.WriteString(fmt.Sprintf("### %s\n", track.Name))
		if track.Language != "" {
			b.WriteString(fmt.Sprintf("- **Language**: %s\n", track.Language))
		}
		if len(track.FilePatterns) > 0 {
			b.WriteString(fmt.Sprintf("- **File patterns**: %s\n", strings.Join(track.FilePatterns, ", ")))
		}
		if len(track.Conventions) > 0 {
			b.WriteString(fmt.Sprintf("- **Conventions**: %s\n", strings.Join(track.Conventions, ", ")))
		}
	}
	b.WriteString("\n")

	// Code context (only when provided)
	if codeContext != "" {
		b.WriteString("## Relevant Code\n\n")
		b.WriteString(codeContext)
		b.WriteString("\n\n")
	}

	// Classification instructions
	b.WriteString("## Instructions\n\n")
	b.WriteString("1. **Classify** the issue as one of: `bug`, `task`, `question`, `reject`\n")
	b.WriteString("2. **Assign priority** from 0 (critical) to 4 (won't fix):\n")
	b.WriteString("   - 0: Critical — security vulnerability, data loss, complete breakage\n")
	b.WriteString("   - 1: High — blocks users, major feature broken\n")
	b.WriteString("   - 2: Medium — degraded experience, workaround exists\n")
	b.WriteString("   - 3: Low — minor issue, cosmetic, nice-to-have\n")
	b.WriteString("   - 4: Minimal — won't fix soon, backlog\n")
	b.WriteString("3. **Pick a track** from the available tracks list above\n")
	b.WriteString("4. **Generate a title** — concise, actionable summary\n")
	b.WriteString("5. **Write a description** — clear explanation of the work needed\n")
	b.WriteString("6. **Define acceptance criteria** — how to verify the work is done\n")

	if mode == "full" {
		b.WriteString("7. **Write design_notes** — technical approach, architecture decisions, implementation strategy\n")
		b.WriteString("8. **List dependencies** — other issues or components this depends on\n")
	}

	b.WriteString("\nIf the issue should be rejected (spam, duplicate, out of scope), set classification to `reject` and provide a `reject_reason`.\n\n")

	// Output format
	b.WriteString("## Output Format\n\n")
	b.WriteString("Respond with a single JSON object:\n\n")
	b.WriteString("```json\n")
	b.WriteString("{\n")
	b.WriteString("  \"classification\": \"bug|task|question|reject\",\n")
	b.WriteString("  \"priority\": 0,\n")
	b.WriteString("  \"track\": \"<track-name>\",\n")
	b.WriteString("  \"title\": \"<concise title>\",\n")
	b.WriteString("  \"description\": \"<detailed description>\",\n")
	b.WriteString("  \"acceptance\": \"<acceptance criteria>\"")

	if mode == "full" {
		b.WriteString(",\n  \"design_notes\": \"<technical approach and design decisions>\",\n")
		b.WriteString("  \"dependencies\": [\"<dependency-1>\", \"<dependency-2>\"]")
	}

	b.WriteString("\n}\n")
	b.WriteString("```\n")

	return b.String()
}

// codeFenceRe matches JSON wrapped in markdown code fences.
var codeFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(.*?)\\n?```")

// ParseTriageResult extracts and validates a TriageResult from an AI response.
// The response may contain JSON directly or wrapped in markdown code fences.
func ParseTriageResult(response string) (*TriageResult, error) {
	jsonStr := response

	// Try to extract JSON from code fences first.
	if matches := codeFenceRe.FindStringSubmatch(response); len(matches) > 1 {
		jsonStr = strings.TrimSpace(matches[1])
	}

	jsonStr = strings.TrimSpace(jsonStr)

	var result TriageResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("bull: failed to parse triage response as JSON: %w", err)
	}

	// Validate required fields.
	if result.Classification == "" {
		return nil, fmt.Errorf("bull: missing required field: classification")
	}
	if result.Title == "" {
		return nil, fmt.Errorf("bull: missing required field: title")
	}

	return &result, nil
}
