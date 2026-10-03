// Package theme defines the dark and light color palettes of serverctl.
// Every UI component derives its lipgloss styles from a Theme.
package theme

import "github.com/charmbracelet/lipgloss"

// Theme is the color set shared by all UI components.
type Theme struct {
	// Name identifies the palette: "dark" or "light".
	Name       string
	Background lipgloss.Color
	Foreground lipgloss.Color
	Primary    lipgloss.Color
	Secondary  lipgloss.Color
	Accent     lipgloss.Color
	Success    lipgloss.Color
	Warning    lipgloss.Color
	Danger     lipgloss.Color
	Muted      lipgloss.Color
	Border     lipgloss.Color
}

// Dark returns the default palette: deep slate background, soft light text,
// cool primary tones with warm accents for warnings.
func Dark() Theme {
	return Theme{
		Name:       "dark",
		Background: lipgloss.Color("#0f172a"),
		Foreground: lipgloss.Color("#e2e8f0"),
		Primary:    lipgloss.Color("#38bdf8"),
		Secondary:  lipgloss.Color("#818cf8"),
		Accent:     lipgloss.Color("#22d3ee"),
		Success:    lipgloss.Color("#34d399"),
		Warning:    lipgloss.Color("#fbbf24"),
		Danger:     lipgloss.Color("#f87171"),
		Muted:      lipgloss.Color("#64748b"),
		Border:     lipgloss.Color("#334155"),
	}
}

// Light returns the light palette: off-white background, dark text,
// with the same accent family adjusted for contrast.
func Light() Theme {
	return Theme{
		Name:       "light",
		Background: lipgloss.Color("#f8fafc"),
		Foreground: lipgloss.Color("#1e293b"),
		Primary:    lipgloss.Color("#0284c7"),
		Secondary:  lipgloss.Color("#4f46e5"),
		Accent:     lipgloss.Color("#0891b2"),
		Success:    lipgloss.Color("#059669"),
		Warning:    lipgloss.Color("#b45309"),
		Danger:     lipgloss.Color("#dc2626"),
		Muted:      lipgloss.Color("#64748b"),
		Border:     lipgloss.Color("#cbd5e1"),
	}
}

// FromName maps a config theme name to its palette; unknown names fall back
// to Dark. Config validation already rejects unknown names, so this is a
// defensive default.
func FromName(name string) Theme {
	if name == "light" {
		return Light()
	}
	return Dark()
}
