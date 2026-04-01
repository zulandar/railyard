package inspect

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

const defaultInspectPollInterval = 60 * time.Second

// DaemonClient combines all GitHub operations the inspect daemon needs.
type DaemonClient interface {
	ListReviewablePRs(ctx context.Context) ([]*github.PullRequest, error)
	GetPRDiff(ctx context.Context, number int) (string, error)
	ListPRFiles(ctx context.Context, number int) ([]*github.CommitFile, error)
	GetFileContent(ctx context.Context, path, ref string) (string, error)
	GetPRState(ctx context.Context, number int) (state string, merged bool, err error)
	SubmitReview(ctx context.Context, number int, summary string, comments []InlineComment) error
	DismissReviewsByBot(ctx context.Context, number int, botLogin string) error
	AddLabel(ctx context.Context, number int, label string) error
	RemoveLabel(ctx context.Context, number int, label string) error
	PRHasLabel(ctx context.Context, number int, label string) (bool, error)
	CountNonBotComments(ctx context.Context, number int, botLogin string) (int, error)
	EnsureLabels(ctx context.Context, labels config.InspectLabelsConfig) error
}

// DaemonStore combines all database operations the inspect daemon needs.
type DaemonStore interface {
	ListPROpenCars(ctx context.Context) ([]models.Car, error)
	ClaimForReview(ctx context.Context, carID, replicaID string) (bool, error)
	ReleaseReview(ctx context.Context, carID string, updates map[string]interface{}) error
	UpdateCarStatus(ctx context.Context, carID, status string) error
}

// DaemonOpts bundles all configuration for RunDaemon.
type DaemonOpts struct {
	Config       config.InspectConfig
	Tracks       []config.TrackConfig
	ReplicaID    string
	PollInterval time.Duration
	AI           ReviewAI
	BotLogin     string
	Logger       *slog.Logger
	OnCycleEnd   func(cycle int) // test hook
}

