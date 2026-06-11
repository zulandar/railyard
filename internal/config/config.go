// Package config provides YAML-based configuration loading for Railyard.
package config

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/models"
)

var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Config is the top-level Railyard configuration, loaded from config.yaml.
type Config struct {
	Owner   string `yaml:"owner"`
	Repo    string `yaml:"repo"`
	Project string `yaml:"project"`
	// YardID is the stable, operator-configured identifier for this
	// railyard instance. Plugins (notably trainmaster) treat it as
	// distinct from Project: two yards in the same project must have
	// different YardIDs. When unset, internal/pluginhost falls back to
	// Project for backward compatibility — see buildYardInfo and NewHost.
	YardID            string              `yaml:"yard_id"`
	BranchPrefix      string              `yaml:"branch_prefix"`
	DefaultBranch     string              `yaml:"default_branch"`
	DefaultAcceptance string              `yaml:"default_acceptance"`
	RequirePR         bool                `yaml:"require_pr"`
	DashboardURL      string              `yaml:"dashboard_url"`
	Database          DatabaseConfig      `yaml:"database"`
	Stall             StallConfig         `yaml:"stall"`
	Tracks            []TrackConfig       `yaml:"tracks"`
	Notifications     NotificationsConfig `yaml:"notifications"`
	CocoIndex         CocoIndexConfig     `yaml:"cocoindex"`
	Bull              BullConfig          `yaml:"bull"`
	Inspect           InspectConfig       `yaml:"inspect"`
	Telegraph         TelegraphConfig     `yaml:"telegraph"`
	Kubernetes        KubernetesConfig    `yaml:"kubernetes"`
	// MCPServers declares additional MCP servers (keyed by server name) to
	// merge into the .mcp.json written to dispatch/engine worktrees. The
	// name "railyard_cocoindex" is reserved for the built-in codesearch
	// server.
	MCPServers    map[string]MCPServerConfig `yaml:"mcp_servers"`
	AgentProvider string                     `yaml:"agent_provider"`
	// AgentModel selects a specific model for the configured agent provider.
	// Unlike AgentProvider (which defaults to "claude"), AgentModel has no
	// default — empty means "let the provider's CLI choose". The value
	// cascades to BullConfig, InspectConfig, and each TrackConfig when those
	// sections don't set their own. Each provider implementation decides how
	// to apply the value (env var or CLI flag), or ignores it if unsupported.
	AgentModel string `yaml:"agent_model"`
	// Codex holds Codex-CLI-specific settings, applied only when
	// AgentProvider is "codex". Optional — the zero value preserves codex's
	// own defaults.
	Codex CodexConfig `yaml:"codex"`
	// AuthMethod mirrors the chart's `auth.method` value (api_key, oauth_token,
	// bedrock, vertex, foundry, do_inference, openrouter, openai_compat) when
	// running in Kubernetes mode. The chart is responsible for injecting this
	// into the application config; locally it is left unset. It drives startup
	// validation and selects the agent backend: openrouter/openai_compat route
	// agent roles through the Railyard-owned native loop (internal/agentloop)
	// with credentials resolved from the environment, while other methods use
	// the CLI providers. See agentloop.IsNativeLoopMethod.
	AuthMethod string           `yaml:"auth_method"`
	Yardmaster YardmasterConfig `yaml:"yardmaster"`

	// Plugins is the host's plugin-system block. It is read by
	// internal/pluginhost during boot to determine which subprocess plugins
	// to launch from the candidate plugins.d directories. Optional — when
	// the block is absent, no plugins are launched and the host is a
	// pass-through.
	Plugins PluginsConfig `yaml:"plugins"`

	// PluginConfigs holds top-level YAML blocks whose keys are not part of the
	// typed Config schema. Plugins read their own block (keyed by plugin name)
	// and decode the yaml.Node into a plugin-defined struct. Nil when no
	// unknown top-level keys are present in the loaded YAML.
	//
	// PluginConfigs is intentionally NOT tagged with a yaml struct tag — it is
	// populated by the loader from leftover top-level keys, never directly
	// unmarshaled.
	PluginConfigs map[string]yaml.Node `yaml:"-"`
}

// CodexConfig holds settings specific to the Codex CLI provider.
type CodexConfig struct {
	// DispatchArgs are extra arguments inserted into the interactive
	// `ry dispatch` codex invocation, placed before the model flag and
	// prompt (e.g. `codex <dispatch_args...> [--model X] <prompt>`).
	//
	// Default: empty. With no args, dispatch uses codex's own interactive
	// defaults and you approve actions in the attached tmux pane. This is
	// intentional: codex's flag layout varies across versions, and some
	// versions reject --full-auto as a top-level flag. Set this to whatever
	// your installed codex accepts for low-friction execution, e.g.:
	//
	//	dispatch_args: ["--full-auto"]
	//	dispatch_args: ["-c", "approval_policy=on-request", "-c", "sandbox_mode=workspace-write"]
	//
	// Note: non-interactive engine/escalation runs (`codex exec`) always pass
	// --full-auto and are not affected by this setting.
	DispatchArgs []string `yaml:"dispatch_args"`
}

