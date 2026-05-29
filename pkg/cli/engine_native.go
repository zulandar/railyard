package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// nativeEngineKickoff is the user message that starts an engine loop turn. It
// mirrors the claude CLI engine path (buildCommand's -p prompt): all the real
// instructions live in the system prompt (the rendered car context).
const nativeEngineKickoff = "Begin working on your assigned car. Follow the instructions in the system prompt."

// nativeEngineMaxIterations bounds an engine loop run. Coding a car involves
// many read/edit/test/commit tool calls, so this is well above the loop default;
// the rate-limit retry wrapper and overall context still cap runaway cost.
const nativeEngineMaxIterations = 80

// nativeSpawnRunner returns a spawnRunner that drives the Railyard-owned agent
// loop (engine tool profile: bash + read_file + write_file + edit_file) instead
// of a CLI subprocess. It plugs into the same spawnAndMonitorWithRetryRunner, so
// the rate-limit pause-and-retry behavior is reused unchanged. Token usage comes
// straight from the API usage block (no text-scraping), and the transcript is
// persisted to agent_logs (redacted) for `ry logs` and outcome stats.
//
// authMethod identifies the active native-loop backend (openrouter /
// openai_compat). It is carried through to RateLimitSignal.Source so downstream
// pause/retry and metrics aren't mis-attributed when the runner serves a
// non-openrouter native backend.
func nativeSpawnRunner(db *gorm.DB, client agentloop.Completer, authMethod string, maxIterations int, cycleLog *slog.Logger) spawnRunner {
	if cycleLog == nil {
		cycleLog = slog.Default()
	}
	if maxIterations <= 0 {
		maxIterations = nativeEngineMaxIterations
	}
	// loop and events persist across the rate-limit retries for THIS car so a
	// retry resumes the prior conversation instead of restarting from the
	// kickoff message (railyard-qf1.4). The engine loop builds a fresh runner —
	// and thus a fresh loop — per car claim, so cars never share state. The
	// events channel is fully drained by runNativeEngineLoop before each call
	// returns, so it is safe to reuse across attempts.
	var loop *agentloop.Loop
	events := make(chan agentloop.Event, 64)
	return func(ctx context.Context, opts engine.SpawnOpts) (*engine.Session, sessionOutcome, error) {
		sessionID, err := engine.GenerateSessionID()
		if err != nil {
			return nil, sessionOutcome{}, err
		}

		// First attempt: build the loop and kick it off. Subsequent attempts
		// (rate-limit retries) reuse the same loop — which still holds the
		// accumulated conversation — and pass an empty input so the kickoff is
		// not re-injected. A RateLimitError returns before the failing
		// assistant turn is appended, so the conversation resumes cleanly.
		userInput := nativeEngineKickoff
		if loop == nil {
			loop = agentloop.NewLoop(client, agentloop.LoopConfig{
				Model:         opts.Model,
				SystemPrompt:  opts.ContextPayload,
				Tools:         agentloop.EngineTools(opts.WorkDir),
				MaxIterations: maxIterations,
				Events:        events,
			})
		} else {
			userInput = ""
		}

		// Record the session id on the engine row for `ry logs` parity.
		db.Model(&models.Engine{}).Where("id = ?", opts.EngineID).Update("session_id", sessionID)
		cycleLog.Info("Native loop session", "session", sessionID, "car", opts.CarID, "model", opts.Model)

		result, transcript, runErr := runNativeEngineLoop(ctx, loop, events, userInput)
		persistNativeAgentLog(db, opts, sessionID, transcript, result, runErr)

		outcome := mapEngineOutcome(runErr, result, carIsDone(db, opts.CarID), authMethod)
		// The native runner does its own cleanup (Run already returned), so unlike
		// the CLI runner there is no subprocess to terminate on rate-limit.
		return &engine.Session{ID: sessionID, EngineID: opts.EngineID, CarID: opts.CarID}, outcome, nil
	}
}

