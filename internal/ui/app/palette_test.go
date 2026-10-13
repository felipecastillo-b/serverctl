package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/config"
	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
)

// newPaletteModel builds a root model with a settled terminal size and
// the command palette open.
func newPaletteModel(t *testing.T) Model {
	t.Helper()
	m, _ := send(t, New(config.Default()), tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if !m.palette.Open() {
		t.Fatal("ctrl+p should open the palette")
	}
	return m
}

func TestPaletteOpensSwallowsAndCloses(t *testing.T) {
	m := newPaletteModel(t)

	// While the palette is open it replaces the body: the sidebar (and
	// its selection mark) must be gone from the render.
	view := m.View()
	if !strings.Contains(view, "Command Palette") {
		t.Fatalf("the open palette must render:\n%s", view)
	}
	if strings.Contains(view, "▎") {
		t.Fatalf("the palette must replace the sidebar, selection mark still rendered:\n%s", view)
	}

	// The palette is modal: q filters instead of quitting (the command
	// it returns is the textinput's cursor blink, never a quit), and
	// tab is swallowed instead of cycling modules.
	m, cmd := send(t, m, runesKey("q"))
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("q must be swallowed by the palette, not quit")
		}
	}
	if !m.palette.Open() {
		t.Fatal("filter keys must keep the palette open")
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.active != screens.Dashboard {
		t.Fatalf("tab must not cycle modules while the palette is open, active = %v", m.active)
	}

	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.palette.Open() {
		t.Fatal("esc should close the palette")
	}

	// Closed, the body is back and the global keymap works again.
	if view := m.View(); !strings.Contains(view, "▎") {
		t.Fatalf("the sidebar must return once the palette is closed:\n%s", view)
	}
	_, cmd = send(t, m, runesKey("q"))
	if cmd == nil {
		t.Fatal("q must quit once the palette is closed")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("q must produce tea.QuitMsg, got %T", cmd())
	}

	// ctrl+p toggles the palette closed, like ? toggles the help overlay.
	m, _ = send(t, New(config.Default()), tea.KeyMsg{Type: tea.KeyCtrlP})
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.palette.Open() {
		t.Fatal("ctrl+p should toggle the palette closed")
	}
}

func TestPaletteFilterEnterJumpsToModule(t *testing.T) {
	m := newPaletteModel(t)

	// "serv" matches Services only, as a contiguous prefix.
	for _, r := range "serv" {
		m, _ = send(t, m, runesKey(string(r)))
	}
	m, cmd := send(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.palette.Open() {
		t.Fatal("enter should close the palette after the jump")
	}
	if m.active != screens.Services {
		t.Fatalf("enter should jump to Services, active = %v", m.active)
	}
	if got := m.sidebar.Selected(); got != screens.Services {
		t.Fatalf("the sidebar must follow the jump, selected = %v", got)
	}
	if cmd == nil {
		t.Fatal("jumping must fire the new screen's initial collection")
	}
}

func TestPaletteCursorWrapsAndSelectsLastModule(t *testing.T) {
	m := newPaletteModel(t)

	// The open palette lists all eight modules; up from the first
	// result wraps to the last (Users), and enter jumps there.
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyUp})
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.active != screens.Users {
		t.Fatalf("up from the first result must wrap to Users, active = %v", m.active)
	}
}

func TestPaletteEmptyResultsStayPut(t *testing.T) {
	m := newPaletteModel(t)

	// "zzz" matches no module: the empty state renders honestly and
	// enter must not jump anywhere.
	for _, r := range "zzz" {
		m, _ = send(t, m, runesKey(string(r)))
	}
	if view := m.View(); !strings.Contains(view, "no matching module") {
		t.Fatalf("zero matches must render the empty state:\n%s", view)
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.active != screens.Dashboard {
		t.Fatalf("enter with zero matches must not jump, active = %v", m.active)
	}
	if !m.palette.Open() {
		t.Fatal("enter with zero matches must keep the palette open")
	}
}
