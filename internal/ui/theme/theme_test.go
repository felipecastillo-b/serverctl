package theme_test

import (
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

func TestFromName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"dark maps to dark", "dark", "dark"},
		{"light maps to light", "light", "light"},
		{"unknown falls back to dark", "neon", "dark"},
		{"empty falls back to dark", "", "dark"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := theme.FromName(tt.input); got.Name != tt.want {
				t.Errorf("FromName(%q).Name = %q, want %q", tt.input, got.Name, tt.want)
			}
		})
	}
}

func TestPalettesAreDistinct(t *testing.T) {
	dark, light := theme.Dark(), theme.Light()
	if dark.Background == light.Background || dark.Foreground == light.Foreground {
		t.Error("dark and light palettes must differ in background and foreground")
	}
}

func TestNoZeroColors(t *testing.T) {
	zero := lipgloss.Color("")

	for _, th := range []theme.Theme{theme.Dark(), theme.Light()} {
		colors := map[string]lipgloss.Color{
			"Background": th.Background,
			"Foreground": th.Foreground,
			"Primary":    th.Primary,
			"Secondary":  th.Secondary,
			"Accent":     th.Accent,
			"Success":    th.Success,
			"Warning":    th.Warning,
			"Danger":     th.Danger,
			"Muted":      th.Muted,
			"Border":     th.Border,
		}
		for field, color := range colors {
			if color == zero {
				t.Errorf("theme %s: %s must not be empty", th.Name, field)
			}
		}
	}
}
