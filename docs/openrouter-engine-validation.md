# Phase 4 local validation — native OpenRouter engine runner

Runbook for validating the native engine runner (`railyard-j89.5`) end-to-end:
an engine on `auth_method: openrouter` claims a car, writes real code with the
native tool loop (`bash` + `read_file` + `write_file` + `edit_file`), marks the
car done, and produces a mergeable branch — with token usage, rate-limit, stall,
and `ry logs` behaving like the CLI path.

This is the `5.3` spike work: it also lets you compare native coding quality
against the codex fallback (Approach C).

## What you're verifying

| Acceptance | How to check |
|---|---|
| Native loop is selected | Startup log: `Engine using native agent loop` + `Native loop session` |
| Engine edits files / runs tests | New commits on the car branch; `ry logs --car <id> --raw` shows `🔧` tool lines |
| Car marked done | `ry car show <id>` → status `done`/`merged`; engine logs `Car completed` |
| Branch is mergeable | `git log origin/<base>..<car-branch>` has commits; no conflicts on merge |
| Usage from API block | `ry logs --car <id>` shows a token count; `agent_logs.token_count` > 0 |
| Rate-limit handling | On upstream 429, engine logs `Rate limit hit, pausing before retry` then resumes the same conversation (native loop preserves prior turns; see below) |
| Stall handling | If the loop hits its iteration cap without `done`, engine logs `Stall detected` (type `max_iterations`) |

## Prerequisites

```bash
# 1. OpenRouter key in the environment (the loop reads it from env, not yaml).
export OPENROUTER_API_KEY=sk-or-...
# Optional: export OPENROUTER_BASE_URL=https://openrouter.ai/api/v1  (default)

# 2. A reachable database (the engine daemon needs one). Configure it in
#    railyard.yaml (see railyard.example.yaml for the `database:` block).
```

> **Model choice:** `openrouter/owl-alpha` is the target weak model, but it is a
> rotating "stealth" provider that is intermittently unavailable (502 / timeouts).
> For a deterministic first run use a reliably-available, tool-capable model such
> as `openai/gpt-4o-mini`, then re-run with `openrouter/owl-alpha` once it's up to
> confirm the weak-model path. (The agentloop live tests behave the same way:
> they skip when the upstream is down.)

## Minimal `railyard.yaml`

```yaml
owner: <you>
repo: git@github.com:<org>/<repo>.git
project: railyard-validate

# Selects the native agent loop. Credentials come from the environment.
auth_method: openrouter
agent_model: openai/gpt-4o-mini   # or openrouter/owl-alpha when it's up

database:
  host: localhost
  port: 5432
  database: railyard
  username: railyard
  password: railyard

tracks:
  - name: backend
    language: go
```

Notes:
- `auth_method: openrouter` is what flips the engine, dispatch, telegraph, and
  bull onto the native loop locally (no Kubernetes required — the env-key check
  is only enforced in k8s mode, but the loop still resolves the key from env).
- Set `agent_model` to a model that supports OpenAI function-calling.

## Steps

```bash
# 0. Build the current branch's ry binary so you exercise the new code.
go build -o ./ry .            # or: go install ./... ; adjust to your layout

# 1. Initialize the database schema.
./ry db init -c railyard.yaml

# 2. Create a small, self-contained car for the 'backend' track.
./ry car create -c railyard.yaml \
  --track backend \
  --type task \
  --title "Add a greeting helper" \
  --description "Add a function Greeting() string in greeting.go that returns \"hello from railyard\", plus a test greeting_test.go that asserts it. Run 'go test ./...' and make it pass." \
  --acceptance "greeting.go and greeting_test.go exist; go test ./... passes; car marked done."
# -> prints: Created car <car-id>

# 3. Publish it (create makes a draft; the engine only claims 'open' cars).
./ry car publish <car-id> -c railyard.yaml

# 4. Start an engine on the track. --log-level debug shows the loop activity.
./ry engine start -c railyard.yaml --track backend --log-level debug
```

