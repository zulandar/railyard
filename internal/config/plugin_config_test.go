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

// TestPluginsConfig_NoEntry_StrictDefault verifies that a plugin listed
// in enabled with no per-plugin allow block gets the strict default
// (empty AllowConfig — every advertised cap denied at the matcher).
func TestPluginsConfig_NoEntry_StrictDefault(t *testing.T) {
	yamlSrc := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
plugins:
  enabled: [trainmaster]
`
	cfg, err := Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Plugins.Enabled) != 1 || cfg.Plugins.Enabled[0] != "trainmaster" {
		t.Errorf("Enabled = %v, want [trainmaster]", cfg.Plugins.Enabled)
	}
	if cfg.Plugins.Settings != nil {
		// Settings may be nil OR empty; both are acceptable
		if _, present := cfg.Plugins.Settings["trainmaster"]; present {
			t.Errorf("trainmaster should not be in Settings when no allow block is given")
		}
	}
}

// TestPluginsConfig_EmptyAllow_StrictDefault confirms `allow: {}`
// decodes to an empty AllowConfig.
func TestPluginsConfig_EmptyAllow_StrictDefault(t *testing.T) {
	yamlSrc := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
plugins:
  enabled: [trainmaster]
  trainmaster:
    allow: {}
`
	cfg, err := Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := cfg.Plugins.Settings["trainmaster"]
	if !ok {
		t.Fatal("Settings missing trainmaster entry")
	}
	if len(s.Allow.Events) != 0 || len(s.Allow.Commands) != 0 {
		t.Errorf("Allow = %+v, want zero", s.Allow)
	}
}

// TestPluginsConfig_ExplicitStar parses ["*"] for both events and
// commands.
func TestPluginsConfig_ExplicitStar(t *testing.T) {
	yamlSrc := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
plugins:
  enabled: [trainmaster]
  trainmaster:
    allow:
      events:   ["*"]
      commands: ["*"]
`
	cfg, err := Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := cfg.Plugins.Settings["trainmaster"]
	if len(s.Allow.Events) != 1 || s.Allow.Events[0] != "*" {
		t.Errorf("Events = %v, want [*]", s.Allow.Events)
	}
	if len(s.Allow.Commands) != 1 || s.Allow.Commands[0] != "*" {
		t.Errorf("Commands = %v, want [*]", s.Allow.Commands)
	}
}

// TestPluginsConfig_PrefixWildcard parses "ns.*" as a valid command
// token.
func TestPluginsConfig_PrefixWildcard(t *testing.T) {
	yamlSrc := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
plugins:
  enabled: [trainmaster]
  trainmaster:
    allow:
      events:   [CarMerged, MergeFailed]
      commands: ["dispatch.*", "ping"]
`
	cfg, err := Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := cfg.Plugins.Settings["trainmaster"]
	if len(s.Allow.Events) != 2 {
		t.Errorf("Events = %v, want 2 entries", s.Allow.Events)
	}
	if len(s.Allow.Commands) != 2 {
		t.Errorf("Commands = %v, want 2 entries", s.Allow.Commands)
	}
}

// TestPluginsConfig_PublishAllowList parses an allow.publish list with
// command-style wildcard tokens (railyard-77h.9).
func TestPluginsConfig_PublishAllowList(t *testing.T) {
	yamlSrc := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
plugins:
  enabled: [trainmaster]
  trainmaster:
    allow:
      events:  [CarMerged]
      publish: ["trainmaster.*", "trainmaster.synced"]
`
	cfg, err := Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := cfg.Plugins.Settings["trainmaster"]
	if len(s.Allow.Publish) != 2 {
		t.Errorf("Publish = %v, want 2 entries", s.Allow.Publish)
	}
}

// TestPluginsConfig_RejectsInvalidPublishToken rejects a malformed
// publish wildcard (railyard-77h.9).
func TestPluginsConfig_RejectsInvalidPublishToken(t *testing.T) {
	yamlSrc := `
owner: alice
repo: r
tracks: [{name: t, language: go}]
plugins:
  enabled: [p]
  p:
    allow:
      publish: ["bad*topic"]
`
	_, err := Parse([]byte(yamlSrc))
	if err == nil {
		t.Fatal("expected error for malformed publish token")
	}
	if !strings.Contains(err.Error(), "publish") {
		t.Errorf("error %q should mention the publish field", err.Error())
	}
}

// TestPluginsConfig_RejectsInvalidWildcard catches the malformed shapes
// the brief calls out.
func TestPluginsConfig_RejectsInvalidWildcard(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		wantText string
	}{
		{
			name: "event_with_star_in_middle",
			yaml: `
owner: alice
repo: r
tracks: [{name: t, language: go}]
plugins:
  enabled: [p]
  p:
    allow:
      events: ["Car*Merged"]
`,
			wantText: "events",
		},
		{
			name: "event_double_star",
			yaml: `
owner: alice
repo: r
tracks: [{name: t, language: go}]
plugins:
  enabled: [p]
  p:
    allow:
      events: ["**"]
`,
			wantText: "events",
		},
		{
			name: "command_leading_star",
			yaml: `
owner: alice
repo: r
tracks: [{name: t, language: go}]
plugins:
  enabled: [p]
  p:
    allow:
      commands: ["*x"]
`,
			wantText: "commands",
		},
		{
			name: "command_double_star",
			yaml: `
owner: alice
repo: r
tracks: [{name: t, language: go}]
plugins:
  enabled: [p]
  p:
    allow:
      commands: ["**"]
`,
			wantText: "commands",
		},
		{
			name: "command_inner_star",
			yaml: `
owner: alice
repo: r
tracks: [{name: t, language: go}]
plugins:
  enabled: [p]
  p:
    allow:
      commands: ["foo.*.bar"]
`,
			wantText: "commands",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected parse error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantText)
			}
		})
	}
}

// TestPluginsConfig_MultiplePlugins covers two plugin entries in
// the same plugins: block.
func TestPluginsConfig_MultiplePlugins(t *testing.T) {
	yamlSrc := `
owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: backend
    language: go
plugins:
  enabled: [trainmaster, slack-notifier]
  trainmaster:
    allow:
      events: ["*"]
      commands: ["dispatch.*"]
  slack-notifier:
    allow:
      events: [CarMerged, MergeFailed]
      commands: []
`
	cfg, err := Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := len(cfg.Plugins.Settings), 2; got != want {
		t.Fatalf("Settings count = %d, want %d", got, want)
	}
	tm := cfg.Plugins.Settings["trainmaster"]
	if len(tm.Allow.Events) != 1 || tm.Allow.Events[0] != "*" {
		t.Errorf("trainmaster.Allow.Events = %v", tm.Allow.Events)
	}
	if len(tm.Allow.Commands) != 1 || tm.Allow.Commands[0] != "dispatch.*" {
		t.Errorf("trainmaster.Allow.Commands = %v", tm.Allow.Commands)
	}
	sn := cfg.Plugins.Settings["slack-notifier"]
	if len(sn.Allow.Events) != 2 {
		t.Errorf("slack-notifier.Allow.Events = %v", sn.Allow.Events)
	}
}