// PluginsConfig configures the railyard plugin subsystem.
//
// Discovery: the host scans three well-known directories in order
//
//  1. /etc/railyard/plugins.d/
//  2. ~/.railyard/plugins/
//  3. ./plugins/  (working directory; dev convenience)
//
// plus any directory under [PluginsConfig.PluginsDir] (highest priority).
// Later paths override earlier on name collision, with a WARN log.
//
// Enabled is the allow-list of plugin names to actually launch. A plugin
// must be both discoverable (executable file found in one of the
// directories) AND listed in Enabled to be launched.
//
// Per-plugin settings (currently just an [AllowConfig] capability allow
// block) live as additional keys under the same `plugins:` mapping. They
// are pulled out by [PluginsConfig.UnmarshalYAML] into [PluginsConfig.Settings]
// keyed by plugin name. Example:
//
//	plugins:
//	  enabled: [trainmaster]
//	  trainmaster:
//	    allow:
//	      events:   ["*"]
//	      commands: ["dispatch.*"]
type PluginsConfig struct {
	// Enabled is the list of plugin names allowed to launch.
	// Names match the executable basename (stripped of any extension).
	Enabled []string `yaml:"enabled"`

	// PluginsDir is an optional additional directory to scan for plugin
	// binaries. When set, it takes precedence over the three default
	// directories on name collision.
	PluginsDir string `yaml:"plugins_dir"`

	// Settings carries per-plugin configuration (currently just the
	// capability allow-list). Keys are plugin names matching entries in
	// Enabled — a setting block for a plugin not listed in Enabled is
	// permitted but has no effect (the plugin will not launch). Plugins
	// not listed here get the strict default: zero-value AllowConfig
	// (everything denied).
	//
	// Populated by [PluginsConfig.UnmarshalYAML] from any keys in the
	// `plugins:` mapping that are not `enabled` or `plugins_dir`.
	Settings map[string]PluginSettings `yaml:"-"`
}

// PluginSettings is the per-plugin configuration block. Future per-plugin
// knobs land here alongside Allow. Plugins continue to read their own
// top-level YAML block (e.g. `trainmaster:` at the document root) via
// Host.Config — those are unrelated to this struct.
type PluginSettings struct {
	// Allow is the capability allow-list for the plugin. Empty struct
	// (the zero value) denies every advertised capability.
	Allow AllowConfig `yaml:"allow"`
}

// AllowConfig is the capability allow-list for one plugin.
//
// Wildcard semantics:
//
//   - Events: each entry must be either "*" (match all topics) or a
//     literal topic name. Prefix wildcards like "Car.*" are NOT supported
//     for events — event topic names do not use a dot namespace today.
//   - Commands: each entry may be "*" (match all), a prefix wildcard
//     "ns.*" (match any command whose name starts with "ns."), or a
//     literal command name.
//
// The same Commands list controls BOTH what a plugin may register
// (provide_commands at Init) AND what it may invoke via
// HostService.DispatchCommand from inside the plugin process. Splitting
// these into two lists is a future bead — the current design matches the
// .4 brief that treats the allow-list as a single capability gate.
type AllowConfig struct {
	Events   []string `yaml:"events"`
	Commands []string `yaml:"commands"`

	// Publish is the set of event topics the plugin may publish onto the
	// bus via HostService.EmitEvent (railyard-77h.9). Topics are
	// namespaced "<plugin>.<name>"; the host independently enforces the
	// caller's own name prefix. Wildcard semantics match Commands: "*"
	// matches all, "ns.*" is a prefix wildcard, otherwise literal.
	// Empty (the zero value) denies all publishing.
	Publish []string `yaml:"publish"`
}

// pluginsConfigRaw is the on-wire shape of `plugins:`. It captures the
// reserved keys typed and stashes the rest as a generic map for
// per-plugin lookup. Used only inside UnmarshalYAML.
type pluginsConfigRaw struct {
	Enabled    []string             `yaml:"enabled"`
	PluginsDir string               `yaml:"plugins_dir"`
	Rest       map[string]yaml.Node `yaml:",inline"`
}

// UnmarshalYAML decodes the `plugins:` block. Reserved keys (`enabled`,
// `plugins_dir`) populate the typed fields; every remaining key is
// decoded into a PluginSettings struct and stored in Settings under the
// key's name.
//
// Validation of allow-list wildcard tokens happens here so a malformed
// entry fails config load with a clear message rather than surfacing
// later at plugin launch time.
func (p *PluginsConfig) UnmarshalYAML(node *yaml.Node) error {
	var raw pluginsConfigRaw
	if err := node.Decode(&raw); err != nil {
		return err
	}
	p.Enabled = raw.Enabled
	p.PluginsDir = raw.PluginsDir
	if len(raw.Rest) == 0 {
		return nil
	}
	p.Settings = make(map[string]PluginSettings, len(raw.Rest))
	for name, n := range raw.Rest {
		var s PluginSettings
		// An empty mapping (e.g. `trainmaster: {}`) decodes to the zero
		// value, which is what we want — strict default allow-list.
		nCopy := n
		if err := nCopy.Decode(&s); err != nil {
			return fmt.Errorf("plugins.%s: %w", name, err)
		}
		if err := validateAllowConfig(name, s.Allow); err != nil {
			return err
		}
		p.Settings[name] = s
	}
	return nil
}

// validateAllowConfig rejects wildcard tokens that are not "*" alone, a
// "prefix.*" suffix wildcard (commands only), or a literal. Anything
// containing a "*" character in any other position is malformed.
func validateAllowConfig(plugin string, a AllowConfig) error {
	for _, e := range a.Events {
		if err := validateEventToken(e); err != nil {
			return fmt.Errorf("plugins.%s.allow.events: %w", plugin, err)
		}
	}
	for _, c := range a.Commands {
		if err := validateCommandToken(c); err != nil {
			return fmt.Errorf("plugins.%s.allow.commands: %w", plugin, err)
		}
	}
	// Publish topics share the command wildcard grammar ("*", "ns.*", or
	// a literal) since plugin-published topics are namespaced like
	// commands (railyard-77h.9).
	for _, p := range a.Publish {
		if err := validateCommandToken(p); err != nil {
			return fmt.Errorf("plugins.%s.allow.publish: %w", plugin, err)
		}
	}
	return nil
}