// mapEngineOutcome maps a loop run result to the engine's outcome model:
//   - rate-limit error      -> outcomeRateLimited (the retry wrapper pauses & respawns)
//   - context cancelled     -> outcomeCancelled (daemon shutting down)
//   - credit/4xx API error  -> outcomeStall (permanent; block & surface, don't churn)
//   - other error           -> outcomeClear (transient attempt; reclaim next cycle)
//   - car marked done       -> outcomeCompleted
//   - hit iteration cap     -> outcomeStall (ran out of steps; escalate, like a CLI stall)
//   - finished, not done    -> outcomeClear
//
// source labels the rate-limit signal so the pause/retry wrapper and metrics
// can tell native backends apart (openrouter vs openai_compat).
func mapEngineOutcome(runErr error, result agentloop.Result, carDone bool, source string) sessionOutcome {
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return sessionOutcome{kind: outcomeCancelled}
		}
		var rle *agentloop.RateLimitError
		if errors.As(runErr, &rle) {
			return sessionOutcome{
				kind:            outcomeRateLimited,
				rateLimitSignal: engine.RateLimitSignal{Source: source, RetryAfter: rle.RetryAfter},
			}
		}
		// Permanent backend failures won't resolve on retry, so treat them like a
		// stall: the car is blocked and surfaced rather than silently reclaimed and
		// re-run every poll forever. Out-of-credits (402) and 4xx client errors
		// (e.g. an invalid model name or bad auth) are permanent; transient
		// failures (exhausted 5xx, transport, decode) fall through to outcomeClear.
		var ce *agentloop.CreditError
		if errors.As(runErr, &ce) {
			return sessionOutcome{
				kind:        outcomeStall,
				stallReason: engine.StallReason{Type: "credit_exhausted", Detail: ce.Error()},
			}
		}
		var ae *agentloop.APIError
		if errors.As(runErr, &ae) && ae.StatusCode >= 400 && ae.StatusCode < 500 {
			return sessionOutcome{
				kind:        outcomeStall,
				stallReason: engine.StallReason{Type: "api_error", Detail: ae.Error()},
			}
		}
		return sessionOutcome{kind: outcomeClear}
	}
	if carDone {
		return sessionOutcome{kind: outcomeCompleted}
	}
	if result.StopReason == agentloop.StopMaxIterations {
		return sessionOutcome{
			kind: outcomeStall,
			stallReason: engine.StallReason{
				Type:   "max_iterations",
				Detail: fmt.Sprintf("native loop hit the %d-iteration cap without marking the car done", result.Iterations),
			},
		}
	}
	return sessionOutcome{kind: outcomeClear}
}

// runNativeEngineLoop runs one loop turn, draining its events into a transcript
// so the full agent activity can be persisted. The loop runs in a goroutine so
// events stream concurrently; after it returns, any buffered events are drained.
//
// userInput is the message that starts the turn: the kickoff on the first
// attempt, or "" on a rate-limit retry so the loop resumes its existing
// conversation rather than re-sending the kickoff.
func runNativeEngineLoop(ctx context.Context, loop *agentloop.Loop, events <-chan agentloop.Event, userInput string) (agentloop.Result, string, error) {
	var transcript strings.Builder
	type runResult struct {
		res agentloop.Result
		err error
	}
	resCh := make(chan runResult, 1)
	go func() {
		r, e := loop.Run(ctx, userInput)
		resCh <- runResult{res: r, err: e}
	}()

	for {
		select {
		case ev := <-events:
			writeTranscriptEvent(&transcript, ev)
		case rr := <-resCh:
			for {
				select {
				case ev := <-events:
					writeTranscriptEvent(&transcript, ev)
				default:
					return rr.res, transcript.String(), rr.err
				}
			}
		}
	}
}

// writeTranscriptEvent appends a human-readable line for a loop event.
func writeTranscriptEvent(b *strings.Builder, ev agentloop.Event) {
	switch ev.Type {
	case agentloop.EventAssistantText:
		b.WriteString(ev.Text)
		b.WriteByte('\n')
	case agentloop.EventToolCallStart:
		fmt.Fprintf(b, "🔧 %s %s\n", ev.ToolName, agentloop.Truncate(ev.ToolArgs, 200))
	case agentloop.EventToolCallEnd:
		if ev.ToolError != "" {
			fmt.Fprintf(b, "→ error: %s\n", agentloop.Truncate(ev.ToolError, 200))
		} else {
			fmt.Fprintf(b, "→ %s\n", agentloop.Truncate(ev.ToolResult, 200))
		}
	}
}

// persistNativeAgentLog writes the transcript + usage to agent_logs (redacted),
// keyed by car so queryCarOutcomeStats can sum tokens and `ry logs` can show it.
func persistNativeAgentLog(db *gorm.DB, opts engine.SpawnOpts, sessionID, transcript string, result agentloop.Result, runErr error) {
	content := transcript
	if runErr != nil {
		if content != "" {
			content += "\n"
		}
		content += "[run error] " + runErr.Error()
	}
	if strings.TrimSpace(content) == "" && result.Usage.TotalTokens == 0 {
		return
	}
	db.Create(&models.AgentLog{
		EngineID:     opts.EngineID,
		SessionID:    sessionID,
		CarID:        opts.CarID,
		Direction:    "out",
		Content:      engine.RedactSecrets(content),
		InputTokens:  result.Usage.PromptTokens,
		OutputTokens: result.Usage.CompletionTokens,
		TokenCount:   result.Usage.TotalTokens,
		Model:        opts.Model,
		CreatedAt:    time.Now(),
	})
}

// carIsDone reports whether the car has reached "done" status in the DB.
func carIsDone(db *gorm.DB, carID string) bool {
	var c models.Car
	if err := db.Select("status").First(&c, "id = ?", carID).Error; err != nil {
		return false
	}
	return c.Status == "done"
}
