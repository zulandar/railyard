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
| `git.owner` | Git repository owner | `""` |
| `git.repo` | Git repository URL | `""` |
| `git.defaultBranch` | Default branch name | `main` |

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
| `auth.copilot.token` | GitHub PAT for Copilot CLI (overrides `githubToken` for Copilot) | `""` |
| `auth.apiKeyHelper` | Command for dynamic key rotation | `""` |

### Dolt Database

| Value | Description | Default |
|-------|-------------|---------|
| `dolt.internal` | Deploy Dolt as a StatefulSet inside the cluster | `true` |
| `dolt.host` | Dolt host (auto-derived when `internal=true`) | `""` |
| `dolt.port` | Dolt port | `3306` |
| `dolt.database` | Database name (defaults to `railyard_{project}`) | `""` |
| `dolt.username` | Database username | `root` |
| `dolt.password` | Database password | `""` |
| `dolt.storage.size` | PVC size for internal Dolt | `10Gi` |
| `dolt.storage.storageClass` | Storage class for internal Dolt | `""` |

### pgvector (PostgreSQL)

| Value | Description | Default |
|-------|-------------|---------|
| `pgvector.internal` | Deploy pgvector as a StatefulSet inside the cluster | `true` |
| `pgvector.host` | pgvector host (auto-derived when `internal=true`) | `""` |
| `pgvector.port` | pgvector port | `5432` |
| `pgvector.database` | Database name | `cocoindex` |
| `pgvector.username` | Database username | `cocoindex` |
| `pgvector.password` | Database password | `cocoindex` |
| `pgvector.storage.size` | PVC size for internal pgvector | `10Gi` |
| `pgvector.storage.storageClass` | Storage class for internal pgvector | `""` |

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

### Engine

| Value | Description | Default |
|-------|-------------|---------|
| `engine.agentProvider` | Agent provider: `claude`, `codex`, `gemini`, `opencode`, `copilot` | `claude` |
| `engine.resources` | Resource requests/limits per engine pod | `{}` |
| `engine.nodeSelector` | Node selector for engine pods | `{}` |
| `engine.tolerations` | Tolerations for engine pods | `[]` |
| `engine.affinity` | Affinity rules for engine pods | `{}` |
| `engine.extraEnv` | Extra environment variables for engine pods | `[]` |

### Dashboard

| Value | Description | Default |
|-------|-------------|---------|
| `dashboard.replicas` | Number of dashboard replicas | `1` |
| `dashboard.service.type` | Service type | `ClusterIP` |
| `dashboard.service.port` | Service port | `8080` |
| `dashboard.ingress.enabled` | Enable ingress for the dashboard | `false` |
| `dashboard.ingress.className` | Ingress class name | `""` |
| `dashboard.ingress.host` | Ingress hostname | `""` |
| `dashboard.oauth2proxy.enabled` | Enable OAuth2 Proxy sidecar | `false` |
| `dashboard.oauth2proxy.clientID` | OAuth2 client ID | `""` |
| `dashboard.oauth2proxy.clientSecret` | OAuth2 client secret | `""` |
| `dashboard.oauth2proxy.cookieSecret` | OAuth2 cookie secret | `""` |

### Telegraph (Chat Bridge)

| Value | Description | Default |
|-------|-------------|---------|
| `telegraph.enabled` | Enable the Telegraph chat bridge | `false` |
| `telegraph.platform` | Platform: `slack` or `discord` | `slack` |
| `telegraph.channel` | Channel name or ID | `""` |
| `telegraph.slack.botToken` | Slack bot token | `""` |
| `telegraph.slack.appToken` | Slack app token | `""` |
| `telegraph.discord.botToken` | Discord bot token | `""` |
| `telegraph.discord.guildID` | Discord guild ID | `""` |
| `telegraph.discord.channelID` | Discord channel ID | `""` |

### CI Test Values

The `ci/` directory contains example values files for chart validation:

| File | Description |
|------|-------------|
| `ci/test-values-minimal.yaml` | Bare minimum — git and auth only. Good for `helm template` smoke tests. |
| `ci/test-values-external-db.yaml` | External databases with `dolt.internal=false` and `pgvector.internal=false`. |
| `ci/test-values-full.yaml` | Full configuration — ingress, OAuth2 proxy, multiple tracks, Telegraph. |
| `ci/test-values-copilot.yaml` | Copilot provider with dedicated auth token. Validates copilot token precedence. |

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
  --set dolt.internal=false \
  --set dolt.host=mysql.example.com \
  --set dolt.database=railyard_prod \
  --set dolt.password=secret \
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
