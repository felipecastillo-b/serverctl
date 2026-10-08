// Package confirm renders the reusable confirmation modal that gates
// every destructive action of serverctl (ARCHITECTURE.md §7). While a
// modal is open, the host screen must route every key and mouse event to
// it and swallow everything else: a confirmation is an exclusive gate.
package confirm

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// Decision is the state of a pending confirmation.
type Decision int

const (
	// Pending means no answer yet: the modal stays open.
	Pending Decision = iota
	// Confirmed authorizes the gated action.
	Confirmed
	// Denied cancels the gated action.
	Denied
)

const (
	padX = 3
	padY = 1
	// gap separates the yes and no buttons.
	gap = 4
)

// Model is a modal confirmation prompt: one question, yes/no buttons,
// keyboard y/n/esc and mouse clicks on the buttons. The zero value is not
// usable; build with New or Reset.
type Model struct {
	question string
	decision Decision
	theme    theme.Theme
	// width and height of the last View are kept for mouse hit-testing:
	// a click only lands after at least one render of this modal.
	width  int
	height int
}

// New builds a confirmation modal asking question.
func New(question string, th theme.Theme) Model {
	m := Model{theme: th}
	m.Reset(question)
	return m
}

// Reset re-arms the modal with a new question, clearing any previous
// decision so one Model can gate successive actions.
func (m *Model) Reset(question string) {
	m.question = question
	m.decision = Pending
}

// Decision reports the current answer; Pending while the modal is open.
func (m Model) Decision() Decision { return m.decision }

// UpdateKey consumes the modal's keys: y/Y confirm, n/N/esc deny. Every
// other key leaves the modal open (the host swallows it).
func (m *Model) UpdateKey(msg tea.KeyMsg) {
	switch strings.ToLower(msg.String()) {
	case "y":
		m.decision = Confirmed
	case "n", "esc":
		m.decision = Denied
	}
}

// rect is a half-open cell rectangle in screen coordinates.
type rect struct {
	x0, y0, x1, y1 int
}

// contains reports whether the cell (x, y) falls inside the rectangle.
func (r rect) contains(x, y int) bool {
	return x >= r.x0 && x < r.x1 && y >= r.y0 && y < r.y1
}

// buttonRects computes the cell rectangles of the rendered yes/no
// buttons from the last View size. The math mirrors View exactly: a
// centered bordered box with the buttons on its last content line.
func (m Model) buttonRects() (yes, no rect) {
	content := []string{m.question, "", "y yes" + strings.Repeat(" ", gap) + "n no"}
	widest := 0
	for _, line := range content {
		if w := lipgloss.Width(line); w > widest {
			widest = w
		}
	}
	// RoundedBorder adds one cell on every side; Padding adds its own.
	totalW := widest + 2*padX + 2
	totalH := len(content) + 2*padY + 2
	ox := (m.width - totalW) / 2
	oy := (m.height - totalH) / 2

	// First content line sits below the border and the top padding.
	buttonY := oy + 1 + padY + len(content) - 1
	buttonX := ox + 1 + padX
	yes = rect{buttonX, buttonY, buttonX + len("y yes"), buttonY + 1}
	noStart := buttonX + len("y yes") + gap
	no = rect{noStart, buttonY, noStart + len("n no"), buttonY + 1}
	return yes, no
}

// UpdateMouse resolves a left-button press against the yes/no buttons.
// Clicks anywhere else leave the modal open (the host swallows them).
func (m *Model) UpdateMouse(msg tea.MouseMsg) {
	event := tea.MouseEvent(msg)
	if event.Action != tea.MouseActionPress || event.Button != tea.MouseButtonLeft {
		return
	}
	yes, no := m.buttonRects()
	switch {
	case yes.contains(event.X, event.Y):
		m.decision = Confirmed
	case no.contains(event.X, event.Y):
		m.decision = Denied
	}
}

// View renders the centered bordered modal. It records the given size so
// the next UpdateMouse can hit-test the buttons.
func (m *Model) View(width, height int) string {
	m.width, m.height = width, height

	keyStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(m.theme.Muted)
	buttons := keyStyle.Render("y") + descStyle.Render(" yes") +
		strings.Repeat(" ", gap) +
		keyStyle.Render("n") + descStyle.Render(" no")

	content := m.question + "\n\n" + buttons
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Border).
		Padding(padY, padX).
		Render(content)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
