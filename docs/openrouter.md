# Running Railyard on OpenRouter (and other OpenAI-compatible backends)

Railyard can drive [OpenRouter](https://openrouter.ai) — and any OpenAI-compatible
endpoint — two different ways. Pick one with `auth_method` (locally in
`railyard.yaml`, or via the chart's `auth.method`, which the chart injects into
the app config).

| | **Approach A — native loop** | **Approach B — claude + skin** |
|---|---|---|
| `auth_method` | `openrouter` / `openai_compat` | `openrouter_skin` |
| Who drives the model | Railyard's own agent loop (`internal/agentloop`) | the `claude` CLI |
| Endpoint | OpenAI-compatible `/chat/completions` | OpenRouter's Anthropic skin (`/v1/messages`) |
| Env (chart-injected) | `OPENROUTER_API_KEY` / `OPENAI_BASE_URL`+`OPENAI_API_KEY` | `ANTHROPIC_BASE_URL`+`ANTHROPIC_API_KEY` |
| Works with weak models (e.g. owl-alpha) | **yes** | **no** (see limitation) |
| Works with capable models | yes | yes |
| Roles covered | telegraph, `ry dispatch`, bull, engines | all (via the claude CLI) |
| Extra code | Railyard-owned loop | near-zero (reuses claude CLI) |

**Default recommendation: Approach A (native loop).** It is the only path that
works across *all* roles and *both* weak and capable models, and it is
self-contained (no coupling to the claude CLI or skin fidelity). Use Approach B
only if you run capable models exclusively and prefer to lean on the claude CLI.

> **Migration note:** before the native loop, `auth_method: openrouter` meant
> "claude CLI via the Anthropic skin." That behavior is now `openrouter_skin`.
> If you were on `openrouter` for the skin, either switch to `openrouter_skin`
> (same behavior) or move to the native loop (recommended).

---

## Approach A — native loop (`openrouter` / `openai_compat`)

Railyard owns the conversation, the (minimal) system prompt, and the tool loop,
talking to the backend's OpenAI-compatible `/chat/completions` endpoint. Because
the prompt is clean and focused, even weak models follow tool calls reliably —
which is exactly what derails them under the claude CLI's built-in harness
prompt (proven 2026-05-27 with `owl-alpha`).

### Local (`railyard.yaml` + env)

```yaml
# railyard.yaml
auth_method: openrouter
agent_model: openrouter/owl-alpha   # or any tool-capable OpenRouter model
```

```bash
export OPENROUTER_API_KEY=sk-or-v1-...
# Optional override (default https://openrouter.ai/api/v1):
# export OPENROUTER_BASE_URL=https://openrouter.ai/api/v1
```

For a generic OpenAI-compatible backend use `auth_method: openai_compat` and set
`OPENAI_BASE_URL` + `OPENAI_API_KEY` instead.

### Kubernetes (Helm)

```yaml
# values.yaml
auth:
  method: openrouter
  openrouter:
    apiKey: sk-or-v1-...
    # baseURL: ""   # optional override
agent_model: openrouter/owl-alpha   # (set in the app config / chart)
```

The chart injects `OPENROUTER_API_KEY` (and `OPENROUTER_BASE_URL` if set). For
`openai_compat`, set `auth.openaiCompat.baseURL` + `apiKey`; the chart injects
`OPENAI_BASE_URL` + `OPENAI_API_KEY`.

### Model naming

Use OpenRouter's `provider/model[:variant]` form, e.g.
`anthropic/claude-sonnet-4.5`, `openrouter/owl-alpha`,
`meta-llama/llama-3.3-70b-instruct:free`. The model must support OpenAI
function-calling. Configure per-key model/provider/budget guardrails on the
OpenRouter dashboard.

### Validating engines end-to-end

See [`docs/openrouter-engine-validation.md`](./openrouter-engine-validation.md)
for a runbook that drives a real engine on `auth_method: openrouter`.

---

## Approach B — claude CLI + Anthropic skin (`openrouter_skin`)

The `claude` CLI is pointed at OpenRouter's Anthropic-compatible skin via
`ANTHROPIC_BASE_URL=https://openrouter.ai/api` (the Anthropic client appends
`/v1/messages`, reaching `https://openrouter.ai/api/v1/messages`). Railyard
writes no extra loop code — claude drives every role, including its own mature
engine coding loop.

**Limitation (why it's capable-models-only):** weak models like `owl-alpha`
are derailed by the claude Code harness's large built-in system prompt (it is
not overridable via config or hooks) — they assert their own identity and emit
no tool calls. This was reproduced 2026-05-27. Use Approach A for weak models.

### Local (`railyard.yaml` + env)

```yaml
# railyard.yaml
auth_method: openrouter_skin
agent_provider: claude              # the skin is claude-only
agent_model: anthropic/claude-sonnet-4.5   # a capable model
```

```bash
export ANTHROPIC_BASE_URL=https://openrouter.ai/api
export ANTHROPIC_API_KEY=sk-or-v1-...   # your OpenRouter key
```

### Kubernetes (Helm)

```yaml
# values.yaml
auth:
  method: openrouter_skin
  openrouter:
    apiKey: sk-or-v1-...
engine:
  agentProvider: claude
agent_model: anthropic/claude-sonnet-4.5   # (set in the app config / chart)
```

The chart injects `ANTHROPIC_BASE_URL=https://openrouter.ai/api` +
`ANTHROPIC_API_KEY` (from `auth.openrouter.apiKey`). `auth_method: openrouter_skin`
is **not** a native-loop method, so Railyard routes through the claude CLI.

---

## Choosing

- **Weak / cheap models, or you want one consistent backend for every role** →
  Approach A (`openrouter`).
- **Capable models only, and you prefer the claude CLI's engine coding loop** →
  Approach B (`openrouter_skin`).
- **A non-OpenRouter OpenAI-compatible backend** (DO Inference, direct OpenAI,
  local LM Studio, …) → Approach A with `openai_compat`.
