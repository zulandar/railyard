package inspect

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
)

// InlineComment represents a single inline review comment on a PR diff.
type InlineComment struct {
	Path string
	Line int
	Side string
	Body string
}

// GitHubClient wraps the google/go-github client with rate-limit handling
// for PR review operations.
type GitHubClient struct {
	client             *github.Client
	owner              string
	repo               string
	rateLimitThreshold int
}

// NewGitHubClient constructs a GitHubClient authenticated as a GitHub App
// installation using the credentials in cfg.
func NewGitHubClient(owner, repo string, cfg config.InspectConfig) (*GitHubClient, error) {
	itr, err := ghinstallation.NewKeyFromFile(
		http.DefaultTransport, cfg.AppID, cfg.InstallationID, cfg.PrivateKeyPath,
	)
	if err != nil {
		return nil, fmt.Errorf("inspect: github app auth: %w", err)
	}
	tc := &http.Client{Transport: itr}
	return &GitHubClient{
		client:             github.NewClient(tc),
		owner:              owner,
		repo:               repo,
		rateLimitThreshold: 100,
	}, nil
}

// GetBotLogin returns the authenticated bot's login name (e.g. "my-app[bot]").
func (g *GitHubClient) GetBotLogin(ctx context.Context) (string, error) {
	user, resp, err := g.client.Users.Get(ctx, "")
	if err != nil {
		return "", fmt.Errorf("inspect: get authenticated user: %w", err)
	}
	g.waitIfRateLimited(resp)
	return user.GetLogin(), nil
}

// ListReviewablePRs returns open, non-draft pull requests with pagination.
func (g *GitHubClient) ListReviewablePRs(ctx context.Context) ([]*github.PullRequest, error) {
	opts := &github.PullRequestListOptions{
		State: "open",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}
	var all []*github.PullRequest
	for {
		prs, resp, err := g.client.PullRequests.List(ctx, g.owner, g.repo, opts)
		if err != nil {
			if _, ok := g.handleRateLimitError(resp, err); ok {
				prs, resp, err = g.client.PullRequests.List(ctx, g.owner, g.repo, opts)
				if err != nil {
					return nil, fmt.Errorf("inspect: list PRs retry: %w", err)
				}
			} else {
				return nil, fmt.Errorf("inspect: list PRs: %w", err)
			}
		}
		for _, pr := range prs {
			if !pr.GetDraft() {
				all = append(all, pr)
			}
		}
		g.waitIfRateLimited(resp)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return all, nil
}

// GetPRDiff returns the unified diff for the given pull request.
func (g *GitHubClient) GetPRDiff(ctx context.Context, number int) (string, error) {
	raw, resp, err := g.client.PullRequests.GetRaw(ctx, g.owner, g.repo, number, github.RawOptions{
		Type: github.Diff,
	})
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			raw, resp, err = g.client.PullRequests.GetRaw(ctx, g.owner, g.repo, number, github.RawOptions{
				Type: github.Diff,
			})
			if err != nil {
				return "", fmt.Errorf("inspect: get PR #%d diff retry: %w", number, err)
			}
		} else {
			return "", fmt.Errorf("inspect: get PR #%d diff: %w", number, err)
		}
	}
	g.waitIfRateLimited(resp)
	return raw, nil
}

