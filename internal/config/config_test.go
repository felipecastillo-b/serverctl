package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/config"
)

// writeFixture writes contents to a temp config file and returns its path.
func writeFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	want := config.Default()
	if cfg.Theme != want.Theme || cfg.RefreshSeconds != want.RefreshSeconds || len(cfg.Keys) != len(want.Keys) {
		t.Fatalf("expected defaults %+v, got %+v", want, cfg)
	}
}

func TestLoadValidFile(t *testing.T) {
	path := writeFixture(t, `
theme: light
refresh_seconds: 5
keys:
  quit: x
`)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Theme != config.ThemeLight {
		t.Errorf("theme: want %q, got %q", config.ThemeLight, cfg.Theme)
	}
	if cfg.RefreshSeconds != 5 {
		t.Errorf("refresh: want 5, got %d", cfg.RefreshSeconds)
	}
	if cfg.Keys["quit"] != "x" {
		t.Errorf("keys: want quit->x, got %v", cfg.Keys)
	}
}

func TestLoadPartialFileMergesOverDefaults(t *testing.T) {
	path := writeFixture(t, "theme: light\n")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Theme != config.ThemeLight {
		t.Errorf("theme should come from file, got %q", cfg.Theme)
	}
	if cfg.RefreshSeconds != config.DefaultRefreshSeconds {
		t.Errorf("refresh should stay default, got %d", cfg.RefreshSeconds)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed yaml", "theme: [unclosed"},
		{"unknown theme", "theme: blue\n"},
		{"refresh below one", "refresh_seconds: -3\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := config.Load(writeFixture(t, tt.body)); err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestPathFallsBackInsideUserConfigDir(t *testing.T) {
	path := config.Path()
	if filepath.Base(path) != "config.yaml" {
		t.Errorf("expected config.yaml basename, got %q", path)
	}
}

func TestLoadIntervalsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	want := config.DefaultIntervals()
	if len(cfg.Intervals) != len(want) {
		t.Fatalf("intervals: want %d default keys, got %d (%v)", len(want), len(cfg.Intervals), cfg.Intervals)
	}
	for key, seconds := range want {
		if cfg.Intervals[key] != seconds {
			t.Errorf("intervals.%s = %d, want the default %d", key, cfg.Intervals[key], seconds)
		}
	}
}

func TestLoadIntervalsPerKeyMerge(t *testing.T) {
	// Only two chains are set: they override, every other key keeps
	// its default — the per-key merge, unlike the wholesale Keys
	// replace.
	path := writeFixture(t, `
intervals:
  services: 3
  network.interfaces: 4
`)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Intervals["services"] != 3 {
		t.Errorf("intervals.services = %d, want 3 from the file", cfg.Intervals["services"])
	}
	if cfg.Intervals["network.interfaces"] != 4 {
		t.Errorf("intervals.network.interfaces = %d, want 4 from the file", cfg.Intervals["network.interfaces"])
	}
	for key, seconds := range config.DefaultIntervals() {
		if key == "services" || key == "network.interfaces" {
			continue
		}
		if cfg.Intervals[key] != seconds {
			t.Errorf("intervals.%s = %d, want the untouched default %d", key, cfg.Intervals[key], seconds)
		}
	}
}

func TestLoadIntervalsRoundTrip(t *testing.T) {
	path := writeFixture(t, `
intervals:
  services: 7
  logs: 8
  storage: 9
  network.interfaces: 11
  network.sockets: 12
  packages: 13
  users.sessions: 14
  users.attempts: 15
  containers: 16
`)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]int{
		"services":           7,
		"logs":               8,
		"storage":            9,
		"network.interfaces": 11,
		"network.sockets":    12,
		"packages":           13,
		"users.sessions":     14,
		"users.attempts":     15,
		"containers":         16,
	}
	if len(cfg.Intervals) != len(want) {
		t.Fatalf("intervals: want %d keys, got %d (%v)", len(want), len(cfg.Intervals), cfg.Intervals)
	}
	for key, seconds := range want {
		if cfg.Intervals[key] != seconds {
			t.Errorf("intervals.%s = %d, want %d", key, cfg.Intervals[key], seconds)
		}
		if got := cfg.Cadence(key); got != time.Duration(seconds)*time.Second {
			t.Errorf("Cadence(%q) = %v, want %v", key, got, time.Duration(seconds)*time.Second)
		}
	}
}

func TestLoadInvalidIntervals(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"unknown key", "intervals:\n  network.interface: 3\n"},
		{"zero seconds", "intervals:\n  services: 0\n"},
		{"negative seconds", "intervals:\n  users.attempts: -5\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(writeFixture(t, tt.body))
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestCadenceMissingKeyIsZero(t *testing.T) {
	// A hand-built Config with no intervals reads as zero, which the
	// screen constructors replace with the built-in defaults.
	var cfg config.Config
	if got := cfg.Cadence(config.IntervalServices); got != 0 {
		t.Errorf("Cadence on an empty config = %v, want 0", got)
	}
}
