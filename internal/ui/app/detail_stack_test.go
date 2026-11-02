package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
)

// pushDetail activates Processes and pushes the static detail the
// processes screen will emit once drill-down lands in S3.
func pushDetail(t *testing.T) Model {
	t.Helper()
	m := newProcessesModel(t)
	m, _ = send(t, m, screens.OpenDetailMsg{
		Title: "Process 42",
		Lines: []string{"pid 42", "name kthreadd"},
	})
	return m
}

func TestOpenDetailPushesStaticView(t *testing.T) {
	m := pushDetail(t)

	if got := m.topScreen().Title(); got != "Process 42" {
		t.Fatalf("top title = %q, want %q", got, "Process 42")
	}
	view := m.View()
	if !strings.Contains(view, "name kthreadd") {
		t.Fatalf("detail lines missing from view:\n%s", view)
	}
	if !strings.Contains(view, "Process 42") {
		t.Fatalf("header title missing from view:\n%s", view)
	}
	if m.active != screens.Processes {
		t.Errorf("active screen = %v, want Processes", m.active)
	}
	if got := m.sidebar.Selected(); got != screens.Processes {
		t.Errorf("sidebar selection = %v, want Processes", got)
	}
	if len(m.stack) != 1 {
		t.Fatalf("stack length = %d, want 1", len(m.stack))
	}
}

func TestEscPopsAndReinitsModule(t *testing.T) {
	m := pushDetail(t)

	m, cmd := send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := m.topScreen().Title(); got != "Processes" {
		t.Fatalf("top title after esc = %q, want Processes", got)
	}
	if len(m.stack) != 0 {
		t.Fatalf("stack length after esc = %d, want 0", len(m.stack))
	}
	if m.active != screens.Processes {
		t.Errorf("active screen = %v, want Processes", m.active)
	}
	if cmd == nil {
		t.Fatal("popping the last screen must re-init the module")
	}
}

func TestKeysReachTheDetailNotTheHiddenBase(t *testing.T) {
	m := pushDetail(t)

	m, _ = send(t, m, runesKey("k"))
	view := m.View()
	if strings.Contains(view, "Send KILL") {
		t.Fatalf("detail leaked the key to the hidden Processes screen:\n%s", view)
	}
	if !strings.Contains(view, "name kthreadd") {
		t.Fatalf("detail view lost its lines after a keypress:\n%s", view)
	}
}

func TestModuleSwitchClearsTheStack(t *testing.T) {
	m := pushDetail(t)

	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.active != screens.Services {
		t.Fatalf("active screen = %v, want Services", m.active)
	}
	if len(m.stack) != 0 {
		t.Fatalf("stack length after module switch = %d, want 0", len(m.stack))
	}
	if view := m.View(); strings.Contains(view, "name kthreadd") {
		t.Fatalf("switched module still renders the abandoned detail:\n%s", view)
	}
}

func TestHelpShowsTopScreenHints(t *testing.T) {
	m := pushDetail(t)

	m, _ = send(t, m, runesKey("?"))
	if !m.showHelp {
		t.Fatal("? should open the help overlay")
	}
	view := m.View()
	if !strings.Contains(view, "Global") {
		t.Fatalf("help overlay missing the global section:\n%s", view)
	}
	if strings.Contains(view, "sort by cpu") {
		t.Fatalf("help overlay lists the hidden base module's hints:\n%s", view)
	}
}