// ListPRFiles returns the list of changed files for the given pull request,
// paginating through all pages.
func (g *GitHubClient) ListPRFiles(ctx context.Context, number int) ([]*github.CommitFile, error) {
	opts := &github.ListOptions{PerPage: 100}
	var allFiles []*github.CommitFile
	for {
		files, resp, err := g.client.PullRequests.ListFiles(ctx, g.owner, g.repo, number, opts)
		if err != nil {
			if _, ok := g.handleRateLimitError(resp, err); ok {
				files, resp, err = g.client.PullRequests.ListFiles(ctx, g.owner, g.repo, number, opts)
				if err != nil {
					return nil, fmt.Errorf("inspect: list PR #%d files retry: %w", number, err)
				}
			} else {
				return nil, fmt.Errorf("inspect: list PR #%d files: %w", number, err)
			}
		}
		allFiles = append(allFiles, files...)
		g.waitIfRateLimited(resp)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return allFiles, nil
}

// GetFileContent returns the text content of a file at the given ref (branch, tag, or SHA).
func (g *GitHubClient) GetFileContent(ctx context.Context, path, ref string) (string, error) {
	opts := &github.RepositoryContentGetOptions{Ref: ref}
	fileContent, _, resp, err := g.client.Repositories.GetContents(ctx, g.owner, g.repo, path, opts)
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			fileContent, _, resp, err = g.client.Repositories.GetContents(ctx, g.owner, g.repo, path, opts)
			if err != nil {
				return "", fmt.Errorf("inspect: get file %q at %s retry: %w", path, ref, err)
			}
		} else {
			return "", fmt.Errorf("inspect: get file %q at %s: %w", path, ref, err)
		}
	}
	g.waitIfRateLimited(resp)
	if fileContent == nil {
		return "", fmt.Errorf("inspect: %q at %s is a directory, not a file", path, ref)
	}
	content, err := fileContent.GetContent()
	if err != nil {
		return "", fmt.Errorf("inspect: decode file %q at %s: %w", path, ref, err)
	}
	return content, nil
}

// GetPRState returns the state (open/closed) and whether the PR has been merged.
func (g *GitHubClient) GetPRState(ctx context.Context, number int) (state string, merged bool, err error) {
	pr, resp, err := g.client.PullRequests.Get(ctx, g.owner, g.repo, number)
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			pr, resp, err = g.client.PullRequests.Get(ctx, g.owner, g.repo, number)
			if err != nil {
				return "", false, fmt.Errorf("inspect: get PR #%d state retry: %w", number, err)
			}
		} else {
			return "", false, fmt.Errorf("inspect: get PR #%d state: %w", number, err)
		}
	}
	g.waitIfRateLimited(resp)
	return pr.GetState(), pr.GetMerged(), nil
}

// SubmitReview posts a PR review with COMMENT event. It includes inline
// comments as DraftReviewComments and the summary as the review body.
func (g *GitHubClient) SubmitReview(ctx context.Context, number int, summary string, comments []InlineComment) error {
	var draftComments []*github.DraftReviewComment
	for _, c := range comments {
		draftComments = append(draftComments, &github.DraftReviewComment{
			Path: github.Ptr(c.Path),
			Line: github.Ptr(c.Line),
			Side: github.Ptr(c.Side),
			Body: github.Ptr(c.Body),
		})
	}
	review := &github.PullRequestReviewRequest{
		Body:     github.Ptr(summary),
		Event:    github.Ptr("COMMENT"),
		Comments: draftComments,
	}
	_, resp, err := g.client.PullRequests.CreateReview(ctx, g.owner, g.repo, number, review)
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			_, resp, err = g.client.PullRequests.CreateReview(ctx, g.owner, g.repo, number, review)
			if err != nil {
				return fmt.Errorf("inspect: submit review on PR #%d retry: %w", number, err)
			}
		} else {
			return fmt.Errorf("inspect: submit review on PR #%d: %w", number, err)
		}
	}
	g.waitIfRateLimited(resp)
	return nil
}

