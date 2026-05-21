# Railyard Helm Chart

Helm chart for deploying [Railyard](https://github.com/zulandar/railyard), an AI-powered software engineering orchestration platform.

## Prerequisites

- Kubernetes 1.26+
- Helm 3.x
- Optional: cert-manager (for TLS certificates)
- Optional: an Ingress controller (e.g., nginx-ingress) if enabling dashboard ingress

## Quick Start

```bash
helm install railyard ./charts/railyard \
  --set git.owner=myorg \
  --set git.repo=git@github.com:myorg/myrepo.git \
  --set auth.apiKey=sk-ant-XXXX
```

## Configuration

### Git

| Value | Description | Default |
|-------|-------------|---------|
| `project` | Project name for namespace derivation and resource naming | `""` |
| `requirePR` | Create draft PRs instead of merging directly (requires `auth.githubToken`) | `false` |
| `git.owner` | Git repository owner | `""` |
| `git.repo` | Git repository URL | `""` |
| `git.defaultBranch` | Default branch name | `main` |

### Image

| Value | Description | Default |
|-------|-------------|---------|
| `image.repository` | Container image repository | `ghcr.io/zulandar/railyard/engine` |
| `image.tag` | Image tag (defaults to `.Chart.AppVersion`) | `""` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `imagePullSecret` | Name of an existing image pull secret | `""` |

### Service Account

| Value | Description | Default |
|-------|-------------|---------|
| `serviceAccount.create` | Create a service account | `true` |
| `serviceAccount.name` | Service account name | `""` |
| `serviceAccount.annotations` | Annotations to add to the service account | `{}` |

### Authentication

| Value | Description | Default |
|-------|-------------|---------|
| `auth.method` | Auth method: `api_key`, `oauth_token`, `bedrock`, `vertex`, `foundry`, `do_inference`, `openrouter`, `openai_compat` | `api_key` |
| `auth.existingSecret` | Use an existing Secret instead of creating one | `""` |
| `auth.apiKey` | API key (for `api_key` method) | `""` |
| `auth.oauthToken` | OAuth token from `claude setup-token` (for `oauth_token` method) | `""` |
| `auth.bedrock.accessKeyID` | AWS access key ID (for `bedrock` method) | `""` |
| `auth.bedrock.secretAccessKey` | AWS secret access key (for `bedrock` method) | `""` |
| `auth.bedrock.region` | AWS region | `us-east-1` |
| `auth.vertex.projectID` | GCP project ID (for `vertex` method) | `""` |
| `auth.vertex.region` | GCP region | `us-central1` |
| `auth.vertex.credentialsSecret` | Secret with service account JSON | `""` |
| `auth.foundry.apiKey` | Azure API key (for `foundry` method) | `""` |
| `auth.foundry.endpoint` | Azure endpoint | `""` |
| `auth.doInference.apiKey` | DigitalOcean model access key or PAT (for `do_inference` method) | `""` |
| `auth.openrouter.apiKey` | OpenRouter API key (for `openrouter` method) | `""` |
| `auth.openaiCompat.baseURL` | OpenAI-compatible endpoint URL (for `openai_compat` method, e.g. `https://inference.do-ai.run/v1`) | `""` |
| `auth.openaiCompat.apiKey` | API key for the OpenAI-compatible backend (for `openai_compat` method) | `""` |
| `auth.openaiCompat.providerName` | Optional codex provider key (defaults to `openai_compat`) | `""` |
| `auth.githubToken` | GitHub PAT for PR operations (requires `requirePR`). Sets `GH_TOKEN` env var | `""` |
| `auth.copilot.token` | GitHub PAT for Copilot CLI (overrides `githubToken` for Copilot) | `""` |
| `auth.apiKeyHelper` | Command for dynamic key rotation | `""` |

### Database (MySQL)

| Value | Description | Default |
|-------|-------------|---------|
| `database.internal` | Deploy MySQL as a StatefulSet inside the cluster | `true` |
| `database.host` | Database host (auto-derived when `internal=true`) | `""` |
| `database.port` | Database port | `3306` |
| `database.database` | Database name (defaults to `railyard_{project}`) | `""` |
| `database.username` | Database username | `root` |
| `database.password` | Database password | `""` |
| `database.tls.enabled` | Enable TLS for database connections | `false` |
| `database.tls.caSecret` | Secret name containing `ca.crt` | `""` |
| `database.tls.clientSecret` | Secret name containing `tls.crt` + `tls.key` | `""` |
| `database.tls.skipVerify` | Skip TLS certificate verification | `false` |
| `database.storage.size` | PVC size for internal MySQL | `10Gi` |
| `database.storage.storageClass` | Storage class for internal MySQL | `""` |
| `database.resources` | Resource requests/limits for the internal MySQL pod | `{}` |

### pgvector (PostgreSQL)

| Value | Description | Default |
|-------|-------------|---------|
| `pgvector.internal` | Deploy pgvector as a StatefulSet inside the cluster | `true` |
| `pgvector.host` | pgvector host (auto-derived when `internal=true`) | `""` |
| `pgvector.port` | pgvector port | `5432` |
| `pgvector.database` | Database name | `cocoindex` |
| `pgvector.username` | Database username | `cocoindex` |
| `pgvector.password` | Database password | `cocoindex` |
| `pgvector.sslmode` | PostgreSQL sslmode for client connections | `prefer` |
| `pgvector.storage.size` | PVC size for internal pgvector | `10Gi` |
| `pgvector.storage.storageClass` | Storage class for internal pgvector | `""` |
| `pgvector.resources` | Resource requests/limits for the internal pgvector pod | `{}` |

### Tracks

| Value | Description | Default |
|-------|-------------|---------|
| `tracks` | List of track definitions | See `values.yaml` |
| `tracks[].name` | Track name | (required) |
| `tracks[].engineSlots` | Number of engine slots | `3` |
| `tracks[].minReplicas` | Minimum replicas for HPA | `1` |
| `tracks[].maxReplicas` | Maximum replicas for HPA | `3` |
| `tracks[].language` | Programming language | (required) |
| `tracks[].testCommand` | Test command to run | (required) |
| `tracks[].preTestCommand` | Command to run before tests (e.g., setup, migrations) | `""` |
| `tracks[].image.repository` | Custom container image for this track's engine pods | (global image) |
| `tracks[].image.tag` | Image tag for the custom track image | (global tag) |
| `tracks[].playwright.enabled` | Enable the Playwright PR Demo feature on this track. See [Playwright PR Demo Setup Guide](../../docs/playwright-pr-demo.md). | `false` |
| `tracks[].playwright.specPath` | Directory (relative to repo root) where engines write new spec files. Required when `enabled: true`. | `""` |
| `tracks[].playwright.filename` | Naming pattern for new specs. `{car_id}` substituted at dispatch time. | `{car_id}.spec.ts` |
| `tracks[].playwright.template` | Optional path to a starter spec the engine copies from. Bullet only renders when the file exists in the engine's worktree. | `""` |

### Engine

| Value | Description | Default |
|-------|-------------|---------|
| `engine.agentProvider` | Agent provider: `claude`, `codex`, `gemini`, `opencode`, `copilot` | `claude` |
| `engine.resources` | Resource requests/limits per engine pod | `{}` |
| `engine.nodeSelector` | Node selector for engine pods | `{}` |
| `engine.tolerations` | Tolerations for engine pods | `[]` |
| `engine.affinity` | Affinity rules for engine pods | `{}` |
| `engine.extraEnv` | Extra environment variables for engine pods | `[]` |

### Dispatch

| Value | Description | Default |
|-------|-------------|---------|
| `dispatch.replicas` | Number of dispatch replicas | `1` |
| `dispatch.resources` | Resource requests/limits for dispatch pods | `{}` |

### Yardmaster

| Value | Description | Default |
|-------|-------------|---------|
| `yardmaster.replicas` | Number of yardmaster replicas | `1` |
| `yardmaster.resources` | Resource requests/limits for yardmaster pods | `{}` |
| `yardmaster.healthPort` | Port for `/healthz` and `/readyz` probes | `8081` |
| `yardmaster.autoMergeOnApproval` | Auto-merge approved PRs via gh CLI (requires `requirePR`) | `false` |

### Dashboard

| Value | Description | Default |
|-------|-------------|---------|
| `dashboard.replicas` | Number of dashboard replicas | `1` |
| `dashboard.resources` | Resource requests/limits for dashboard pods | `{}` |
| `dashboard.service.type` | Service type | `ClusterIP` |
| `dashboard.service.port` | Service port | `8080` |
| `dashboard.ingress.enabled` | Enable ingress for the dashboard | `false` |
| `dashboard.ingress.className` | Ingress class name | `""` |
| `dashboard.ingress.host` | Ingress hostname | `""` |
| `dashboard.rateLimit.enabled` | Enable per-IP rate limiting for dashboard routes | `false` |
| `dashboard.rateLimit.requestsPerMinute` | Maximum requests per minute per IP | `120` |
| `dashboard.oauth2proxy.enabled` | Enable OAuth2 Proxy sidecar | `false` |
| `dashboard.oauth2proxy.clientID` | OAuth2 client ID | `""` |
| `dashboard.oauth2proxy.clientSecret` | OAuth2 client secret | `""` |
| `dashboard.oauth2proxy.cookieSecret` | OAuth2 cookie secret | `""` |

### Telegraph (Chat Bridge)

| Value | Description | Default |
|-------|-------------|---------|
| `telegraph.enabled` | Enable the Telegraph chat bridge | `false` |
| `telegraph.replicas` | Number of Telegraph replicas | `1` |
| `telegraph.resources` | Resource requests/limits for Telegraph pods | `{}` |
| `telegraph.platform` | Platform: `slack` or `discord` | `slack` |
| `telegraph.channel` | Channel name or ID | `""` |
| `telegraph.processTimeoutSec` | Max seconds a dispatch subprocess may run | `900` |
| `telegraph.healthPort` | Port for `/healthz` and `/readyz` probes | `8086` |
| `telegraph.slack.botToken` | Slack bot token | `""` |
| `telegraph.slack.appToken` | Slack app token | `""` |
| `telegraph.discord.botToken` | Discord bot token | `""` |
| `telegraph.discord.guildID` | Discord guild ID | `""` |
| `telegraph.discord.channelID` | Discord channel ID | `""` |

### Bull (Issue Triage)

| Value | Description | Default |
|-------|-------------|---------|
| `bull.enabled` | Enable the Bull GitHub issue triage daemon | `false` |
| `bull.replicas` | Number of Bull replicas | `1` |
| `bull.resources` | Resource requests/limits for Bull pods | `{}` |
| `bull.pollIntervalSec` | Poll interval in seconds for checking new GitHub issues | `60` |
| `bull.triageMode` | Triage mode: `standard` (heuristic + AI) or `full` (AI for all issues) | `standard` |
| `bull.githubToken` | GitHub token for Bull (falls back to `auth.githubToken` if empty) | `""` |
| `bull.appID` | GitHub App ID (set non-zero to enable GitHub App auth) | `0` |
| `bull.privateKeySecret` | Kubernetes Secret containing the GitHub App private key PEM | `""` |
| `bull.privateKeySecretKey` | Key within `privateKeySecret` that holds the PEM data | `private-key.pem` |
| `bull.installationID` | GitHub App installation ID | `0` |
| `bull.comments.enabled` | Enable issue commenting | `false` |
| `bull.comments.answerQuestions` | Answer questions in issue comments | `false` |
| `bull.labels.underReview` | Label for issues under review | `bull: under review` |
| `bull.labels.inProgress` | Label for issues in progress | `bull: in progress` |
| `bull.labels.fixMerged` | Label for issues with a merged fix | `bull: fix merged` |
| `bull.labels.ignore` | Label to exclude issues from triage | `bull: ignore` |

### Network Policy

| Value | Description | Default |
|-------|-------------|---------|
| `networkPolicy.enabled` | Enable NetworkPolicy resources restricting inter-pod traffic | `false` |
| `networkPolicy.dashboard.ingressCIDR` | CIDRs allowed to reach the dashboard (empty allows same namespace only) | `[]` |

### CI Test Values

The `ci/` directory contains example values files for chart validation:

| File | Description |
|------|-------------|
| `ci/test-values-minimal.yaml` | Bare minimum — git and auth only. Good for `helm template` smoke tests. |
| `ci/test-values-external-db.yaml` | External databases with `database.internal=false` and `pgvector.internal=false`. |
| `ci/test-values-full.yaml` | Full configuration — ingress, OAuth2 proxy, multiple tracks, Telegraph. |
| `ci/test-values-copilot.yaml` | Copilot provider with dedicated auth token. Validates copilot token precedence. |
| `ci/test-values-existing-secret.yaml` | Existing secret with `auth.existingSecret`. Enables Bull and Telegraph. |
| `ci/test-values-kind.yaml` | Kind cluster setup with local image, Bull enabled, and dummy credentials. |
| `ci/test-values-networkpolicy.yaml` | NetworkPolicy enabled with dashboard ingress CIDR. Enables Telegraph and Bull. |

Use these to validate chart rendering:

```bash
# Lint the chart
helm lint ./charts/railyard -f ./charts/railyard/ci/test-values-minimal.yaml

# Render templates without installing
helm template railyard ./charts/railyard -f ./charts/railyard/ci/test-values-full.yaml
```

## Usage Examples

### Basic install with API key

```bash
helm install railyard ./charts/railyard \
  --set git.owner=myorg \
  --set git.repo=git@github.com:myorg/myrepo.git \
  --set auth.apiKey=sk-ant-XXXX
```

### Install with external database

```bash
helm install railyard ./charts/railyard \
  --set git.owner=myorg \
  --set git.repo=git@github.com:myorg/myrepo.git \
  --set auth.apiKey=sk-ant-XXXX \
  --set database.internal=false \
  --set database.host=mysql.example.com \
  --set database.database=railyard_prod \
  --set database.password=secret \
  --set pgvector.internal=false \
  --set pgvector.host=pgvector.example.com \
  --set pgvector.password=secret
```

### Install with ingress and OAuth

```bash
helm install railyard ./charts/railyard \
  -f my-values.yaml
```

Where `my-values.yaml` contains:

```yaml
git:
  owner: myorg
  repo: git@github.com:myorg/myrepo.git
auth:
  method: oauth_token
  oauthToken: "your-token-here"
dashboard:
  ingress:
    enabled: true
    className: nginx
    host: railyard.example.com
  oauth2proxy:
    enabled: true
    clientID: github-client-id
    clientSecret: github-client-secret
    cookieSecret: random-cookie-secret
```

### Install with DigitalOcean Serverless Inference

`auth.method: do_inference` routes the `claude` CLI to DigitalOcean's
multi-tenant inference endpoint (`https://inference.do-ai.run`) by injecting
`ANTHROPIC_BASE_URL` and `ANTHROPIC_API_KEY` into the engine pod. Unlike the
standard Anthropic API, DO has **no implicit default model** — every request
must specify one — so `agent_model` is required at the application config
layer (top-level in `railyard.yaml`). Startup validation fails if it is
missing.

See the [DigitalOcean Inference docs](https://docs.digitalocean.com/products/inference/)
for the model catalog and to obtain an access key.

```yaml
git:
  owner: myorg
  repo: git@github.com:myorg/myrepo.git
auth:
  method: do_inference
  doInference:
    apiKey: "do_pat_or_model_access_key"
engine:
  agentProvider: claude
  # agent_model must be set so DO knows which model to route to.
  # This surfaces in the rendered railyard.yaml as top-level agent_model.
  agentModel: "anthropic-claude-4.6-sonnet"
```

#### Verifying the install

After installing with `auth.method: do_inference`, run through the following
steps to confirm the integration is wired up correctly end-to-end. Substitute
`<engine-pod>` with an actual engine pod name (e.g. `railyard-engine-backend-0`)
and `<car-id>` with the ID returned from `ry car create`.

1. **Confirm pod env contains DO base URL and key:**

   ```bash
   kubectl exec -n railyard <engine-pod> -- env | grep ANTHROPIC
   ```

   Expected: `ANTHROPIC_BASE_URL=https://inference.do-ai.run` and
   `ANTHROPIC_API_KEY=<your-key>` are both present. `ANTHROPIC_MODEL` is
   injected per-subprocess by the claude provider, not at the pod level.

2. **Confirm the rendered ConfigMap carries `agent_model` and `auth_method`:**

   ```bash
   kubectl get configmap -n railyard railyard-config -o yaml \
     | grep -E 'agent_model|auth_method'
   ```

   Expected: both keys appear with the values from `engine.agentModel` and
   `auth.method`.

3. **Spawn a trivial test car** (e.g. a typo fix in README) and watch it claim:

   ```bash
   ry car create --track backend --title "smoke: typo fix" \
     --description "Fix a typo in README.md"
   ry car list
   ```

   Expected: status transitions `queued → claimed → running` within one
   dispatch poll interval.

4. **Check engine logs for the model invocation:**

   ```bash
   kubectl logs -n railyard <engine-pod> --tail=200 | grep -iE 'model|anthropic'
   ```

   Expected: a log line shows the claude subprocess invoked with
   `ANTHROPIC_MODEL=anthropic-claude-4.6-sonnet`. No "unknown model" or
   "model is required" errors from DO.

5. **Verify DO control panel records the request:** Visit
   `https://cloud.digitalocean.com/` → **Inference → Usage**. Within ~60s of
   the car claim, a request against `anthropic-claude-4.6-sonnet` with
   non-zero token counts should appear.

6. **Confirm the car completes:**

   ```bash
   ry car show <car-id>
   ```

   Expected: status moves to `done` (or `pr_open` if `requirePR=true`) with
   non-zero `tokens_in`/`tokens_out`.

If any step fails, the most common causes are: (a) `engine.agentModel` not set
— startup validation will fail-fast with a clear error; (b) the DO key lacks
inference scope — pod logs will show a 401 from `inference.do-ai.run`; (c) the
configured model name does not exist in DO's catalog — pod logs will show a
4xx from the `/v1/messages` call.

### Install with OpenRouter

`auth.method: openrouter` routes the `claude` CLI to OpenRouter's unified
inference gateway (`https://openrouter.ai/api`) by injecting
`ANTHROPIC_BASE_URL` and `ANTHROPIC_API_KEY` into the engine pod. OpenRouter
fronts ~100+ models from Anthropic, OpenAI, Google, Meta, DeepSeek, Mistral,
Qwen, and others behind a single Anthropic-compatible endpoint. Like DO
inference, OpenRouter has **no implicit default model** — every request must
specify one — so `agent_model` is required at the application config layer
(top-level in `railyard.yaml`). Startup validation fails if it is missing.

See the [OpenRouter docs](https://openrouter.ai/docs) for the full model
catalog and to obtain an API key.

**Naming convention:** OpenRouter uses `provider/model[:variant]`. Set this
exact string in `agent_model`; railyard does not parse or translate model
names. Examples:

- `anthropic/claude-sonnet-4.5` — Anthropic, paid
- `meta-llama/llama-3.3-70b-instruct:free` — OSS Llama, free
- `deepseek/deepseek-r1:free` — OSS reasoning model, free

**Per-key guardrails (recommended):** Configure model allowlists, provider
allowlists, and budget caps **on the OpenRouter dashboard per API key**, not
in railyard config. Create a scoped key for each deployment (e.g. "free
models only, $5/day cap") and railyard will treat the key as opaque
credentials. Mirroring these controls in chart values would create two
sources of truth; the dashboard is authoritative.

**Free models:** `:free` variants cost nothing and are useful for smoke
testing the integration. They are rate-limited (typically ~10 requests/min,
~50 requests/day) and not viable for production cars; use a paid model for
real workloads.

```yaml
git:
  owner: myorg
  repo: git@github.com:myorg/myrepo.git
auth:
  method: openrouter
  openrouter:
    apiKey: "sk-or-v1-..."
engine:
  agentProvider: claude
  # agent_model must be set in OpenRouter's provider/model[:variant] form.
  # This surfaces in the rendered railyard.yaml as top-level agent_model.
  agentModel: "meta-llama/llama-3.3-70b-instruct:free"
```

The `agentModel` value renders as the top-level `agent_model` field in
`railyard.yaml` and cascades to tracks/bull/inspect the same way as for any
other auth method. See the commented `agent_model` block in
`railyard.example.yaml` for the override pattern.

### Install with an OpenAI-compatible backend (codex)

`auth.method: openai_compat` routes the `codex` CLI to any OpenAI-compatible
backend — DigitalOcean Inference's `/v1/chat/completions`, OpenRouter's
OpenAI-compat endpoint, direct OpenAI, a local LM Studio, etc. — by injecting
`OPENAI_API_KEY` and a `~/.codex/config.toml` ConfigMap into the engine pod
that selects the backend's `base_url`. This unlocks non-Anthropic catalogs
(Gemma, Llama, DeepSeek, Qwen, GPT, …) that the existing `do_inference` and
`openrouter` methods cannot reach, since both of those speak the Anthropic
Messages API and route the `claude` CLI only.

Key facts:

- **`engine.agentProvider: codex` is required.** The `claude` CLI cannot speak
  OpenAI-compat, and opencode is currently broken on this path (tracked in
  follow-up issue `railyard-tsm`). Startup config validation enforces this.
- **`engine.agentModel` is required** — the chart and codex have no implicit
  default model. Startup validation fails fast if it is missing.
- **Naming is backend-specific** — railyard does not parse or translate model
  names. Consult your backend's docs:
  - DO Inference: bare names like `gemma-4-31B-it`
  - OpenRouter: `provider/model[:variant]` like
    `meta-llama/llama-3.3-70b-instruct:free`
  - Direct OpenAI: bare names like `gpt-4`, `gpt-4o`
- **codex `web_search` is disabled automatically.** codex enables web search
  by default but most non-OpenAI backends reject the tool. The chart's
  `codex-config.yaml` ConfigMap sets `web_search = "disabled"` for you; no
  manual configuration needed.
- **Per-key guardrails (recommended):** Configure model allowlists, provider
  allowlists, and budget caps **on the backend's own dashboard per API key**
  (DO's *Model Access Keys* page, OpenRouter's API key settings, etc.).
  Railyard treats the key as opaque credentials.

**Sample values — DO Inference + Gemma 4 free model:**

```yaml
git:
  owner: myorg
  repo: git@github.com:myorg/myrepo.git
auth:
  method: openai_compat
  openaiCompat:
    baseURL: "https://inference.do-ai.run/v1"
    apiKey: "doo_v1_..."
engine:
  agentProvider: codex
  agentModel: "gemma-4-31B-it"
```

**Sample values — OpenRouter + free Llama:**

```yaml
git:
  owner: myorg
  repo: git@github.com:myorg/myrepo.git
auth:
  method: openai_compat
  openaiCompat:
    baseURL: "https://openrouter.ai/api/v1"
    apiKey: "sk-or-v1-..."
engine:
  agentProvider: codex
  agentModel: "meta-llama/llama-3.3-70b-instruct:free"
```

**When to pick which method:** `do_inference` and `openrouter` remain the
right choice when you want the `claude` CLI against an Anthropic-skin gateway
(DO's `/v1/messages`, OpenRouter's Anthropic-compatible endpoint). Reach for
`openai_compat` when you want non-Anthropic models — or direct OpenAI —
through the `codex` CLI.

### ArgoCD Application

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: railyard
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/zulandar/railyard.git
    targetRevision: main
    path: charts/railyard
    helm:
      valueFiles:
        - values.yaml
        - values-production.yaml
  destination:
    server: https://kubernetes.default.svc
    namespace: railyard
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

## Rate limit handling

When an engine subprocess hits a 429 from an upstream LLM provider (free-tier
OpenRouter exhaustion, hourly Anthropic API limits, etc.), railyard used to
mark the car as stalled and block it — requiring an operator to apply the
`railyard: rework` label to recover. Railyard now recognizes upstream
rate-limit errors as a distinct outcome and **pauses-and-retries** the engine
subprocess before falling through to the stall path, so transient limits no
longer cost the operator a manual rework.

### What gets recognized

- **Anthropic native** `rate_limit_error` — the Claude CLI talking direct to
  the Anthropic API (any `auth.method` that ends up at `api.anthropic.com`)
  and the do_inference Claude passthrough.
- **OpenRouter wrapped 429** — `auth.method: openrouter` against any upstream
  that returns OpenRouter's wrapped error shape (`{"code":429,...,
  "metadata":{"raw":"...rate-limited..."}}`). Particularly common with free
  `:free` model variants.
- **Generic HTTP 429** with a `Retry-After` header — any backend that
  surfaces these markers in CLI output (`HTTP 429`, `HTTP/1.1 429`,
  `status: 429`).

### How the retry behaves

- **Honors `Retry-After`** when the upstream provides one (e.g. OpenRouter's
  `retry_after_seconds` value).
- **Falls back to exponential backoff with jitter** when no upstream wait is
  available: 10s, 30s, 60s, 120s, 300s (with ±20% jitter to avoid concurrent
  engines re-synchronizing on the same upstream).
- **Caps each wait at `stall.rate_limit_max_wait_sec`** (default 300s).
- **Caps consecutive retries at `stall.rate_limit_max_retries`** (default 3).
- **After exhaustion**, the engine falls through to the existing stall path:
  the car is blocked with reason `rate_limit_exhausted` and surfaces for
  operator rework. This is intentional — it keeps the engine from looping
  forever against a daily quota that won't clear in the current shift.

### Tunable knobs

| Knob | Default | When to tune |
|------|---------|--------------|
| `stall.rate_limit_max_retries` | `3` | Raise (5–10) for production tracks pinned to a paid Anthropic key with occasional spikes — work that's worth waiting for. Lower (1–2) if you'd rather fail fast and escalate to operator review. |
| `stall.rate_limit_max_wait_sec` | `300` | Raise for backends with long cooldowns (some paid tiers expose multi-minute `Retry-After` values). The default 300s caps OpenRouter free-tier hourly resets at a reasonable ceiling. |
| `tracks[].stall_stdout_timeout_sec` | `120` (global `stall.stdout_timeout_sec`) | Raise on tracks pinned to rate-limit-sensitive backends so the existing stall detector doesn't trip mid-retry-backoff. The retry loop terminates the subprocess between attempts, but cumulative delays on long Retry-After values can exceed the 120s default. |

### What operators observe

On detection, the engine emits an info-level log line via slog:

```
Rate limit hit, pausing before retry  car=<id> source=anthropic|openrouter|http wait=30s attempt=1 max=3
```

On retry exhaustion, a warn-level line precedes the standard stall handling:

```
Rate limit retries exhausted, treating as stall  car=<id> retries=3 source=<source>
```

The car is **not** blocked unless retries actually exhaust — the whole point
is that work continues across transient limits. While retries are in flight,
the engine pod stays attached to the car (no other engine will claim it).

### What it does NOT do

For operator-side honesty:

- **Doesn't resume the agent's prior conversation across retries.** The
  re-spawn uses the same initial context payload as the original attempt, not
  the partial assistant turns from before the rate-limit hit. Tracked as
  future work under the resume-from-prior-turns stretch in `railyard-qf1.4`.
- **Doesn't share retry state across engine restarts.** Retry counts are
  in-memory only. If the engine pod is killed (drain, OOM, node reboot)
  mid-retry-backoff, the car becomes a fresh claim on the next cycle —
  retries start from zero on the new pod.
- **Doesn't differentiate "transient" from "exhausted" rate limits.** If you
  hit a daily quota that won't clear for 23 hours, retries will exhaust and
  the car will be stalled. This is correct behavior — operators should
  intervene rather than have an engine pod sleep through the night.

### Sample config snippet

```yaml
stall:
  rate_limit_max_retries: 5       # default 3; raise for paid-tier production with occasional spikes
  rate_limit_max_wait_sec: 180    # default 300; lower for "fail fast" tracks
tracks:
  - name: experimental
    stall_stdout_timeout_sec: 600 # bump fuse for tracks pinned to rate-limit-sensitive backends
```

## Upgrading

```bash
helm upgrade railyard ./charts/railyard -f my-values.yaml
```

Review the default `values.yaml` for any new or changed values before upgrading.
