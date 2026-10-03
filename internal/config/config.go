// Package config loads serverctl's user configuration from a YAML file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Valid values for Config.Theme.
const (
	ThemeDark  = "dark"
	ThemeLight = "light"
)

// DefaultRefreshSeconds is how often screens refresh their data by default.
const DefaultRefreshSeconds = 2

// Config holds every user-tunable setting of serverctl.
type Config struct {
	// Theme is the UI palette name: "dark" (default) or "light".
	Theme string `yaml:"theme"`
	// RefreshSeconds is the base interval between data refreshes.
	RefreshSeconds int `yaml:"refresh_seconds"`
	// Keys overrides default keybindings: action name -> key string.
	Keys map[string]string `yaml:"keys"`
}

// Default returns the built-in configuration used when no config file exists.
func Default() Config {
	return Config{
		Theme:          ThemeDark,
		RefreshSeconds: DefaultRefreshSeconds,
		Keys:           map[string]string{},
	}
}

// Path returns the conventional config file location:
// $XDG_CONFIG_HOME/serverctl/config.yaml (or the OS equivalent).
func Path() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(".", "serverctl.yaml")
	}
	return filepath.Join(base, "serverctl", "config.yaml")
}

// Load reads the YAML file at path and merges its values over the defaults,
// so a partial config file keeps every unset option at its default.
// A missing file is not an error: the defaults are returned as-is.
func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var file Config
	if err := yaml.Unmarshal(data, &file); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	if file.Theme != "" {
		cfg.Theme = file.Theme
	}
	if file.RefreshSeconds != 0 {
		cfg.RefreshSeconds = file.RefreshSeconds
	}
	if file.Keys != nil {
		cfg.Keys = file.Keys
	}

	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// validate rejects values that would break the UI or monitoring loop.
func (c Config) validate() error {
	if c.Theme != ThemeDark && c.Theme != ThemeLight {
		return fmt.Errorf("theme must be %q or %q, got %q", ThemeDark, ThemeLight, c.Theme)
	}
	if c.RefreshSeconds < 1 {
		return fmt.Errorf("refresh_seconds must be at least 1, got %d", c.RefreshSeconds)
	}
	return nil
}
