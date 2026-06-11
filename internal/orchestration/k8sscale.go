package orchestration

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zulandar/railyard/internal/config"
)

// K8sScaler abstracts the Kubernetes apps/v1 scale operations that scale_track
// drives in cluster mode. It is deliberately tiny so callers can wire a real
// client-go-backed implementation while tests (and the OSS default build) can
// supply a fake or leave it nil.
//
// Keeping this an interface — rather than reaching for the k8s clientset
// directly inside orchestration — means the package carries no client-go
// dependency. The concrete scaler lives wherever a kube client is already
// constructed (the in-cluster daemon), and is injected at the call site.
type K8sScaler interface {
	// ScaleDeployment sets the replica count of the named Deployment in the
	// given namespace. Implementations should be idempotent: scaling to the
	// current replica count is a successful no-op.
	ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) error
}

// EngineDeploymentName returns the Helm-convention Deployment name for a
// track's engine workload. The chart's engine-deployment.yaml names each
// per-track Deployment "<release>-engine-<track>" (see
// charts/railyard/templates/engine-deployment.yaml), so the running daemon can
// derive the target without listing resources.
func EngineDeploymentName(releaseName, track string) string {
	return fmt.Sprintf("%s-engine-%s", releaseName, track)
}

// K8sScaleOpts configures ScaleK8sReplicas.
type K8sScaleOpts struct {
	// Config is the loaded railyard config. K8s mode is detected from
	// Config.Kubernetes.Namespace being non-empty.
	Config *config.Config
	// Scaler performs the actual apps/v1 scale calls. May be nil in builds
	// (e.g. OSS) that ship no in-cluster kube client; the operation then
	// degrades to a logged no-op.
	Scaler K8sScaler
	// ReleaseName is the Helm release name used to derive workload names. When
	// empty it falls back to the Kubernetes namespace, matching the common
	// "release name == namespace" deployment convention.
	ReleaseName string
	// Track is the track whose engine Deployment is scaled.
	Track string
	// Count is the desired engine replica count for the track.
	Count int
	// Logger receives the no-op / scaled log line. Defaults to slog.Default().
	Logger *slog.Logger
}

// ScaleK8sReplicas manages Kubernetes pod replicas for a track's engine
// workload. It is the cluster-mode counterpart to the tmux-session scaling
// performed by Scale.
//
// Mode detection. When Config.Kubernetes.Namespace is empty the daemon is
// running in local (tmux) mode; this function is a no-op that emits a single
// clear log line and returns nil — engine processes there are tmux sessions,
// not pods, and are scaled by Scale. When a namespace is present but no Scaler
// is wired (the OSS default), the call likewise degrades to a logged no-op
// rather than panicking, so plugins that dispatch scale_track behave
// predictably on every build.
//
// In cluster mode with a Scaler wired, it scales the track's engine Deployment
// ("<release>-engine-<track>") to Count replicas. The per-track engine workload
// is a Deployment in the chart; the only StatefulSets are the mysql/pgvector
// data stores, which are never scaled by a track-level command — so managing
// the engine Deployment replicas is the correct k8s-side behavior here.
func ScaleK8sReplicas(ctx context.Context, opts K8sScaleOpts) error {
	if opts.Config == nil {
		return fmt.Errorf("orchestration: config is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	namespace := opts.Config.Kubernetes.Namespace
	if namespace == "" {
		// Local (tmux) mode: pod scaling does not apply.
		logger.Info("scale_track: local mode, skipping k8s replica management",
			"track", opts.Track, "count", opts.Count)
		return nil
	}

	if opts.Track == "" {
		return fmt.Errorf("orchestration: track is required")
	}
	if opts.Count < 0 {
		return fmt.Errorf("orchestration: count must be non-negative")
	}

	if opts.Scaler == nil {
		// K8s mode but no kube client wired (e.g. OSS build): degrade to a
		// logged no-op rather than panicking.
		logger.Warn("scale_track: kubernetes mode but no scaler wired, skipping replica management",
			"namespace", namespace, "track", opts.Track, "count", opts.Count)
		return nil
	}

	releaseName := opts.ReleaseName
	if releaseName == "" {
		releaseName = namespace
	}
	deployment := EngineDeploymentName(releaseName, opts.Track)

	if err := opts.Scaler.ScaleDeployment(ctx, namespace, deployment, int32(opts.Count)); err != nil {
		return fmt.Errorf("orchestration: scale engine deployment %s/%s: %w", namespace, deployment, err)
	}

	logger.Info("scale_track: scaled engine deployment replicas",
		"namespace", namespace, "deployment", deployment, "replicas", opts.Count)
	return nil
}
