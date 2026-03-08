package bull

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// SyncClient abstracts GitHub operations needed for reverse sync.
type SyncClient interface {
	AddLabel(ctx context.Context, number int, label string) error
	RemoveLabel(ctx context.Context, number int, label string) error
	AddComment(ctx context.Context, number int, body string) error
}

// SyncStore abstracts database operations for reverse sync.
type SyncStore interface {
	GetTrackedIssues(ctx context.Context) ([]models.BullIssue, error)
	GetCarStatus(ctx context.Context, carID string) (string, error)
	UpdateIssueStatus(ctx context.Context, issueID uint, newStatus string) error
}

// SyncCarStatuses queries all tracked bull issues and updates GitHub labels
// and comments when the linked car's status has changed since the last sync.
func SyncCarStatuses(ctx context.Context, client SyncClient, store SyncStore, cfg config.BullConfig) error {
	issues, err := store.GetTrackedIssues(ctx)
	if err != nil {
		return fmt.Errorf("sync: get tracked issues: %w", err)
	}

	for _, issue := range issues {
		if issue.CarID == "" {
			continue
		}

		carStatus, err := store.GetCarStatus(ctx, issue.CarID)
		if err != nil {
			return fmt.Errorf("sync: get car status for %q: %w", issue.CarID, err)
		}

		if carStatus == issue.LastKnownStatus {
			continue
		}

		if err := applyTransition(ctx, client, cfg, issue.IssueNumber, carStatus); err != nil {
			return fmt.Errorf("sync: apply transition for issue #%d: %w", issue.IssueNumber, err)
		}

		if err := store.UpdateIssueStatus(ctx, issue.ID, carStatus); err != nil {
			return fmt.Errorf("sync: update issue status for #%d: %w", issue.IssueNumber, err)
		}
	}

	return nil
}

// ReleaseClient abstracts GitHub operations needed for release sync.
type ReleaseClient interface {
	ListReleases(ctx context.Context, since time.Time) ([]*github.RepositoryRelease, error)
	CloseIssue(ctx context.Context, number int, comment string) error
	RemoveLabel(ctx context.Context, number int, label string) error
}

// ReleaseStore abstracts database operations for release sync.
type ReleaseStore interface {
	GetMergedIssues(ctx context.Context) ([]models.BullIssue, error)
	GetLastReleaseCheck(ctx context.Context) (time.Time, error)
	SetLastReleaseCheck(ctx context.Context, t time.Time) error
	UpdateIssueStatus(ctx context.Context, issueID uint, newStatus string) error
}

// SyncReleases polls for new GitHub releases and closes issues that have the
// fix-merged label. It persists the last-checked timestamp to avoid reprocessing.
func SyncReleases(ctx context.Context, client ReleaseClient, store ReleaseStore, cfg config.BullConfig) error {
	since, err := store.GetLastReleaseCheck(ctx)
	if err != nil {
		return fmt.Errorf("sync: get last release check: %w", err)
	}

	releases, err := client.ListReleases(ctx, since)
	if err != nil {
		return fmt.Errorf("sync: list releases: %w", err)
	}

	if len(releases) == 0 {
		return nil
	}

	// Find the latest release time for the checkpoint.
	var latestTime time.Time
	var latestTag, latestURL string
	for _, r := range releases {
		if r.CreatedAt != nil && r.CreatedAt.After(latestTime) {
			latestTime = r.CreatedAt.Time
			latestTag = r.GetTagName()
			latestURL = r.GetHTMLURL()
		}
	}

	issues, err := store.GetMergedIssues(ctx)
	if err != nil {
		return fmt.Errorf("sync: get merged issues: %w", err)
	}

	for _, issue := range issues {
		comment := fmt.Sprintf("This issue has been resolved in release [%s](%s).", latestTag, latestURL)
		if err := client.CloseIssue(ctx, issue.IssueNumber, comment); err != nil {
			return fmt.Errorf("sync: close issue #%d: %w", issue.IssueNumber, err)
		}

		if err := client.RemoveLabel(ctx, issue.IssueNumber, cfg.Labels.FixMerged); err != nil {
			return fmt.Errorf("sync: remove fix-merged label from #%d: %w", issue.IssueNumber, err)
		}

		if err := store.UpdateIssueStatus(ctx, issue.ID, "released"); err != nil {
			return fmt.Errorf("sync: update issue status for #%d: %w", issue.IssueNumber, err)
		}
	}

	if err := store.SetLastReleaseCheck(ctx, latestTime); err != nil {
		return fmt.Errorf("sync: set last release check: %w", err)
	}

	return nil
}

// applyTransition performs the GitHub label and comment changes for a car
// status transition.
func applyTransition(ctx context.Context, client SyncClient, cfg config.BullConfig, issueNumber int, carStatus string) error {
	labels := cfg.Labels

	switch carStatus {
	case "open", "ready", "claimed":
		if err := client.AddLabel(ctx, issueNumber, labels.InProgress); err != nil {
			return err
		}
		if err := client.RemoveLabel(ctx, issueNumber, labels.UnderReview); err != nil {
			return err
		}

	case "merged":
		if err := client.AddLabel(ctx, issueNumber, labels.FixMerged); err != nil {
			return err
		}
		if err := client.RemoveLabel(ctx, issueNumber, labels.InProgress); err != nil {
			return err
		}
		if cfg.Comments.Enabled {
			now := time.Now().UTC().Format(time.RFC3339)
			body := fmt.Sprintf("The fix for this issue has been merged. (synced at %s)", now)
			if err := client.AddComment(ctx, issueNumber, body); err != nil {
				return err
			}
		}

	case "cancelled":
		// Remove all bull labels.
		for _, label := range []string{labels.UnderReview, labels.InProgress, labels.FixMerged, labels.Ignore} {
			if err := client.RemoveLabel(ctx, issueNumber, label); err != nil {
				return err
			}
		}
		if cfg.Comments.Enabled {
			now := time.Now().UTC().Format(time.RFC3339)
			body := fmt.Sprintf("The linked car has been cancelled. (synced at %s)", now)
			if err := client.AddComment(ctx, issueNumber, body); err != nil {
				return err
			}
		}

	case "done", "in_progress":
		if err := client.AddLabel(ctx, issueNumber, labels.InProgress); err != nil {
			return err
		}
	}

	return nil
}