Watch the engine log for, in order:
- `Engine using native agent loop  auth_method=openrouter model=...`
- `Claimed car  car=<car-id>`
- `Native loop session  session=sess-... car=<car-id> model=...`
- then one of: `Car completed`, `Agent exited, clear cycle`, or `Stall detected`.

Stop the engine with Ctrl-C once the car reaches a terminal outcome.

## Verify

```bash
# Car reached done (then yardmaster may move it to merged).
./ry car show <car-id> -c railyard.yaml

# Full transcript (assistant text + 🔧 tool calls/results) and token usage,
# persisted to agent_logs (redacted). --raw shows full content.
./ry logs --car <car-id> --raw -c railyard.yaml

# The car branch has real commits and is mergeable. The engine works in a
# per-engine worktree; the branch is named by your branch_prefix + car id.
git fetch
git log --oneline origin/<base-branch>..<car-branch>
git merge --no-commit --no-ff <car-branch>   # expect a clean merge; then `git merge --abort`
```

Expected: `greeting.go` + `greeting_test.go` on the branch, `go test ./...`
passing in the worktree, car status `done`/`merged`, and `ry logs` showing the
tool calls and a non-zero token count.

## Lifecycle checks

- **Usage** — `agent_logs.token_count` for the car is non-zero and taken straight
  from the API `usage` block (not text-scraped). `ry logs --car <id>` surfaces it.
- **Rate-limit** — hard to force deliberately; if the upstream returns HTTP 429
  the engine log shows `Rate limit hit, pausing before retry` and retries up to
  `stall.rate_limit_max_retries`, then converts to a stall. On the **native
  loop** path (OpenRouter / openai_compat), a retry **resumes the same
  conversation** — prior assistant turns, tool calls, and tool results are
  replayed as history, so the agent picks up where the rate limit hit rather
  than restarting from the kickoff message. Work done before the 429 (reads,
  edits, partial reasoning) is preserved. The 429 returns before the failing
  assistant turn is recorded, so the resumed conversation is clean. Each attempt
  still persists its own `agent_logs` row, so token usage across retries is
  summed into the car's outcome stats. (Unit-covered: `pkg/cli`
  `TestNativeSpawnRunner_RateLimited`,
  `TestNativeSpawnRunner_ResumesConversationAfterRateLimit`,
  `TestNativeSpawnRunner_PersistsEachRetryAttempt`.)
  - **CLI subprocess path** (claude/codex providers) does **not** resume — a
    retry re-spawns the subprocess from the kickoff message, so prior in-session
    progress is restarted. Conversation resume is native-loop-only for now.
- **Stall** — to see the stall path, give a car the model can't finish within the
  iteration cap (`nativeEngineMaxIterations`, currently 80); the engine logs
  `Stall detected` with `type=max_iterations` and escalates, exactly like a CLI
  stall. (Unit-covered: `TestNativeSpawnRunner_StallOnMaxIterations`.)
- **Logs** — confirm secrets are redacted (no raw `sk-...` in `agent_logs`).

## Compare against the codex fallback (Approach C)

To judge whether the hand-rolled toolset is sufficient vs a purpose-built coding
CLI, run the same car twice and compare diff quality / iterations / tokens:

```bash
# Native loop:
#   auth_method: openrouter ; agent_model: <model>
# Codex fallback:
#   auth_method: openai_compat ; agent_provider: codex ; agent_model: <model>
#   (codex reads OPENAI_BASE_URL/OPENAI_API_KEY from env)
```

Record findings on `railyard-j89.5.3`. If native underperforms on larger edits,
the documented fallback is routing engine code-writing through codex while
keeping the native loop for telegraph/dispatch/bull.

## CI / automated coverage (no key needed)

- `go test ./pkg/cli/ -run 'TestMapEngineOutcome|TestNativeSpawnRunner'` — outcome
  mapping + runner (completed/clear/stall/rate-limited) + usage persistence.
- `go test ./internal/agentloop/ -run TestLive_OpenRouter` — gated live smoke +
  engine-tools write-file (skips without `RAILYARD_LIVE_OPENROUTER_KEY` or
  `/tmp/or_test_key`, and skips when the upstream model is unavailable).
