package config

import (
	"os"
	"path/filepath"
	"testing"
)

// withMSTConfigHome sets MST_CONFIG_HOME for the duration of the test and
// restores the original value on cleanup.
func withMSTConfigHome(t *testing.T, dir string) {
	t.Helper()
	orig := os.Getenv("MST_CONFIG_HOME")
	os.Setenv("MST_CONFIG_HOME", dir)
	t.Cleanup(func() { os.Setenv("MST_CONFIG_HOME", orig) })
}

func TestLoad_DefaultsOnly(t *testing.T) {
	// Ensure no MST_CONFIG_HOME is set so discovery finds nothing.
	orig := os.Getenv("MST_CONFIG_HOME")
	os.Unsetenv("MST_CONFIG_HOME")
	defer os.Setenv("MST_CONFIG_HOME", orig)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Version != SupportedConfigVersion {
		t.Errorf("version = %d, want %d", cfg.Version, SupportedConfigVersion)
	}
	if cfg.Defaults.Limit != DefaultLimit {
		t.Errorf("limit = %d, want %d", cfg.Defaults.Limit, DefaultLimit)
	}
	if cfg.Defaults.Format != DefaultFormat {
		t.Errorf("format = %q, want %q", cfg.Defaults.Format, DefaultFormat)
	}
	if cfg.Defaults.Timeout != DefaultTimeoutStr {
		t.Errorf("timeout = %q, want %q", cfg.Defaults.Timeout, DefaultTimeoutStr)
	}
}

func TestLoad_VersionEnforcement(t *testing.T) {
	tmpDir := t.TempDir()
	mstDir := filepath.Join(tmpDir, ConfigDir)
	if err := os.MkdirAll(mstDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(mstDir, configFile)

	// Write a config with an unsupported version.
	if err := os.WriteFile(cfgPath, []byte("version: 99\n"), 0644); err != nil {
		t.Fatal(err)
	}

	withMSTConfigHome(t, mstDir)

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for unsupported version, got nil")
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	tmpDir := t.TempDir()
	mstDir := filepath.Join(tmpDir, ConfigDir)
	if err := os.MkdirAll(mstDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(mstDir, configFile)

	if err := os.WriteFile(cfgPath, []byte("version: 1\nengines:\n  brave:\n    weight: [invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	withMSTConfigHome(t, mstDir)

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for malformed YAML, got nil")
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	mstDir := filepath.Join(tmpDir, ConfigDir)
	if err := os.MkdirAll(mstDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(mstDir, configFile)

	content := `version: 1
defaults:
  limit: 20
  format: jsonl
engines:
  brave:
    weight: 3.0
    enabled: true
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	withMSTConfigHome(t, mstDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Defaults.Limit != 20 {
		t.Errorf("limit = %d, want 20", cfg.Defaults.Limit)
	}
	if cfg.Defaults.Format != "jsonl" {
		t.Errorf("format = %q, want \"jsonl\"", cfg.Defaults.Format)
	}
	if cfg.Engines["brave"] == nil {
		t.Fatal("brave engine config is nil")
	}
	if cfg.Engines["brave"].Weight != 3.0 {
		t.Errorf("brave weight = %f, want 3.0", cfg.Engines["brave"].Weight)
	}
}

// TestLoad_MSTConfigHomeDirect verifies MST_CONFIG_HOME points DIRECTLY at the
// mst config directory (like DOCKER_CONFIG), not a base dir: the file lives at
// $MST_CONFIG_HOME/config.yaml, not $MST_CONFIG_HOME/mst/config.yaml.
func TestLoad_MSTConfigHomeDirect(t *testing.T) {
	tmpDir := t.TempDir()
	// Place config.yaml directly in tmpDir (no trailing /mst/).
	cfgPath := filepath.Join(tmpDir, configFile)
	content := "version: 1\ndefaults:\n  limit: 7\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	withMSTConfigHome(t, tmpDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Defaults.Limit != 7 {
		t.Errorf("limit = %d, want 7 (MST_CONFIG_HOME must be read as the config dir directly)", cfg.Defaults.Limit)
	}
}
