package app

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
)

// ansiSGR matches the SGR escapes lipgloss emits; stripping them leaves the
// visual text so string positions match terminal cells.
var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestConfirmButtonClickUsesContentCoordinates is the regression test for the
// shell passing raw terminal coordinates to the active screen: screens render
// inside the content pane (below the header, right of the sidebar), so a
// click on a modal button only resolves once the shell translates the event.
// It opens the Processes KILL confirmation, finds the "no" button in the
// rendered view, and clicks it at those terminal coordinates.
func TestConfirmButtonClickUsesContentCoordinates(t *testing.T) {
	m := newProcessesModel(t)

	m, _ = send(t, m, runesKey("k"))
	if view := m.View(); !strings.Contains(view, "Send KILL") {
		t.Fatalf("KILL confirmation missing from view:\n%s", view)
	}

	plain := ansiSGR.ReplaceAllString(m.View(), "")
	col, row := -1, -1
	for y, line := range strings.Split(plain, "\n") {
		if x := strings.Index(line, "n no"); x >= 0 {
			col, row = x, y
			break
		}
	}
	if col < 0 {
		t.Fatalf("confirmation buttons missing from view:\n%s", plain)
	}

	click := tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      col,
		Y:      row,
	}
	m, cmd := send(t, m, click)
	if cmd != nil {
		t.Fatal("denying must not emit commands")
	}
	if view := m.View(); strings.Contains(view, "Send KILL") {
		t.Fatalf("clicking the no button must close the modal, still open:\n%s", view)
	}
	if view := m.View(); !strings.Contains(view, "cancelled") {
		t.Fatalf("denial should set the status line to cancelled:\n%s", view)
	}
}

// TestOpenConfirmSwallowsSidebarClick keeps the §7 gate exclusive: even an
// event outside the content pane reaches the modal screen first, so clicking
// the sidebar cannot switch modules and abandon a pending confirmation.
func TestOpenConfirmSwallowsSidebarClick(t *testing.T) {
	m := newProcessesModel(t)
	m, _ = send(t, m, runesKey("k"))

	// Sidebar row 3 is Services: y=1 is Dashboard, y=2 Processes, y=3
	// Services. The press lands outside the content pane.
	press := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 2, Y: 3}
	m, _ = send(t, m, press)

	if m.active != screens.Processes {
		t.Fatalf("active screen = %v, want Processes while the gate is open", m.active)
	}
	if view := m.View(); !strings.Contains(view, "Send KILL") {
		t.Fatalf("confirmation must stay open after a sidebar click:\n%s", view)
	}
}
