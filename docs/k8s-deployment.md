# Kubernetes Deployment Guide

This guide walks through deploying Railyard on Kubernetes using the official Helm chart. Railyard deploys five core components -- Dolt (metadata database), pgvector (vector store for CocoIndex), dispatch, yardmaster, dashboard -- plus a pool of engine pods per track.

## 1. Prerequisites

Before you begin, ensure the following are available:

- **Kubernetes 1.26+** cluster (EKS, GKE, AKS, or self-managed). For local testing with minikube, see [k8s-local-testing.md](k8s-local-testing.md).
- **kubectl** configured and pointed at your cluster (`kubectl cluster-info` should succeed)
- **Helm 3.x** installed ([helm.sh/docs/intro/install](https://helm.sh/docs/intro/install/))
- **Agent credentials** for your chosen AI provider — API keys for Claude/Codex/Gemini/OpenCode, or a GitHub PAT for Copilot (see [k8s-authentication.md](k8s-authentication.md))

Optional:

- **cert-manager** -- required if you want automatic TLS certificates for ingress
- **Ingress controller** (e.g. nginx-ingress, Traefik) -- required for host-based dashboard access
- **External databases** -- if you prefer managed MySQL/Dolt or PostgreSQL instead of in-cluster StatefulSets

## 2. Quick Start

The fastest path is to install with internal databases (Dolt and pgvector run as StatefulSets inside the cluster).

### Add the chart (or use the local path)

```bash
# From a local clone of the repository:
helm install railyard ./charts/railyard \
  --namespace railyard --create-namespace \
  -f my-values.yaml
```

### Minimal values file

Create `my-values.yaml` with the essentials:

```yaml
project: myproject

git:
  owner: my-org
  repo: my-repo
  defaultBranch: main

auth:
  method: api_key
  apiKey: "sk-ant-..."

engine:
  agentProvider: claude

tracks:
  - name: backend
    engineSlots: 3
    minReplicas: 1
    maxReplicas: 3
    language: go
    testCommand: "go test ./..."
```

### Verify the deployment

```bash
kubectl get pods -n railyard
```

You should see pods for each component:

```
NAME                                    READY   STATUS    RESTARTS   AGE
railyard-dolt-0                         1/1     Running   0          2m
railyard-pgvector-0                     1/1     Running   0          2m
railyard-dispatch-6b8f9c7d4-xxxxx       1/1     Running   0          2m
railyard-yardmaster-5c4d8e9f1-xxxxx     1/1     Running   0          2m
railyard-dashboard-7a2b3c4d5-xxxxx      1/1     Running   0          2m
railyard-engine-backend-xxxxx           1/1     Running   0          1m
```

### Access the dashboard

```bash
kubectl port-forward svc/railyard-dashboard 8080:8080 -n railyard
```

Then open [http://localhost:8080](http://localhost:8080) in your browser.

## 3. Using Managed Databases

By default the chart deploys Dolt and pgvector as internal StatefulSets. For production workloads you may prefer managed database services.

### External MySQL / Dolt

Set `dolt.internal: false` and provide connection details:

```yaml
dolt:
  internal: false
  host: mysql.example.com
  port: 3306
  database: railyard
  username: railyard
  password: secret
```

If your managed database requires TLS:

```yaml
dolt:
  internal: false
  host: mysql.example.com
  port: 3306
  database: railyard
  username: railyard
  password: secret
  tls:
    enabled: true
    caSecret: dolt-ca-cert        # Secret containing ca.crt
    clientSecret: dolt-client-cert # Secret containing tls.crt + tls.key
```

### External PostgreSQL for pgvector

Set `pgvector.internal: false` and provide connection details:

```yaml
pgvector:
  internal: false
  host: pgvector.example.com
  port: 5432
  database: cocoindex
  username: cocoindex
  password: secret
```

Any PostgreSQL 15+ instance with the `pgvector` extension enabled will work (e.g. Amazon RDS, Cloud SQL, Azure Database for PostgreSQL).

## 4. Dashboard Access

### Port-forward (simplest)

No ingress or load balancer required:

```bash
kubectl port-forward svc/railyard-dashboard 8080:8080 -n railyard
```

### Ingress with host-based routing

Enable ingress in your values:

```yaml
dashboard:
  ingress:
    enabled: true
    className: nginx
    host: railyard.example.com
    annotations:
      cert-manager.io/cluster-issuer: letsencrypt-prod
    tls:
      - secretName: railyard-dashboard-tls
        hosts:
          - railyard.example.com
```

### Adding OAuth2 Proxy for authentication

The chart includes an optional OAuth2 Proxy sidecar. Enable it to require login before accessing the dashboard:

```yaml
dashboard:
  oauth2proxy:
    enabled: true
    provider: github
    clientID: "your-github-oauth-app-id"
    clientSecret: "your-github-oauth-secret"
    cookieSecret: "a-random-32-byte-base64-string"
    extraArgs:
      - --email-domain=yourcompany.com
```

Generate a cookie secret:

```bash
python3 -c 'import os,base64; print(base64.b64encode(os.urandom(32)).decode())'
```

For full details on authentication methods (API keys, OAuth tokens, cloud provider auth), see [k8s-authentication.md](k8s-authentication.md).

### GitHub PAT for PR mode

When `require_pr: true` is set in your Railyard config, the yardmaster and engines use the `gh` CLI to create PRs and detect review feedback. The `gh` CLI is pre-installed in the engine container image, but it needs a GitHub Personal Access Token (PAT) to authenticate.

1. Create a **fine-grained PAT** at [github.com/settings/tokens](https://github.com/settings/tokens?type=beta) with **repo** scope (or the `Contents` and `Pull requests` repository permissions for fine-grained tokens).

2. Set the token in your Helm values:

```yaml
auth:
  githubToken: "ghp_..."
```

Or pass it at install/upgrade time:

```bash
helm upgrade --install railyard ./charts/railyard \
  --namespace railyard \
  -f my-values.yaml \
  --set auth.githubToken="ghp_..."
```

The chart adds `GH_TOKEN` to the auth secret, which is mounted in engine, yardmaster, and dispatch pods via `envFrom`. See [k8s-authentication.md](k8s-authentication.md) for details.

## 5. Scaling

Each track has its own pool of engine pods. Scaling is controlled by the `tracks` configuration and Kubernetes HPA (Horizontal Pod Autoscaler).

### HPA configuration per track

Each track entry in `tracks` defines scaling bounds:

```yaml
tracks:
  - name: backend
    engineSlots: 5
    minReplicas: 1
    maxReplicas: 5
    language: go
    testCommand: "go test ./..."

  - name: frontend
    engineSlots: 3
    minReplicas: 1
    maxReplicas: 3
    language: typescript
    testCommand: "npm test"
```

- `engineSlots` -- the maximum number of concurrent cars the track can process.
- `minReplicas` -- the minimum number of engine pods kept warm (even when idle).
- `maxReplicas` -- the upper bound for HPA scale-out. HPA scales based on the number of ready cars waiting for engines.

### Adjusting replica counts

To change scaling on a running deployment, update your values and upgrade:

```bash
helm upgrade railyard ./charts/railyard \
  --namespace railyard \
  -f my-values.yaml
```

### Engine resource tuning

Engine pods run AI coding agents and may need significant memory. Set resource requests and limits based on your provider and workload:

```yaml
engine:
  resources:
    requests:
      cpu: "500m"
      memory: "1Gi"
    limits:
      cpu: "2"
      memory: "4Gi"
  nodeSelector:
    node-type: compute
  tolerations:
    - key: dedicated
      operator: Equal
      value: engines
      effect: NoSchedule
```

## 6. Resource Tuning

The Helm chart sets `resources: {}` for all components by default, which means no requests or limits. For anything beyond a quick test you should set explicit values to ensure stable scheduling and prevent resource contention.

### What drives resource needs

| Component | CPU driver | Memory driver |
|-----------|-----------|--------------|
| **Dolt** | Query load from dispatch/yardmaster | Working set size; grows slowly with metadata volume |
| **pgvector** | Vector similarity search (CocoIndex queries) | Index size; pgvector loads HNSW indexes into RAM |
| **Engine** | Git operations, test execution, agent tool calls | Cloned repo size, test suite memory, agent context window |
| **Dispatch** | Car assignment loop (lightweight) | Minimal -- in-memory state is small |
| **Yardmaster** | Engine lifecycle polling (lightweight) | Minimal |
| **Dashboard** | HTTP request serving | Minimal |
| **Telegraph** | Websocket connection handling | One goroutine per connection; minimal unless fan-out is large |

### Recommended resource settings

| Component | | Small (dev/test) | Medium (team) | Large (org-wide) |
|-----------|---|-----------------|---------------|-----------------|
| **Dolt** | requests | 250m / 256Mi | 500m / 512Mi | 1 CPU / 1Gi |
| | limits | 500m / 512Mi | 1 CPU / 1Gi | 2 CPU / 2Gi |
| **pgvector** | requests | 250m / 512Mi | 500m / 1Gi | 1 CPU / 2Gi |
| | limits | 500m / 1Gi | 1 CPU / 2Gi | 2 CPU / 4Gi |
| **Engine** | requests | 250m / 512Mi | 500m / 1Gi | 1 CPU / 2Gi |
| | limits | 1 CPU / 2Gi | 2 CPU / 4Gi | 4 CPU / 8Gi |
| **Dispatch** | requests | 50m / 64Mi | 100m / 128Mi | 250m / 256Mi |
| | limits | 200m / 128Mi | 500m / 256Mi | 1 CPU / 512Mi |
| **Yardmaster** | requests | 50m / 64Mi | 100m / 128Mi | 250m / 256Mi |
| | limits | 200m / 128Mi | 500m / 256Mi | 1 CPU / 512Mi |
| **Dashboard** | requests | 50m / 64Mi | 100m / 128Mi | 250m / 256Mi |
| | limits | 200m / 256Mi | 500m / 512Mi | 1 CPU / 512Mi |
| **Telegraph** | requests | 50m / 64Mi | 100m / 128Mi | 250m / 256Mi |
| | limits | 200m / 128Mi | 500m / 256Mi | 1 CPU / 512Mi |

**Tier guidance:**
- **Small** -- single developer or CI testing; 1-2 tracks, 1-3 engine slots.
- **Medium** -- team of 5-15; 2-5 tracks, 5-15 total engine slots.
- **Large** -- organization-wide; many tracks, 15+ engine slots, large repositories.

### Example values.yaml for a medium deployment

```yaml
dolt:
  resources:
    requests:
      cpu: "500m"
      memory: "512Mi"
    limits:
      cpu: "1"
      memory: "1Gi"

pgvector:
  resources:
    requests:
      cpu: "500m"
      memory: "1Gi"
    limits:
      cpu: "1"
      memory: "2Gi"

engine:
  resources:
    requests:
      cpu: "500m"
      memory: "1Gi"
    limits:
      cpu: "2"
      memory: "4Gi"

dispatch:
  resources:
    requests:
      cpu: "100m"
      memory: "128Mi"
    limits:
      cpu: "500m"
      memory: "256Mi"

yardmaster:
  resources:
    requests:
      cpu: "100m"
      memory: "128Mi"
    limits:
      cpu: "500m"
      memory: "256Mi"

dashboard:
  resources:
    requests:
      cpu: "100m"
      memory: "128Mi"
    limits:
      cpu: "500m"
      memory: "512Mi"

telegraph:
  resources:
    requests:
      cpu: "100m"
      memory: "128Mi"
    limits:
      cpu: "500m"
      memory: "256Mi"
```

> **Tip:** Start with medium values and adjust based on `kubectl top pods -n railyard`. If pgvector OOMKills under load, increase its memory limit first -- vector index size is the most common surprise.

## 7. Storage and PVC Sizing

The internal Dolt and pgvector StatefulSets each create a PersistentVolumeClaim (PVC). This section covers sizing, storage class selection, and how to resize PVCs after initial deployment.

### Sizing guidelines

| Component | What it stores | Default size | Sizing rule of thumb |
|-----------|---------------|-------------|---------------------|
| **Dolt** | Car/engine/track metadata + git-like version history | `10Gi` | Grows slowly. 10Gi covers most single-project deployments. Large orgs with many tracks and high car throughput may need 20-50Gi. |
| **pgvector** | CocoIndex vector embeddings of indexed source code | `10Gi` | Scales with codebase size. Estimate ~1GB per 100K lines of indexed code. A monorepo with 500K lines should start at 10Gi; 1M+ lines should use 20Gi or more. |

Configure sizes in your values file:

```yaml
dolt:
  storage:
    size: 10Gi
    storageClass: ""   # empty string uses the cluster default

pgvector:
  storage:
    size: 10Gi
    storageClass: ""
```

### Storage class recommendations

| Environment | Recommended `storageClass` | Notes |
|------------|---------------------------|-------|
| Minikube | `standard` (default) | Uses hostPath; fine for local testing |
| AWS EKS | `gp3` | Good balance of cost and performance; default `gp2` also works |
| GCP GKE | `premium-rwo` (SSD) or `standard-rwo` (HDD) | Use SSD for pgvector if query latency matters |
| Azure AKS | `managed-premium` (SSD) | Falls back to `managed` for standard HDD |

### Resizing PVCs

PVC expansion requires a storage class that supports `allowVolumeExpansion: true` (most cloud providers do by default).

**Step 1** -- Update your values file with the new size and upgrade:

```bash
helm upgrade railyard ./charts/railyard \
  --namespace railyard \
  -f my-values.yaml
```

**Step 2** -- If the StatefulSet PVC is not updated automatically, patch it directly:

```bash
# Resize Dolt PVC
kubectl patch pvc data-railyard-dolt-0 -n railyard \
  -p '{"spec":{"resources":{"requests":{"storage":"20Gi"}}}}'

# Resize pgvector PVC
kubectl patch pvc data-railyard-pgvector-0 -n railyard \
  -p '{"spec":{"resources":{"requests":{"storage":"20Gi"}}}}'
```

The underlying volume expands online for most cloud storage classes. No pod restart is needed.

### Data persistence warning

> **Warning:** Deleting a PVC permanently destroys all data on the underlying volume. `helm uninstall` does **not** delete PVCs (this is intentional), but running `kubectl delete pvc` will. If you are using internal databases, back up Dolt and pgvector data before removing PVCs. There is no recovery path once the volume is deleted.

## 8. Telegraph (Chat Bridge)

Telegraph bridges railyard events (car status changes, build failures, merge completions) to Slack or Discord. It runs as an optional deployment managed by the Helm chart. For local setup details see [telegraph-setup.md](telegraph-setup.md); this section covers Kubernetes-specific configuration.

Enable Telegraph by setting `telegraph.enabled: true` in your values file.

### Slack configuration

```yaml
telegraph:
  enabled: true
  replicas: 1
  platform: slack
  channel: "#railyard-notifications"
  slack:
    botToken: "xoxb-..."
    appToken: "xapp-..."
  resources:
    requests:
      cpu: "100m"
      memory: "128Mi"
    limits:
      cpu: "250m"
      memory: "256Mi"
```

### Discord configuration

```yaml
telegraph:
  enabled: true
  replicas: 1
  platform: discord
  discord:
    botToken: "MTI..."
    guildID: "123456789012345678"
    channelID: "987654321098765432"
  resources:
    requests:
      cpu: "100m"
      memory: "128Mi"
    limits:
      cpu: "250m"
      memory: "256Mi"
```

### Using an existing Secret for tokens

Storing bot tokens directly in values files is fine for development but should be avoided in production. Create a Secret containing your tokens and reference it with `existingSecret`:

```bash
kubectl create secret generic telegraph-tokens \
  --from-literal=slack-bot-token="xoxb-..." \
  --from-literal=slack-app-token="xapp-..." \
  -n railyard
```

Then in your values file:

```yaml
telegraph:
  enabled: true
  platform: slack
  channel: "#railyard-notifications"
  existingSecret: telegraph-tokens
```

The chart expects the Secret keys to match the platform (`slack-bot-token` / `slack-app-token` for Slack, `discord-bot-token` for Discord).

## 9. Service Networking and DNS

All Railyard components deploy into a single namespace (e.g. `railyard` or `railyard-{project}`). Pods discover each other through Kubernetes DNS using the pattern `<service>.<namespace>.svc.cluster.local`. In practice you rarely need the full form -- the Helm-generated `railyard.yaml` configmap already uses the short service names (e.g. `railyard-dolt`), which resolve within the same namespace automatically.

### Internal services

| Service | Port | Type | Description |
|---------|------|------|-------------|
| `railyard-dolt` | 3306 | Headless ClusterIP | Dolt MySQL-compatible metadata database |
| `railyard-pgvector` | 5432 | Headless ClusterIP | pgvector PostgreSQL for CocoIndex vector storage |
| `railyard-dashboard` | 8080 | ClusterIP | Dashboard web UI |

Dispatch, yardmaster, and engine pods all read connection strings from the `railyard-config` ConfigMap. The Helm helpers (`railyard.doltHost`, `railyard.pgvectorHost`) resolve to the internal service names when `dolt.internal` and `pgvector.internal` are `true`.

### External database mode

When you set `dolt.internal: false` or `pgvector.internal: false`, the chart skips creating the corresponding Service and StatefulSet. The ConfigMap is populated with the external host you provide in values (see section 3). Internal services for the other components are unaffected.

### Network policies

The chart includes optional NetworkPolicy resources that restrict inter-pod traffic to only required communication paths. Enable them by setting `networkPolicy.enabled: true` in your values:

```yaml
networkPolicy:
  enabled: true
  dashboard:
    ingressCIDR:
      - "10.0.0.0/8"  # restrict dashboard access to internal network
```

When enabled, the following traffic paths are permitted:

| Source | Destination | Port | Purpose |
|--------|-------------|------|---------|
| Engine, dispatch, yardmaster, dashboard, bull | Dolt | 3306 | Database access |
| Engine | pgvector | 5432 | Vector store access |
| Engine | External (0.0.0.0/0) | 443 | AI provider API calls |
| Bull | External (0.0.0.0/0) | 443 | GitHub API calls |
| Telegraph | External (0.0.0.0/0) | 443 | Slack/Discord API calls |
| Configured CIDR / namespace | Dashboard | 8080 | User access |
| All pods | kube-dns (kube-system) | 53 | DNS resolution |

All other intra-namespace and cross-namespace traffic is blocked. If `networkPolicy.dashboard.ingressCIDR` is empty, dashboard access is allowed from pods within the same namespace only.

## 10. Monitoring and Logs

### Check pod status

```bash
kubectl get pods -n railyard
```

For more detail on a specific pod:

```bash
kubectl describe pod <pod-name> -n railyard
```

### View component logs

```bash
# Dispatch logs
kubectl logs -l app.kubernetes.io/component=dispatch -n railyard --tail=100 -f

# Yardmaster logs
kubectl logs -l app.kubernetes.io/component=yardmaster -n railyard --tail=100 -f

# Engine logs (all engines)
kubectl logs -l app.kubernetes.io/component=engine -n railyard --tail=100 -f

# Engine logs (specific track)
kubectl logs -l app.kubernetes.io/component=engine,railyard.dev/track=backend -n railyard --tail=100 -f

# Dolt logs
kubectl logs railyard-dolt-0 -n railyard --tail=100 -f
```

### Dashboard for real-time status

The dashboard provides a live view of:

- Active engines and their current assignments
- Car status across all tracks
- Track health and throughput

Access it via port-forward or ingress as described in section 4.

## 11. Troubleshooting

### Pods in CrashLoopBackOff

**Symptom:** Engine or dispatch pods restart repeatedly.

**Common causes:**
- Invalid or missing auth credentials. Verify your `auth.method` and credentials match your provider. See [k8s-authentication.md](k8s-authentication.md).
- Database connection failure. Check that Dolt and pgvector pods are running and ready.

**Diagnosis:**

```bash
kubectl logs <crashing-pod> -n railyard --previous
kubectl describe pod <crashing-pod> -n railyard
```

### Engine pods not starting

**Symptom:** Engine pods stay in `Pending` or `Init` state.

**Common causes:**
- Git repository access denied. Ensure deploy keys or credentials are configured correctly.
- Insufficient cluster resources. Check node capacity with `kubectl describe nodes`.
- Image pull failures. Verify `image.repository` and `imagePullSecret` values.

**Diagnosis:**

```bash
kubectl describe pod <engine-pod> -n railyard
kubectl get events -n railyard --sort-by='.lastTimestamp'
```

### Dashboard not accessible

**Symptom:** `kubectl port-forward` hangs or the dashboard returns errors.

**Check list:**
1. Verify the dashboard pod is running: `kubectl get pods -l app.kubernetes.io/component=dashboard -n railyard`
2. Verify the service exists: `kubectl get svc railyard-dashboard -n railyard`
3. If using ingress, check ingress status: `kubectl describe ingress -n railyard`
4. If using OAuth2 Proxy, check the sidecar logs: `kubectl logs <dashboard-pod> -c oauth2-proxy -n railyard`

### Dolt connection refused

**Symptom:** Components report "connection refused" or "dial tcp ... connect: connection refused" for the Dolt database.

**Check list:**
1. Verify the Dolt StatefulSet is ready: `kubectl get statefulset railyard-dolt -n railyard`
2. Check Dolt pod logs: `kubectl logs railyard-dolt-0 -n railyard`
3. Verify the PVC is bound: `kubectl get pvc -n railyard`
4. If using external Dolt/MySQL, verify network connectivity from inside the cluster:

```bash
kubectl run -it --rm debug --image=busybox -n railyard -- nc -zv mysql.example.com 3306
```

### pgvector connection issues

**Symptom:** CocoIndex-related errors or vector search failures.

**Check list:**
1. Verify the pgvector StatefulSet: `kubectl get statefulset railyard-pgvector -n railyard`
2. Check pgvector pod logs: `kubectl logs railyard-pgvector-0 -n railyard`
3. Confirm the `pgvector` extension is installed (relevant for external databases):

```bash
kubectl run -it --rm debug --image=postgres:16 -n railyard -- \
  psql "host=<host> dbname=cocoindex user=cocoindex" -c "SELECT extname FROM pg_extension;"
```

## Upgrading

To upgrade to a newer version of Railyard:

```bash
helm upgrade railyard ./charts/railyard \
  --namespace railyard \
  -f my-values.yaml
```

Engine pods perform a rolling restart. In-progress cars are finished before the old pod terminates (engines handle SIGTERM gracefully).

## Uninstalling

```bash
helm uninstall railyard --namespace railyard
```

Note: PersistentVolumeClaims for Dolt and pgvector are **not** deleted automatically. To remove all data:

```bash
kubectl delete pvc -l app.kubernetes.io/instance=railyard -n railyard
```
