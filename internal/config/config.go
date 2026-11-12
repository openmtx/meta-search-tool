// Package config handles discovery, loading, and precedence merging of the
// user configuration file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// SupportedConfigVersion is the only supported config version.
	SupportedConfigVersion = 1
	// ConfigDir is the directory under XDG_CONFIG_HOME / ~/.config where mst
	// looks for its configuration file.
	ConfigDir  = "mst"
	configFile = "config.yaml"

	// DefaultLimit is the default number of merged results to return.
	DefaultLimit = 10
	// DefaultTimeout is the default global timeout for a search run.
	DefaultTimeoutStr = "8s"
	// DefaultFormat is the default output format.
	DefaultFormat = "json"
)

// Config represents the merged configuration after applying all precedence
// layers (built-in defaults → config file → env vars → CLI flags).
type Config struct {
	Version  int           `yaml:"version"`
	Defaults Defaults      `yaml:"defaults"`
	Engines  EngineConfigs `yaml:"engines"`
}

// Defaults holds top-level default settings.
type Defaults struct {
	Limit   int    `yaml:"limit"`
	Format  string `yaml:"format"`
	Timeout string `yaml:"timeout"`
}

// EngineConfig holds per-engine configuration from the config file.
type EngineConfig struct {
	Weight        float64 `yaml:"weight"`
	Enabled       *bool   `yaml:"enabled"`
	APIKeyEnv     string  `yaml:"api_key_env"`
	CXEnv         string  `yaml:"cx_env"`
	Timeout       string  `yaml:"timeout"`
	IncludeAnswer *bool   `yaml:"include_answer,omitempty"` // Tavily: request AI-generated answer
	SearchDepth   string  `yaml:"search_depth,omitempty"`   // Tavily: "basic" or "advanced"
	JinaEngine    string  `yaml:"engine,omitempty"`         // Jina: "reader" (default), "google", "bing"
}

// EngineConfigs maps engine names to their config entries.
type EngineConfigs map[string]*EngineConfig

// Load discovers and loads the user config file, returning a Config with
// built-in defaults filled in for any missing fields. A missing config file
// is not an error — defaults are used. An existing but unreadable or malformed
// YAML file causes an error (exit 2).
func Load() (*Config, error) {
	cfg := &Config{
		Version: SupportedConfigVersion,
		Defaults: Defaults{
			Limit:   DefaultLimit,
			Format:  DefaultFormat,
			Timeout: DefaultTimeoutStr,
		},
		Engines: make(EngineConfigs),
	}

	path := discoverPath()
	if path == "" {
		// No config file found; return defaults only.
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unreadable config %s: %w", path, err)
	}

	var fileCfg Config
	if err := yamlUnmarshal(data, &fileCfg); err != nil {
		return nil, fmt.Errorf("malformed config %s: %w", path, err)
	}

	// Merge: file values override defaults.
	if fileCfg.Version != 0 && fileCfg.Version != SupportedConfigVersion {
		return nil, fmt.Errorf("unsupported config version %d (expected %d)", fileCfg.Version, SupportedConfigVersion)
	}
	cfg.Version = fileCfg.Version // 0 → SupportedConfigVersion handled by caller

	if fileCfg.Defaults.Limit > 0 {
		cfg.Defaults.Limit = fileCfg.Defaults.Limit
	}
	if fileCfg.Defaults.Format != "" {
		cfg.Defaults.Format = fileCfg.Defaults.Format
	}
	if fileCfg.Defaults.Timeout != "" {
		cfg.Defaults.Timeout = fileCfg.Defaults.Timeout
	}

	for name, ec := range fileCfg.Engines {
		cfg.Engines[name] = ec
	}

	return cfg, nil
}

// discoverPath returns the path to the config file, or "" if not found.
//
// mst owns its config location via MST_CONFIG_HOME and does NOT read XDG
// desktop-environment variables (decision 2026-07-07, folded into spec §6).
// MST_CONFIG_HOME points directly at the mst config directory, so the file is
// $MST_CONFIG_HOME/config.yaml. When unset/empty, fall back to
// ~/.config/mst/config.yaml. A missing file is not an error.
func discoverPath() string {
	dir := os.Getenv("MST_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config", ConfigDir)
	}

	path := filepath.Join(dir, configFile)

	// Only return if the file exists.
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

// yamlUnmarshal wraps yaml.Unmarshal.
func yamlUnmarshal(data []byte, v any) error {
	return unmarshalYAML(data, v)
}
