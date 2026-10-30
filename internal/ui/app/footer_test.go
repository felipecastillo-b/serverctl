package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestFitFooterHints pins the whole-hint fitter: hints are never cut
// mid-word, separators only sit between included hints, and the footer
// never exceeds the terminal width. Widths are derived from the hints
// themselves where the case is about an exact boundary, so the test does
// not encode the default keymap.
func TestFitFooterHints(t *testing.T) {
	parts := []string{"q quit", "ctrl+p palette", "tab next module"}
	all := " " + strings.Join(parts, footerSep)

	tests := []struct {
		name   string
		parts  []string
		width  int
		want   string
		absent []string
	}{
		{"zero width renders nothing", parts, 0, "", parts},
		{"width below the first hint renders nothing", parts, lipgloss.Width(" "+parts[0]) - 1, "", parts},
		{"exact fit of the first hint", parts, lipgloss.Width(" " + parts[0]), " " + parts[0], parts[1:]},
		{
			name:   "narrow width drops later hints whole",
			parts:  parts,
			width:  lipgloss.Width(" "+parts[0]) + 4,
			want:   " " + parts[0],
			absent: []string{"ctrl", "palette", "next module"},
		},
		{
			name:  "exact fit of two hints",
			parts: parts,
			width: lipgloss.Width(" " + parts[0] + footerSep + parts[1]),
			want:  " " + parts[0] + footerSep + parts[1],
		},
		{"wide width joins every hint", parts, 100, all, nil},
		{"no parts renders nothing", nil, 100, "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fitFooterHints(tt.parts, tt.width)
			if got != tt.want {
				t.Errorf("fitFooterHints(%q, %d) = %q, want %q", tt.parts, tt.width, got, tt.want)
			}
			if w := lipgloss.Width(got); w > tt.width {
				t.Errorf("fitFooterHints(%q, %d) = %q renders at %d columns", tt.parts, tt.width, got, w)
			}
			if strings.HasSuffix(got, footerSep) {
				t.Errorf("fitFooterHints(%q, %d) = %q ends with a separator", tt.parts, tt.width, got)
			}
			if got != "" && !strings.HasPrefix(got, " ") {
				t.Errorf("fitFooterHints(%q, %d) = %q lost the footer padding", tt.parts, tt.width, got)
			}
			for _, fragment := range tt.absent {
				if strings.Contains(got, fragment) {
					t.Errorf("fitFooterHints(%q, %d) = %q rendered the dropped hint fragment %q",
						tt.parts, tt.width, got, fragment)
				}
			}
		})
	}
}

// TestFitFooterHintsNeverExceedsWidth sweeps representative widths: the
// fitted footer must always fit, whatever slice of the hints it keeps.
func TestFitFooterHintsNeverExceedsWidth(t *testing.T) {
	parts := []string{"q quit", "ctrl+p palette", "tab next module", "? help", "esc back"}
	for width := 0; width <= 80; width++ {
		got := fitFooterHints(parts, width)
		if w := lipgloss.Width(got); w > width {
			t.Fatalf("width %d rendered %q at %d columns", width, got, w)
		}
	}
}
