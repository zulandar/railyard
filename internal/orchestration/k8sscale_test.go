package orchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

// fakeK8sScaler records the scale calls it receives so tests can assert the
// adapter computed the right workload names and replica counts. It mirrors the
// mockTmux style used elsewhere in this package.
type fakeK8sScaler struct {
	deploymentCalls []scaleCall
	deploymentErr   error
}

type scaleCall struct {
	namespace string
	name      string
	replicas  int32
}

func (f *fakeK8sScaler) ScaleDeployment(_ context.Context, namespace, name string, replicas int32) error {
	f.deploymentCalls = append(f.deploymentCalls, scaleCall{namespace, name, replicas})
	return f.deploymentErr
}

// TestEngineDeploymentName builds the Helm-convention Deployment name for a
// track's engine workload: "<release>-engine-<track>".
func TestEngineDeploymentName(t *testing.T) {
	got := EngineDeploymentName("railyard-myapp", "backend")
	if got != "railyard-myapp-engine-backend" {
		t.Errorf("EngineDeploymentName = %q, want railyard-myapp-engine-backend", got)
	}
}

// TestScaleK8sReplicas_K8sMode exercises the k8s code path: when the config
// carries a Kubernetes namespace and a scaler is wired, the engine Deployment
// for the named track is scaled to the requested replica count.
func TestScaleK8sReplicas_K8sMode(t *testing.T) {
	scaler := &fakeK8sScaler{}
	cfg := &config.Config{
		Owner: "test",
		Kubernetes: config.KubernetesConfig{
			Namespace: "railyard-myapp",
		},
	}
	err := ScaleK8sReplicas(context.Background(), K8sScaleOpts{
		Config:      cfg,
		Scaler:      scaler,
		ReleaseName: "railyard-myapp",
		Track:       "backend",
		Count:       3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scaler.deploymentCalls) != 1 {
		t.Fatalf("deployment scale calls = %d, want 1", len(scaler.deploymentCalls))
	}
	call := scaler.deploymentCalls[0]
	if call.namespace != "railyard-myapp" {
		t.Errorf("namespace = %q, want railyard-myapp", call.namespace)
	}
	if call.name != "railyard-myapp-engine-backend" {
		t.Errorf("deployment = %q, want railyard-myapp-engine-backend", call.name)
	}
	if call.replicas != 3 {
		t.Errorf("replicas = %d, want 3", call.replicas)
	}
}

// TestScaleK8sReplicas_LocalNoOp exercises the local code path: when the
// config has no Kubernetes namespace, the function must be a no-op (no scaler
// calls) and return nil — even when a scaler happens to be wired.
func TestScaleK8sReplicas_LocalNoOp(t *testing.T) {
	scaler := &fakeK8sScaler{}
	cfg := &config.Config{Owner: "test"} // no Kubernetes section
	err := ScaleK8sReplicas(context.Background(), K8sScaleOpts{
		Config:      cfg,
		Scaler:      scaler,
		ReleaseName: "railyard-myapp",
		Track:       "backend",
		Count:       3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scaler.deploymentCalls) != 0 {
		t.Fatalf("deployment scale calls = %d, want 0 in local mode", len(scaler.deploymentCalls))
	}
}

// TestScaleK8sReplicas_NilScalerNoOp verifies that k8s mode with no scaler
// wired (the OSS/default build) is a no-op rather than a nil-pointer panic.
func TestScaleK8sReplicas_NilScalerNoOp(t *testing.T) {
	cfg := &config.Config{
		Owner:      "test",
		Kubernetes: config.KubernetesConfig{Namespace: "railyard-myapp"},
	}
	err := ScaleK8sReplicas(context.Background(), K8sScaleOpts{
		Config:      cfg,
		Scaler:      nil,
		ReleaseName: "railyard-myapp",
		Track:       "backend",
		Count:       3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestScaleK8sReplicas_DeploymentErrorPropagates ensures a scaler failure is
// wrapped and surfaced rather than swallowed.
func TestScaleK8sReplicas_DeploymentErrorPropagates(t *testing.T) {
	scaler := &fakeK8sScaler{deploymentErr: errors.New("boom")}
	cfg := &config.Config{
		Owner:      "test",
		Kubernetes: config.KubernetesConfig{Namespace: "railyard-myapp"},
	}
	err := ScaleK8sReplicas(context.Background(), K8sScaleOpts{
		Config:      cfg,
		Scaler:      scaler,
		ReleaseName: "railyard-myapp",
		Track:       "backend",
		Count:       2,
	})
	if err == nil {
		t.Fatal("expected error from scaler to propagate")
	}
}

// TestScaleK8sReplicas_Validation rejects empty track / nil config.
func TestScaleK8sReplicas_Validation(t *testing.T) {
	if err := ScaleK8sReplicas(context.Background(), K8sScaleOpts{}); err == nil {
		t.Error("expected error for nil config")
	}
	cfg := &config.Config{Kubernetes: config.KubernetesConfig{Namespace: "ns"}}
	if err := ScaleK8sReplicas(context.Background(), K8sScaleOpts{Config: cfg, Scaler: &fakeK8sScaler{}, ReleaseName: "r"}); err == nil {
		t.Error("expected error for empty track")
	}
}
