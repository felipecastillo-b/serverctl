package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/config"
)

// newProcessesModel builds a root model on the golden /proc fixtures with
// the processes screen active and its first collection round delivered.
func newProcessesModel(t *testing.T) Model {
	t.Helper()
	m := NewWithSystem(config.Config{}, collectors.New("../../collectors/testdata"))
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, cmd := send(t, m, tea.KeyMsg{Type: tea.KeyTab}) // Dashboard → Processes
	if cmd == nil {
		t.Fatal("switching to Processes must fire its initial collection")
	}
	m, _ = send(t, m, cmd())
	return m
}

func TestProcessesSignalKeyShadowsGlobalQuit(t *testing.T) {
	m := newProcessesModel(t)

	// On the processes screen, k opens the KILL confirmation instead of
	// moving the sidebar: §6 shadowing at the root router.
	m, cmd := send(t, m, runesKey("k"))
	if cmd != nil {
		t.Fatal("k must only open the modal, not emit commands")
	}
	view := m.View()
	if !strings.Contains(view, "Send KILL") {
		t.Fatalf("KILL confirmation missing from view:\n%s", view)
	}

	// While the modal is open q no longer quits: a §7 gate is exclusive.
	m, cmd = send(t, m, runesKey("q"))
	if cmd != nil {
		t.Fatal("q must be swallowed while the confirmation is open")
	}

	// esc denies; the table returns and q works globally again.
	m, cmd = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("denial must not emit commands")
	}
	_, cmd = send(t, m, runesKey("q"))
	if cmd == nil {
		t.Fatal("q must quit once no modal is open")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("q must produce tea.QuitMsg, got %T", cmd())
	}
}

func TestProcessesHelpShowsScreenKeymap(t *testing.T) {
	m := newProcessesModel(t)

	m, _ = send(t, m, runesKey("?"))
	view := m.View()
	for _, want := range []string{"Global", "Screen", "sort by cpu", "filter"} {
		if !strings.Contains(view, want) {
			t.Errorf("help overlay missing %q:\n%s", want, view)
		}
	}
}
