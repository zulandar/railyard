package bull

import (
	"context"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

func validBullConfig() *config.Config {
	return &config.Config{
		Owner:        "org",
		Repo:         "myapp",
		BranchPrefix: "ry/test",
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go"},
		},
		Bull: config.BullConfig{
			Enabled:     true,
			GitHubToken: "ghp_test_token",
		},
	}
}

func TestStart_NilConfig(t *testing.T) {
	err := Start(context.Background(), StartOpts{Config: nil})
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "bull: config is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bull: config is required")
	}
}

func TestStart_BullNotEnabled(t *testing.T) {
	cfg := validBullConfig()
	cfg.Bull.Enabled = false
	err := Start(context.Background(), StartOpts{Config: cfg})
	if err == nil {
		t.Fatal("expected error when bull is not enabled")
	}
	if !strings.Contains(err.Error(), "bull: bull.enabled is not true") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bull: bull.enabled is not true")
	}
}

func TestStart_MissingGitHubToken(t *testing.T) {
	cfg := validBullConfig()
	cfg.Bull.GitHubToken = ""
	err := Start(context.Background(), StartOpts{Config: cfg})
	if err == nil {
		t.Fatal("expected error for missing github token")
	}
	if !strings.Contains(err.Error(), "bull: bull.github_token is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bull: bull.github_token is required")
	}
}

func TestStart_NoTracks(t *testing.T) {
	cfg := validBullConfig()
	cfg.Tracks = nil
	err := Start(context.Background(), StartOpts{Config: cfg})
	if err == nil {
		t.Fatal("expected error for no tracks")
	}
	if !strings.Contains(err.Error(), "bull: at least one track must be configured") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bull: at least one track must be configured")
	}
}

func TestStart_NilDB(t *testing.T) {
	cfg := validBullConfig()
	err := Start(context.Background(), StartOpts{Config: cfg, DB: nil})
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
	if !strings.Contains(err.Error(), "bull: database connection is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bull: database connection is required")
	}
}

func TestStartOpts_ZeroValue(t *testing.T) {
	opts := StartOpts{}
	if opts.ConfigPath != "" || opts.Config != nil || opts.DB != nil {
		t.Error("zero-value StartOpts should have empty fields")
	}
}