// validateEventToken accepts "*" alone or a literal topic name with no
// "*" anywhere inside it.
func validateEventToken(tok string) error {
	if tok == "" {
		return fmt.Errorf("empty token")
	}
	if tok == "*" {
		return nil
	}
	if strings.Contains(tok, "*") {
		return fmt.Errorf("invalid event token %q: only \"*\" (match all) or literal names allowed", tok)
	}
	return nil
}

// validateCommandToken accepts "*" alone, a "prefix.*" suffix wildcard,
// or a literal command name. Bare "**", "*x", and "x*y" are rejected.
func validateCommandToken(tok string) error {
	if tok == "" {
		return fmt.Errorf("empty token")
	}
	if tok == "*" {
		return nil
	}
	if strings.HasSuffix(tok, ".*") {
		prefix := tok[:len(tok)-2]
		// The prefix must be a non-empty literal with no further '*'.
		if prefix == "" {
			return fmt.Errorf("invalid command token %q: prefix wildcard requires a non-empty prefix", tok)
		}
		if strings.Contains(prefix, "*") {
			return fmt.Errorf("invalid command token %q: only one trailing \".*\" wildcard allowed", tok)
		}
		return nil
	}
	if strings.Contains(tok, "*") {
		return fmt.Errorf("invalid command token %q: only \"*\", \"prefix.*\", or a literal name allowed", tok)
	}
	return nil
}

// YardmasterConfig holds settings for the yardmaster daemon.
type YardmasterConfig struct {
	HealthPort          int    `yaml:"health_port"`
	AutoMergeOnApproval bool   `yaml:"auto_merge_on_approval"`
	ReworkLabel         string `yaml:"rework_label"`
	RevisedLabel        string `yaml:"revised_label"`
}

// IsKubernetesMode returns true when the config targets a Kubernetes deployment.
func (c *Config) IsKubernetesMode() bool {
	return c.Kubernetes.Namespace != ""
}

// KnownProviders is the set of recognized agent provider names.
var KnownProviders = map[string]bool{
	"claude":  true,
	"gemini":  true,
	"codex":   true,
	"copilot": true,
}

// MethodsRequiringAgentModel is the set of auth methods whose upstream endpoints
// have no implicit default model — a request without one will fail at runtime.
// Enforced in Kubernetes mode by Config.validate().
var MethodsRequiringAgentModel = map[string]bool{
	"do_inference":    true,
	"openrouter":      true,
	"openai_compat":   true,
	"openrouter_skin": true, // Approach B: claude CLI -> OpenRouter skin (no default model)
}

// CocoIndexConfig holds settings for the CocoIndex semantic search integration.
type CocoIndexConfig struct {
	DatabaseURL string        `yaml:"database_url"`
	VenvPath    string        `yaml:"venv_path"`
	ScriptsPath string        `yaml:"scripts_path"`
	Overlay     OverlayConfig `yaml:"overlay"`
}

// OverlayConfig holds settings for per-engine overlay indexing.
type OverlayConfig struct {
	Enabled         bool `yaml:"enabled"`
	MaxChunks       int  `yaml:"max_chunks"`
	AutoRefresh     bool `yaml:"auto_refresh"`
	BuildTimeoutSec int  `yaml:"build_timeout_sec"`
}

// NotificationsConfig controls push notifications for human-targeted messages.
type NotificationsConfig struct {
	Command string `yaml:"command"` // shell command template, e.g. "notify-send 'Railyard' '{{.Subject}}'"
}

// StallConfig holds thresholds for engine stall detection.
type StallConfig struct {
	StdoutTimeoutSec         int `yaml:"stdout_timeout_sec"`         // no stdout for N seconds = stall (default 120)
	RepeatedErrorMax         int `yaml:"repeated_error_max"`         // same error N times = stall (default 3)
	MaxClearCycles           int `yaml:"max_clear_cycles"`           // more than N cycles = stall (default 5)
	MaxSwitchFailures        int `yaml:"max_switch_failures"`        // repeated switch failures before escalation (default 3)
	SwitchTimeoutSec         int `yaml:"switch_timeout_sec"`         // max seconds for switch/runTests (default 600)
	EscalationCooldownSec    int `yaml:"escalation_cooldown_sec"`    // per-car cooldown between escalations (default 600)
	MaxConcurrentEscalations int `yaml:"max_concurrent_escalations"` // limit concurrent escalation goroutines (default 3)
	StaleEngineThresholdSec  int `yaml:"stale_engine_threshold_sec"` // seconds before an engine is considered stale (default 60)
	RateLimitMaxRetries      int `yaml:"rate_limit_max_retries"`     // max consecutive rate-limit retries before stalling (default 3)
	RateLimitMaxWaitSec      int `yaml:"rate_limit_max_wait_sec"`    // max seconds to wait between retries (default 300)
}

// TLSConfig holds TLS settings for encrypted database connections.
type TLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CACert     string `yaml:"ca_cert"`
	ClientCert string `yaml:"client_cert"`
	ClientKey  string `yaml:"client_key"`
	SkipVerify bool   `yaml:"skip_verify"`
}

// DatabaseConfig holds connection settings for the MySQL database server.
type DatabaseConfig struct {
	Host     string    `yaml:"host"`
	Port     int       `yaml:"port"`
	Database string    `yaml:"database"`
	Username string    `yaml:"username"`
	Password string    `yaml:"password"`
	TLS      TLSConfig `yaml:"tls"`
}

// KubernetesConfig holds settings for Kubernetes deployment mode.
type KubernetesConfig struct {
	Namespace       string        `yaml:"namespace"`
	Image           string        `yaml:"image"`
	ImagePullSecret string        `yaml:"image_pull_secret"`
	ServiceAccount  string        `yaml:"service_account"`
	Scaling         ScalingConfig `yaml:"scaling"`
}

