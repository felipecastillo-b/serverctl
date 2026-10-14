// Package palette renders the command palette overlay: a filter input
// above a fuzzy result list over the module titles (ARCHITECTURE.md
// §6). While the palette is open it is an exclusive modal, like the
// help overlay: the shell routes every key and mouse event to it and
// swallows everything else.
//
// The palette is keyboard-only. The §6 parity rule requires every mouse
// action to have a keyboard equivalent, not the reverse, so a modal
// without mouse handling is legal; adding row clicks later would not
// change the keyboard contract.
package palette

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// Action tells the shell what a keypress requires from it. The palette
// never jumps or closes on its own: the shell owns modality and the
// module switch.
type Action int

const (
	// ActionNone: the key was claimed by the filter or swallowed; the
	// shell only forwards the returned command.
	ActionNone Action = iota
	// ActionSelect: enter was pressed with a highlighted result; the
	// shell jumps to SelectedIndex().
	ActionSelect
	// ActionClose: esc was pressed; the shell closes the palette.
	ActionClose
)

// Model is the command palette modal, in the confirm.Model shape:
// pointer receivers mutate, value receivers read. The zero value is
// not usable; build it with New. The candidate titles are the module
// titles in catalog order; results are indices into that slice, so the
// shell can map them back to screens without this package depending on
// the screens package.
type Model struct {
	titles []string
	filter textinput.Model
	// results holds the indices of the titles matching the filter, best
	// match first (ties keep catalog order).
	results []int
	// cursor is the position in results of the highlighted row.
	cursor int
	open   bool
	theme  theme.Theme
}

// New builds a closed palette over the module titles in catalog order.
func New(titles []string, th theme.Theme) Model {
	// The textinput mirrors the processes screen's filter
	// (processes.go): short prompt, bounded input, fixed width.
	filter := textinput.New()
	filter.Prompt = "> "
	filter.Placeholder = "type to filter"
	filter.CharLimit = 64
	filter.Width = 24
	return Model{titles: titles, filter: filter, theme: th}
}

// Open reports whether the palette is currently shown.
func (m Model) Open() bool { return m.open }

// Show opens the palette with a cleared filter and the cursor on the
// first result. The textinput focus command is discarded, mirroring the
// processes screen's filter: the cursor renders, it just never blinks.
func (m *Model) Show() {
	m.open = true
	m.filter.SetValue("")
	_ = m.filter.Focus()
	m.cursor = 0
	m.results = Search("", m.titles)
}

// Hide closes the palette without selecting a module.
func (m *Model) Hide() {
	m.open = false
	m.filter.Blur()
}

// UpdateKey consumes one keypress while the palette is open. Filter
// characters go to the textinput; up/down move the cursor with wrap;
// enter reports the highlighted result; esc reports a close. Every
// other key is swallowed: the modal is exclusive. The returned command
// comes from the textinput and is forwarded by the shell.
func (m *Model) UpdateKey(msg tea.KeyMsg) (Action, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return ActionClose, nil
	case "enter":
		if len(m.results) == 0 {
			return ActionNone, nil
		}
		return ActionSelect, nil
	case "up":
		if len(m.results) > 0 {
			m.cursor = (m.cursor - 1 + len(m.results)) % len(m.results)
		}
		return ActionNone, nil
	case "down":
		if len(m.results) > 0 {
			m.cursor = (m.cursor + 1) % len(m.results)
		}
		return ActionNone, nil
	}

	// Everything else edits the filter; a changed query re-ranks the
	// results and resets the cursor, so it can never point past the
	// narrowed list.
	before := m.filter.Value()
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	if m.filter.Value() != before {
		m.cursor = 0
		m.results = Search(m.filter.Value(), m.titles)
	}
	return ActionNone, cmd
}

// SelectedIndex returns the catalog index of the highlighted result, or
// false when the palette has no result to select.
func (m Model) SelectedIndex() (int, bool) {
	if len(m.results) == 0 || m.cursor >= len(m.results) {
		return 0, false
	}
	return m.results[m.cursor], true
}

// View renders the centered bordered palette: title, filter, results
// and a hint line, in the help/confirm overlay shape. The highlighted
// row inverts on the theme's Accent color — the role's first consumer.
// A closed palette renders nothing. An honest empty state ("no matching
// module") replaces the result list when the filter matches nothing.
func (m Model) View(width, height int) string {
	if !m.open {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	selectedStyle := lipgloss.NewStyle().Background(m.theme.Accent).Foreground(m.theme.Background)
	rowStyle := lipgloss.NewStyle().Foreground(m.theme.Foreground)
	mutedStyle := lipgloss.NewStyle().Foreground(m.theme.Muted)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Command Palette"))
	b.WriteString("\n\n")
	b.WriteString(m.filter.View())
	b.WriteString("\n\n")
	if len(m.results) == 0 {
		b.WriteString(mutedStyle.Render("no matching module"))
	} else {
		for i, idx := range m.results {
			row := "  " + m.titles[idx]
			if i == m.cursor {
				row = selectedStyle.Render(row)
			} else {
				row = rowStyle.Render(row)
			}
			b.WriteString(row)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("↑/↓ move · enter jump · esc close"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Border).
		Padding(1, 2).
		Render(b.String())

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
