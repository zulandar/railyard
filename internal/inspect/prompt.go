// Package inspect implements automated PR code review for the Inspection Pit daemon.
package inspect

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ReviewContext holds the inputs for building an AI review prompt.
type ReviewContext struct {
	PRNumber      int
	PRTitle       string
	Diff          string
	Files         []FileContext
	CodeContext    string
	TrackName     string
	TrackLanguage string
	Conventions   map[string]interface{}
	Truncated     bool
	TotalFiles    int
	IncludedFiles int
}

// FileContext represents a source file included for review context.
type FileContext struct {
	Path    string
	Content string
}

// ReviewResult is the structured output from an AI code review.
type ReviewResult struct {
	Summary  string          `json:"summary"`
	Comments []ReviewComment `json:"comments"`
	Severity string          `json:"severity"`
}

// ReviewComment is a single inline comment on a PR diff.
type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// DiffFile represents a single file's diff with metadata.
type DiffFile struct {
	Path  string
	Diff  string
	Lines int
}

// TruncateResult holds the outcome of diff truncation.
type TruncateResult struct {
	Files         []DiffFile
	Truncated     bool
	TotalFiles    int
	IncludedFiles int
}

// generatedPatterns lists path patterns for files that should be excluded
// from review because they are generated or vendored.
var generatedPatterns = []string{
	"vendor/",
	"go.sum",
	"package-lock.json",
	".min.js",
	".min.css",
	"_gen.go",
	".generated.go",
	".pb.go",
}

// isGenerated reports whether the given file path matches a generated/vendored pattern.
func isGenerated(path string) bool {
	for _, pat := range generatedPatterns {
		if strings.Contains(path, pat) {
			return true
		}
	}
	return false
}

// TruncateDiff filters generated/vendored files, sorts by change size
// descending, and truncates when cumulative lines exceed maxLines.
// At least one file is always included even if it exceeds the limit.
func TruncateDiff(files []DiffFile, maxLines int) TruncateResult {
	// Filter out generated/vendored files.
	var filtered []DiffFile
	for _, f := range files {
		if !isGenerated(f.Path) {
			filtered = append(filtered, f)
		}
	}

	totalFiles := len(filtered)

	// Sort by change size descending.
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Lines > filtered[j].Lines
	})

	// Accumulate files up to the line limit.
	var included []DiffFile
	cumulative := 0
	for i, f := range filtered {
		if i > 0 && cumulative+f.Lines > maxLines {
			break
		}
		included = append(included, f)
		cumulative += f.Lines
	}

	// Always include at least 1 file if available.
	if len(included) == 0 && len(filtered) > 0 {
		included = append(included, filtered[0])
	}

	truncated := len(included) < totalFiles

	return TruncateResult{
		Files:         included,
		Truncated:     truncated,
		TotalFiles:    totalFiles,
		IncludedFiles: len(included),
	}
}

// BuildReviewPrompt constructs the AI prompt for reviewing a pull request.
func BuildReviewPrompt(ctx ReviewContext) string {
	var b strings.Builder

	// Role description.
	b.WriteString("You are the Inspection Pit, Railyard's automated code reviewer.\n")
	b.WriteString("Your job is to review pull request diffs and provide actionable feedback.\n\n")

	// PR metadata.
	fmt.Fprintf(&b, "## Pull Request #%d: %s\n\n", ctx.PRNumber, ctx.PRTitle)

	// Track info.
	if ctx.TrackName != "" || ctx.TrackLanguage != "" {
		b.WriteString("### Track Info\n")
		if ctx.TrackName != "" {
			fmt.Fprintf(&b, "- Track: %s\n", ctx.TrackName)
		}
		if ctx.TrackLanguage != "" {
			fmt.Fprintf(&b, "- Language: %s\n", ctx.TrackLanguage)
		}
		b.WriteString("\n")
	}

	// Conventions.
	if len(ctx.Conventions) > 0 {
		b.WriteString("### Conventions\n")
		for k, v := range ctx.Conventions {
			fmt.Fprintf(&b, "- %s: %v\n", k, v)
		}
		b.WriteString("\n")
	}

	// Truncation note.
	if ctx.Truncated {
		fmt.Fprintf(&b, "**Note:** The diff has been truncated. Showing %d of %d total files.\n\n",
			ctx.IncludedFiles, ctx.TotalFiles)
	}

	// Diff in a code fence.
	b.WriteString("### Diff\n")
	b.WriteString("```diff\n")
	b.WriteString(ctx.Diff)
	if !strings.HasSuffix(ctx.Diff, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")

	// Full file contents.
	if len(ctx.Files) > 0 {
		b.WriteString("### Full File Contents\n")
		for _, f := range ctx.Files {
			fmt.Fprintf(&b, "#### %s\n", f.Path)
			b.WriteString("```\n")
			b.WriteString(f.Content)
			if !strings.HasSuffix(f.Content, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n\n")
		}
	}

	// Code context for deep review.
	if ctx.CodeContext != "" {
		b.WriteString("### Additional Code Context\n")
		b.WriteString(ctx.CodeContext)
		b.WriteString("\n\n")
	}

	// Review focus areas.
	b.WriteString("### Review Focus\n")
	b.WriteString("Focus on the following areas:\n")
	b.WriteString("- Bugs and logic errors\n")
	b.WriteString("- Security vulnerabilities\n")
	b.WriteString("- Error handling issues\n")
	b.WriteString("- Style consistency\n\n")
	b.WriteString("Do NOT comment on:\n")
	b.WriteString("- Nitpicks or minor style preferences\n")
	b.WriteString("- Refactoring suggestions\n")
	b.WriteString("- Feature requests\n\n")

	// Output format specification.
	b.WriteString("### Output Format\n")
	b.WriteString("Respond with a JSON object in the following format:\n")
	b.WriteString("```json\n")
	b.WriteString(`{
  "summary": "Brief overall assessment of the PR",
  "comments": [
    {
      "path": "file/path.go",
      "line": 42,
      "side": "RIGHT",
      "body": "Description of the issue"
    }
  ],
  "severity": "info|warning|critical"
}`)
	b.WriteString("\n```\n")

	return b.String()
}

// ParseReviewResult parses the AI's JSON response into a ReviewResult.
// It handles raw JSON and markdown-fenced JSON (```json...```).
func ParseReviewResult(raw string) (*ReviewResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty review result")
	}

	// Strip markdown code fences if present.
	content := raw
	if strings.HasPrefix(content, "```") {
		// Remove opening fence (e.g., "```json\n").
		idx := strings.Index(content, "\n")
		if idx >= 0 {
			content = content[idx+1:]
		}
		// Remove closing fence.
		if last := strings.LastIndex(content, "```"); last >= 0 {
			content = content[:last]
		}
		content = strings.TrimSpace(content)
	}

	var result ReviewResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse review result: %w", err)
	}

	return &result, nil
}