// ScalingConfig controls engine auto-scaling in Kubernetes mode.
type ScalingConfig struct {
	MinEngines           int `yaml:"min_engines"`
	MaxEngines           int `yaml:"max_engines"`
	ScaleUpThreshold     int `yaml:"scale_up_threshold"`
	ScaleDownIdleMinutes int `yaml:"scale_down_idle_minutes"`
}

// TrackConfig defines an area of concern within the repo.
type TrackConfig struct {
	Name                  string                   `yaml:"name"`
	Language              string                   `yaml:"language"`
	FilePatterns          []string                 `yaml:"file_patterns"`
	EngineSlots           int                      `yaml:"engine_slots"`
	StallStdoutTimeoutSec int                      `yaml:"stall_stdout_timeout_sec"`
	PreTestCommand        string                   `yaml:"pre_test_command"`
	TestCommand           string                   `yaml:"test_command"`
	Conventions           map[string]interface{}   `yaml:"conventions"`
	AgentProvider         string                   `yaml:"agent_provider"`
	AgentModel            string                   `yaml:"agent_model"`
	Playwright            *models.PlaywrightConfig `yaml:"playwright,omitempty"`
}

// ReservedMCPServerName is the .mcp.json server key Railyard owns for its
// built-in CocoIndex codesearch server. User-configured mcp_servers entries
// may not use it. engine.CocoIndexMCPServerName aliases this value so the
// writers and the config validation cannot drift apart.
const ReservedMCPServerName = "railyard_cocoindex"

// MCPServerConfig declares a user-supplied MCP server to expose to CLI-based
// agent providers. Entries are merged into the .mcp.json written to each
// dispatch/engine worktree, alongside Railyard's own cocoindex server. Only
// providers that discover project .mcp.json files (the claude CLI) consume
// these; the native loop and other provider CLIs ignore them.
type MCPServerConfig struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
}

// BullCommentsConfig controls Bull's issue commenting behavior.
type BullCommentsConfig struct {
	Enabled         bool   `yaml:"enabled"`
	RejectTemplate  string `yaml:"reject_template"`
	AnswerQuestions bool   `yaml:"answer_questions"`
}

// BullLabelsConfig defines the GitHub labels Bull uses for issue triage.
type BullLabelsConfig struct {
	UnderReview string `yaml:"under_review"`
	InProgress  string `yaml:"in_progress"`
	FixMerged   string `yaml:"fix_merged"`
	Ignore      string `yaml:"ignore"`
}

// BullConfig holds settings for the Bull GitHub issue triage daemon.
type BullConfig struct {
	Enabled         bool               `yaml:"enabled"`
	GitHubToken     string             `yaml:"github_token"`
	AppID           int64              `yaml:"app_id"`
	PrivateKeyPath  string             `yaml:"private_key_path"`
	InstallationID  int64              `yaml:"installation_id"`
	PollIntervalSec int                `yaml:"poll_interval_sec"`
	TriageMode      string             `yaml:"triage_mode"`
	AgentProvider   string             `yaml:"agent_provider"`
	AgentModel      string             `yaml:"agent_model"`
	Comments        BullCommentsConfig `yaml:"comments"`
	Labels          BullLabelsConfig   `yaml:"labels"`
}

// InspectLabelsConfig defines the GitHub labels Inspection Pit uses.
type InspectLabelsConfig struct {
	InProgress string `yaml:"in_progress"`
	Reviewed   string `yaml:"reviewed"`
	ReReview   string `yaml:"re_review"`
}

// InspectConfig holds settings for the Inspection Pit PR review daemon.
type InspectConfig struct {
	Enabled          bool                `yaml:"enabled"`
	AppID            int64               `yaml:"app_id"`
	PrivateKeyPath   string              `yaml:"private_key_path"`
	InstallationID   int64               `yaml:"installation_id"`
	PollIntervalSec  int                 `yaml:"poll_interval_sec"`
	AgentProvider    string              `yaml:"agent_provider"`
	AgentModel       string              `yaml:"agent_model"`
	DeepReview       bool                `yaml:"deep_review"`
	ReviewTimeoutSec int                 `yaml:"review_timeout_sec"`
	MaxDiffLines     int                 `yaml:"max_diff_lines"`
	HealthPort       int                 `yaml:"health_port"`
	Labels           InspectLabelsConfig `yaml:"labels"`
}

// TelegraphConfig holds settings for the Telegraph chat bridge.
type TelegraphConfig struct {
	Platform          string              `yaml:"platform"`            // "slack" or "discord"
	Channel           string              `yaml:"channel"`             // default channel ID
	AllowedChannels   []string            `yaml:"allowed_channels"`    // channel IDs the bot may respond in; empty = all
	ProcessTimeoutSec int                 `yaml:"process_timeout_sec"` // max seconds a dispatch subprocess may run; default 900
	HealthPort        int                 `yaml:"health_port"`         // HTTP health check port; default 8086
	Slack             SlackConfig         `yaml:"slack"`
	Discord           DiscordConfig       `yaml:"discord"`
	DispatchLock      DispatchLockConfig  `yaml:"dispatch_lock"`
	Events            EventsConfig        `yaml:"events"`
	Digest            DigestConfig        `yaml:"digest"`
	Conversations     ConversationsConfig `yaml:"conversations"`
}

// SlackConfig holds Slack-specific credentials.
type SlackConfig struct {
	BotToken string `yaml:"bot_token"` // xoxb-...
	AppToken string `yaml:"app_token"` // xapp-...
}

// DiscordConfig holds Discord-specific credentials.
type DiscordConfig struct {
	BotToken  string `yaml:"bot_token"`
	GuildID   string `yaml:"guild_id"`
	ChannelID string `yaml:"channel_id"`
}

