package screens

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Detail is a static screen pushed on top of the active module. The shell
// owns the navigation stack; a detail only renders the title and lines it
// was handed and claims no input.
type Detail struct {
	title string
	lines []string
}

// NewDetail builds a static detail screen from the title shown in the
// header and the plain-text body lines.
func NewDetail(title string, lines []string) *Detail {
	return &Detail{title: title, lines: lines}
}

// Init returns no command: details render data handed in at construction.
func (d *Detail) Init() tea.Cmd { return nil }

// Update ignores every message: a detail holds no state of its own.
func (d *Detail) Update(tea.Msg) (Screen, tea.Cmd) { return d, nil }

// UpdateKey never claims a key: the shell's global keymap stays active
// underneath a detail, and esc pops it.
func (d *Detail) UpdateKey(tea.KeyMsg) (Screen, tea.Cmd, bool) { return d, nil, false }

// UpdateMouse never claims a mouse event.
func (d *Detail) UpdateMouse(tea.MouseMsg) (Screen, tea.Cmd, bool) { return d, nil, false }

// View renders the body lines as plain text, one line per slice entry,
// each clipped to width display cells and the body to at most height
// lines. The header already shows the title, so View does not repeat it.
func (d *Detail) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := d.lines
	if len(lines) > height {
		lines = lines[:height]
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = clipDetailLine(line, width)
	}
	return strings.Join(out, "\n")
}

// Title returns the detail title shown in the header.
func (d *Detail) Title() string { return d.title }

// Hints is empty: details add no bindings; esc remains the shell's back
// key.
func (d *Detail) Hints() []key.Binding { return nil }

// clipDetailLine truncates line to at most width terminal cells without
// splitting a rune. Display width is measured with lipgloss.Width so a
// wide rune counts as the cells it occupies.
func clipDetailLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	var b strings.Builder
	used := 0
	for _, r := range line {
		w := lipgloss.Width(string(r))
		if used+w > width {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String()
}
