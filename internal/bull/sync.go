package bull

import (
	"context"
	"fmt"
	"time"

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
