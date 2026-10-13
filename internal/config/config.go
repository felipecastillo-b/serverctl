// Package config loads serverctl's user configuration from a YAML file.
package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Valid values for Config.Theme.
const (
	ThemeDark  = "dark"
	ThemeLight = "light"
)

// DefaultRefreshSeconds is how often screens refresh their data by default.
const DefaultRefreshSeconds = 2

// The interval keys of the `intervals` section: one per self-ticked
// collection chain of ARCHITECTURE.md §3. The dotted names spell the
// two-chain screens, Network and Users.
const (
	IntervalServices          = "services"
	IntervalLogs              = "logs"
	IntervalStorage           = "storage"
	IntervalNetworkInterfaces = "network.interfaces"
	IntervalNetworkSockets    = "network.sockets"
	IntervalPackages          = "packages"
	IntervalUsersSessions     = "users.sessions"
	IntervalUsersAttempts     = "users.attempts"
)

// Config holds every user-tunable setting of serverctl.
type Config struct {
	// Theme is the UI palette name: "dark" (default) or "light".
	Theme string `yaml:"theme"`
	// RefreshSeconds is the base interval between data refreshes.
	RefreshSeconds int `yaml:"refresh_seconds"`
	// Keys overrides default keybindings: action name -> key string.
	Keys map[string]string `yaml:"keys"`
	// Intervals overrides the per-chain refresh cadences in seconds:
	// interval key -> seconds. Absent keys keep their defaults; a set
	// key overrides only its own chain (unlike Keys, which replaces
	// the whole map).
	Intervals map[string]int `yaml:"intervals"`
}

// Default returns the built-in configuration used when no config file exists.
func Default() Config {
	return Config{
		Theme:          ThemeDark,
		RefreshSeconds: DefaultRefreshSeconds,
		Keys:           map[string]string{},
		Intervals:      DefaultIntervals(),
	}
}

// DefaultIntervals returns the built-in per-chain refresh cadences in
// seconds: the ARCHITECTURE.md §3 values the screens shipped with
// before they became configurable.
func DefaultIntervals() map[string]int {
	return map[string]int{
		IntervalServices:          5,
		IntervalLogs:              1,
		IntervalStorage:           30,
		IntervalNetworkInterfaces: 2,
		IntervalNetworkSockets:    5,
		IntervalPackages:          30,
		IntervalUsersSessions:     10,
		IntervalUsersAttempts:     30,
	}
}

// Cadence returns the configured refresh interval of the named chain
// as a duration. Keys come from the Interval* constants. A key missing
// from the map reads as the zero duration, which the screen
// constructors replace with the built-in default, so a hand-built
// Config with no intervals still boots on the §3 cadences.
func (c Config) Cadence(key string) time.Duration {
	return time.Duration(c.Intervals[key]) * time.Second
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
	// Intervals merge per key, deliberately unlike the wholesale Keys
	// replace above: a set key overrides only its chain and absent keys
	// keep their defaults, so a partial intervals section never resets
	// the other cadences.
	for key, seconds := range file.Intervals {
		cfg.Intervals[key] = seconds
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
	if err := c.validateIntervals(); err != nil {
		return err
	}
	return nil
}

// validateIntervals rejects interval values that would stall a
// collection chain. Unknown keys fail fast like the theme enum: a
// typo (say `network.interface`) silently keeping a default would be
// invisible to the user — nothing renders the resolved cadences — so
// Load reports it with the valid key list instead. Every set value
// must be at least 1 second, the refresh_seconds precedent.
func (c Config) validateIntervals() error {
	if len(c.Intervals) == 0 {
		return nil
	}
	builtin := DefaultIntervals()
	names := slices.Sorted(maps.Keys(builtin))
	for _, key := range slices.Sorted(maps.Keys(c.Intervals)) {
		if _, ok := builtin[key]; !ok {
			return fmt.Errorf("intervals key %q must be one of: %s", key, strings.Join(names, ", "))
		}
		if c.Intervals[key] < 1 {
			return fmt.Errorf("intervals.%s must be at least 1 second, got %d", key, c.Intervals[key])
		}
	}
	return nil
}
