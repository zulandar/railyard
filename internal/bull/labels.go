package bull

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
)

// LabelClient abstracts GitHub label operations for testability.
type LabelClient interface {
	CreateLabel(ctx context.Context, name, color, description string) error
	AddLabel(ctx context.Context, number int, label string) error
	RemoveLabel(ctx context.Context, number int, label string) error
	ListLabels(ctx context.Context) ([]string, error)
}

// labelDef pairs a label name with its display color.
type labelDef struct {
	name  string
	color string
}

// EnsureLabels lists existing repo labels and creates any bull labels that
// don't already exist. It is idempotent — safe to call repeatedly.
func EnsureLabels(ctx context.Context, client LabelClient, labels config.BullLabelsConfig) error {
	existing, err := client.ListLabels(ctx)
	if err != nil {
		return fmt.Errorf("bull: list labels: %w", err)
	}

	existingSet := make(map[string]bool, len(existing))
	for _, l := range existing {
		existingSet[l] = true
	}

	defs := []labelDef{
		{name: labels.UnderReview, color: "#0e8a16"},
		{name: labels.InProgress, color: "#1d76db"},
		{name: labels.FixMerged, color: "#5319e7"},
		{name: labels.Ignore, color: "#e4e669"},
	}

	for _, d := range defs {
		if existingSet[d.name] {
			continue
		}
		if err := client.CreateLabel(ctx, d.name, d.color, "Managed by Bull"); err != nil {
			return fmt.Errorf("bull: create label %q: %w", d.name, err)
		}
	}
	return nil
}

// ApplyLabel adds a label to the given issue. It is idempotent — if the label
// already exists on the issue, the GitHub API returns success.
func ApplyLabel(ctx context.Context, client LabelClient, number int, label string) error {
	if err := client.AddLabel(ctx, number, label); err != nil {
		return fmt.Errorf("bull: apply label %q to #%d: %w", label, number, err)
	}
	return nil
}

// RemoveBullLabel removes a label from the given issue. It is idempotent — if
// the label doesn't exist on the issue (404), the error is swallowed.
func RemoveBullLabel(ctx context.Context, client LabelClient, number int, label string) error {
	err := client.RemoveLabel(ctx, number, label)
	if err == nil {
		return nil
	}
	// Swallow 404 errors (label not on issue).
	if ghErr, ok := err.(*github.ErrorResponse); ok && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusNotFound {
		return nil
	}
	// Also handle non-github error types that carry an HTTP status code.
	type statusCoder interface {
		Error() string
	}
	type httpStatuser interface {
		statusCoder
		StatusCode() int
	}
	if he, ok := err.(httpStatuser); ok && he.StatusCode() == http.StatusNotFound {
		return nil
	}
	// Check for simple string-based 404 indicators as a fallback.
	if errMsg := err.Error(); errMsg == "Not Found" || errMsg == "404 Not Found" {
		return nil
	}
	return fmt.Errorf("bull: remove label %q from #%d: %w", label, number, err)
}

// RemoveAllBullLabels removes all bull-managed labels from the given issue.
func RemoveAllBullLabels(ctx context.Context, client LabelClient, number int, labels config.BullLabelsConfig) error {
	for _, label := range []string{labels.UnderReview, labels.InProgress, labels.FixMerged, labels.Ignore} {
		if err := RemoveBullLabel(ctx, client, number, label); err != nil {
			return err
		}
	}
	return nil
}
