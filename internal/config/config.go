// Package config provides YAML-based configuration loading for Railyard.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level Railyard configuration, loaded from config.yaml.
type Config struct {
	Owner        string        `yaml:"owner"`
	Repo         string        `yaml:"repo"`
	BranchPrefix string        `yaml:"branch_prefix"`
	Dolt         DoltConfig    `yaml:"dolt"`
	Stall        StallConfig   `yaml:"stall"`
	Tracks       []TrackConfig `yaml:"tracks"`
}

// StallConfig holds thresholds for engine stall detection.
type StallConfig struct {
	StdoutTimeoutSec int `yaml:"stdout_timeout_sec"` // no stdout for N seconds = stall (default 120)
	RepeatedErrorMax int `yaml:"repeated_error_max"` // same error N times = stall (default 3)
	MaxClearCycles   int `yaml:"max_clear_cycles"`   // more than N cycles = stall (default 5)
}

// DoltConfig holds connection settings for the Dolt SQL server.
type DoltConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database"`
}

// TrackConfig defines an area of concern within the repo.
type TrackConfig struct {
	Name         string                 `yaml:"name"`
	Language     string                 `yaml:"language"`
	FilePatterns []string               `yaml:"file_patterns"`
	EngineSlots  int                    `yaml:"engine_slots"`
	TestCommand  string                 `yaml:"test_command"`
	Conventions  map[string]interface{} `yaml:"conventions"`
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
	for i := range c.Tracks {
		if c.Tracks[i].EngineSlots == 0 {
			c.Tracks[i].EngineSlots = 3
		}
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
	if len(errs) > 0 {
		return fmt.Errorf("config: validation failed: %s", strings.Join(errs, "; "))
	}
	return nil
}
