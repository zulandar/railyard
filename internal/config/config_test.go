package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fullYAML = `
owner: alice
repo: git@github.com:org/myapp.git
branch_prefix: ry/alice

dolt:
  host: 10.0.0.5
  port: 3307
  database: railyard_alice

tracks:
  - name: backend
    language: go
    file_patterns: ["cmd/**", "internal/**", "pkg/**", "*.go"]
    engine_slots: 5
    conventions:
      go_version: "1.22"
      style: "stdlib-first, no frameworks"

  - name: frontend
    language: typescript
    file_patterns: ["src/**", "*.ts", "*.tsx"]
    engine_slots: 3
    conventions:
      framework: "Next.js 15"
`

const minimalYAML = `
owner: bob
repo: git@github.com:org/app.git
tracks:
  - name: infra
    language: mixed
`

func TestParse_FullConfig(t *testing.T) {
	cfg, err := Parse([]byte(fullYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Owner != "alice" {
		t.Errorf("Owner = %q, want %q", cfg.Owner, "alice")
	}
	if cfg.Repo != "git@github.com:org/myapp.git" {
		t.Errorf("Repo = %q, want git@github.com:org/myapp.git", cfg.Repo)
	}
	if cfg.BranchPrefix != "ry/alice" {
		t.Errorf("BranchPrefix = %q, want %q", cfg.BranchPrefix, "ry/alice")
	}
	if cfg.Dolt.Host != "10.0.0.5" {
		t.Errorf("Dolt.Host = %q, want %q", cfg.Dolt.Host, "10.0.0.5")
	}
	if cfg.Dolt.Port != 3307 {
		t.Errorf("Dolt.Port = %d, want %d", cfg.Dolt.Port, 3307)
	}
	if cfg.Dolt.Database != "railyard_alice" {
		t.Errorf("Dolt.Database = %q, want %q", cfg.Dolt.Database, "railyard_alice")
	}
	if len(cfg.Tracks) != 2 {
		t.Fatalf("len(Tracks) = %d, want 2", len(cfg.Tracks))
	}

	be := cfg.Tracks[0]
	if be.Name != "backend" {
		t.Errorf("Tracks[0].Name = %q, want %q", be.Name, "backend")
	}
	if be.Language != "go" {
		t.Errorf("Tracks[0].Language = %q, want %q", be.Language, "go")
	}
	if len(be.FilePatterns) != 4 {
		t.Errorf("len(Tracks[0].FilePatterns) = %d, want 4", len(be.FilePatterns))
	}
	if be.EngineSlots != 5 {
		t.Errorf("Tracks[0].EngineSlots = %d, want 5", be.EngineSlots)
	}
	if be.Conventions["go_version"] != "1.22" {
		t.Errorf("Tracks[0].Conventions[go_version] = %v, want 1.22", be.Conventions["go_version"])
	}

	fe := cfg.Tracks[1]
	if fe.Name != "frontend" {
		t.Errorf("Tracks[1].Name = %q, want %q", fe.Name, "frontend")
	}
	if fe.EngineSlots != 3 {
		t.Errorf("Tracks[1].EngineSlots = %d, want 3", fe.EngineSlots)
	}
	if fe.Conventions["framework"] != "Next.js 15" {
		t.Errorf("Tracks[1].Conventions[framework] = %v, want Next.js 15", fe.Conventions["framework"])
	}
}

func TestParse_MinimalConfig_AppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.BranchPrefix != "ry/bob" {
		t.Errorf("BranchPrefix = %q, want %q (derived from owner)", cfg.BranchPrefix, "ry/bob")
	}
	if cfg.Dolt.Host != "127.0.0.1" {
		t.Errorf("Dolt.Host = %q, want %q (default)", cfg.Dolt.Host, "127.0.0.1")
	}
	if cfg.Dolt.Port != 3306 {
		t.Errorf("Dolt.Port = %d, want %d (default)", cfg.Dolt.Port, 3306)
	}
	if cfg.Dolt.Database != "railyard_bob" {
		t.Errorf("Dolt.Database = %q, want %q (derived from owner)", cfg.Dolt.Database, "railyard_bob")
	}
	if cfg.Tracks[0].EngineSlots != 3 {
		t.Errorf("Tracks[0].EngineSlots = %d, want %d (default)", cfg.Tracks[0].EngineSlots, 3)
	}
}

func TestParse_ExplicitBranchPrefix_NotOverridden(t *testing.T) {
	yaml := `
owner: carol
repo: git@github.com:org/app.git
branch_prefix: custom/prefix
tracks:
  - name: api
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BranchPrefix != "custom/prefix" {
		t.Errorf("BranchPrefix = %q, want %q (should not be overridden)", cfg.BranchPrefix, "custom/prefix")
	}
}

func TestParse_ExplicitDoltDatabase_NotOverridden(t *testing.T) {
	yaml := `
owner: carol
repo: git@github.com:org/app.git
dolt:
  database: my_custom_db
tracks:
  - name: api
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Dolt.Database != "my_custom_db" {
		t.Errorf("Dolt.Database = %q, want %q (should not be overridden)", cfg.Dolt.Database, "my_custom_db")
	}
}

func TestParse_MissingOwner(t *testing.T) {
	yaml := `
repo: git@github.com:org/app.git
tracks:
  - name: api
    language: go
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing owner")
	}
	if !strings.Contains(err.Error(), "owner is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "owner is required")
	}
}

func TestParse_MissingRepo(t *testing.T) {
	yaml := `
owner: alice
tracks:
  - name: api
    language: go
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing repo")
	}
	if !strings.Contains(err.Error(), "repo is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "repo is required")
	}
}