// DismissReviewsByBot lists reviews on a PR and dismisses those authored by botLogin.
func (g *GitHubClient) DismissReviewsByBot(ctx context.Context, number int, botLogin string) error {
	opts := &github.ListOptions{PerPage: 100}
	for {
		reviews, resp, err := g.client.PullRequests.ListReviews(ctx, g.owner, g.repo, number, opts)
		if err != nil {
			if _, ok := g.handleRateLimitError(resp, err); ok {
				reviews, resp, err = g.client.PullRequests.ListReviews(ctx, g.owner, g.repo, number, opts)
				if err != nil {
					return fmt.Errorf("inspect: list reviews on PR #%d retry: %w", number, err)
				}
			} else {
				return fmt.Errorf("inspect: list reviews on PR #%d: %w", number, err)
			}
		}
		for _, r := range reviews {
			if r.GetUser().GetLogin() == botLogin {
				_, dresp, derr := g.client.PullRequests.DismissReview(ctx, g.owner, g.repo, number, r.GetID(), &github.PullRequestReviewDismissalRequest{
					Message: github.Ptr("Superseded by new review."),
				})
				if derr != nil {
					if _, ok := g.handleRateLimitError(dresp, derr); ok {
						_, dresp, derr = g.client.PullRequests.DismissReview(ctx, g.owner, g.repo, number, r.GetID(), &github.PullRequestReviewDismissalRequest{
							Message: github.Ptr("Superseded by new review."),
						})
						if derr != nil {
							return fmt.Errorf("inspect: dismiss review %d on PR #%d retry: %w", r.GetID(), number, derr)
						}
					} else {
						return fmt.Errorf("inspect: dismiss review %d on PR #%d: %w", r.GetID(), number, derr)
					}
				}
				g.waitIfRateLimited(dresp)
			}
		}
		g.waitIfRateLimited(resp)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return nil
}

// AddLabel adds a label to the given PR/issue.
func (g *GitHubClient) AddLabel(ctx context.Context, number int, label string) error {
	_, resp, err := g.client.Issues.AddLabelsToIssue(ctx, g.owner, g.repo, number, []string{label})
	if err != nil {
		if _, ok := g.handleRateLimitError(resp, err); ok {
			_, resp, err = g.client.Issues.AddLabelsToIssue(ctx, g.owner, g.repo, number, []string{label})
			if err != nil {
				return fmt.Errorf("inspect: add label %q to #%d retry: %w", label, number, err)
			}
		} else {
			return fmt.Errorf("inspect: add label %q to #%d: %w", label, number, err)
		}
	}
	g.waitIfRateLimited(resp)
	return nil
}

// RemoveLabel removes a label from the given PR/issue. It tolerates 404
// errors (label not present).
func (g *GitHubClient) RemoveLabel(ctx context.Context, number int, label string) error {
	resp, err := g.client.Issues.RemoveLabelForIssue(ctx, g.owner, g.repo, number, label)
	if err != nil {
		// Tolerate 404 — the label was already absent.
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil
		}
		if _, ok := g.handleRateLimitError(resp, err); ok {
			resp, err = g.client.Issues.RemoveLabelForIssue(ctx, g.owner, g.repo, number, label)
			if err != nil {
				if resp != nil && resp.StatusCode == http.StatusNotFound {
					return nil
				}
				return fmt.Errorf("inspect: remove label %q from #%d retry: %w", label, number, err)
			}
		} else {
			return fmt.Errorf("inspect: remove label %q from #%d: %w", label, number, err)
		}
	}
	g.waitIfRateLimited(resp)
	return nil
}

// CountNonBotComments counts inline review comments on a PR that were not
// authored by botLogin.
// CountTotalComments returns the total number of comments on a PR (conversation
// + inline review comments), matching the same counting method used by
// yardmaster's CountComments. Bot-authored comments are excluded so that
// Inspection Pit's own comments don't inflate LastPRCommentCount.
func (g *GitHubClient) CountTotalComments(ctx context.Context, number int, botLogin string) (int, error) {
	count := 0

	// Step 1: Count conversation (issue) comments, excluding bot.
	issueOpts := &github.IssueListCommentsOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		comments, resp, err := g.client.Issues.ListComments(ctx, g.owner, g.repo, number, issueOpts)
		if err != nil {
			return 0, fmt.Errorf("inspect: list conversation comments on PR #%d: %w", number, err)
		}
		for _, c := range comments {
			if c.GetUser().GetLogin() != botLogin {
				count++
			}
		}
		g.waitIfRateLimited(resp)
		if resp.NextPage == 0 {
			break
		}
		issueOpts.Page = resp.NextPage
	}

	// Step 2: Count inline review comments, excluding bot.
	reviewOpts := &github.PullRequestListCommentsOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		comments, resp, err := g.client.PullRequests.ListComments(ctx, g.owner, g.repo, number, reviewOpts)
		if err != nil {
			if _, ok := g.handleRateLimitError(resp, err); ok {
				comments, resp, err = g.client.PullRequests.ListComments(ctx, g.owner, g.repo, number, reviewOpts)
				if err != nil {
					return 0, fmt.Errorf("inspect: count inline comments on PR #%d retry: %w", number, err)
				}
			} else {
				return 0, fmt.Errorf("inspect: count inline comments on PR #%d: %w", number, err)
			}
		}
		for _, c := range comments {
			if c.GetUser().GetLogin() != botLogin {
				count++
			}
		}
		g.waitIfRateLimited(resp)
		if resp.NextPage == 0 {
			break
		}
		reviewOpts.Page = resp.NextPage
	}

	return count, nil
}

