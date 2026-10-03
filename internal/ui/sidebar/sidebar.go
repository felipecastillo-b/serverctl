// Package sidebar renders the serverctl module switcher and owns its
// selection state.
package sidebar

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// Width is the fixed sidebar column width in terminal cells.
const Width = 24

// selectionMark is the one-cell left border drawn on the selected row.
const selectionMark = "▎"

// Model holds the sidebar entries and the index of the highlighted row.
type Model struct {
	ids      []screens.ID
	titles   []string
	selected int
}

// New builds a sidebar from the screen IDs and their display titles.
// Both slices must have the same length and order, as guaranteed by
// screens.IDs() and screens.All().
func New(ids []screens.ID, titles []string) Model {
	return Model{ids: ids, titles: titles}
}

// Selected returns the ID of the highlighted entry.
func (m Model) Selected() screens.ID {
	if len(m.ids) == 0 {
		return screens.Dashboard
	}
	return m.ids[m.selected]
}

// MoveUp highlights the previous entry, clamped at the top.
func (m *Model) MoveUp() {
	if m.selected > 0 {
		m.selected--
	}
}

// MoveDown highlights the next entry, clamped at the bottom.
func (m *Model) MoveDown() {
	if m.selected < len(m.ids)-1 {
		m.selected++
	}
}

// SelectAtRow highlights the entry drawn at the given row (row 0 is the
// sidebar's top row) and reports its ID. It is the hit-test used by mouse
// clicks; out-of-range rows are ignored and report false.
func (m *Model) SelectAtRow(y int) (screens.ID, bool) {
	if y < 0 || y >= len(m.ids) {
		return screens.Dashboard, false
	}
	m.selected = y
	return m.ids[y], true
}

// SetSelected highlights the entry with the given ID. Unknown IDs are
// ignored so the sidebar never loses its selection.
func (m *Model) SetSelected(id screens.ID) {
	for i, candidate := range m.ids {
		if candidate == id {
			m.selected = i
			return
		}
	}
}

// View renders the sidebar in a fixed-width column of the given height.
// The selected row is highlighted with the primary color and carries a
// one-cell left border; the remaining rows are muted.
func (m Model) View(th theme.Theme, height int) string {
	rowStyle := lipgloss.NewStyle().
		Foreground(th.Muted).
		Width(Width - 1).
		MaxWidth(Width - 1)
	selectedStyle := lipgloss.NewStyle().
		Background(th.Primary).
		Foreground(th.Background).
		Width(Width - 1).
		MaxWidth(Width - 1)
	markStyle := lipgloss.NewStyle().Foreground(th.Primary)

	rows := make([]string, 0, len(m.titles))
	for i, title := range m.titles {
		if i == m.selected {
			rows = append(rows, markStyle.Render(selectionMark)+selectedStyle.Render(" "+title))
			continue
		}
		rows = append(rows, " "+rowStyle.Render(" "+title))
	}

	return lipgloss.NewStyle().
		Width(Width).
		Height(height).
		MaxHeight(height).
		Render(strings.Join(rows, "\n"))
}
