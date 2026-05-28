package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/models"
)

// loopFakeCompleter is a fake agentloop.Completer returning a scripted sequence
// of responses (one per Complete call); an err is returned on every call when set.
type loopFakeCompleter struct {
	responses []agentloop.Response
	err       error

	mu    sync.Mutex
	calls int
}

func (c *loopFakeCompleter) Complete(_ context.Context, _ agentloop.Request) (agentloop.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return agentloop.Response{}, c.err
	}
	i := c.calls
	c.calls++
	if i >= len(c.responses) {
		// Keep emitting tool calls so a "never finishes" script can hit the cap.
		return agentloop.Response{}, errors.New("loopFakeCompleter: out of responses")
	}
	return c.responses[i], nil
}

func writeFileCall(id, path, content string) agentloop.Response {
	args, _ := json.Marshal(map[string]string{"path": path, "content": content})
	return agentloop.Response{
		FinishReason: "tool_calls",
		ToolCalls:    []agentloop.ToolCall{{ID: id, Name: "write_file", Arguments: args}},
	}
}

func noopBashCall(id string) agentloop.Response {
	args, _ := json.Marshal(map[string]string{"command": "true"})
	return agentloop.Response{
		FinishReason: "tool_calls",
		ToolCalls:    []agentloop.ToolCall{{ID: id, Name: "bash", Arguments: args}},
	}
}

func stopRespWithUsage(content string, prompt, completion int) agentloop.Response {
	return agentloop.Response{
		Content:      content,
		FinishReason: "stop",
		Usage:        agentloop.Usage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion},
	}
}

// ---------------------------------------------------------------------------
// mapEngineOutcome (pure outcome mapping)
// ---------------------------------------------------------------------------

func TestMapEngineOutcome(t *testing.T) {
	rle := &agentloop.RateLimitError{RetryAfter: 7 * time.Second, Message: "slow"}

	t.Run("rate limit (openrouter)", func(t *testing.T) {
		o := mapEngineOutcome(rle, agentloop.Result{}, false, "openrouter")
		if o.kind != outcomeRateLimited {
			t.Fatalf("kind = %v, want outcomeRateLimited", o.kind)
		}
		if o.rateLimitSignal.RetryAfter != 7*time.Second {
			t.Errorf("RetryAfter = %v, want 7s", o.rateLimitSignal.RetryAfter)
		}
		if o.rateLimitSignal.Source != "openrouter" {
			t.Errorf("Source = %q, want %q", o.rateLimitSignal.Source, "openrouter")
		}
	})
	t.Run("rate limit (openai_compat) carries its own source", func(t *testing.T) {
		// The native runner serves both openrouter and openai_compat; the
		// rate-limit signal must name the active auth method so downstream
		// pause/retry and metrics aren't mis-attributed.
		o := mapEngineOutcome(rle, agentloop.Result{}, false, "openai_compat")
		if o.kind != outcomeRateLimited {
			t.Fatalf("kind = %v, want outcomeRateLimited", o.kind)
		}
		if o.rateLimitSignal.Source != "openai_compat" {
			t.Errorf("Source = %q, want %q", o.rateLimitSignal.Source, "openai_compat")
		}
	})
	t.Run("context cancelled", func(t *testing.T) {
		if o := mapEngineOutcome(context.Canceled, agentloop.Result{}, false, "openrouter"); o.kind != outcomeCancelled {
			t.Errorf("kind = %v, want outcomeCancelled", o.kind)
		}
	})
	t.Run("generic error -> clear", func(t *testing.T) {
		if o := mapEngineOutcome(errors.New("boom"), agentloop.Result{}, false, "openrouter"); o.kind != outcomeClear {
			t.Errorf("kind = %v, want outcomeClear", o.kind)
		}
	})
	t.Run("car done -> completed", func(t *testing.T) {
		o := mapEngineOutcome(nil, agentloop.Result{StopReason: agentloop.StopFinished}, true, "openrouter")
		if o.kind != outcomeCompleted {
			t.Errorf("kind = %v, want outcomeCompleted", o.kind)
		}
	})
	t.Run("max iterations, not done -> stall", func(t *testing.T) {
		o := mapEngineOutcome(nil, agentloop.Result{StopReason: agentloop.StopMaxIterations}, false, "openrouter")
		if o.kind != outcomeStall {
			t.Fatalf("kind = %v, want outcomeStall", o.kind)
		}
		if o.stallReason.Type == "" {
			t.Error("stallReason.Type should be set for a max-iterations stall")
		}
	})
	t.Run("finished, not done -> clear", func(t *testing.T) {
		o := mapEngineOutcome(nil, agentloop.Result{StopReason: agentloop.StopFinished}, false, "openrouter")
		if o.kind != outcomeClear {
			t.Errorf("kind = %v, want outcomeClear", o.kind)
		}
	})
}

// ---------------------------------------------------------------------------
// nativeSpawnRunner (integration with sqlite + tempdir worktree)
// ---------------------------------------------------------------------------