// PRHasLabel checks whether the given PR/issue has a specific label.
func (g *GitHubClient) PRHasLabel(ctx context.Context, number int, label string) (bool, error) {
	opts := &github.ListOptions{PerPage: 100}
	for {
		labels, resp, err := g.client.Issues.ListLabelsByIssue(ctx, g.owner, g.repo, number, opts)
		if err != nil {
			if _, ok := g.handleRateLimitError(resp, err); ok {
				labels, resp, err = g.client.Issues.ListLabelsByIssue(ctx, g.owner, g.repo, number, opts)
				if err != nil {
					return false, fmt.Errorf("inspect: list labels on #%d retry: %w", number, err)
				}
			} else {
				return false, fmt.Errorf("inspect: list labels on #%d: %w", number, err)
			}
		}
		for _, l := range labels {
			if l.GetName() == label {
				return true, nil
			}
		}
		g.waitIfRateLimited(resp)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return false, nil
}

// EnsureLabels creates any missing labels defined in the InspectLabelsConfig.
func (g *GitHubClient) EnsureLabels(ctx context.Context, labels config.InspectLabelsConfig) error {
	required := map[string]string{
		labels.InProgress: "1d76db",
		labels.Reviewed:   "0e8a16",
		labels.ReReview:   "fbca04",
	}

	// Fetch existing labels.
	existing := make(map[string]bool)
	opts := &github.ListOptions{PerPage: 100}
	for {
		repoLabels, resp, err := g.client.Issues.ListLabels(ctx, g.owner, g.repo, opts)
		if err != nil {
			if _, ok := g.handleRateLimitError(resp, err); ok {
				repoLabels, resp, err = g.client.Issues.ListLabels(ctx, g.owner, g.repo, opts)
				if err != nil {
					return fmt.Errorf("inspect: list labels retry: %w", err)
				}
			} else {
				return fmt.Errorf("inspect: list labels: %w", err)
			}
		}
		for _, l := range repoLabels {
			existing[l.GetName()] = true
		}
		g.waitIfRateLimited(resp)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	// Create missing labels.
	for name, color := range required {
		if name == "" {
			continue
		}
		if existing[name] {
			continue
		}
		label := &github.Label{
			Name:  github.Ptr(name),
			Color: github.Ptr(color),
		}
		_, resp, err := g.client.Issues.CreateLabel(ctx, g.owner, g.repo, label)
		if err != nil {
			if _, ok := g.handleRateLimitError(resp, err); ok {
				_, resp, err = g.client.Issues.CreateLabel(ctx, g.owner, g.repo, label)
				if err != nil {
					return fmt.Errorf("inspect: create label %q retry: %w", name, err)
				}
			} else {
				return fmt.Errorf("inspect: create label %q: %w", name, err)
			}
		}
		g.waitIfRateLimited(resp)
	}
	return nil
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
		slog.Warn("inspect: rate limited, sleeping until reset",
			"reset", resetTime,
			"duration", sleepDuration,
		)
		time.Sleep(sleepDuration)
	}
}
