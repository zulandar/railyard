package bull

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// StartOpts holds parameters for starting the Bull daemon.
type StartOpts struct {
	ConfigPath   string
	Config       *config.Config
	DB           *gorm.DB
	PollInterval time.Duration // default 60s
	Out          io.Writer     // default os.Stdout
}

// Start launches the bull daemon loop. It validates options, constructs
// dependencies, and delegates to RunDaemon.
func Start(ctx context.Context, opts StartOpts) error {
	if opts.Config == nil {
		return fmt.Errorf("bull: config is required")
	}
	if !opts.Config.Bull.Enabled {
		return fmt.Errorf("bull: bull.enabled is not true")
	}
	if opts.Config.Bull.GitHubToken == "" {
		return fmt.Errorf("bull: bull.github_token is required")
	}
	if len(opts.Config.Tracks) == 0 {
		return fmt.Errorf("bull: at least one track must be configured")
	}
	if opts.DB == nil {
		return fmt.Errorf("bull: database connection is required")
	}

	out := opts.Out
	if out == nil {
		out = io.Discard
	}

	client := NewClient(opts.Config.Owner, opts.Config.Repo, opts.Config.Bull.GitHubToken)
	store := NewStore(opts.DB, opts.Config.BranchPrefix)

	var tracks []string
	for _, t := range opts.Config.Tracks {
		tracks = append(tracks, t.Name)
	}

	deps := &daemonDeps{client: client, store: store}

	return RunDaemon(ctx, deps, DaemonOpts{
		Config:       opts.Config.Bull,
		Tracks:       tracks,
		BranchPrefix: opts.Config.BranchPrefix,
		PollInterval: opts.PollInterval,
		Out:          out,
	})
}

// daemonDeps bundles a DaemonClient and DaemonStore into a single value
// that satisfies the interface{ DaemonClient; DaemonStore } constraint.
type daemonDeps struct {
	client DaemonClient
	store  DaemonStore
}

// DaemonClient methods — delegate to client.
func (d *daemonDeps) ListNewIssues(ctx context.Context, since time.Time) ([]*github.Issue, error) {
	return d.client.ListNewIssues(ctx, since)
}
func (d *daemonDeps) AddComment(ctx context.Context, number int, body string) error {
	return d.client.AddComment(ctx, number, body)
}
func (d *daemonDeps) AddLabel(ctx context.Context, number int, label string) error {
	return d.client.AddLabel(ctx, number, label)
}
func (d *daemonDeps) RemoveLabel(ctx context.Context, number int, label string) error {
	return d.client.RemoveLabel(ctx, number, label)
}
func (d *daemonDeps) ListReleases(ctx context.Context, since time.Time) ([]*github.RepositoryRelease, error) {
	return d.client.ListReleases(ctx, since)
}
func (d *daemonDeps) CloseIssue(ctx context.Context, number int, comment string) error {
	return d.client.CloseIssue(ctx, number, comment)
}

// DaemonStore methods — delegate to store.
func (d *daemonDeps) GetTrackedIssues(ctx context.Context) ([]models.BullIssue, error) {
	return d.store.GetTrackedIssues(ctx)
}
func (d *daemonDeps) GetCarStatus(ctx context.Context, carID string) (string, error) {
	return d.store.GetCarStatus(ctx, carID)
}
func (d *daemonDeps) UpdateIssueStatus(ctx context.Context, issueID uint, newStatus string) error {
	return d.store.UpdateIssueStatus(ctx, issueID, newStatus)
}
func (d *daemonDeps) GetMergedIssues(ctx context.Context) ([]models.BullIssue, error) {
	return d.store.GetMergedIssues(ctx)
}
func (d *daemonDeps) GetLastReleaseCheck(ctx context.Context) (time.Time, error) {
	return d.store.GetLastReleaseCheck(ctx)
}
func (d *daemonDeps) SetLastReleaseCheck(ctx context.Context, t time.Time) error {
	return d.store.SetLastReleaseCheck(ctx, t)
}
func (d *daemonDeps) CreateCar(ctx context.Context, opts CarCreateOpts) (string, error) {
	return d.store.CreateCar(ctx, opts)
}
func (d *daemonDeps) RecordTriagedIssue(ctx context.Context, issue models.BullIssue) error {
	return d.store.RecordTriagedIssue(ctx, issue)
}
