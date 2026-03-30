package bull

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// TriageClient abstracts GitHub operations for triage.
type TriageClient interface {
	AddComment(ctx context.Context, number int, body string) error
	AddLabel(ctx context.Context, number int, label string) error
	RemoveLabel(ctx context.Context, number int, label string) error
}

// TriageAI abstracts the AI provider for triage.
type TriageAI interface {
	RunPrompt(ctx context.Context, prompt string) (string, error)
}

// TriageStore abstracts database operations for triage.
type TriageStore interface {
	CreateCar(ctx context.Context, opts CarCreateOpts) (string, error)
	RecordTriagedIssue(ctx context.Context, issue models.BullIssue) error
}

// CarCreateOpts holds parameters for creating a car from triage.
type CarCreateOpts struct {
	Title        string
	Description  string
	Type         string
	Priority     int
	Track        string
	Acceptance   string
	DesignNotes  string
	SourceIssue  int
	BranchPrefix string
	RequestedBy  string
}

// TriageOpts bundles all dependencies and configuration for ExecuteTriage.
type TriageOpts struct {
	Client       TriageClient
	AI           TriageAI
	Store        TriageStore
	Config       config.BullConfig
	Tracks       []TrackInfo
	IgnoreLabel  string
	Tracked      []ExistingIssue
	CodeContext   string
	BranchPrefix string
	Comments     []CommentContext
}

// TriageOutcome describes the result of triaging a single issue.
type TriageOutcome struct {
	Action         string // "created_car", "answered", "rejected", "filtered"
	FilterReason   string // only when Action="filtered"
	Classification string // bug, task, question, reject
	CarID          string // only when Action="created_car"
	RawResponse    string // full raw AI response string
}

// ExecuteTriage runs the full AI triage pipeline for a single GitHub issue.
func ExecuteTriage(ctx context.Context, issue *github.Issue, opts TriageOpts) (*TriageOutcome, error) {
	// 1. Heuristic pre-filter.
	filterResult := FilterIssue(issue, opts.IgnoreLabel, opts.Tracked)
	if !filterResult.Pass {
		return &TriageOutcome{
			Action:       "filtered",
			FilterReason: filterResult.Reason,
		}, nil
	}

	// 2. Build triage prompt.
	issueCtx := IssueContext{
		Number: issue.GetNumber(),
		Title:  issue.GetTitle(),
		Body:   issue.GetBody(),
		Author: issue.GetUser().GetLogin(),
	}
	for _, lbl := range issue.Labels {
		issueCtx.Labels = append(issueCtx.Labels, lbl.GetName())
	}
	issueCtx.Comments = opts.Comments
	prompt := BuildTriagePrompt(issueCtx, opts.Config.TriageMode, opts.Tracks, opts.CodeContext)

	// 3. Call AI.
	response, err := opts.AI.RunPrompt(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("bull: AI triage failed: %w", err)
	}

	// 4. Parse result.
	result, err := ParseTriageResult(response)
	if err != nil {
		return nil, fmt.Errorf("bull: parse triage result: %w", err)
	}

	// 5. Act on classification.
	switch result.Classification {
	case "bug", "task":
		return handleCreateCar(ctx, issue, opts, result, response)
	case "question":
		return handleQuestion(ctx, issue, opts, result, response)
	case "reject":
		return handleReject(ctx, issue, opts, result, response)
	default:
		return nil, fmt.Errorf("bull: unknown classification %q", result.Classification)
	}
}

func handleCreateCar(ctx context.Context, issue *github.Issue, opts TriageOpts, result *TriageResult, rawResponse string) (*TriageOutcome, error) {
	number := issue.GetNumber()

	carOpts := CarCreateOpts{
		Title:        result.Title,
		Description:  result.Description,
		Type:         result.Classification,
		Priority:     result.Priority,
		Track:        result.Track,
		Acceptance:   result.Acceptance,
		DesignNotes:  result.DesignNotes,
		SourceIssue:  number,
		BranchPrefix: opts.BranchPrefix,
		RequestedBy:  issue.GetUser().GetLogin(),
	}

	carID, err := opts.Store.CreateCar(ctx, carOpts)
	if err != nil {
		return nil, fmt.Errorf("bull: create car for #%d: %w", number, err)
	}

	bullIssue := models.BullIssue{
		IssueNumber:     number,
		CarID:           carID,
		LastKnownStatus: "draft",
		TriageSummary:   result.Description,
		TriageResponse:  rawResponse,
		TriageMode:      opts.Config.TriageMode,
	}
	if err := opts.Store.RecordTriagedIssue(ctx, bullIssue); err != nil {
		return nil, fmt.Errorf("bull: record triaged issue #%d: %w", number, err)
	}

	if err := opts.Client.AddLabel(ctx, number, opts.Config.Labels.UnderReview); err != nil {
		return nil, fmt.Errorf("bull: apply under_review label to #%d: %w", number, err)
	}

	return &TriageOutcome{
		Action:         "created_car",
		Classification: result.Classification,
		CarID:          carID,
		RawResponse:    rawResponse,
	}, nil
}

func handleQuestion(ctx context.Context, issue *github.Issue, opts TriageOpts, result *TriageResult, rawResponse string) (*TriageOutcome, error) {
	number := issue.GetNumber()

	if opts.Config.Comments.Enabled {
		if err := opts.Client.AddComment(ctx, number, result.Description); err != nil {
			return nil, fmt.Errorf("bull: post answer comment on #%d: %w", number, err)
		}
	}

	if err := opts.Client.AddLabel(ctx, number, opts.Config.Labels.Ignore); err != nil {
		return nil, fmt.Errorf("bull: apply ignore label to #%d: %w", number, err)
	}

	return &TriageOutcome{
		Action:         "answered",
		Classification: "question",
		RawResponse:    rawResponse,
	}, nil
}

func handleReject(ctx context.Context, issue *github.Issue, opts TriageOpts, result *TriageResult, rawResponse string) (*TriageOutcome, error) {
	number := issue.GetNumber()

	if opts.Config.Comments.Enabled {
		comment := result.RejectReason
		if opts.Config.Comments.RejectTemplate != "" {
			rendered, err := renderRejectTemplate(opts.Config.Comments.RejectTemplate, result.RejectReason)
			if err != nil {
				return nil, fmt.Errorf("bull: render reject template for #%d: %w", number, err)
			}
			comment = rendered
		}
		if err := opts.Client.AddComment(ctx, number, comment); err != nil {
			return nil, fmt.Errorf("bull: post reject comment on #%d: %w", number, err)
		}
	}

	if err := opts.Client.AddLabel(ctx, number, opts.Config.Labels.Ignore); err != nil {
		return nil, fmt.Errorf("bull: apply ignore label to #%d: %w", number, err)
	}

	return &TriageOutcome{
		Action:         "rejected",
		Classification: "reject",
		RawResponse:    rawResponse,
	}, nil
}

func renderRejectTemplate(tmplStr, reason string) (string, error) {
	tmpl, err := template.New("reject").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ Reason string }{Reason: reason}); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}
