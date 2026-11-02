package screens

import (
	"fmt"
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

// View renders the body lines as plain text, wrapping each logical line to
// width display cells and keeping at most height rendered lines. The header
// already shows the title, so View does not repeat it.
func (d *Detail) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	out := make([]string, 0, height)
	for _, line := range d.lines {
		for _, chunk := range wrapDetailLine(line, width) {
			if len(out) == height {
				return strings.Join(out, "\n")
			}
			out = append(out, chunk)
		}
	}
	return strings.Join(out, "\n")
}

// Title returns the detail title shown in the header.
func (d *Detail) Title() string { return d.title }

// Hints is empty: details add no bindings; esc remains the shell's back
// key.
func (d *Detail) Hints() []key.Binding { return nil }

// renderDetailFields renders label/value pairs as aligned lines, one per
// field, with the colonized labels padded to the widest one so every value
// starts in the same column.
func renderDetailFields(fields [][2]string) []string {
	widest := 0
	for _, field := range fields {
		label := field[0] + ":"
		if len(label) > widest {
			widest = len(label)
		}
	}
	lines := make([]string, len(fields))
	for i, field := range fields {
		label := field[0] + ":"
		lines[i] = fmt.Sprintf("%-*s  %s", widest, label, field[1])
	}
	return lines
}

// wrapDetailLine wraps line into chunks of at most width terminal cells.
// Tabs expand to four spaces and an empty line yields one empty chunk. A
// chunk breaks at its last space when one came before the overflow — the
// space itself is dropped — and hard-breaks otherwise. Runes are never
// split; display width is measured with lipgloss.Width so a wide rune
// counts as the cells it occupies. A single rune wider than width is the
// only chunk that may exceed the bound.
func wrapDetailLine(line string, width int) []string {
	if width <= 0 {
		return nil
	}
	line = strings.ReplaceAll(line, "\t", "    ")
	if line == "" {
		return []string{""}
	}

	runes := []rune(line)
	out := make([]string, 0, len(runes)/width+1)
	start := 0
	for start < len(runes) {
		end, used, lastSpace := start, 0, -1
		for end < len(runes) {
			w := lipgloss.Width(string(runes[end]))
			if used+w > width {
				break
			}
			if runes[end] == ' ' {
				lastSpace = end
			}
			used += w
			end++
		}
		if end < len(runes) && lastSpace > start {
			out = append(out, string(runes[start:lastSpace]))
			start = lastSpace + 1
			continue
		}
		if end == start {
			// A rune wider than the bound: keep it alone.
			end++
		}
		out = append(out, string(runes[start:end]))
		start = end
	}
	return out
}
