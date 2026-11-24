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

// TestProcessesWheelReachesTheScreenThroughContentCoordinates pins the
// shell translation for the wheel: an event over the content pane moves the
// selection, while sidebar and header events carry negative local
// coordinates and leave it alone. An open confirmation still swallows it.
func TestProcessesWheelReachesTheScreenThroughContentCoordinates(t *testing.T) {
	wheel := func(x, y int) tea.MouseMsg {
		return tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown, X: x, Y: y}
	}
	target := func(m Model) screens.SignalTarget {
		t.Helper()
		targeter, ok := m.topScreen().(screens.SignalTargeter)
		if !ok {
			t.Fatal("the processes screen must expose SignalTarget")
		}
		target, ok := targeter.SignalTarget()
		if !ok {
			t.Fatal("no selected process")
		}
		return target
	}

	m := newProcessesModel(t)
	m.View()
	before := target(m)
	// Raw (26, 3) is local (2, 2) inside the content pane.
	m, _ = send(t, m, wheel(26, 3))
	if after := target(m); after.PID == before.PID {
		t.Errorf("wheel inside the pane did not move the selection: pid %d", after.PID)
	}

	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"sidebar", 2, 3},
		{"header", 30, 0},
	} {
		n := newProcessesModel(t)
		n.View()
		before := target(n)
		n, _ = send(t, n, wheel(tc.x, tc.y))
		if after := target(n); after.PID != before.PID {
			t.Errorf("wheel over the %s moved the selection %d -> %d",
				tc.name, before.PID, after.PID)
		}
	}

	modal := newProcessesModel(t)
	modal.View()
	modal, _ = send(t, modal, runesKey("k"))
	before = target(modal)
	modal, _ = send(t, modal, wheel(26, 3))
	if !strings.Contains(modal.View(), "Send KILL") {
		t.Error("the wheel closed the confirmation")
	}
	if after := target(modal); after.PID != before.PID {
		t.Errorf("wheel under the modal moved the selection %d -> %d", before.PID, after.PID)
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
