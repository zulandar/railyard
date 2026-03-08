package bull

import (
	"fmt"
	"strings"

	"github.com/google/go-github/v68/github"
)

// FilterResult holds the outcome of heuristic filtering.
type FilterResult struct {
	Pass   bool
	Reason string
}

// ExistingIssue represents a tracked issue for duplicate checking.
type ExistingIssue struct {
	IssueNumber int
	Title       string
}

// FilterIssue applies heuristic pre-filter checks to a GitHub issue before
// AI triage. It rejects issues that clearly don't need AI analysis, saving
// token costs. The tracked slice contains already-known bull issues for
// duplicate detection.
func FilterIssue(issue *github.Issue, ignoreLabel string, tracked []ExistingIssue) FilterResult {
	// 1. Nil issue
	if issue == nil {
		return FilterResult{Pass: false, Reason: "nil issue"}
	}

	// 2. Pull request
	if issue.PullRequestLinks != nil {
		return FilterResult{Pass: false, Reason: "pull request, not an issue"}
	}

	// 3. Ignore label
	for _, label := range issue.Labels {
		if label.GetName() == ignoreLabel {
			return FilterResult{Pass: false, Reason: "has ignore label"}
		}
	}

	// 4. Body too short
	body := issue.GetBody()
	if len(body) < 20 {
		return FilterResult{Pass: false, Reason: "body too short (minimum 20 characters)"}
	}

	// 5. Already tracked
	issueNum := issue.GetNumber()
	for _, t := range tracked {
		if t.IssueNumber == issueNum {
			return FilterResult{Pass: false, Reason: "already tracked as bull issue"}
		}
	}

	// 6. Near-duplicate title
	title := strings.ToLower(strings.TrimSpace(issue.GetTitle()))
	for _, t := range tracked {
		existing := strings.ToLower(strings.TrimSpace(t.Title))
		if existing == "" || title == "" {
			continue
		}
		if title == existing || strings.Contains(title, existing) || strings.Contains(existing, title) {
			return FilterResult{
				Pass:   false,
				Reason: fmt.Sprintf("duplicate of existing issue #%d", t.IssueNumber),
			}
		}
	}

	return FilterResult{Pass: true}
}
