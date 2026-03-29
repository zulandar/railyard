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
| `auth.method` | Auth method: `api_key`, `oauth_token`, `bedrock`, `vertex`, `foundry` | `api_key` |
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

## Upgrading

```bash
helm upgrade railyard ./charts/railyard -f my-values.yaml
```

Review the default `values.yaml` for any new or changed values before upgrading.