// DispatchLockConfig controls the dispatch lock heartbeat and queue.
type DispatchLockConfig struct {
	HeartbeatIntervalSec int `yaml:"heartbeat_interval_sec"` // default 30
	HeartbeatTimeoutSec  int `yaml:"heartbeat_timeout_sec"`  // default 90
	QueueMax             int `yaml:"queue_max"`              // default 5
}

// EventsConfig controls which Railyard events Telegraph posts.
type EventsConfig struct {
	CarLifecycle    bool `yaml:"car_lifecycle"`     // default true
	EngineStalls    bool `yaml:"engine_stalls"`     // default true
	Escalations     bool `yaml:"escalations"`       // default true
	PollIntervalSec int  `yaml:"poll_interval_sec"` // default 15
}

// DigestConfig controls periodic summary messages.
type DigestConfig struct {
	Pulse  DigestSchedule `yaml:"pulse"`
	Daily  DigestSchedule `yaml:"daily"`
	Weekly DigestSchedule `yaml:"weekly"`
}

// DigestSchedule configures a single digest schedule.
type DigestSchedule struct {
	Enabled bool   `yaml:"enabled"`
	Cron    string `yaml:"cron"`
}

// ConversationsConfig controls dispatch conversation behavior.
type ConversationsConfig struct {
	MaxTurns             int `yaml:"max_turns"`              // default 20
	RecoveryLookbackDays int `yaml:"recovery_lookback_days"` // default 7
}

// Load reads a YAML config file from path and returns a validated Config.
func Load(path string) (*Config, error) {
	// Warn if the config file is world-readable (may contain credentials).
	// Skip in Kubernetes — ConfigMap volumes are always mounted 0644.
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		if info, err := os.Stat(path); err == nil {
			if perm := info.Mode().Perm(); perm&0o077 != 0 {
				log.Printf("config: WARNING: %s has permissive permissions %04o (recommended: 0600)", path, perm)
			}
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return Parse(data)
}

// Parse unmarshals YAML bytes into a validated Config.
//
// Top-level keys that are not part of the typed Config schema (i.e. keys
// owned by plugins) are stashed in Config.PluginConfigs for later retrieval
// by the plugin host. Unknown keys are logged at DEBUG and do not fail the
// load.
func Parse(data []byte) (*Config, error) {
	// Detect deprecated 'dolt:' key from pre-rename configs.
	if err := checkDeprecatedKeys(data); err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}

	// Stash unknown top-level keys for plugin consumption. We parse the YAML
	// a second time into a generic yaml.Node so we can walk the document's
	// top-level mapping and identify keys the typed Config struct does not
	// declare. This is purely additive — every existing validation behavior
	// above is preserved.
	if err := cfg.stashPluginConfigs(data); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// stashPluginConfigs walks the top-level YAML mapping and stores any keys
// that aren't part of the typed Config struct into c.PluginConfigs. Unknown
// keys are logged at DEBUG via slog so config typos still surface in dev.
func (c *Config) stashPluginConfigs(data []byte) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		// If the strict re-parse fails we silently bail — the primary
		// unmarshal already succeeded, so there is no actionable error here.
		return nil
	}
	// A document node wraps the actual content. An empty document has no
	// content, which is valid (nothing to stash).
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil
	}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil
	}
	known := knownTopLevelKeys()
	logger := slog.Default()
	// Mapping content alternates [key, value, key, value, ...].
	for i := 0; i+1 < len(top.Content); i += 2 {
		keyNode := top.Content[i]
		valNode := top.Content[i+1]
		if keyNode.Kind != yaml.ScalarNode {
			continue
		}
		key := keyNode.Value
		if known[key] {
			continue
		}
		if c.PluginConfigs == nil {
			c.PluginConfigs = make(map[string]yaml.Node)
		}
		c.PluginConfigs[key] = *valNode
		logger.Debug(fmt.Sprintf("config: unknown top-level key %q ignored (plugin will not load)", key))
	}
	return nil
}

// knownTopLevelKeys returns the set of YAML keys the typed Config struct
// declares. Built via reflection so the set stays in sync with the struct
// definition.
func knownTopLevelKeys() map[string]bool {
	t := reflect.TypeOf(Config{})
	out := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		// yaml tag may include options like ",omitempty" — keep only the name.
		if idx := strings.Index(tag, ","); idx >= 0 {
			tag = tag[:idx]
		}
		if tag != "" {
			out[tag] = true
		}
	}
	return out
}

// checkDeprecatedKeys inspects raw YAML for renamed top-level keys and
// returns a helpful error if any are found.
func checkDeprecatedKeys(data []byte) error {
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil // let the main unmarshal report the parse error
	}
	if _, ok := raw["dolt"]; ok {
		return fmt.Errorf("config: the 'dolt' key has been renamed to 'database' — update your config file:\n\n  # Before\n  dolt:\n    host: ...\n\n  # After\n  database:\n    host: ...")
	}
	return nil
}

