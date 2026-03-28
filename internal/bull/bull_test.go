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

// Fix #6: Start() no longer has its own auth guard — config validation
// handles it. With valid token + nil DB, Start should fail on DB, not auth.
func TestStart_MissingGitHubToken_PassesThroughToDBCheck(t *testing.T) {
	cfg := validBullConfig()
	cfg.Bull.GitHubToken = ""
	cfg.Bull.AppID = 12345 // App auth present, so no auth error
	cfg.Bull.PrivateKeyPath = "/tmp/key.pem"
	cfg.Bull.InstallationID = 67890
	err := Start(context.Background(), StartOpts{Config: cfg})
	if err == nil {
		t.Fatal("expected error")
	}
	// Should fail on DB requirement, not auth, proving the stale guard is gone.
	if !strings.Contains(err.Error(), "bull: database connection is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bull: database connection is required")
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

// Fix #1: Conventions map values should be included, not just keys.
func TestStart_ConventionsIncludeValues(t *testing.T) {
	cfg := validBullConfig()
	cfg.Tracks = []config.TrackConfig{
		{
			Name:     "backend",
			Language: "go",
			Conventions: map[string]interface{}{
				"naming": "snake_case",
				"errors": "wrap with fmt.Errorf",
			},
		},
	}

	// Start() will fail later (no DB), but we can inspect the track building
	// by calling the helper directly. We test the logic in buildTrackInfos.
	tracks := buildTrackInfos(cfg.Tracks)
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	// Conventions should be "key: value" formatted, sorted by key.
	expected := []string{"errors: wrap with fmt.Errorf", "naming: snake_case"}
	if len(tracks[0].Conventions) != len(expected) {
		t.Fatalf("expected %d conventions, got %d: %v", len(expected), len(tracks[0].Conventions), tracks[0].Conventions)
	}
	for i, want := range expected {
		if tracks[0].Conventions[i] != want {
			t.Errorf("conventions[%d] = %q, want %q", i, tracks[0].Conventions[i], want)
		}
	}
}

func TestStartOpts_ZeroValue(t *testing.T) {
	opts := StartOpts{}
	if opts.ConfigPath != "" || opts.Config != nil || opts.DB != nil {
		t.Error("zero-value StartOpts should have empty fields")
	}
}
