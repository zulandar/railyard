package bull

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v68/github"
	"golang.org/x/oauth2"
)

// GitHubClient wraps the google/go-github client with rate-limit handling.
type GitHubClient struct {
	client             *github.Client
	owner              string
	repo               string
	rateLimitThreshold int
}

// NewClient constructs a GitHubClient authenticated with the given oauth2 token.
func NewClient(owner, repo, token string) *GitHubClient {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)
	return &GitHubClient{
		client:             github.NewClient(tc),
		owner:              owner,
		repo:               repo,
		rateLimitThreshold: 100,
	}
}

// ListNewIssues returns open issues created or updated since the given time.
// It paginates through all pages to avoid silently dropping issues.
func (g *GitHubClient) ListNewIssues(ctx context.Context, since time.Time) ([]*github.Issue, error) {
	opts := &github.IssueListByRepoOptions{
		State: "open",
		Since: since,
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}
	var allIssues []*github.Issue
	for {
		issues, resp, err := g.client.Issues.ListByRepo(ctx, g.owner, g.repo, opts)
		if err != nil {
			if _, ok := g.handleRateLimitError(resp, err); ok {
				issues, resp, err = g.client.Issues.ListByRepo(ctx, g.owner, g.repo, opts)
				if err != nil {
					return nil, fmt.Errorf("bull: list issues retry: %w", err)
				}
			} else {
				return nil, fmt.Errorf("bull: list issues: %w", err)
			}
		}
		allIssues = append(allIssues, issues...)
		g.waitIfRateLimited(resp)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return allIssues, nil
}

// GetIssue retrieves a single issue by number.
func (g *GitHubClient) GetIssue(ctx context.Context, number int) (*github.Issue, error) {
	issue, resp, err := g.client.Issues.Get(ctx, g.owner, g.repo, number)
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			issue, resp, err = g.client.Issues.Get(ctx, g.owner, g.repo, number)
			if err != nil {
				return nil, fmt.Errorf("bull: get issue #%d retry: %w", number, err)
			}
		} else {
			return nil, fmt.Errorf("bull: get issue #%d: %w", number, err)
		}
	}
	g.waitIfRateLimited(resp)
	return issue, nil
}

// AddLabel adds a label to the given issue.
func (g *GitHubClient) AddLabel(ctx context.Context, number int, label string) error {
	_, resp, err := g.client.Issues.AddLabelsToIssue(ctx, g.owner, g.repo, number, []string{label})
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			_, resp, err = g.client.Issues.AddLabelsToIssue(ctx, g.owner, g.repo, number, []string{label})
			if err != nil {
				return fmt.Errorf("bull: add label %q to #%d retry: %w", label, number, err)
			}
		} else {
			return fmt.Errorf("bull: add label %q to #%d: %w", label, number, err)
		}
	}
	g.waitIfRateLimited(resp)
	return nil
}

// RemoveLabel removes a label from the given issue.
func (g *GitHubClient) RemoveLabel(ctx context.Context, number int, label string) error {
	resp, err := g.client.Issues.RemoveLabelForIssue(ctx, g.owner, g.repo, number, label)
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			resp, err = g.client.Issues.RemoveLabelForIssue(ctx, g.owner, g.repo, number, label)
			if err != nil {
				return fmt.Errorf("bull: remove label %q from #%d retry: %w", label, number, err)
			}
		} else {
			return fmt.Errorf("bull: remove label %q from #%d: %w", label, number, err)
		}
	}
	g.waitIfRateLimited(resp)
	return nil
}

// CreateLabel creates a new label in the repository.
func (g *GitHubClient) CreateLabel(ctx context.Context, name, color, description string) error {
	label := &github.Label{
		Name:        github.Ptr(name),
		Color:       github.Ptr(strings.TrimPrefix(color, "#")),
		Description: github.Ptr(description),
	}
	_, resp, err := g.client.Issues.CreateLabel(ctx, g.owner, g.repo, label)
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			_, resp, err = g.client.Issues.CreateLabel(ctx, g.owner, g.repo, label)
			if err != nil {
				return fmt.Errorf("bull: create label %q retry: %w", name, err)
			}
		} else {
			return fmt.Errorf("bull: create label %q: %w", name, err)
		}
	}
	g.waitIfRateLimited(resp)
	return nil
}