func TestParse_NoTracks(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for no tracks")
	}
	if !strings.Contains(err.Error(), "at least one track is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "at least one track is required")
	}
}

func TestParse_TrackMissingName(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - language: go
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for track missing name")
	}
	if !strings.Contains(err.Error(), "tracks[0].name is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "tracks[0].name is required")
	}
}

func TestParse_TrackMissingLanguage(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for track missing language")
	}
	if !strings.Contains(err.Error(), "tracks[0].language is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "tracks[0].language is required")
	}
}

func TestParse_MultipleValidationErrors(t *testing.T) {
	yaml := `
tracks:
  - name: backend
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "owner is required") {
		t.Errorf("error missing 'owner is required': %s", msg)
	}
	if !strings.Contains(msg, "repo is required") {
		t.Errorf("error missing 'repo is required': %s", msg)
	}
	if !strings.Contains(msg, "tracks[0].language is required") {
		t.Errorf("error missing 'tracks[0].language is required': %s", msg)
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	_, err := Parse([]byte(":::invalid"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "config: parse:") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "config: parse:")
	}
}

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(minimalYAML), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Owner != "bob" {
		t.Errorf("Owner = %q, want %q", cfg.Owner, "bob")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "config: read") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "config: read")
	}
}

// --- Fixture-based tests using testdata/ files ---

func TestLoad_FullFixture(t *testing.T) {
	cfg, err := Load("testdata/valid_full.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Owner != "alice" {
		t.Errorf("Owner = %q, want %q", cfg.Owner, "alice")
	}
	if cfg.Dolt.Host != "10.0.0.5" {
		t.Errorf("Dolt.Host = %q, want %q", cfg.Dolt.Host, "10.0.0.5")
	}
	if len(cfg.Tracks) != 2 {
		t.Fatalf("len(Tracks) = %d, want 2", len(cfg.Tracks))
	}
}

func TestLoad_MinimalFixture(t *testing.T) {
	cfg, err := Load("testdata/valid_minimal.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Owner != "bob" {
		t.Errorf("Owner = %q, want %q", cfg.Owner, "bob")
	}
	if cfg.Dolt.Host != "127.0.0.1" {
		t.Errorf("Dolt.Host = %q, want default %q", cfg.Dolt.Host, "127.0.0.1")
	}
	if cfg.Dolt.Port != 3306 {
		t.Errorf("Dolt.Port = %d, want default %d", cfg.Dolt.Port, 3306)
	}
}

func TestLoad_MissingOwnerFixture(t *testing.T) {
	_, err := Load("testdata/missing_owner.yaml")
	if err == nil {
		t.Fatal("expected error for missing owner")
	}
	if !strings.Contains(err.Error(), "owner is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "owner is required")
	}
}

func TestLoad_MissingRepoFixture(t *testing.T) {
	_, err := Load("testdata/missing_repo.yaml")
	if err == nil {
		t.Fatal("expected error for missing repo")
	}
	if !strings.Contains(err.Error(), "repo is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "repo is required")
	}
}

func TestLoad_NoTracksFixture(t *testing.T) {
	_, err := Load("testdata/no_tracks.yaml")
	if err == nil {
		t.Fatal("expected error for no tracks")
	}
	if !strings.Contains(err.Error(), "at least one track is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "at least one track is required")
	}
}

func TestLoad_InvalidYAMLFixture(t *testing.T) {
	_, err := Load("testdata/invalid.yaml")
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "config: parse:") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "config: parse:")
	}
}

func TestParse_ConventionsWithSliceValues(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
    conventions:
      forbidden:
        - python
        - node
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	forbidden, ok := cfg.Tracks[0].Conventions["forbidden"].([]interface{})
	if !ok {
		t.Fatalf("Conventions[forbidden] type = %T, want []interface{}", cfg.Tracks[0].Conventions["forbidden"])
	}
	if len(forbidden) != 2 {
		t.Errorf("len(forbidden) = %d, want 2", len(forbidden))
	}
	if forbidden[0] != "python" {
		t.Errorf("forbidden[0] = %v, want python", forbidden[0])
	}
}

func TestParse_NilConventions(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tracks[0].Conventions != nil {
		t.Errorf("Conventions = %v, want nil when not specified", cfg.Tracks[0].Conventions)
	}
}

func TestParse_StallDefaults(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Stall.StdoutTimeoutSec != 120 {
		t.Errorf("Stall.StdoutTimeoutSec = %d, want 120 (default)", cfg.Stall.StdoutTimeoutSec)
	}
	if cfg.Stall.RepeatedErrorMax != 3 {
		t.Errorf("Stall.RepeatedErrorMax = %d, want 3 (default)", cfg.Stall.RepeatedErrorMax)
	}
	if cfg.Stall.MaxClearCycles != 5 {
		t.Errorf("Stall.MaxClearCycles = %d, want 5 (default)", cfg.Stall.MaxClearCycles)
	}
}

func TestParse_StallCustomValues(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
stall:
  stdout_timeout_sec: 60
  repeated_error_max: 5
  max_clear_cycles: 10
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Stall.StdoutTimeoutSec != 60 {
		t.Errorf("Stall.StdoutTimeoutSec = %d, want 60", cfg.Stall.StdoutTimeoutSec)
	}
	if cfg.Stall.RepeatedErrorMax != 5 {
		t.Errorf("Stall.RepeatedErrorMax = %d, want 5", cfg.Stall.RepeatedErrorMax)
	}
	if cfg.Stall.MaxClearCycles != 10 {
		t.Errorf("Stall.MaxClearCycles = %d, want 10", cfg.Stall.MaxClearCycles)
	}
}

func TestParse_DefaultBranch_Explicit(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
default_branch: develop
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultBranch != "develop" {
		t.Errorf("DefaultBranch = %q, want %q", cfg.DefaultBranch, "develop")
	}
}

func TestParse_DefaultBranch_OmittedIsEmpty(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultBranch != "" {
		t.Errorf("DefaultBranch = %q, want empty when omitted", cfg.DefaultBranch)
	}
}

func TestParse_RequirePR(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
require_pr: true
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.RequirePR {
		t.Error("RequirePR = false, want true")
	}
}

func TestParse_RequirePR_DefaultFalse(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RequirePR {
		t.Error("RequirePR should default to false")
	}
}

func TestParse_EmptyFilePatterns(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: infra
    language: mixed
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tracks[0].FilePatterns != nil {
		t.Errorf("FilePatterns = %v, want nil when not specified", cfg.Tracks[0].FilePatterns)
	}
}

func TestParse_CocoIndexDefaults(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CocoIndex.VenvPath != "cocoindex/.venv" {
		t.Errorf("CocoIndex.VenvPath = %q, want default %q", cfg.CocoIndex.VenvPath, "cocoindex/.venv")
	}
	if cfg.CocoIndex.ScriptsPath != "cocoindex" {
		t.Errorf("CocoIndex.ScriptsPath = %q, want default %q", cfg.CocoIndex.ScriptsPath, "cocoindex")
	}
	// Without database_url, overlay should not be auto-enabled.
	if cfg.CocoIndex.Overlay.Enabled {
		t.Error("CocoIndex.Overlay.Enabled should be false without database_url")
	}
	if cfg.CocoIndex.Overlay.MaxChunks != 5000 {
		t.Errorf("CocoIndex.Overlay.MaxChunks = %d, want 5000", cfg.CocoIndex.Overlay.MaxChunks)
	}
	if cfg.CocoIndex.Overlay.BuildTimeoutSec != 60 {
		t.Errorf("CocoIndex.Overlay.BuildTimeoutSec = %d, want 60", cfg.CocoIndex.Overlay.BuildTimeoutSec)
	}
}

func TestParse_CocoIndexWithDatabaseURL(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
cocoindex:
  database_url: "postgresql://localhost:5432/cocoindex"
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CocoIndex.DatabaseURL != "postgresql://localhost:5432/cocoindex" {
		t.Errorf("CocoIndex.DatabaseURL = %q", cfg.CocoIndex.DatabaseURL)
	}
	if !cfg.CocoIndex.Overlay.Enabled {
		t.Error("CocoIndex.Overlay.Enabled should default to true when database_url is set")
	}
}

func TestParse_CocoIndexCustomValues(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
cocoindex:
  database_url: "postgresql://localhost:5432/cocoindex"
  venv_path: "/custom/venv"
  scripts_path: "/custom/scripts"
  overlay:
    enabled: true
    max_chunks: 10000
    build_timeout_sec: 120
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CocoIndex.VenvPath != "/custom/venv" {
		t.Errorf("CocoIndex.VenvPath = %q, want /custom/venv", cfg.CocoIndex.VenvPath)
	}
	if cfg.CocoIndex.ScriptsPath != "/custom/scripts" {
		t.Errorf("CocoIndex.ScriptsPath = %q, want /custom/scripts", cfg.CocoIndex.ScriptsPath)
	}
	if !cfg.CocoIndex.Overlay.Enabled {
		t.Error("CocoIndex.Overlay.Enabled should be true")
	}
	if cfg.CocoIndex.Overlay.MaxChunks != 10000 {
		t.Errorf("CocoIndex.Overlay.MaxChunks = %d, want 10000", cfg.CocoIndex.Overlay.MaxChunks)
	}
	if cfg.CocoIndex.Overlay.BuildTimeoutSec != 120 {
		t.Errorf("CocoIndex.Overlay.BuildTimeoutSec = %d, want 120", cfg.CocoIndex.Overlay.BuildTimeoutSec)
	}
}

// ---------------------------------------------------------------------------
// Telegraph config tests
// ---------------------------------------------------------------------------

func TestParse_TelegraphFullConfig(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
telegraph:
  platform: slack
  channel: C0123456789
  slack:
    bot_token: xoxb-test-bot-token
    app_token: xapp-test-app-token
  dispatch_lock:
    heartbeat_interval_sec: 15
    heartbeat_timeout_sec: 60
    queue_max: 3
  events:
    car_lifecycle: true
    engine_stalls: true
    escalations: false
    poll_interval_sec: 10
  digest:
    pulse:
      enabled: true
      cron: "*/30 * * * *"
    daily:
      enabled: true
      cron: "0 9 * * *"
  conversations:
    max_turns: 30
    recovery_lookback_days: 14
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tg := cfg.Telegraph
	if tg.Platform != "slack" {
		t.Errorf("Platform = %q, want slack", tg.Platform)
	}
	if tg.Channel != "C0123456789" {
		t.Errorf("Channel = %q, want C0123456789", tg.Channel)
	}
	if tg.Slack.BotToken != "xoxb-test-bot-token" {
		t.Errorf("Slack.BotToken = %q", tg.Slack.BotToken)
	}
	if tg.Slack.AppToken != "xapp-test-app-token" {
		t.Errorf("Slack.AppToken = %q", tg.Slack.AppToken)
	}
	if tg.DispatchLock.HeartbeatIntervalSec != 15 {
		t.Errorf("DispatchLock.HeartbeatIntervalSec = %d, want 15", tg.DispatchLock.HeartbeatIntervalSec)
	}
	if tg.DispatchLock.HeartbeatTimeoutSec != 60 {
		t.Errorf("DispatchLock.HeartbeatTimeoutSec = %d, want 60", tg.DispatchLock.HeartbeatTimeoutSec)
	}
	if tg.DispatchLock.QueueMax != 3 {
		t.Errorf("DispatchLock.QueueMax = %d, want 3", tg.DispatchLock.QueueMax)
	}
	if !tg.Events.CarLifecycle {
		t.Error("Events.CarLifecycle = false, want true")
	}
	if !tg.Events.EngineStalls {
		t.Error("Events.EngineStalls = false, want true")
	}
	if tg.Events.Escalations {
		t.Error("Events.Escalations = true, want false")
	}
	if tg.Events.PollIntervalSec != 10 {
		t.Errorf("Events.PollIntervalSec = %d, want 10", tg.Events.PollIntervalSec)
	}
	if !tg.Digest.Pulse.Enabled {
		t.Error("Digest.Pulse.Enabled = false, want true")
	}
	if tg.Digest.Pulse.Cron != "*/30 * * * *" {
		t.Errorf("Digest.Pulse.Cron = %q", tg.Digest.Pulse.Cron)
	}
	if !tg.Digest.Daily.Enabled {
		t.Error("Digest.Daily.Enabled = false, want true")
	}
	if tg.Digest.Weekly.Enabled {
		t.Error("Digest.Weekly.Enabled should default to false")
	}
	if tg.Conversations.MaxTurns != 30 {
		t.Errorf("Conversations.MaxTurns = %d, want 30", tg.Conversations.MaxTurns)
	}
	if tg.Conversations.RecoveryLookbackDays != 14 {
		t.Errorf("Conversations.RecoveryLookbackDays = %d, want 14", tg.Conversations.RecoveryLookbackDays)
	}
}

func TestParse_TelegraphDefaults(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
telegraph:
  platform: slack
  channel: C0123456789
  slack:
    bot_token: xoxb-token
    app_token: xapp-token
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tg := cfg.Telegraph
	if tg.DispatchLock.HeartbeatIntervalSec != 30 {
		t.Errorf("DispatchLock.HeartbeatIntervalSec = %d, want 30 (default)", tg.DispatchLock.HeartbeatIntervalSec)
	}
	if tg.DispatchLock.HeartbeatTimeoutSec != 90 {
		t.Errorf("DispatchLock.HeartbeatTimeoutSec = %d, want 90 (default)", tg.DispatchLock.HeartbeatTimeoutSec)
	}
	if tg.DispatchLock.QueueMax != 5 {
		t.Errorf("DispatchLock.QueueMax = %d, want 5 (default)", tg.DispatchLock.QueueMax)
	}
	if tg.Events.PollIntervalSec != 15 {
		t.Errorf("Events.PollIntervalSec = %d, want 15 (default)", tg.Events.PollIntervalSec)
	}
	if !tg.Events.CarLifecycle {
		t.Error("Events.CarLifecycle should default to true")
	}
	if !tg.Events.EngineStalls {
		t.Error("Events.EngineStalls should default to true")
	}
	if !tg.Events.Escalations {
		t.Error("Events.Escalations should default to true")
	}
	if tg.Conversations.MaxTurns != 20 {
		t.Errorf("Conversations.MaxTurns = %d, want 20 (default)", tg.Conversations.MaxTurns)
	}
	if tg.Conversations.RecoveryLookbackDays != 7 {
		t.Errorf("Conversations.RecoveryLookbackDays = %d, want 7 (default)", tg.Conversations.RecoveryLookbackDays)
	}
}

func TestParse_TelegraphOmitted(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// When telegraph section is absent, no defaults should be applied.
	if cfg.Telegraph.Platform != "" {
		t.Errorf("Telegraph.Platform = %q, want empty", cfg.Telegraph.Platform)
	}
	if cfg.Telegraph.DispatchLock.HeartbeatIntervalSec != 0 {
		t.Errorf("defaults should not apply without platform: HeartbeatIntervalSec = %d", cfg.Telegraph.DispatchLock.HeartbeatIntervalSec)
	}
}

func TestParse_TelegraphSlackMissingBotToken(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
telegraph:
  platform: slack
  channel: C0123456789
  slack:
    app_token: xapp-token
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing bot_token")
	}
	if !strings.Contains(err.Error(), "telegraph.slack.bot_token is required") {
		t.Errorf("error = %q", err)
	}
}

func TestParse_TelegraphSlackMissingAppToken(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
telegraph:
  platform: slack
  channel: C0123456789
  slack:
    bot_token: xoxb-token
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing app_token")
	}
	if !strings.Contains(err.Error(), "telegraph.slack.app_token is required") {
		t.Errorf("error = %q", err)
	}
}

func TestParse_TelegraphMissingChannel(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
telegraph:
  platform: slack
  slack:
    bot_token: xoxb-token
    app_token: xapp-token
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing channel")
	}
	if !strings.Contains(err.Error(), "telegraph.channel is required") {
		t.Errorf("error = %q", err)
	}
}

func TestParse_TelegraphUnsupportedPlatform(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
telegraph:
  platform: teams
  channel: C123
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unsupported platform")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %q", err)
	}
}

func TestParse_TelegraphDiscordValidation(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
telegraph:
  platform: discord
  channel: "123456789"
  discord:
    bot_token: discord-bot-token
    guild_id: "987654321"
    channel_id: "123456789"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegraph.Platform != "discord" {
		t.Errorf("Platform = %q, want discord", cfg.Telegraph.Platform)
	}
	if cfg.Telegraph.Discord.BotToken != "discord-bot-token" {
		t.Errorf("Discord.BotToken = %q", cfg.Telegraph.Discord.BotToken)
	}
	if cfg.Telegraph.Discord.GuildID != "987654321" {
		t.Errorf("Discord.GuildID = %q", cfg.Telegraph.Discord.GuildID)
	}
}

func TestParse_TelegraphDiscordMissingBotToken(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
telegraph:
  platform: discord
  channel: "123456789"
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing discord bot_token")
	}
	if !strings.Contains(err.Error(), "telegraph.discord.bot_token is required") {
		t.Errorf("error = %q", err)
	}
}

func TestParse_TelegraphEnvVarResolution(t *testing.T) {
	t.Setenv("TEST_SLACK_BOT_TOKEN", "xoxb-from-env")
	t.Setenv("TEST_SLACK_APP_TOKEN", "xapp-from-env")

	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
telegraph:
  platform: slack
  channel: C0123456789
  slack:
    bot_token: "${TEST_SLACK_BOT_TOKEN}"
    app_token: "${TEST_SLACK_APP_TOKEN}"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegraph.Slack.BotToken != "xoxb-from-env" {
		t.Errorf("Slack.BotToken = %q, want xoxb-from-env", cfg.Telegraph.Slack.BotToken)
	}
	if cfg.Telegraph.Slack.AppToken != "xapp-from-env" {
		t.Errorf("Slack.AppToken = %q, want xapp-from-env", cfg.Telegraph.Slack.AppToken)
	}
}

func TestParse_TelegraphEnvVarUnset(t *testing.T) {
	// Ensure the env var is not set.
	t.Setenv("TEST_UNSET_TOKEN", "")
	os.Unsetenv("TEST_UNSET_TOKEN")

	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
telegraph:
  platform: slack
  channel: C0123456789
  slack:
    bot_token: "${TEST_UNSET_TOKEN}"
    app_token: xapp-literal
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected validation error for empty resolved token")
	}
	if !strings.Contains(err.Error(), "telegraph.slack.bot_token is required") {
		t.Errorf("error = %q", err)
	}
}

func TestResolveEnvVars(t *testing.T) {
	t.Setenv("FOO", "bar")
	t.Setenv("BAZ", "qux")

	tests := []struct {
		input string
		want  string
	}{
		{"${FOO}", "bar"},
		{"prefix-${FOO}-suffix", "prefix-bar-suffix"},
		{"${FOO}-${BAZ}", "bar-qux"},
		{"no-vars-here", "no-vars-here"},
		{"", ""},
	}
	for _, tt := range tests {
		got := resolveEnvVars(tt.input)
		if got != tt.want {
			t.Errorf("resolveEnvVars(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLoad_TelegraphFixture(t *testing.T) {
	cfg, err := Load("testdata/valid_telegraph.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegraph.Platform != "slack" {
		t.Errorf("Platform = %q, want slack", cfg.Telegraph.Platform)
	}
	if cfg.Telegraph.Channel != "C0123456789" {
		t.Errorf("Channel = %q, want C0123456789", cfg.Telegraph.Channel)
	}
	if cfg.Telegraph.DispatchLock.HeartbeatIntervalSec != 15 {
		t.Errorf("DispatchLock.HeartbeatIntervalSec = %d, want 15", cfg.Telegraph.DispatchLock.HeartbeatIntervalSec)
	}
}