func TestNativeSpawnRunner_CompletedWhenCarDone(t *testing.T) {
	db := engineTestDB(t)
	if err := db.Create(&models.Engine{ID: "eng-1"}).Error; err != nil {
		t.Fatalf("seed engine: %v", err)
	}
	if err := db.Create(&models.Car{ID: "car-1", Status: "done"}).Error; err != nil {
		t.Fatalf("seed car: %v", err)
	}

	c := &loopFakeCompleter{responses: []agentloop.Response{
		writeFileCall("c1", "out.txt", "hello world"),
		stopRespWithUsage("I implemented the change and marked the car done.", 100, 25),
	}}
	runner := nativeSpawnRunner(db, c, "openrouter", 10, nil)

	sess, outcome, err := runner(context.Background(), engine.SpawnOpts{
		EngineID:       "eng-1",
		CarID:          "car-1",
		ContextPayload: "you are an engine working on car-1",
		WorkDir:        t.TempDir(),
		Model:          "openrouter/owl-alpha",
	})
	if err != nil {
		t.Fatalf("runner: %v", err)
	}
	if outcome.kind != outcomeCompleted {
		t.Fatalf("outcome = %v, want outcomeCompleted", outcome.kind)
	}
	if sess == nil || sess.ID == "" || sess.CarID != "car-1" {
		t.Fatalf("session = %+v, want non-empty ID and CarID=car-1", sess)
	}

	// Usage must be persisted to agent_logs so queryCarOutcomeStats can sum it.
	var row models.AgentLog
	if err := db.Where("car_id = ?", "car-1").Order("id DESC").First(&row).Error; err != nil {
		t.Fatalf("expected an agent_logs row: %v", err)
	}
	if row.TokenCount != 125 {
		t.Errorf("TokenCount = %d, want 125 (100+25)", row.TokenCount)
	}
	if row.Model != "openrouter/owl-alpha" {
		t.Errorf("Model = %q, want openrouter/owl-alpha", row.Model)
	}
	if !strings.Contains(row.Content, "write_file") {
		t.Errorf("transcript should record the tool call; got:\n%s", row.Content)
	}
}

func TestNativeSpawnRunner_ClearWhenNotDone(t *testing.T) {
	db := engineTestDB(t)
	db.Create(&models.Engine{ID: "eng-1"})
	db.Create(&models.Car{ID: "car-1", Status: "in_progress"})

	c := &loopFakeCompleter{responses: []agentloop.Response{
		stopRespWithUsage("I think I'm done but forgot to mark it.", 10, 5),
	}}
	runner := nativeSpawnRunner(db, c, "openrouter", 10, nil)

	_, outcome, err := runner(context.Background(), engine.SpawnOpts{
		EngineID: "eng-1", CarID: "car-1", ContextPayload: "sys", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("runner: %v", err)
	}
	if outcome.kind != outcomeClear {
		t.Errorf("outcome = %v, want outcomeClear", outcome.kind)
	}
}

func TestNativeSpawnRunner_StallOnMaxIterations(t *testing.T) {
	db := engineTestDB(t)
	db.Create(&models.Engine{ID: "eng-1"})
	db.Create(&models.Car{ID: "car-1", Status: "in_progress"})

	// Never stops calling tools -> hits the iteration cap.
	c := &loopFakeCompleter{responses: []agentloop.Response{
		noopBashCall("c1"), noopBashCall("c2"), noopBashCall("c3"),
		noopBashCall("c4"), noopBashCall("c5"),
	}}
	runner := nativeSpawnRunner(db, c, "openrouter", 3, nil)

	_, outcome, err := runner(context.Background(), engine.SpawnOpts{
		EngineID: "eng-1", CarID: "car-1", ContextPayload: "sys", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("runner: %v", err)
	}
	if outcome.kind != outcomeStall {
		t.Errorf("outcome = %v, want outcomeStall (hit iteration cap)", outcome.kind)
	}
}

func TestNativeSpawnRunner_RateLimited(t *testing.T) {
	// The rate-limit signal's Source must equal the auth method that the
	// runner was built for; mapEngineOutcome serves both openrouter and
	// openai_compat, so the source can't be hardcoded.
	for _, authMethod := range []string{"openrouter", "openai_compat"} {
		t.Run(authMethod, func(t *testing.T) {
			db := engineTestDB(t)
			db.Create(&models.Engine{ID: "eng-1"})
			db.Create(&models.Car{ID: "car-1", Status: "in_progress"})

			c := &loopFakeCompleter{err: &agentloop.RateLimitError{RetryAfter: 3 * time.Second, Message: "429"}}
			runner := nativeSpawnRunner(db, c, authMethod, 10, nil)

			_, outcome, err := runner(context.Background(), engine.SpawnOpts{
				EngineID: "eng-1", CarID: "car-1", ContextPayload: "sys", WorkDir: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("runner: %v", err)
			}
			if outcome.kind != outcomeRateLimited {
				t.Fatalf("outcome = %v, want outcomeRateLimited", outcome.kind)
			}
			if outcome.rateLimitSignal.RetryAfter != 3*time.Second {
				t.Errorf("RetryAfter = %v, want 3s", outcome.rateLimitSignal.RetryAfter)
			}
			if outcome.rateLimitSignal.Source != authMethod {
				t.Errorf("Source = %q, want %q", outcome.rateLimitSignal.Source, authMethod)
			}
		})
	}
}