// RunDaemon runs the inspect daemon loop: poll GitHub for reviewable PRs,
// match them against pr_open cars, claim, review, and release.
func RunDaemon(ctx context.Context, client DaemonClient, store DaemonStore, opts DaemonOpts) error {
	// Set defaults.
	if opts.PollInterval <= 0 {
		if opts.Config.PollIntervalSec > 0 {
			opts.PollInterval = time.Duration(opts.Config.PollIntervalSec) * time.Second
		} else {
			opts.PollInterval = defaultInspectPollInterval
		}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("inspect daemon starting",
		"replica", opts.ReplicaID,
		"poll_interval", opts.PollInterval.String(),
		"deep_review", opts.Config.DeepReview,
	)

	// Start health server in a background goroutine.
	hs := NewHealthServer(opts.PollInterval)
	go func() {
		if err := StartHealthServer(ctx, opts.Config.HealthPort, hs); err != nil {
			logger.Error("inspect health server failed", "error", err)
		}
	}()

	// Ensure required labels exist (non-fatal on error).
	if err := client.EnsureLabels(ctx, opts.Config.Labels); err != nil {
		logger.Warn("inspect: failed to ensure labels", "error", err)
	}

	cycle := 0
	for {
		cycle++
		reviewCycle(ctx, client, store, opts, logger)
		hs.RecordPoll()

		if opts.OnCycleEnd != nil {
			opts.OnCycleEnd(cycle)
		}

		select {
		case <-ctx.Done():
			logger.Info("inspect daemon shutting down")
			return ctx.Err()
		case <-time.After(opts.PollInterval):
		}
	}
}

// reviewCycle runs one poll cycle: list PRs, match against cars, review each.
func reviewCycle(ctx context.Context, client DaemonClient, store DaemonStore, opts DaemonOpts, logger *slog.Logger) {
	prs, err := client.ListReviewablePRs(ctx)
	if err != nil {
		logger.Error("inspect: list PRs failed", "error", err)
		return
	}

	cars, err := store.ListPROpenCars(ctx)
	if err != nil {
		logger.Error("inspect: list pr_open cars failed", "error", err)
		return
	}

	// Build branch -> car map.
	branchToCar := make(map[string]models.Car, len(cars))
	for _, c := range cars {
		if c.Branch != "" {
			branchToCar[c.Branch] = c
		}
	}

	// Build branch -> PR map.
	branchToPR := make(map[string]*github.PullRequest, len(prs))
	for _, pr := range prs {
		branch := pr.GetHead().GetRef()
		if branch != "" {
			branchToPR[branch] = pr
		}
	}

	// For each matched car+PR, attempt a review.
	for branch, car := range branchToCar {
		pr, ok := branchToPR[branch]
		if !ok {
			continue
		}

		prNum := pr.GetNumber()
		labels := opts.Config.Labels

		// Skip if already reviewed without a re-review request.
		hasReviewed, err := client.PRHasLabel(ctx, prNum, labels.Reviewed)
		if err != nil {
			logger.Error("inspect: check reviewed label", "pr", prNum, "error", err)
			continue
		}
		hasReReview, err := client.PRHasLabel(ctx, prNum, labels.ReReview)
		if err != nil {
			logger.Error("inspect: check re-review label", "pr", prNum, "error", err)
			continue
		}
		if hasReviewed && !hasReReview {
			continue
		}

		// Skip if already in-progress (another replica is reviewing).
		hasInProgress, err := client.PRHasLabel(ctx, prNum, labels.InProgress)
		if err != nil {
			logger.Error("inspect: check in-progress label", "pr", prNum, "error", err)
			continue
		}
		if hasInProgress {
			continue
		}

		// Attempt to claim the car for review.
		claimed, err := store.ClaimForReview(ctx, car.ID, opts.ReplicaID)
		if err != nil {
			logger.Error("inspect: claim failed", "car", car.ID, "error", err)
			continue
		}
		if !claimed {
			continue
		}

		reviewOnePR(ctx, client, store, opts, logger, car, pr, hasReReview)
	}
}

// reviewOnePR performs the full review lifecycle for a single car/PR pair.
func reviewOnePR(
	ctx context.Context,
	client DaemonClient,
	store DaemonStore,
	opts DaemonOpts,
	logger *slog.Logger,
	car models.Car,
	pr *github.PullRequest,
	isReReview bool,
) {
	prNum := pr.GetNumber()
	labels := opts.Config.Labels
	start := time.Now()

	// Add in-progress label.
	if err := client.AddLabel(ctx, prNum, labels.InProgress); err != nil {
		logger.Error("inspect: add in-progress label", "pr", prNum, "error", err)
		releaseWithCleanup(ctx, client, store, logger, car.ID, prNum, labels)
		return
	}

	// If re-review: remove stale labels and dismiss old bot reviews.
	if isReReview {
		_ = client.RemoveLabel(ctx, prNum, labels.ReReview)
		_ = client.RemoveLabel(ctx, prNum, labels.Reviewed)
		if opts.BotLogin != "" {
			if err := client.DismissReviewsByBot(ctx, prNum, opts.BotLogin); err != nil {
				logger.Warn("inspect: dismiss old reviews", "pr", prNum, "error", err)
			}
		}
	}

	// Fetch diff.
	diff, err := client.GetPRDiff(ctx, prNum)
	if err != nil {
		logger.Error("inspect: get diff", "pr", prNum, "error", err)
		releaseWithCleanup(ctx, client, store, logger, car.ID, prNum, labels)
		return
	}

	// List PR files and build DiffFiles.
	prFiles, err := client.ListPRFiles(ctx, prNum)
	if err != nil {
		logger.Error("inspect: list files", "pr", prNum, "error", err)
		releaseWithCleanup(ctx, client, store, logger, car.ID, prNum, labels)
		return
	}

	var diffFiles []DiffFile
	for _, f := range prFiles {
		diffFiles = append(diffFiles, DiffFile{
			Path:  f.GetFilename(),
			Diff:  f.GetPatch(),
			Lines: f.GetChanges(),
		})
	}

	// Truncate diff to configured max lines.
	maxLines := opts.Config.MaxDiffLines
	if maxLines <= 0 {
		maxLines = 10000
	}
	truncResult := TruncateDiff(diffFiles, maxLines)

	// Fetch file contents for included files using the PR head SHA.
	headSHA := pr.GetHead().GetSHA()
	var fileContexts []FileContext
	for _, df := range truncResult.Files {
		content, err := client.GetFileContent(ctx, df.Path, headSHA)
		if err != nil {
			// Non-fatal: the file may have been deleted.
			logger.Warn("inspect: get file content", "path", df.Path, "error", err)
			continue
		}
		fileContexts = append(fileContexts, FileContext{
			Path:    df.Path,
			Content: content,
		})
	}

	// Find matching track for conventions.
	var trackName, trackLanguage string
	var conventions map[string]interface{}
	for _, t := range opts.Tracks {
		if t.Name == car.Track {
			trackName = t.Name
			trackLanguage = t.Language
			conventions = t.Conventions
			break
		}
	}

	// Build review prompt using the full unified diff.
	reviewCtx := ReviewContext{
		PRNumber:      prNum,
		PRTitle:       pr.GetTitle(),
		Diff:          diff,
		Files:         fileContexts,
		TrackName:     trackName,
		TrackLanguage: trackLanguage,
		Conventions:   conventions,
		Truncated:     truncResult.Truncated,
		TotalFiles:    truncResult.TotalFiles,
		IncludedFiles: truncResult.IncludedFiles,
	}
	prompt := BuildReviewPrompt(reviewCtx)

	// Run AI prompt.
	raw, err := opts.AI.RunPrompt(ctx, prompt)
	if err != nil {
		logger.Error("inspect: AI prompt failed", "pr", prNum, "error", err)
		releaseWithCleanup(ctx, client, store, logger, car.ID, prNum, labels)
		return
	}

	// Parse result; retry once on parse failure, then fallback.
	result, err := ParseReviewResult(raw)
	if err != nil {
		logger.Warn("inspect: parse failed, retrying", "pr", prNum, "error", err)
		raw, err = opts.AI.RunPrompt(ctx, prompt)
		if err != nil {
			logger.Error("inspect: AI retry failed", "pr", prNum, "error", err)
			submitFallbackReview(ctx, client, logger, prNum, raw)
			releaseWithCleanup(ctx, client, store, logger, car.ID, prNum, labels)
			return
		}
		result, err = ParseReviewResult(raw)
		if err != nil {
			logger.Error("inspect: parse retry failed", "pr", prNum, "error", err)
			submitFallbackReview(ctx, client, logger, prNum, raw)
			releaseWithCleanup(ctx, client, store, logger, car.ID, prNum, labels)
			return
		}
	}

	// Pre-submit: check PR state. If merged or closed, update car and return.
	state, merged, err := client.GetPRState(ctx, prNum)
	if err != nil {
		logger.Error("inspect: get PR state", "pr", prNum, "error", err)
		releaseWithCleanup(ctx, client, store, logger, car.ID, prNum, labels)
		return
	}
	if merged {
		_ = store.UpdateCarStatus(ctx, car.ID, "merged")
		_ = client.RemoveLabel(ctx, prNum, labels.InProgress)
		return
	}
	if state == "closed" {
		_ = store.UpdateCarStatus(ctx, car.ID, "cancelled")
		_ = client.RemoveLabel(ctx, prNum, labels.InProgress)
		return
	}

	// Convert ReviewComment[] to InlineComment[] for the GitHub client.
	inlineComments := make([]InlineComment, 0, len(result.Comments))
	for _, c := range result.Comments {
		inlineComments = append(inlineComments, InlineComment{
			Path: c.Path,
			Line: c.Line,
			Side: c.Side,
			Body: c.Body,
		})
	}

	// Submit the review.
	if err := client.SubmitReview(ctx, prNum, result.Summary, inlineComments); err != nil {
		logger.Error("inspect: submit review", "pr", prNum, "error", err)
		releaseWithCleanup(ctx, client, store, logger, car.ID, prNum, labels)
		return
	}

	latency := time.Since(start)
	logger.Info("inspect: review submitted",
		"car", car.ID,
		"pr", prNum,
		"branch", car.Branch,
		"comments", len(result.Comments),
		"severity", result.Severity,
		"latency_ms", latency.Milliseconds(),
		"replica", opts.ReplicaID,
	)

	// Count non-bot comments for tracking.
	commentCount := 0
	if opts.BotLogin != "" {
		commentCount, err = client.CountNonBotComments(ctx, prNum, opts.BotLogin)
		if err != nil {
			logger.Warn("inspect: count comments", "pr", prNum, "error", err)
		}
	}

	// Release the claim with updated comment count.
	if err := store.ReleaseReview(ctx, car.ID, map[string]interface{}{
		"status":                "pr_open",
		"last_pr_comment_count": commentCount,
	}); err != nil {
		logger.Error("inspect: release review", "car", car.ID, "error", err)
	}

	// Add reviewed label, remove in-progress label.
	_ = client.AddLabel(ctx, prNum, labels.Reviewed)
	_ = client.RemoveLabel(ctx, prNum, labels.InProgress)
}

// releaseWithCleanup releases the car claim and removes the in-progress label.
func releaseWithCleanup(
	ctx context.Context,
	client DaemonClient,
	store DaemonStore,
	logger *slog.Logger,
	carID string,
	prNum int,
	labels config.InspectLabelsConfig,
) {
	if err := store.ReleaseReview(ctx, carID, nil); err != nil {
		logger.Error("inspect: release cleanup", "car", carID, "error", err)
	}
	if err := client.RemoveLabel(ctx, prNum, labels.InProgress); err != nil {
		logger.Error("inspect: remove in-progress cleanup", "pr", prNum, "error", err)
	}
}

// submitFallbackReview posts a summary-only review when AI output can't be parsed.
func submitFallbackReview(
	ctx context.Context,
	client DaemonClient,
	logger *slog.Logger,
	prNum int,
	rawOutput string,
) {
	summary := fmt.Sprintf(
		"Automated review encountered an issue parsing AI output. Raw response (truncated):\n\n```\n%.500s\n```",
		rawOutput,
	)
	if err := client.SubmitReview(ctx, prNum, summary, nil); err != nil {
		logger.Error("inspect: fallback review failed", "pr", prNum, "error", err)
	}
}
