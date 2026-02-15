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
