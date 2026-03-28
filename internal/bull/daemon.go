package bull

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/logutil"
	"github.com/zulandar/railyard/internal/models"
)

const defaultBullPollInterval = 60 * time.Second

// DaemonClient combines all GitHub operations the bull daemon needs.
type DaemonClient interface {
	ListNewIssues(ctx context.Context, since time.Time) ([]*github.Issue, error)
	// TriageClient
	AddComment(ctx context.Context, number int, body string) error
	AddLabel(ctx context.Context, number int, label string) error
	RemoveLabel(ctx context.Context, number int, label string) error
	// ReleaseClient
	ListReleases(ctx context.Context, since time.Time) ([]*github.RepositoryRelease, error)
	CloseIssue(ctx context.Context, number int, comment string) error
}

// DaemonStore combines all database operations the bull daemon needs.
type DaemonStore interface {
	// SyncStore
	GetTrackedIssues(ctx context.Context) ([]models.BullIssue, error)
	GetCarStatus(ctx context.Context, carID string) (string, error)
	UpdateIssueStatus(ctx context.Context, issueID uint, newStatus string) error
	// ReleaseStore
	GetMergedIssues(ctx context.Context) ([]models.BullIssue, error)
	GetLastReleaseCheck(ctx context.Context) (time.Time, error)
	SetLastReleaseCheck(ctx context.Context, t time.Time) error
	// TriageStore
	CreateCar(ctx context.Context, opts CarCreateOpts) (string, error)
	RecordTriagedIssue(ctx context.Context, issue models.BullIssue) error
}

// DaemonOpts bundles all configuration for RunDaemon.
type DaemonOpts struct {
	Config         config.BullConfig
	Tracks         []string
	BranchPrefix   string
	PollInterval   time.Duration
	Out            io.Writer
	AI             TriageAI
	CodeContext    string
	RateLimitUntil time.Time
	OnCycleEnd     func(cycle int) // test hook; called after each cycle
}

// RunDaemon runs the bull daemon loop with six phases per cycle:
//  1. Poll GitHub for new/updated issues
//  2. Filter already-tracked and ignored
//  3. Heuristic filter
//  4. AI triage for passing issues
//  5. Reverse sync car statuses
//  6. Release scan
func RunDaemon(ctx context.Context, deps interface {
	DaemonClient
	DaemonStore
}, opts DaemonOpts) error {
	if opts.PollInterval <= 0 {
		if opts.Config.PollIntervalSec > 0 {
			opts.PollInterval = time.Duration(opts.Config.PollIntervalSec) * time.Second
		} else {
			opts.PollInterval = defaultBullPollInterval
		}
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	out = logutil.NewTimestampWriter(out)

	fmt.Fprintf(out, "Bull daemon starting (poll every %s)...\n", opts.PollInterval)

	lastPoll := time.Time{}
	cycle := 0

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(out, "Bull daemon shutting down...\n")
			return nil
		default:
		}

		// Rate limit backoff
		if !opts.RateLimitUntil.IsZero() && time.Now().Before(opts.RateLimitUntil) {
			fmt.Fprintf(out, "Bull: rate limit backoff until %s\n", opts.RateLimitUntil.Format(time.RFC3339))
			sleepCtx(ctx, opts.PollInterval)
			continue
		}
		opts.RateLimitUntil = time.Time{} // reset after backoff

		// Collect tracked issues once per cycle for filtering.
		trackedIssues, err := deps.GetTrackedIssues(ctx)
		if err != nil {
			log.Printf("bull: get tracked issues: %v", err)
			trackedIssues = nil
		}
		tracked := make([]ExistingIssue, len(trackedIssues))
		for i, ti := range trackedIssues {
			tracked[i] = ExistingIssue{IssueNumber: ti.IssueNumber, Title: ""}
		}
		trackedSet := make(map[int]bool, len(trackedIssues))
		for _, ti := range trackedIssues {
			trackedSet[ti.IssueNumber] = true
		}

		// Phase 1: Poll GitHub for new/updated issues.
		// Capture the poll boundary before fetching so that issues updated
		// during processing are not lost on the next cycle.
		pollBoundary := time.Now()
		fmt.Fprintf(out, "Phase 1: Polling GitHub for issues since %s\n", lastPoll.Format(time.RFC3339))
		issues, err := deps.ListNewIssues(ctx, lastPoll)
		if err != nil {
			log.Printf("bull: poll error: %v", err)
			goto endCycle
		}
		lastPoll = pollBoundary

		// Phase 2-4: Filter and triage new issues.
		{
			seen := make(map[int]bool)
			for _, issue := range issues {
				num := issue.GetNumber()

				// Deduplicate within this batch.
				if seen[num] {
					continue
				}
				seen[num] = true

				// Phase 2: Skip already-tracked issues.
				if trackedSet[num] {
					fmt.Fprintf(out, "Phase 2: Issue #%d already tracked, skipping\n", num)
					continue
				}

				// Phase 3: Heuristic filter.
				filterResult := FilterIssue(issue, opts.Config.Labels.Ignore, tracked)
				if !filterResult.Pass {
					fmt.Fprintf(out, "Phase 3: Issue #%d filtered: %s\n", num, filterResult.Reason)
					continue
				}

				// Phase 4: AI triage.
				if opts.AI != nil {
					fmt.Fprintf(out, "Phase 4: Triaging issue #%d\n", num)
					triageOpts := TriageOpts{
						Client:       deps,
						AI:           opts.AI,
						Store:        deps,
						Config:       opts.Config,
						Tracks:       opts.Tracks,
						IgnoreLabel:  opts.Config.Labels.Ignore,
						Tracked:      tracked,
						CodeContext:  opts.CodeContext,
						BranchPrefix: opts.BranchPrefix,
					}
					outcome, triageErr := ExecuteTriage(ctx, issue, triageOpts)
					if triageErr != nil {
						log.Printf("bull: triage issue #%d error: %v", num, triageErr)
						continue
					}
					fmt.Fprintf(out, "Phase 4: Issue #%d → %s\n", num, outcome.Action)
					if outcome.RawResponse != "" {
						log.Printf("bull: triage issue #%d raw response: %s", num, outcome.RawResponse)
					}
				} else {
					fmt.Fprintf(out, "Phase 4: No AI configured, skipping triage for #%d\n", num)
				}
			}
		}

		// Phase 5: Reverse sync car statuses.
		fmt.Fprintf(out, "Phase 5: Syncing car statuses\n")
		if err := SyncCarStatuses(ctx, deps, deps, opts.Config); err != nil {
			log.Printf("bull: sync car statuses error: %v", err)
		}

		// Phase 6: Release scan.
		fmt.Fprintf(out, "Phase 6: Scanning for new releases\n")
		if err := SyncReleases(ctx, deps, deps, opts.Config); err != nil {
			log.Printf("bull: release scan error: %v", err)
		}

	endCycle:
		cycle++
		if opts.OnCycleEnd != nil {
			opts.OnCycleEnd(cycle)
		}

		sleepCtx(ctx, opts.PollInterval)
	}
}

// sleepCtx sleeps for d, returning early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
