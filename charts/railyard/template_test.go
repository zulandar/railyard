package railyard_test

import (
	"os/exec"
	"strings"
	"testing"
)

// helmTemplate renders the chart with the given values file and returns the
// configmap YAML. Skips the test if helm is not installed.
func helmTemplate(t *testing.T, valuesFile string) string {
	t.Helper()
	return helmTemplateShow(t, valuesFile, "templates/configmap.yaml")
}

// helmTemplateShow renders the chart with the given values file and returns
// the YAML for the specified template. Skips if helm is not installed.
func helmTemplateShow(t *testing.T, valuesFile, showOnly string) string {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not found, skipping chart template test")
	}
	cmd := exec.Command("helm", "template", "test-release", ".",
		"-f", valuesFile,
		"--show-only", showOnly,
	)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	return string(out)
}

func TestConfigmap_TelegraphAllowedChannels_Rendered(t *testing.T) {
	out := helmTemplate(t, "ci/test-values-telegraph-allowed-channels.yaml")

	if !strings.Contains(out, "allowed_channels:") {
		t.Fatal("expected allowed_channels in configmap output")
	}
	if !strings.Contains(out, `- "1487921215430594571"`) {
		t.Error("expected first allowed channel ID in output")
	}
	if !strings.Contains(out, `- "9999999999999999999"`) {
		t.Error("expected second allowed channel ID in output")
	}
}

func TestConfigmap_TelegraphAllowedChannels_OmittedWhenEmpty(t *testing.T) {
	out := helmTemplate(t, "ci/test-values-full.yaml")

	if strings.Contains(out, "allowed_channels:") {
		t.Error("allowed_channels should not appear when allowedChannels is empty")
	}
}

func TestConfigmap_TelegraphAllowedChannels_OmittedWhenNotSet(t *testing.T) {
	out := helmTemplate(t, "ci/test-values-minimal.yaml")

	// Minimal values may not enable telegraph at all.
	if strings.Contains(out, "allowed_channels:") {
		t.Error("allowed_channels should not appear when not configured")
	}
}

func TestEngineDeployment_LogLevel_Set(t *testing.T) {
	out := helmTemplateShow(t, "ci/test-values-loglevel.yaml", "templates/engine-deployment.yaml")

	if !strings.Contains(out, "LOG_LEVEL") {
		t.Fatal("expected LOG_LEVEL env var in engine deployment when logLevel is set")
	}
	if !strings.Contains(out, `"debug"`) {
		t.Error("expected LOG_LEVEL value to be 'debug'")
	}
}

func TestYardmasterDeployment_LogLevel_Set(t *testing.T) {
	out := helmTemplateShow(t, "ci/test-values-loglevel.yaml", "templates/yardmaster-deployment.yaml")

	if !strings.Contains(out, "LOG_LEVEL") {
		t.Fatal("expected LOG_LEVEL env var in yardmaster deployment when logLevel is set")
	}
	if !strings.Contains(out, `"debug"`) {
		t.Error("expected LOG_LEVEL value to be 'debug'")
	}
}

func TestEngineDeployment_LogLevel_OmittedWhenEmpty(t *testing.T) {
	out := helmTemplateShow(t, "ci/test-values-full.yaml", "templates/engine-deployment.yaml")

	if strings.Contains(out, "LOG_LEVEL") {
		t.Error("LOG_LEVEL should not appear when logLevel is not set")
	}
}

func TestYardmasterDeployment_LogLevel_OmittedWhenEmpty(t *testing.T) {
	out := helmTemplateShow(t, "ci/test-values-full.yaml", "templates/yardmaster-deployment.yaml")

	if strings.Contains(out, "LOG_LEVEL") {
		t.Error("LOG_LEVEL should not appear when logLevel is not set")
	}
}
