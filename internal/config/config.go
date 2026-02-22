// Package config provides YAML-based configuration loading for Railyard.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Config is the top-level Railyard configuration, loaded from config.yaml.
type Config struct {
	Owner             string              `yaml:"owner"`
	Repo              string              `yaml:"repo"`
	BranchPrefix      string              `yaml:"branch_prefix"`
	DefaultBranch     string              `yaml:"default_branch"`
	DefaultAcceptance string              `yaml:"default_acceptance"`
	RequirePR         bool                `yaml:"require_pr"`
	Dolt              DoltConfig          `yaml:"dolt"`
	Stall             StallConfig         `yaml:"stall"`
	Tracks            []TrackConfig       `yaml:"tracks"`
	Notifications     NotificationsConfig `yaml:"notifications"`
	CocoIndex         CocoIndexConfig     `yaml:"cocoindex"`
	Telegraph         TelegraphConfig     `yaml:"telegraph"`
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
	StdoutTimeoutSec  int `yaml:"stdout_timeout_sec"`  // no stdout for N seconds = stall (default 120)
	RepeatedErrorMax  int `yaml:"repeated_error_max"`  // same error N times = stall (default 3)
	MaxClearCycles    int `yaml:"max_clear_cycles"`    // more than N cycles = stall (default 5)
	MaxSwitchFailures int `yaml:"max_switch_failures"` // repeated switch failures before escalation (default 3)
}

// DoltConfig holds connection settings for the Dolt SQL server.
type DoltConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database"`
}

// TrackConfig defines an area of concern within the repo.
type TrackConfig struct {
	Name           string                 `yaml:"name"`
	Language       string                 `yaml:"language"`
	FilePatterns   []string               `yaml:"file_patterns"`
	EngineSlots    int                    `yaml:"engine_slots"`
	PreTestCommand string                 `yaml:"pre_test_command"`
	TestCommand    string                 `yaml:"test_command"`
	Conventions    map[string]interface{} `yaml:"conventions"`
}

// TelegraphConfig holds settings for the Telegraph chat bridge.
type TelegraphConfig struct {
	Platform      string              `yaml:"platform"` // "slack" or "discord"
	Channel       string              `yaml:"channel"`  // default channel ID
	Slack         SlackConfig         `yaml:"slack"`
	Discord       DiscordConfig       `yaml:"discord"`
	DispatchLock  DispatchLockConfig  `yaml:"dispatch_lock"`
	Events        EventsConfig        `yaml:"events"`
	Digest        DigestConfig        `yaml:"digest"`
	Conversations ConversationsConfig `yaml:"conversations"`
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return Parse(data)
}

// Parse unmarshals YAML bytes into a validated Config.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyDefaults fills in derived and default values.
func (c *Config) applyDefaults() {
	if c.BranchPrefix == "" && c.Owner != "" {
		c.BranchPrefix = "ry/" + c.Owner
	}
	if c.Dolt.Host == "" {
		c.Dolt.Host = "127.0.0.1"
	}
	if c.Dolt.Port == 0 {
		c.Dolt.Port = 3306
	}
	if c.Dolt.Database == "" && c.Owner != "" {
		c.Dolt.Database = "railyard_" + c.Owner
	}
	if c.Stall.StdoutTimeoutSec == 0 {
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
	for i := range c.Tracks {
		if c.Tracks[i].EngineSlots == 0 {
			c.Tracks[i].EngineSlots = 3
		}
	}
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
// environment variable value. Unset variables resolve to empty string.
func resolveEnvVars(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		name := envVarRe.FindStringSubmatch(match)[1]
		return os.Getenv(name)
	})
}
