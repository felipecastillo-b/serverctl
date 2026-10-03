// Package help renders the overlay that lists the keybindings active in
// the current context (ARCHITECTURE.md §6).
package help

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// Overlay renders a centered, bordered box listing "key — description" rows
// for the global bindings first and the active screen's bindings second.
// It is a pure function of its inputs: it holds no state of its own.
func Overlay(global []key.Binding, screen []key.Binding, th theme.Theme, width, height int) string {
	headerStyle := lipgloss.NewStyle().Foreground(th.Primary).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(th.Primary)
	descStyle := lipgloss.NewStyle().Foreground(th.Foreground)

	var b strings.Builder
	writeSection := func(title string, bindings []key.Binding) {
		b.WriteString(headerStyle.Render(title))
		for _, binding := range bindings {
			h := binding.Help()
			if !binding.Enabled() || h.Key == "" {
				continue
			}
			b.WriteString("\n  " + keyStyle.Render(h.Key) + descStyle.Render("  —  "+h.Desc))
		}
	}

	writeSection("Global", global)
	if len(screen) > 0 {
		b.WriteString("\n\n")
		writeSection("Screen", screen)
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(th.Border).
		Padding(1, 2).
		Render(b.String())

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