// applyDefaults fills in derived and default values.
func (c *Config) applyDefaults() {
	if c.BranchPrefix == "" {
		if c.Project != "" {
			c.BranchPrefix = "ry"
		} else if c.Owner != "" {
			c.BranchPrefix = "ry/" + c.Owner
		}
	}
	if c.Database.Host == "" {
		c.Database.Host = "127.0.0.1"
	}
	if c.Database.Port == 0 {
		c.Database.Port = 3306
	}
	if c.Database.Database == "" && c.Owner != "" {
		c.Database.Database = "railyard_" + c.Owner
	}
	if c.Database.Username == "" {
		c.Database.Username = "root"
	}
	c.Database.Username = resolveEnvVars(c.Database.Username)
	c.Database.Password = resolveEnvVars(c.Database.Password)
	c.Database.TLS.CACert = resolveEnvVars(c.Database.TLS.CACert)
	c.Database.TLS.ClientCert = resolveEnvVars(c.Database.TLS.ClientCert)
	c.Database.TLS.ClientKey = resolveEnvVars(c.Database.TLS.ClientKey)
	// MCP server env blocks typically carry tokens — resolve ${VAR} there
	// like the other credential fields above.
	for _, srv := range c.MCPServers {
		for k, v := range srv.Env {
			srv.Env[k] = resolveEnvVars(v)
		}
	}
	if c.Stall.StdoutTimeoutSec <= 0 {
		c.Stall.StdoutTimeoutSec = 120
	}
	if c.Stall.RepeatedErrorMax == 0 {
		c.Stall.RepeatedErrorMax = 3
	}
	if c.Stall.MaxClearCycles == 0 {
		c.Stall.MaxClearCycles = 5
	}
	if c.Stall.MaxSwitchFailures == 0 {
		c.Stall.MaxSwitchFailures = 3
	}
	if c.Stall.SwitchTimeoutSec == 0 {
		c.Stall.SwitchTimeoutSec = 600
	}
	if c.Stall.EscalationCooldownSec == 0 {
		c.Stall.EscalationCooldownSec = 600
	}
	if c.Stall.MaxConcurrentEscalations == 0 {
		c.Stall.MaxConcurrentEscalations = 3
	}
	if c.Stall.RateLimitMaxRetries == 0 {
		c.Stall.RateLimitMaxRetries = 3
	}
	if c.Stall.RateLimitMaxWaitSec == 0 {
		c.Stall.RateLimitMaxWaitSec = 300
	}
	if c.Yardmaster.HealthPort == 0 {
		c.Yardmaster.HealthPort = 8081
	}
	if c.Yardmaster.ReworkLabel == "" {
		c.Yardmaster.ReworkLabel = "railyard: rework"
	}
	if c.Yardmaster.RevisedLabel == "" {
		c.Yardmaster.RevisedLabel = "railyard: revised"
	}
	if c.AgentProvider == "" {
		c.AgentProvider = "claude"
	}
	if c.Bull.AgentProvider == "" {
		c.Bull.AgentProvider = c.AgentProvider
	}
	// AgentModel inheritance cascade. Unlike AgentProvider (which defaults to
	// "claude"), AgentModel has no default — empty at the top level stays
	// empty. We only propagate a non-empty top-level value down to sub-configs
	// that didn't set their own.
	if c.Bull.AgentModel == "" {
		c.Bull.AgentModel = c.AgentModel
	}
	for i := range c.Tracks {
		if c.Tracks[i].EngineSlots == 0 {
			c.Tracks[i].EngineSlots = 3
		}
		// stall_stdout_timeout_sec — per-track override beats global Stall.StdoutTimeoutSec.
		// The global is guaranteed positive (defaulted to 120 above), so when the track
		// didn't set its own value we just inherit the global directly.
		if c.Tracks[i].StallStdoutTimeoutSec == 0 {
			c.Tracks[i].StallStdoutTimeoutSec = c.Stall.StdoutTimeoutSec
		}
		if c.Tracks[i].AgentProvider == "" {
			c.Tracks[i].AgentProvider = c.AgentProvider
		}
		if c.Tracks[i].AgentModel == "" {
			c.Tracks[i].AgentModel = c.AgentModel
		}
		// Playwright defaults — only apply when the block is present and enabled.
		if pw := c.Tracks[i].Playwright; pw != nil && pw.Enabled {
			if pw.Filename == "" {
				pw.Filename = "{car_id}.spec.ts"
			}
		}
	}
	// Kubernetes defaults — only apply when kubernetes section is present.
	if c.Kubernetes.Namespace != "" || c.Kubernetes.Image != "" {
		if c.Kubernetes.Namespace == "" && c.Project != "" {
			c.Kubernetes.Namespace = "railyard-" + c.Project
		}
		if c.Kubernetes.Scaling.MinEngines == 0 {
			c.Kubernetes.Scaling.MinEngines = 1
		}
		if c.Kubernetes.Scaling.MaxEngines == 0 {
			c.Kubernetes.Scaling.MaxEngines = 10
		}
		if c.Kubernetes.Scaling.ScaleUpThreshold == 0 {
			c.Kubernetes.Scaling.ScaleUpThreshold = 3
		}
		if c.Kubernetes.Scaling.ScaleDownIdleMinutes == 0 {
			c.Kubernetes.Scaling.ScaleDownIdleMinutes = 10
		}
	}
	c.CocoIndex.DatabaseURL = resolveEnvVars(c.CocoIndex.DatabaseURL)
	if c.CocoIndex.VenvPath == "" {
		c.CocoIndex.VenvPath = "cocoindex/.venv"
	}
	if c.CocoIndex.ScriptsPath == "" {
		c.CocoIndex.ScriptsPath = "cocoindex"
	}
	// Overlay defaults: enabled=true unless explicitly set to false via YAML.
	// Since Go zero-values bools as false, we use a pragmatic approach:
	// if the entire cocoindex section is absent (DatabaseURL empty), overlay stays disabled.
	// If cocoindex section is present, overlay defaults to enabled.
	if c.CocoIndex.DatabaseURL != "" && !c.CocoIndex.Overlay.Enabled {
		c.CocoIndex.Overlay.Enabled = true
	}
	if c.CocoIndex.Overlay.MaxChunks == 0 {
		c.CocoIndex.Overlay.MaxChunks = 5000
	}
	if c.CocoIndex.Overlay.BuildTimeoutSec == 0 {
		c.CocoIndex.Overlay.BuildTimeoutSec = 60
	}
	// Bull defaults — only apply when bull is enabled.
	if c.Bull.Enabled {
		c.Bull.GitHubToken = resolveEnvVars(c.Bull.GitHubToken)
		c.Bull.PrivateKeyPath = resolveEnvVars(c.Bull.PrivateKeyPath)
		if c.Bull.PollIntervalSec == 0 {
			c.Bull.PollIntervalSec = 60
		}
		if c.Bull.TriageMode == "" {
			c.Bull.TriageMode = "standard"
		}
		if c.Bull.Labels.UnderReview == "" {
			c.Bull.Labels.UnderReview = "bull: under review"
		}
		if c.Bull.Labels.InProgress == "" {
			c.Bull.Labels.InProgress = "bull: in progress"
		}
		if c.Bull.Labels.FixMerged == "" {
			c.Bull.Labels.FixMerged = "bull: fix merged"
		}
		if c.Bull.Labels.Ignore == "" {
			c.Bull.Labels.Ignore = "bull: ignore"
		}
	}
	// Inspect defaults — only apply when inspect is enabled.
	if c.Inspect.Enabled {
		c.Inspect.PrivateKeyPath = resolveEnvVars(c.Inspect.PrivateKeyPath)
		if c.Inspect.PollIntervalSec == 0 {
			c.Inspect.PollIntervalSec = 60
		}
		if c.Inspect.ReviewTimeoutSec == 0 {
			c.Inspect.ReviewTimeoutSec = 300
		}
		if c.Inspect.MaxDiffLines == 0 {
			c.Inspect.MaxDiffLines = 10000
		}
		if c.Inspect.HealthPort == 0 {
			c.Inspect.HealthPort = 8082
		}
		if c.Inspect.AgentProvider == "" {
			c.Inspect.AgentProvider = c.AgentProvider
		}
		if c.Inspect.AgentModel == "" {
			c.Inspect.AgentModel = c.AgentModel
		}
		if c.Inspect.Labels.InProgress == "" {
			c.Inspect.Labels.InProgress = "inspect: in-progress"
		}
		if c.Inspect.Labels.Reviewed == "" {
			c.Inspect.Labels.Reviewed = "inspect: reviewed"
		}
		if c.Inspect.Labels.ReReview == "" {
			c.Inspect.Labels.ReReview = "inspect: re-review"
		}
	}
	// Telegraph defaults — only apply when telegraph section is present (platform set).
	if c.Telegraph.Platform != "" {
		if c.Telegraph.DispatchLock.HeartbeatIntervalSec == 0 {
			c.Telegraph.DispatchLock.HeartbeatIntervalSec = 30
		}
		if c.Telegraph.DispatchLock.HeartbeatTimeoutSec == 0 {
			c.Telegraph.DispatchLock.HeartbeatTimeoutSec = 90
		}
		if c.Telegraph.DispatchLock.QueueMax == 0 {
			c.Telegraph.DispatchLock.QueueMax = 5
		}
		if c.Telegraph.Events.PollIntervalSec == 0 {
			c.Telegraph.Events.PollIntervalSec = 15
		}
		// Bool defaults for events: true unless explicitly set.
		// Since YAML false and Go zero are the same, we default to true
		// when the platform is configured but events section is absent.
		// If any event field is explicitly set to true, we leave the rest as-is.
		if !c.Telegraph.Events.CarLifecycle && !c.Telegraph.Events.EngineStalls && !c.Telegraph.Events.Escalations {
			c.Telegraph.Events.CarLifecycle = true
			c.Telegraph.Events.EngineStalls = true
			c.Telegraph.Events.Escalations = true
		}
		if c.Telegraph.Conversations.MaxTurns == 0 {
			c.Telegraph.Conversations.MaxTurns = 20
		}
		if c.Telegraph.Conversations.RecoveryLookbackDays == 0 {
			c.Telegraph.Conversations.RecoveryLookbackDays = 7
		}
		if c.Telegraph.ProcessTimeoutSec == 0 {
			c.Telegraph.ProcessTimeoutSec = 900
		}
		if c.Telegraph.HealthPort == 0 {
			c.Telegraph.HealthPort = 8086
		}
		// Resolve env vars in token fields.
		c.Telegraph.Slack.BotToken = resolveEnvVars(c.Telegraph.Slack.BotToken)
		c.Telegraph.Slack.AppToken = resolveEnvVars(c.Telegraph.Slack.AppToken)
		c.Telegraph.Discord.BotToken = resolveEnvVars(c.Telegraph.Discord.BotToken)
	}
}

