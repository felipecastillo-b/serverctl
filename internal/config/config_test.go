package config_test

import (
	"os"
	"path/filepath"
	"testing"

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
