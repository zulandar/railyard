package yardmaster

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// EscalationTracker tracks the last escalation time per car to implement cooldowns.
type EscalationTracker struct {
	mu       sync.Mutex
	lastEsc  map[string]time.Time // car_id -> last escalation time
	cooldown time.Duration
}

// NewEscalationTracker creates a tracker with the given cooldown duration.
func NewEscalationTracker(cooldown time.Duration) *EscalationTracker {
	return &EscalationTracker{
		lastEsc:  make(map[string]time.Time),
		cooldown: cooldown,
	}
}

// ShouldEscalate returns true if the car has not been escalated within the cooldown period.
// If it returns true, it also records the current time as the last escalation time.
func (et *EscalationTracker) ShouldEscalate(carID string) bool {
	et.mu.Lock()
	defer et.mu.Unlock()

	if last, ok := et.lastEsc[carID]; ok {
		if time.Since(last) < et.cooldown {
			return false
		}
	}
	et.lastEsc[carID] = time.Now()
	return true
}

// Reset removes tracking for a car (e.g., when the car is closed).
func (et *EscalationTracker) Reset(carID string) {
	et.mu.Lock()
	defer et.mu.Unlock()
	delete(et.lastEsc, carID)
}

// EscalateAction represents a decision from Claude escalation.
type EscalateAction string

const (
	EscalateReassign EscalateAction = "REASSIGN"
	EscalateGuidance EscalateAction = "GUIDANCE"
	EscalateHuman    EscalateAction = "ESCALATE_HUMAN"
	EscalateRetry    EscalateAction = "RETRY"
	EscalateSkip     EscalateAction = "SKIP"
)

// EscalateOpts holds parameters for an agent escalation call.
type EscalateOpts struct {
	CarID        string
	EngineID     string
	Reason       string
	Details      string
	DB           *gorm.DB
	ProviderName string // agent provider name; defaults to "claude"
}

// EscalateResult contains the agent's decision after escalation.
type EscalateResult struct {
	Action  EscalateAction
	Message string
}

// EscalateToAgent spawns a short-lived agent session with a focused prompt
// and parses the structured decision response. Uses the provider configured
// in opts.ProviderName (defaults to "claude").
func EscalateToAgent(ctx context.Context, opts EscalateOpts) (*EscalateResult, error) {
	providerName := opts.ProviderName
	if providerName == "" {
		providerName = "claude"
	}
	provider, err := engine.GetProvider(providerName)
	if err != nil {
		return nil, fmt.Errorf("yardmaster: escalate: resolve provider: %w", err)
	}

	prompt := buildEscalationPrompt(opts)
	cmd, cancel := provider.BuildPromptCommand(ctx, prompt)
	defer cancel()

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yardmaster: escalate: %w", err)
	}

	return parseEscalateResponse(string(output)), nil
}

// EscalateToClaude is a backward-compatible alias for EscalateToAgent.
// Deprecated: use EscalateToAgent with opts.ProviderName instead.
func EscalateToClaude(ctx context.Context, opts EscalateOpts) (*EscalateResult, error) {
	return EscalateToAgent(ctx, opts)
}

// buildEscalationPrompt constructs a focused prompt with car details, progress,
// and available actions for Claude to choose from.
func buildEscalationPrompt(opts EscalateOpts) string {
	var b strings.Builder

	b.WriteString("You are the Yardmaster supervisor. An issue requires your judgment.\n\n")

	if opts.CarID != "" && opts.DB != nil {
		var c models.Car
		if err := opts.DB.First(&c, "id = ?", opts.CarID).Error; err == nil {
			b.WriteString("## Car Details\n")
			fmt.Fprintf(&b, "- ID: %s\n", c.ID)
			fmt.Fprintf(&b, "- Title: %s\n", c.Title)
			fmt.Fprintf(&b, "- Track: %s\n", c.Track)
			fmt.Fprintf(&b, "- Status: %s\n", c.Status)
			fmt.Fprintf(&b, "- Branch: %s\n\n", c.Branch)
		}

		var progress []models.CarProgress
		opts.DB.Where("car_id = ?", opts.CarID).Order("created_at DESC").Limit(5).Find(&progress)
		if len(progress) > 0 {
			b.WriteString("## Recent Progress\n")
			for _, p := range progress {
				fmt.Fprintf(&b, "- [%s] %s\n", p.CreatedAt.Format("15:04"), p.Note)
			}
			b.WriteString("\n")
		}
	}

	if opts.EngineID != "" {
		fmt.Fprintf(&b, "## Engine: %s\n\n", opts.EngineID)
	}

	b.WriteString("## Issue\n")
	fmt.Fprintf(&b, "Reason: %s\n", opts.Reason)
	if opts.Details != "" {
		fmt.Fprintf(&b, "Details: %s\n", opts.Details)
	}

	b.WriteString("\n## Available Actions\n")
	b.WriteString("Respond with exactly ONE of these on a single line:\n")
	b.WriteString("- REASSIGN — release the car for another engine\n")
	b.WriteString("- GUIDANCE:<message> — send guidance to the engine\n")
	b.WriteString("- ESCALATE_HUMAN:<reason> — alert the human operator\n")
	b.WriteString("- RETRY — do nothing, let the engine retry\n")
	b.WriteString("- SKIP — skip this issue\n")

	return b.String()
}

// parseEscalateResponse extracts the action from Claude's output.
// It scans each line for a recognized action keyword.
func parseEscalateResponse(output string) *EscalateResult {
	lines := strings.Split(strings.TrimSpace(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		switch {
		case line == "REASSIGN":
			return &EscalateResult{Action: EscalateReassign}
		case strings.HasPrefix(line, "GUIDANCE:"):
			return &EscalateResult{
				Action:  EscalateGuidance,
				Message: strings.TrimSpace(strings.TrimPrefix(line, "GUIDANCE:")),
			}
		case strings.HasPrefix(line, "ESCALATE_HUMAN:"):
			return &EscalateResult{
				Action:  EscalateHuman,
				Message: strings.TrimSpace(strings.TrimPrefix(line, "ESCALATE_HUMAN:")),
			}
		case line == "RETRY":
			return &EscalateResult{Action: EscalateRetry}
		case line == "SKIP":
			return &EscalateResult{Action: EscalateSkip}
		}
	}

	return &EscalateResult{Action: EscalateSkip, Message: "unrecognized response"}
}
