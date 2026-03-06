# Kubernetes Deployment Guide

This guide walks through deploying Railyard on Kubernetes using the official Helm chart. Railyard deploys five core components -- Dolt (metadata database), pgvector (vector store for CocoIndex), dispatch, yardmaster, dashboard -- plus a pool of engine pods per track.

## 1. Prerequisites

Before you begin, ensure the following are available:

- **Kubernetes 1.26+** cluster (EKS, GKE, AKS, or self-managed). For local testing with minikube, see [k8s-local-testing.md](k8s-local-testing.md).
- **kubectl** configured and pointed at your cluster (`kubectl cluster-info` should succeed)
- **Helm 3.x** installed ([helm.sh/docs/intro/install](https://helm.sh/docs/intro/install/))
- **Agent credentials** for your chosen AI provider (see [k8s-authentication.md](k8s-authentication.md))

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

## 6. Monitoring and Logs

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

## 7. Troubleshooting

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
