# Local Kubernetes Testing with Minikube

This guide walks through running Railyard on minikube for local development and testing. For production deployment, see [k8s-deployment.md](k8s-deployment.md).

## Prerequisites

Install the following before you begin:

- **minikube** — [minikube.sigs.k8s.io/docs/start](https://minikube.sigs.k8s.io/docs/start/)
- **kubectl** — [kubernetes.io/docs/tasks/tools](https://kubernetes.io/docs/tasks/tools/)
- **Helm 3.x** — [helm.sh/docs/intro/install](https://helm.sh/docs/intro/install/)
- **Docker** — minikube's default driver ([docs.docker.com/get-docker](https://docs.docker.com/get-docker/))
- **Go 1.23+** — needed to build the `ry` binary

You also need an Anthropic API key (or another supported provider — see [k8s-authentication.md](k8s-authentication.md)).

## 1. Start minikube

```bash
minikube start --cpus=4 --memory=8192
```

Enable the metrics-server addon so that HPA can scale engine pods:

```bash
minikube addons enable metrics-server
```

Verify the cluster is ready:

```bash
kubectl cluster-info
```

## 2. Build the image locally

Building directly into minikube's Docker daemon avoids pushing to a remote registry.

First, build the `ry` binary:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ry ./cmd/ry
```

Then build the container image inside minikube:

```bash
minikube image build -t railyard:local -f Dockerfile.engine .
```

Clean up the local binary:

```bash
rm ry
```

## 3. Create a values file

Create `minikube-values.yaml` at the repo root:

```yaml
project: railyard

git:
  owner: zulandar
  repo: https://github.com/zulandar/railyard.git
  defaultBranch: main

image:
  repository: railyard
  tag: local
  pullPolicy: Never  # use the locally built image

auth:
  method: api_key
  apiKey: "YOUR_ANTHROPIC_API_KEY"

engine:
  agentProvider: claude

tracks:
  - name: backend
    engineSlots: 2
    minReplicas: 1
    maxReplicas: 2
    language: go
    testCommand: "go test ./..."
```

Replace `YOUR_ANTHROPIC_API_KEY` with your actual key.

> **Tip:** Keep resource requests unset (the default). Minikube nodes are constrained and empty `resources: {}` lets pods schedule without competing for guarantees.

## 4. Install the chart

```bash
helm install railyard ./charts/railyard \
  --namespace railyard --create-namespace \
  -f minikube-values.yaml
```

## 5. Verify the deployment

Watch pods come up:

```bash
kubectl get pods -n railyard -w
```

You should see something like:

```
NAME                                    READY   STATUS    RESTARTS   AGE
railyard-mysql-0                         1/1     Running   0          90s
railyard-pgvector-0                     1/1     Running   0          90s
railyard-dispatch-6b8f9c7d4-xxxxx       1/1     Running   0          90s
railyard-yardmaster-5c4d8e9f1-xxxxx     1/1     Running   0          90s
railyard-dashboard-7a2b3c4d5-xxxxx      1/1     Running   0          90s
railyard-engine-backend-xxxxx           1/1     Running   0          60s
```

If any pod stays in `Pending` or `CrashLoopBackOff`, jump to [Troubleshooting](#troubleshooting).

## 6. Access the dashboard

```bash
kubectl port-forward svc/railyard-dashboard 8080:8080 -n railyard
```

Open [http://localhost:8080](http://localhost:8080) in your browser.

## 7. Test it out

With the dashboard open, you can submit work through railyard's normal workflow. Use `ry` locally (pointed at the same MySQL instance via port-forward) or create cars directly through the dashboard.

To verify engines are processing, check engine logs:

```bash
kubectl logs -l app.kubernetes.io/component=engine -n railyard --tail=50 -f
```

## 8. Iterate on changes

After making code changes to railyard, rebuild and redeploy:

```bash
# Rebuild the binary and image
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ry ./cmd/ry
minikube image build -t railyard:local -f Dockerfile.engine .
rm ry

# Restart pods to pick up the new image
kubectl rollout restart deployment -n railyard
kubectl rollout restart statefulset -n railyard
```

## 9. Teardown

Uninstall the chart and delete PVCs:

```bash
helm uninstall railyard --namespace railyard
kubectl delete pvc -l app.kubernetes.io/instance=railyard -n railyard
```

Stop or delete the minikube cluster:

```bash
# Stop (preserves state, restartable with minikube start)
minikube stop

# Or delete entirely (removes all data)
minikube delete
```

## Troubleshooting

### Pods stuck in Pending

**Cause:** Insufficient CPU or memory on the minikube node.

**Fix:** Start minikube with more resources, or reduce `maxReplicas` in your values:

```bash
minikube delete
minikube start --cpus=6 --memory=12288
```

Check what's blocking scheduling:

```bash
kubectl describe pod <pod-name> -n railyard
```

### ErrImageNeverPull or image not found

**Cause:** The image was built outside minikube's Docker daemon, or the tag doesn't match.

**Fix:** Make sure you used `minikube image build` (not plain `docker build`) and that your values have `pullPolicy: Never` with the correct tag:

```bash
# Verify the image exists in minikube
minikube image list | grep railyard
```

### HPA not scaling engines

**Cause:** metrics-server is not enabled or hasn't collected data yet.

**Fix:**

```bash
minikube addons enable metrics-server

# Wait ~60 seconds for metrics collection, then check
kubectl get hpa -n railyard
```

The `TARGETS` column should show actual CPU values instead of `<unknown>`.

### MySQL or pgvector CrashLoopBackOff

**Cause:** Usually a storage issue on the minikube node.

**Fix:** Check the pod logs and events:

```bash
kubectl logs railyard-mysql-0 -n railyard
kubectl describe pod railyard-mysql-0 -n railyard
kubectl get pvc -n railyard
```

Ensure PVCs are in `Bound` state. If not, the default storage class may not be provisioning correctly:

```bash
kubectl get storageclass
```

### Dashboard port-forward not working

**Fix:** Verify the pod and service are running:

```bash
kubectl get pods -l app.kubernetes.io/component=dashboard -n railyard
kubectl get svc railyard-dashboard -n railyard
```

If the pod is running but port-forward fails, try restarting minikube's tunnel:

```bash
minikube tunnel
```
