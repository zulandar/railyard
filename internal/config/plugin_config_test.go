package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestParse_StashesUnknownTopLevelKey verifies a config with an unknown
// top-level plugin block (trainmaster) parses successfully and the block is
// stashed in Config.PluginConfigs for later consumption by the plugin host.
func TestParse_StashesUnknownTopLevelKey(t *testing.T) {
	yamlSrc := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
trainmaster:
  enabled: false
  endpoint: "https://tm.example.com"
`
	cfg, err := Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PluginConfigs == nil {
		t.Fatal("PluginConfigs is nil, want map with trainmaster entry")
	}
	node, ok := cfg.PluginConfigs["trainmaster"]
	if !ok {
		t.Fatalf("PluginConfigs missing 'trainmaster' key; got keys: %v", keysOf(cfg.PluginConfigs))
	}
	// The stashed node should be a populated YAML mapping node.
	if node.Kind != yaml.MappingNode {
		t.Errorf("PluginConfigs[trainmaster].Kind = %v, want MappingNode", node.Kind)
	}
	if len(node.Content) == 0 {
		t.Error("PluginConfigs[trainmaster].Content is empty, want populated mapping content")
	}

	// Decode the stashed node into a plugin-defined struct.
	type tmCfg struct {
		Enabled  bool   `yaml:"enabled"`
		Endpoint string `yaml:"endpoint"`
	}
	var tm tmCfg
	if err := node.Decode(&tm); err != nil {
		t.Fatalf("Decode trainmaster node: %v", err)
	}
	if tm.Enabled {
		t.Errorf("trainmaster.Enabled = true, want false")
	}
	if tm.Endpoint != "https://tm.example.com" {
		t.Errorf("trainmaster.Endpoint = %q, want %q", tm.Endpoint, "https://tm.example.com")
	}
}

// TestParse_StashesMultipleUnknownTopLevelKeys verifies multiple unknown
// plugin blocks all land in PluginConfigs.
func TestParse_StashesMultipleUnknownTopLevelKeys(t *testing.T) {
	yamlSrc := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
trainmaster:
  enabled: true
  yard_id: "yard-42"
shipper:
  destination: "remote"
  retries: 3
`
	cfg, err := Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PluginConfigs == nil {
		t.Fatal("PluginConfigs is nil")
	}
	if _, ok := cfg.PluginConfigs["trainmaster"]; !ok {
		t.Errorf("PluginConfigs missing 'trainmaster'; keys: %v", keysOf(cfg.PluginConfigs))
	}
	if _, ok := cfg.PluginConfigs["shipper"]; !ok {
		t.Errorf("PluginConfigs missing 'shipper'; keys: %v", keysOf(cfg.PluginConfigs))
	}

	// Sanity-decode the shipper node.
	type shipperCfg struct {
		Destination string `yaml:"destination"`
		Retries     int    `yaml:"retries"`
	}
	var sc shipperCfg
	shipperNode := cfg.PluginConfigs["shipper"]
	if err := shipperNode.Decode(&sc); err != nil {
		t.Fatalf("Decode shipper node: %v", err)
	}
	if sc.Destination != "remote" {
		t.Errorf("shipper.Destination = %q, want %q", sc.Destination, "remote")
	}
	if sc.Retries != 3 {
		t.Errorf("shipper.Retries = %d, want 3", sc.Retries)
	}
}

// TestParse_NoUnknownKeys_PluginConfigsEmpty verifies that when no plugin
// blocks are present, PluginConfigs is either nil or empty (acceptance allows
// nil zero-value).
func TestParse_NoUnknownKeys_PluginConfigsEmpty(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.PluginConfigs) != 0 {
		t.Errorf("PluginConfigs = %v, want empty (no plugin blocks present)", keysOf(cfg.PluginConfigs))
	}
}

// TestParse_KnownKeys_NotStashedAsPluginConfigs ensures we don't accidentally
// stash known top-level keys (e.g. 'database', 'tracks') in PluginConfigs.
func TestParse_KnownKeys_NotStashedAsPluginConfigs(t *testing.T) {
	cfg, err := Parse([]byte(fullYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, known := range []string{"owner", "repo", "database", "tracks", "branch_prefix", "agent_provider"} {
		if _, ok := cfg.PluginConfigs[known]; ok {
			t.Errorf("PluginConfigs unexpectedly contains known key %q", known)
		}
	}
}

// TestParse_DeprecatedDoltKey_StillReported confirms the deprecated 'dolt'
// key check still fires even with the new stashing logic in place — it must
// produce its specific error rather than being silently stashed.
func TestParse_DeprecatedDoltKey_StillReported_PluginRefactor(t *testing.T) {
	oldConfig := `
owner: alice
repo: git@github.com:org/app.git
dolt:
  host: 10.0.0.5
tracks:
  - name: backend
    language: go
`
	_, err := Parse([]byte(oldConfig))
	if err == nil {
		t.Fatal("expected deprecation error for 'dolt' key")
	}
	if !strings.Contains(err.Error(), "renamed to 'database'") {
		t.Errorf("error = %q, want deprecation message about rename", err.Error())
	}
}

// keysOf is a small helper for stable error messages.
func keysOf(m map[string]yaml.Node) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
