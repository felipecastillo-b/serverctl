package screens

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// emptyTableView keeps the table header and paints the empty-state
// message in the first data row, padding the rest so the status line
// keeps its place at the bottom of the pane. The header line is taken
// verbatim from the widget's render so it keeps the exact table styling.
// Every table screen shares this body, so an empty listing reads the
// same way everywhere.
func emptyTableView(rendered string, body int, message string, width int, muted lipgloss.Style) string {
	header, _, _ := strings.Cut(rendered, "\n")
	lines := make([]string, 0, body)
	lines = append(lines, header)
	if width > 2 {
		message = ansi.Truncate(message, width-2, "…")
	} else {
		message = ""
	}
	lines = append(lines, muted.Render("  "+message))
	for len(lines) < body {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