// validate checks that all required fields are present and consistent.
func (c *Config) validate() error {
	var errs []string
	if c.Owner == "" {
		errs = append(errs, "owner is required")
	}
	if c.Repo == "" {
		errs = append(errs, "repo is required")
	}
	if len(c.Tracks) == 0 {
		errs = append(errs, "at least one track is required")
	}
	for i, t := range c.Tracks {
		if t.Name == "" {
			errs = append(errs, fmt.Sprintf("tracks[%d].name is required", i))
		}
		if t.Language == "" {
			errs = append(errs, fmt.Sprintf("tracks[%d].language is required", i))
		}
		// Playwright validation — only when the block is present and enabled.
		// Template is preserved as-written and not validated for existence here
		// (the file may not yet exist at config-load time).
		if t.Playwright != nil && t.Playwright.Enabled {
			if t.Playwright.SpecPath == "" {
				errs = append(errs, fmt.Sprintf("track %q has playwright.enabled but missing spec_path", t.Name))
			}
		}
	}
	// mcp_servers validation — sorted for deterministic error output.
	mcpNames := make([]string, 0, len(c.MCPServers))
	for name := range c.MCPServers {
		mcpNames = append(mcpNames, name)
	}
	sort.Strings(mcpNames)
	for _, name := range mcpNames {
		if name == ReservedMCPServerName {
			errs = append(errs, fmt.Sprintf("mcp_servers: name %q is reserved for Railyard's built-in codesearch server", ReservedMCPServerName))
			continue
		}
		if c.MCPServers[name].Command == "" {
			errs = append(errs, fmt.Sprintf("mcp_servers[%q]: command is required", name))
		}
	}
	// Kubernetes validation (only when namespace or image is set).
	if c.Kubernetes.Namespace != "" || c.Kubernetes.Image != "" {
		if c.Kubernetes.Image == "" {
			errs = append(errs, "kubernetes.image is required when kubernetes is configured")
		}
	}
	// agent_model is required when the upstream API has no implicit default.
	// Native-loop methods (openrouter / openai_compat) need it in ANY mode —
	// the API rejects requests with an empty model — so local operators get
	// a clear startup error instead of a deferred 400 on the first triage /
	// dispatch / engine call. Other entries in MethodsRequiringAgentModel
	// (do_inference, openrouter_skin) route through CLI providers that the
	// chart configures; local operators manage those themselves, so the
	// requirement stays K8s-only there.
	requireAgentModel := MethodsRequiringAgentModel[c.AuthMethod] &&
		(c.IsKubernetesMode() || agentloop.IsNativeLoopMethod(c.AuthMethod))
	if requireAgentModel && c.AgentModel == "" {
		// applyDefaults cascades the top-level agent_model into Bull/Inspect/Tracks
		// only when those are empty, so a config that sets models per-role (with no
		// top-level default) is valid. Flag only the roles whose effective model is
		// still empty; if no role is configured, require the top-level default.
		var missing []string
		for _, t := range c.Tracks {
			if t.AgentModel == "" {
				missing = append(missing, fmt.Sprintf("tracks[%q]", t.Name))
			}
		}
		if c.Bull.Enabled && c.Bull.AgentModel == "" {
			missing = append(missing, "bull")
		}
		if c.Inspect.Enabled && c.Inspect.AgentModel == "" {
			missing = append(missing, "inspect")
		}
		if len(c.Tracks) == 0 && !c.Bull.Enabled && !c.Inspect.Enabled {
			missing = append(missing, "agent_model (top level)")
		}
		if len(missing) > 0 {
			errs = append(errs, fmt.Sprintf(
				"agent_model is required when auth_method is %s (no implicit default model); missing for: %s",
				c.AuthMethod, strings.Join(missing, ", "),
			))
		}
	}
	// openrouter / openai_compat select the Railyard-owned native agent loop
	// (internal/agentloop), so agent_provider is irrelevant for those methods —
	// no specific CLI provider is required. Their credentials come from the
	// environment (the chart injects them), so in Kubernetes mode require the
	// API key (and base URL) to be present, with an actionable error.
	if c.IsKubernetesMode() && agentloop.IsNativeLoopMethod(c.AuthMethod) {
		if err := agentloop.ValidateEnv(c.AuthMethod); err != nil {
			errs = append(errs, err.Error())
		}
	}
	// Bull validation (only when enabled).
	if c.Bull.Enabled {
		// Auth: require either a PAT (github_token) or complete GitHub App credentials, not both.
		hasPAT := c.Bull.GitHubToken != ""
		hasApp := c.Bull.AppID != 0 && c.Bull.PrivateKeyPath != "" && c.Bull.InstallationID != 0
		partialApp := (c.Bull.AppID != 0 || c.Bull.PrivateKeyPath != "" || c.Bull.InstallationID != 0) && !hasApp
		if hasPAT && hasApp {
			errs = append(errs, "bull: set github_token or GitHub App credentials, not both")
		} else if partialApp {
			errs = append(errs, "bull: GitHub App auth requires all three fields: app_id, private_key_path, and installation_id")
		} else if !hasPAT && !hasApp {
			errs = append(errs, "bull: authentication is required; set github_token or all of app_id, private_key_path, and installation_id")
		}
		switch c.Bull.TriageMode {
		case "standard", "full":
			// valid
		default:
			errs = append(errs, fmt.Sprintf("bull.triage_mode %q is not supported (use standard or full)", c.Bull.TriageMode))
		}
	}
	// Inspect validation (only when enabled).
	if c.Inspect.Enabled {
		hasApp := c.Inspect.AppID != 0 && c.Inspect.PrivateKeyPath != "" && c.Inspect.InstallationID != 0
		partialApp := (c.Inspect.AppID != 0 || c.Inspect.PrivateKeyPath != "" || c.Inspect.InstallationID != 0) && !hasApp
		if partialApp {
			errs = append(errs, "inspect: GitHub App auth requires all three fields: app_id, private_key_path, and installation_id")
		} else if !hasApp {
			errs = append(errs, "inspect: GitHub App authentication is required; set app_id, private_key_path, and installation_id")
		}
	}
	// Telegraph validation (only when platform is configured).
	if c.Telegraph.Platform != "" {
		switch c.Telegraph.Platform {
		case "slack":
			if c.Telegraph.Slack.BotToken == "" {
				errs = append(errs, "telegraph.slack.bot_token is required when platform is slack")
			}
			if c.Telegraph.Slack.AppToken == "" {
				errs = append(errs, "telegraph.slack.app_token is required when platform is slack")
			}
		case "discord":
			if c.Telegraph.Discord.BotToken == "" {
				errs = append(errs, "telegraph.discord.bot_token is required when platform is discord")
			}
		default:
			errs = append(errs, fmt.Sprintf("telegraph.platform %q is not supported (use slack or discord)", c.Telegraph.Platform))
		}
		if c.Telegraph.Channel == "" {
			errs = append(errs, "telegraph.channel is required")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: validation failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

// resolveEnvVars replaces ${VAR_NAME} tokens in s with the corresponding
// environment variable value. Logs a warning for unset variables.
func resolveEnvVars(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		name := envVarRe.FindStringSubmatch(match)[1]
		val, ok := os.LookupEnv(name)
		if !ok {
			log.Printf("config: WARNING: environment variable %s is not set (referenced as ${%s})", name, name)
		}
		return val
	})
}