// ListLabels returns the names of all labels in the repository.
func (g *GitHubClient) ListLabels(ctx context.Context) ([]string, error) {
	opts := &github.ListOptions{PerPage: 100}
	var allNames []string
	for {
		labels, resp, err := g.client.Issues.ListLabels(ctx, g.owner, g.repo, opts)
		if err != nil {
			if _, ok := g.handleRateLimitError(resp, err); ok {
				labels, resp, err = g.client.Issues.ListLabels(ctx, g.owner, g.repo, opts)
				if err != nil {
					return nil, fmt.Errorf("bull: list labels retry: %w", err)
				}
			} else {
				return nil, fmt.Errorf("bull: list labels: %w", err)
			}
		}
		for _, l := range labels {
			allNames = append(allNames, l.GetName())
		}
		g.waitIfRateLimited(resp)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return allNames, nil
}

// AddComment posts a comment on the given issue.
func (g *GitHubClient) AddComment(ctx context.Context, number int, body string) error {
	comment := &github.IssueComment{Body: github.Ptr(body)}
	_, resp, err := g.client.Issues.CreateComment(ctx, g.owner, g.repo, number, comment)
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			_, resp, err = g.client.Issues.CreateComment(ctx, g.owner, g.repo, number, comment)
			if err != nil {
				return fmt.Errorf("bull: add comment to #%d retry: %w", number, err)
			}
		} else {
			return fmt.Errorf("bull: add comment to #%d: %w", number, err)
		}
	}
	g.waitIfRateLimited(resp)
	return nil
}

// CloseIssue adds a comment and then closes the issue.
func (g *GitHubClient) CloseIssue(ctx context.Context, number int, comment string) error {
	if err := g.AddComment(ctx, number, comment); err != nil {
		return fmt.Errorf("bull: close issue #%d comment: %w", number, err)
	}

	state := "closed"
	_, resp, err := g.client.Issues.Edit(ctx, g.owner, g.repo, number, &github.IssueRequest{
		State: &state,
	})
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			_, resp, err = g.client.Issues.Edit(ctx, g.owner, g.repo, number, &github.IssueRequest{
				State: &state,
			})
			if err != nil {
				return fmt.Errorf("bull: close issue #%d retry: %w", number, err)
			}
		} else {
			return fmt.Errorf("bull: close issue #%d: %w", number, err)
		}
	}
	g.waitIfRateLimited(resp)
	return nil
}

// ListReleases returns releases created since the given time.
func (g *GitHubClient) ListReleases(ctx context.Context, since time.Time) ([]*github.RepositoryRelease, error) {
	opts := &github.ListOptions{PerPage: 100}
	releases, resp, err := g.client.Repositories.ListReleases(ctx, g.owner, g.repo, opts)
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			releases, resp, err = g.client.Repositories.ListReleases(ctx, g.owner, g.repo, opts)
			if err != nil {
				return nil, fmt.Errorf("bull: list releases retry: %w", err)
			}
		} else {
			return nil, fmt.Errorf("bull: list releases: %w", err)
		}
	}
	g.waitIfRateLimited(resp)

	var filtered []*github.RepositoryRelease
	for _, r := range releases {
		if r.CreatedAt != nil && r.CreatedAt.After(since) {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

// handleRateLimitError checks if the error is a 403 rate limit response.
// If so, it sleeps until the reset time and returns true to signal a retry.
func (g *GitHubClient) handleRateLimitError(resp *github.Response, err error) (*github.Response, bool) {
	if resp == nil {
		return resp, false
	}
	if resp.StatusCode != http.StatusForbidden {
		return resp, false
	}
	// Check if this is actually a rate limit error.
	if errResp, ok := err.(*github.ErrorResponse); ok {
		if !strings.Contains(errResp.Message, "rate limit") {
			return resp, false
		}
	}
	g.sleepUntilReset(resp)
	return resp, true
}

// waitIfRateLimited sleeps until the reset time if remaining calls are below the threshold.
func (g *GitHubClient) waitIfRateLimited(resp *github.Response) {
	if resp == nil {
		return
	}
	if resp.Rate.Remaining < g.rateLimitThreshold {
		g.sleepUntilReset(resp)
	}
}

// sleepUntilReset sleeps until the rate limit reset time.
func (g *GitHubClient) sleepUntilReset(resp *github.Response) {
	resetTime := resp.Rate.Reset.Time
	sleepDuration := time.Until(resetTime)
	if sleepDuration > 0 {
		time.Sleep(sleepDuration)
	}
}
