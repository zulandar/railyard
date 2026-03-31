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
agent_provider: claude

database:
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
	if cfg.Database.Host != "10.0.0.5" {
		t.Errorf("Database.Host = %q, want %q", cfg.Database.Host, "10.0.0.5")
	}
	if cfg.Database.Port != 3307 {
		t.Errorf("Database.Port = %d, want %d", cfg.Database.Port, 3307)
	}
	if cfg.Database.Database != "railyard_alice" {
		t.Errorf("Database.Database = %q, want %q", cfg.Database.Database, "railyard_alice")
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
	if cfg.AgentProvider != "claude" {
		t.Errorf("AgentProvider = %q, want %q", cfg.AgentProvider, "claude")
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
	if cfg.Database.Host != "127.0.0.1" {
		t.Errorf("Database.Host = %q, want %q (default)", cfg.Database.Host, "127.0.0.1")
	}
	if cfg.Database.Port != 3306 {
		t.Errorf("Database.Port = %d, want %d (default)", cfg.Database.Port, 3306)
	}
	if cfg.Database.Database != "railyard_bob" {
		t.Errorf("Database.Database = %q, want %q (derived from owner)", cfg.Database.Database, "railyard_bob")
	}
	if cfg.Tracks[0].EngineSlots != 3 {
		t.Errorf("Tracks[0].EngineSlots = %d, want %d (default)", cfg.Tracks[0].EngineSlots, 3)
	}
	if cfg.Database.Username != "root" {
		t.Errorf("Database.Username = %q, want %q (default)", cfg.Database.Username, "root")
	}
	if cfg.Database.Password != "" {
		t.Errorf("Database.Password = %q, want %q (default)", cfg.Database.Password, "")
	}
}

func TestParse_DatabaseCredentials(t *testing.T) {
	yaml := `
owner: carol
repo: git@github.com:org/app.git
database:
  username: admin
  password: secret123
tracks:
  - name: api
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Database.Username != "admin" {
		t.Errorf("Database.Username = %q, want %q", cfg.Database.Username, "admin")
	}
	if cfg.Database.Password != "secret123" {
		t.Errorf("Database.Password = %q, want %q", cfg.Database.Password, "secret123")
	}
}

func TestParse_DatabaseCredentials_EnvVar(t *testing.T) {
	t.Setenv("TEST_DB_USER", "envuser")
	t.Setenv("TEST_DB_PASS", "envpass")
	yaml := `
owner: carol
repo: git@github.com:org/app.git
database:
  username: ${TEST_DB_USER}
  password: ${TEST_DB_PASS}
tracks:
  - name: api
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Database.Username != "envuser" {
		t.Errorf("Database.Username = %q, want %q", cfg.Database.Username, "envuser")
	}
	if cfg.Database.Password != "envpass" {
		t.Errorf("Database.Password = %q, want %q", cfg.Database.Password, "envpass")
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

func TestParse_ExplicitDatabase_NotOverridden(t *testing.T) {
	yaml := `
owner: carol
repo: git@github.com:org/app.git
database:
  database: my_custom_db
tracks:
  - name: api
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Database.Database != "my_custom_db" {
		t.Errorf("Database.Database = %q, want %q (should not be overridden)", cfg.Database.Database, "my_custom_db")
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
	if cfg.Database.Host != "10.0.0.5" {
		t.Errorf("Database.Host = %q, want %q", cfg.Database.Host, "10.0.0.5")
	}
	if len(cfg.Tracks) != 2 {
		t.Fatalf("len(Tracks) = %d, want 2", len(cfg.Tracks))
	}
	if cfg.AgentProvider != "claude" {
		t.Errorf("AgentProvider = %q, want %q", cfg.AgentProvider, "claude")
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
	if cfg.Database.Host != "127.0.0.1" {
		t.Errorf("Database.Host = %q, want default %q", cfg.Database.Host, "127.0.0.1")
	}
	if cfg.Database.Port != 3306 {
		t.Errorf("Database.Port = %d, want default %d", cfg.Database.Port, 3306)
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

func TestParse_CocoIndexDatabaseURL_EnvVar(t *testing.T) {
	t.Setenv("TEST_COCOINDEX_URL", "postgresql://user:pass@localhost:5432/cocoindex")
	yaml := `
owner: alice
repo: git@github.com:org/app.git
cocoindex:
  database_url: "${TEST_COCOINDEX_URL}"
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CocoIndex.DatabaseURL != "postgresql://user:pass@localhost:5432/cocoindex" {
		t.Errorf("CocoIndex.DatabaseURL = %q, want resolved env var value", cfg.CocoIndex.DatabaseURL)
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
  process_timeout_sec: 1200
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
	if tg.ProcessTimeoutSec != 1200 {
		t.Errorf("ProcessTimeoutSec = %d, want 1200", tg.ProcessTimeoutSec)
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
	if tg.ProcessTimeoutSec != 900 {
		t.Errorf("ProcessTimeoutSec = %d, want 900 (default)", tg.ProcessTimeoutSec)
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

func TestParse_TelegraphHealthPortDefault(t *testing.T) {
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
	if cfg.Telegraph.HealthPort != 8086 {
		t.Errorf("Telegraph.HealthPort = %d, want 8086 (default)", cfg.Telegraph.HealthPort)
	}
}

func TestParse_TelegraphHealthPortExplicit(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
telegraph:
  platform: slack
  channel: C0123456789
  health_port: 9090
  slack:
    bot_token: xoxb-token
    app_token: xapp-token
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegraph.HealthPort != 9090 {
		t.Errorf("Telegraph.HealthPort = %d, want 9090", cfg.Telegraph.HealthPort)
	}
}

func TestParse_TelegraphHealthPortAbsent(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// When telegraph section is absent, HealthPort default should not be applied.
	if cfg.Telegraph.HealthPort != 0 {
		t.Errorf("Telegraph.HealthPort = %d, want 0 (no platform, no default)", cfg.Telegraph.HealthPort)
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

func TestParse_AgentProviderDefault(t *testing.T) {
	// minimalYAML has no agent_provider set
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentProvider != "claude" {
		t.Errorf("AgentProvider = %q, want %q (default)", cfg.AgentProvider, "claude")
	}
	// Track should inherit global default
	if cfg.Tracks[0].AgentProvider != "claude" {
		t.Errorf("Tracks[0].AgentProvider = %q, want %q (inherited)", cfg.Tracks[0].AgentProvider, "claude")
	}
}

func TestParse_AgentProviderGlobalOverride(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
agent_provider: opencode
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentProvider != "opencode" {
		t.Errorf("AgentProvider = %q, want %q", cfg.AgentProvider, "opencode")
	}
	if cfg.Tracks[0].AgentProvider != "opencode" {
		t.Errorf("Tracks[0].AgentProvider = %q, want %q (inherited from global)", cfg.Tracks[0].AgentProvider, "opencode")
	}
}

func TestParse_AgentProviderPerTrackOverride(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
agent_provider: claude
tracks:
  - name: backend
    language: go
    agent_provider: opencode
  - name: frontend
    language: typescript
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tracks[0].AgentProvider != "opencode" {
		t.Errorf("Tracks[0].AgentProvider = %q, want %q (per-track override)", cfg.Tracks[0].AgentProvider, "opencode")
	}
	if cfg.Tracks[1].AgentProvider != "claude" {
		t.Errorf("Tracks[1].AgentProvider = %q, want %q (inherited from global)", cfg.Tracks[1].AgentProvider, "claude")
	}
}

// ---------------------------------------------------------------------------
// TLS config tests
// ---------------------------------------------------------------------------

func TestParse_TLSConfig_Full(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
database:
  host: db.k8s.internal
  port: 3306
  tls:
    enabled: true
    ca_cert: /certs/ca.pem
    client_cert: /certs/client-cert.pem
    client_key: /certs/client-key.pem
    skip_verify: false
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Database.TLS.Enabled {
		t.Error("Database.TLS.Enabled = false, want true")
	}
	if cfg.Database.TLS.CACert != "/certs/ca.pem" {
		t.Errorf("Database.TLS.CACert = %q, want /certs/ca.pem", cfg.Database.TLS.CACert)
	}
	if cfg.Database.TLS.ClientCert != "/certs/client-cert.pem" {
		t.Errorf("Database.TLS.ClientCert = %q", cfg.Database.TLS.ClientCert)
	}
	if cfg.Database.TLS.ClientKey != "/certs/client-key.pem" {
		t.Errorf("Database.TLS.ClientKey = %q", cfg.Database.TLS.ClientKey)
	}
	if cfg.Database.TLS.SkipVerify {
		t.Error("Database.TLS.SkipVerify = true, want false")
	}
}

func TestParse_TLSConfig_Disabled_ByDefault(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Database.TLS.Enabled {
		t.Error("Database.TLS.Enabled should be false by default")
	}
}

func TestParse_TLSConfig_SkipVerify(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
database:
  tls:
    enabled: true
    skip_verify: true
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Database.TLS.Enabled {
		t.Error("Database.TLS.Enabled = false, want true")
	}
	if !cfg.Database.TLS.SkipVerify {
		t.Error("Database.TLS.SkipVerify = false, want true")
	}
}

func TestParse_TLSConfig_EnvVarResolution(t *testing.T) {
	t.Setenv("TEST_CA_PATH", "/resolved/ca.pem")
	t.Setenv("TEST_CERT_PATH", "/resolved/cert.pem")
	t.Setenv("TEST_KEY_PATH", "/resolved/key.pem")
	yaml := `
owner: alice
repo: git@github.com:org/app.git
database:
  tls:
    enabled: true
    ca_cert: "${TEST_CA_PATH}"
    client_cert: "${TEST_CERT_PATH}"
    client_key: "${TEST_KEY_PATH}"
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Database.TLS.CACert != "/resolved/ca.pem" {
		t.Errorf("Database.TLS.CACert = %q, want /resolved/ca.pem", cfg.Database.TLS.CACert)
	}
	if cfg.Database.TLS.ClientCert != "/resolved/cert.pem" {
		t.Errorf("Database.TLS.ClientCert = %q, want /resolved/cert.pem", cfg.Database.TLS.ClientCert)
	}
	if cfg.Database.TLS.ClientKey != "/resolved/key.pem" {
		t.Errorf("Database.TLS.ClientKey = %q, want /resolved/key.pem", cfg.Database.TLS.ClientKey)
	}
}

func TestParse_AgentProviderUnknownAllowed(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
agent_provider: some-future-provider
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error (unknown providers should be allowed): %v", err)
	}
	if cfg.AgentProvider != "some-future-provider" {
		t.Errorf("AgentProvider = %q, want %q", cfg.AgentProvider, "some-future-provider")
	}
}

// ---------------------------------------------------------------------------
// Kubernetes config tests
// ---------------------------------------------------------------------------

func TestParse_KubernetesConfig_Full(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
project: myapp
kubernetes:
  namespace: railyard-myapp
  image: ghcr.io/org/railyard-engine:latest
  image_pull_secret: ghcr-creds
  service_account: railyard-sa
  scaling:
    min_engines: 1
    max_engines: 10
    scale_up_threshold: 5
    scale_down_idle_minutes: 15
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project != "myapp" {
		t.Errorf("Project = %q, want myapp", cfg.Project)
	}
	k := cfg.Kubernetes
	if k.Namespace != "railyard-myapp" {
		t.Errorf("Kubernetes.Namespace = %q, want railyard-myapp", k.Namespace)
	}
	if k.Image != "ghcr.io/org/railyard-engine:latest" {
		t.Errorf("Kubernetes.Image = %q", k.Image)
	}
	if k.ImagePullSecret != "ghcr-creds" {
		t.Errorf("Kubernetes.ImagePullSecret = %q", k.ImagePullSecret)
	}
	if k.ServiceAccount != "railyard-sa" {
		t.Errorf("Kubernetes.ServiceAccount = %q", k.ServiceAccount)
	}
	if k.Scaling.MinEngines != 1 {
		t.Errorf("Scaling.MinEngines = %d, want 1", k.Scaling.MinEngines)
	}
	if k.Scaling.MaxEngines != 10 {
		t.Errorf("Scaling.MaxEngines = %d, want 10", k.Scaling.MaxEngines)
	}
	if k.Scaling.ScaleUpThreshold != 5 {
		t.Errorf("Scaling.ScaleUpThreshold = %d, want 5", k.Scaling.ScaleUpThreshold)
	}
	if k.Scaling.ScaleDownIdleMinutes != 15 {
		t.Errorf("Scaling.ScaleDownIdleMinutes = %d, want 15", k.Scaling.ScaleDownIdleMinutes)
	}
}

func TestParse_KubernetesConfig_Defaults(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
project: myapp
kubernetes:
  namespace: railyard-myapp
  image: ghcr.io/org/engine:latest
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	k := cfg.Kubernetes
	if k.Scaling.MinEngines != 1 {
		t.Errorf("Scaling.MinEngines = %d, want 1 (default)", k.Scaling.MinEngines)
	}
	if k.Scaling.MaxEngines != 10 {
		t.Errorf("Scaling.MaxEngines = %d, want 10 (default)", k.Scaling.MaxEngines)
	}
	if k.Scaling.ScaleUpThreshold != 3 {
		t.Errorf("Scaling.ScaleUpThreshold = %d, want 3 (default)", k.Scaling.ScaleUpThreshold)
	}
	if k.Scaling.ScaleDownIdleMinutes != 10 {
		t.Errorf("Scaling.ScaleDownIdleMinutes = %d, want 10 (default)", k.Scaling.ScaleDownIdleMinutes)
	}
}

func TestParse_KubernetesConfig_NamespaceFromProject(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
project: coolproject
kubernetes:
  image: ghcr.io/org/engine:v1
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Kubernetes.Namespace != "railyard-coolproject" {
		t.Errorf("Kubernetes.Namespace = %q, want railyard-coolproject (derived from project)", cfg.Kubernetes.Namespace)
	}
}

func TestParse_KubernetesConfig_Absent_LocalMode(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.IsKubernetesMode() {
		t.Error("IsKubernetesMode() = true, want false when kubernetes section absent")
	}
	if cfg.Kubernetes.Namespace != "" {
		t.Errorf("Kubernetes.Namespace = %q, want empty", cfg.Kubernetes.Namespace)
	}
}

func TestParse_KubernetesConfig_IsKubernetesMode(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
project: myapp
kubernetes:
  namespace: railyard-myapp
  image: ghcr.io/org/engine:latest
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsKubernetesMode() {
		t.Error("IsKubernetesMode() = false, want true when namespace is set")
	}
}

func TestParse_KubernetesConfig_ValidationRequiresImage(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
kubernetes:
  namespace: railyard-test
tracks:
  - name: backend
    language: go
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected validation error for k8s without image")
	}
	if !strings.Contains(err.Error(), "kubernetes.image is required") {
		t.Errorf("error = %q, want to mention kubernetes.image", err.Error())
	}
}

func TestParse_BranchPrefix_DefaultsToRy_WhenProjectSet(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
project: myapp
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BranchPrefix != "ry" {
		t.Errorf("BranchPrefix = %q, want %q when project is set", cfg.BranchPrefix, "ry")
	}
}

func TestParse_BranchPrefix_DefaultsToRyOwner_WhenNoProject(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BranchPrefix != "ry/bob" {
		t.Errorf("BranchPrefix = %q, want %q when only owner is set", cfg.BranchPrefix, "ry/bob")
	}
}

func TestParse_BranchPrefix_ExplicitNotOverridden_WithProject(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
project: myapp
branch_prefix: custom/prefix
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BranchPrefix != "custom/prefix" {
		t.Errorf("BranchPrefix = %q, want %q (explicit should not be overridden)", cfg.BranchPrefix, "custom/prefix")
	}
}

func TestParse_ProjectField(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
project: my-project
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project != "my-project" {
		t.Errorf("Project = %q, want my-project", cfg.Project)
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

// ---------------------------------------------------------------------------
// Bull config tests
// ---------------------------------------------------------------------------

func TestParse_BullFullConfig(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
  github_token: "ghp_test123"
  poll_interval_sec: 120
  triage_mode: full
  comments:
    enabled: true
    reject_template: "Closing: {{.Reason}}"
    answer_questions: true
  labels:
    under_review: "triage: reviewing"
    in_progress: "triage: active"
    fix_merged: "triage: done"
    ignore: "triage: skip"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := cfg.Bull
	if !b.Enabled {
		t.Error("Bull.Enabled = false, want true")
	}
	if b.GitHubToken != "ghp_test123" {
		t.Errorf("Bull.GitHubToken = %q, want ghp_test123", b.GitHubToken)
	}
	if b.PollIntervalSec != 120 {
		t.Errorf("Bull.PollIntervalSec = %d, want 120", b.PollIntervalSec)
	}
	if b.TriageMode != "full" {
		t.Errorf("Bull.TriageMode = %q, want full", b.TriageMode)
	}
	if !b.Comments.Enabled {
		t.Error("Bull.Comments.Enabled = false, want true")
	}
	if b.Comments.RejectTemplate != "Closing: {{.Reason}}" {
		t.Errorf("Bull.Comments.RejectTemplate = %q", b.Comments.RejectTemplate)
	}
	if !b.Comments.AnswerQuestions {
		t.Error("Bull.Comments.AnswerQuestions = false, want true")
	}
	if b.Labels.UnderReview != "triage: reviewing" {
		t.Errorf("Bull.Labels.UnderReview = %q, want %q", b.Labels.UnderReview, "triage: reviewing")
	}
	if b.Labels.InProgress != "triage: active" {
		t.Errorf("Bull.Labels.InProgress = %q, want %q", b.Labels.InProgress, "triage: active")
	}
	if b.Labels.FixMerged != "triage: done" {
		t.Errorf("Bull.Labels.FixMerged = %q, want %q", b.Labels.FixMerged, "triage: done")
	}
	if b.Labels.Ignore != "triage: skip" {
		t.Errorf("Bull.Labels.Ignore = %q, want %q", b.Labels.Ignore, "triage: skip")
	}
}

func TestParse_BullDefaults(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
  github_token: "ghp_test123"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := cfg.Bull
	if b.PollIntervalSec != 60 {
		t.Errorf("Bull.PollIntervalSec = %d, want 60 (default)", b.PollIntervalSec)
	}
	if b.TriageMode != "standard" {
		t.Errorf("Bull.TriageMode = %q, want standard (default)", b.TriageMode)
	}
	if b.Labels.UnderReview != "bull: under review" {
		t.Errorf("Bull.Labels.UnderReview = %q, want %q (default)", b.Labels.UnderReview, "bull: under review")
	}
	if b.Labels.InProgress != "bull: in progress" {
		t.Errorf("Bull.Labels.InProgress = %q, want %q (default)", b.Labels.InProgress, "bull: in progress")
	}
	if b.Labels.FixMerged != "bull: fix merged" {
		t.Errorf("Bull.Labels.FixMerged = %q, want %q (default)", b.Labels.FixMerged, "bull: fix merged")
	}
	if b.Labels.Ignore != "bull: ignore" {
		t.Errorf("Bull.Labels.Ignore = %q, want %q (default)", b.Labels.Ignore, "bull: ignore")
	}
}

func TestParse_BullOmitted(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Bull.Enabled {
		t.Error("Bull.Enabled should be false when section absent")
	}
	// Defaults should NOT be applied when bull section is absent.
	if cfg.Bull.PollIntervalSec != 0 {
		t.Errorf("defaults should not apply without enabled: PollIntervalSec = %d", cfg.Bull.PollIntervalSec)
	}
}

func TestParse_BullValidation_MissingAuthFails(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error when bull is enabled with no auth credentials")
	}
	if !strings.Contains(err.Error(), "bull: authentication is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bull: authentication is required")
	}
}

func TestParse_BullValidation_InvalidTriageMode(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
  github_token: "ghp_test123"
  triage_mode: invalid_mode
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid triage_mode")
	}
	if !strings.Contains(err.Error(), "bull.triage_mode") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bull.triage_mode")
	}
}

func TestParse_BullDisabled_NoTokenRequired(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: false
`
	_, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: disabled bull should not require token: %v", err)
	}
}

func TestParse_BullEnvVarResolution(t *testing.T) {
	t.Setenv("TEST_BULL_GH_TOKEN", "ghp_from_env")
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
  github_token: "${TEST_BULL_GH_TOKEN}"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Bull.GitHubToken != "ghp_from_env" {
		t.Errorf("Bull.GitHubToken = %q, want ghp_from_env", cfg.Bull.GitHubToken)
	}
}

func TestLoad_BullFixture(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_fixture_test")
	cfg, err := Load("testdata/valid_bull.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Bull.Enabled {
		t.Error("Bull.Enabled = false, want true")
	}
	if cfg.Bull.GitHubToken != "ghp_fixture_test" {
		t.Errorf("Bull.GitHubToken = %q, want ghp_fixture_test", cfg.Bull.GitHubToken)
	}
	if cfg.Bull.PollIntervalSec != 120 {
		t.Errorf("Bull.PollIntervalSec = %d, want 120", cfg.Bull.PollIntervalSec)
	}
	if cfg.Bull.TriageMode != "full" {
		t.Errorf("Bull.TriageMode = %q, want full", cfg.Bull.TriageMode)
	}
}

func TestLoad_BullMinimalFixture(t *testing.T) {
	cfg, err := Load("testdata/valid_bull_minimal.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Bull.PollIntervalSec != 60 {
		t.Errorf("Bull.PollIntervalSec = %d, want 60 (default)", cfg.Bull.PollIntervalSec)
	}
	if cfg.Bull.TriageMode != "standard" {
		t.Errorf("Bull.TriageMode = %q, want standard (default)", cfg.Bull.TriageMode)
	}
}

func TestLoad_BullNoTokenFixture(t *testing.T) {
	// No auth credentials: config load should fail validation.
	_, err := Load("testdata/invalid_bull_no_token.yaml")
	if err == nil {
		t.Fatal("expected error for bull config with no auth credentials")
	}
	if !strings.Contains(err.Error(), "bull: authentication is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bull: authentication is required")
	}
}

func TestLoad_BullInvalidTriageModeFixture(t *testing.T) {
	_, err := Load("testdata/invalid_bull_triage_mode.yaml")
	if err == nil {
		t.Fatal("expected error for invalid triage_mode")
	}
	if !strings.Contains(err.Error(), "bull.triage_mode") {
		t.Errorf("error = %q", err)
	}
}

// Bull GitHub App credential tests.
// ---------------------------------------------------------------------------

func TestParse_BullAuth_PATOnly(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
  github_token: "ghp_test123"
`
	_, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("PAT-only config should be valid, got error: %v", err)
	}
}

func TestParse_BullAuth_AppOnly(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
  app_id: 12345
  private_key_path: "/path/to/key.pem"
  installation_id: 67890
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("App-only config with all three fields should be valid, got error: %v", err)
	}
	if cfg.Bull.AppID != 12345 {
		t.Errorf("Bull.AppID = %d, want 12345", cfg.Bull.AppID)
	}
	if cfg.Bull.PrivateKeyPath != "/path/to/key.pem" {
		t.Errorf("Bull.PrivateKeyPath = %q, want /path/to/key.pem", cfg.Bull.PrivateKeyPath)
	}
	if cfg.Bull.InstallationID != 67890 {
		t.Errorf("Bull.InstallationID = %d, want 67890", cfg.Bull.InstallationID)
	}
}

func TestParse_BullAuth_NoAuth(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error when bull is enabled with no auth credentials")
	}
	if !strings.Contains(err.Error(), "bull: authentication is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bull: authentication is required")
	}
}

func TestParse_BullAuth_PartialAppFields(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "app_id only",
			yaml: `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
  app_id: 12345
`,
		},
		{
			name: "app_id and private_key_path but no installation_id",
			yaml: `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
  app_id: 12345
  private_key_path: "/path/to/key.pem"
`,
		},
		{
			name: "private_key_path only",
			yaml: `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
  private_key_path: "/path/to/key.pem"
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected error for partial App credentials (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), "bull: GitHub App auth requires all three fields") {
				t.Errorf("error = %q, want to contain %q", err.Error(), "bull: GitHub App auth requires all three fields")
			}
		})
	}
}

// Fix #4: Config should reject setting both PAT and App credentials.
func TestParse_BullAuth_BothPATAndApp(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
bull:
  enabled: true
  github_token: "ghp_test123"
  app_id: 12345
  private_key_path: "/path/to/key.pem"
  installation_id: 67890
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error when both PAT and App credentials are set")
	}
	if !strings.Contains(err.Error(), "bull: set github_token or GitHub App credentials, not both") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bull: set github_token or GitHub App credentials, not both")
	}
}

func TestParse_DashboardURL(t *testing.T) {
	data, err := os.ReadFile("testdata/valid_full.yaml")
	if err != nil {
		t.Fatalf("read test data: %v", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.DashboardURL != "https://ry.example.com" {
		t.Errorf("DashboardURL = %q, want %q", cfg.DashboardURL, "https://ry.example.com")
	}
}

func TestParse_DeprecatedDoltKey(t *testing.T) {
	oldConfig := `
owner: alice
repo: git@github.com:org/app.git
dolt:
  host: 10.0.0.5
  port: 3307
tracks:
  - name: backend
    language: go
`
	_, err := Parse([]byte(oldConfig))
	if err == nil {
		t.Fatal("expected error for deprecated 'dolt' key")
	}
	if !strings.Contains(err.Error(), "renamed to 'database'") {
		t.Errorf("error should mention rename: %v", err)
	}
}

func TestParse_DatabaseKeyWorks(t *testing.T) {
	// Ensure the new 'database' key does not trigger the deprecation error.
	cfg, err := Parse([]byte(fullYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Database.Host != "10.0.0.5" {
		t.Errorf("Database.Host = %q, want %q", cfg.Database.Host, "10.0.0.5")
	}
}

func TestDefaults_YardmasterReworkLabel(t *testing.T) {
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
	if cfg.Yardmaster.ReworkLabel != "railyard: rework" {
		t.Errorf("Yardmaster.ReworkLabel = %q, want %q", cfg.Yardmaster.ReworkLabel, "railyard: rework")
	}
}

func TestParse_YardmasterReworkLabelCustom(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
yardmaster:
  rework_label: "needs-rework"
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Yardmaster.ReworkLabel != "needs-rework" {
		t.Errorf("Yardmaster.ReworkLabel = %q, want %q", cfg.Yardmaster.ReworkLabel, "needs-rework")
	}
}

func TestParse_YardmasterReworkLabelEmptyGetsDefault(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
yardmaster:
  health_port: 9090
tracks:
  - name: backend
    language: go
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Yardmaster.ReworkLabel != "railyard: rework" {
		t.Errorf("Yardmaster.ReworkLabel = %q, want default %q", cfg.Yardmaster.ReworkLabel, "railyard: rework")
	}
}

func TestDefaults_YardmasterRevisedLabel(t *testing.T) {
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
	if cfg.Yardmaster.RevisedLabel != "railyard: revised" {
		t.Errorf("Yardmaster.RevisedLabel = %q, want %q", cfg.Yardmaster.RevisedLabel, "railyard: revised")
	}
}

func TestParse_YardmasterRevisedLabelCustom(t *testing.T) {
	yaml := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
yardmaster:
  revised_label: "custom: revised"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Yardmaster.RevisedLabel != "custom: revised" {
		t.Errorf("Yardmaster.RevisedLabel = %q, want %q", cfg.Yardmaster.RevisedLabel, "custom: revised")
	}
}
