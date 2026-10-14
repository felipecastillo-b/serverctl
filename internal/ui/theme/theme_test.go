package theme_test

import (
	"math"
	"strconv"
	"strings"
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

// TestMutedDiffersBetweenPalettes pins the m7b light-contrast fix:
// the light palette must not reuse the dark palette's muted hex, which
// sat at borderline contrast on the light background.
func TestMutedDiffersBetweenPalettes(t *testing.T) {
	if theme.Dark().Muted == theme.Light().Muted {
		t.Error("dark and light palettes must use different muted colors")
	}
}

// TestMutedContrastOnBothBackgrounds pins why the muted role can
// carry secondary text and the dashboard bar's empty cells: WCAG
// contrast of at least 3:1 on each palette's own background (the AA
// threshold for large-size text), a property the border role lacks on
// the light palette.
func TestMutedContrastOnBothBackgrounds(t *testing.T) {
	for _, th := range []theme.Theme{theme.Dark(), theme.Light()} {
		ratio := contrastRatio(th.Muted, th.Background)
		if ratio < 3 {
			t.Errorf("%s muted contrast = %.2f:1, want >= 3:1", th.Name, ratio)
		}
	}
}

// contrastRatio computes the WCAG 2.x contrast ratio of two colors:
// relative luminance per channel (linearized sRGB), lighter over
// darker with the 0.05 offsets.
func contrastRatio(a, b lipgloss.Color) float64 {
	la, ok := relativeLuminance(a)
	lb, ok2 := relativeLuminance(b)
	if !ok || !ok2 {
		return 0
	}
	lighter, darker := la, lb
	if lb > la {
		lighter, darker = lb, la
	}
	return (lighter + 0.05) / (darker + 0.05)
}

// relativeLuminance returns the WCAG relative luminance of a hex
// color; non-hex colors report failure, which surfaces as a zero
// contrast ratio in the calling test.
func relativeLuminance(c lipgloss.Color) (float64, bool) {
	hex := strings.TrimPrefix(string(c), "#")
	if len(hex) != 6 {
		return 0, false
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, false
	}
	r := linearize(float64(v>>16&0xff) / 255)
	g := linearize(float64(v>>8&0xff) / 255)
	b := linearize(float64(v&0xff) / 255)
	return 0.2126*r + 0.7152*g + 0.0722*b, true
}

// linearize maps one sRGB channel to its WCAG luminance space.
func linearize(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
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
