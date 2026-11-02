package app

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/config"
	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// fakeUnitLister lists a fixed unit set for the services screen.
type fakeUnitLister struct{ units []core.Service }

func (f *fakeUnitLister) Units() ([]core.Service, error) { return f.units, nil }

// fakeUnitManager satisfies the screen's mutate port; lifecycle dispatch
// is injected through Model.unitAction in these tests.
type fakeUnitManager struct{}

func (f *fakeUnitManager) Start(string) error   { return nil }
func (f *fakeUnitManager) Stop(string) error    { return nil }
func (f *fakeUnitManager) Restart(string) error { return nil }

// newServicesPaletteModel builds a root model with the Services screen
// active, its fake unit listing collected, and the palette closed.
func newServicesPaletteModel(t *testing.T) Model {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := NewWithSystem(config.Config{}, collectors.New("../../collectors/testdata"))
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.screens[screens.Services] = screens.NewServices(
		&fakeUnitLister{units: []core.Service{{
			Name:        "sshd.service",
			Description: "OpenSSH server",
			Load:        "loaded",
			Active:      "active",
			Sub:         "running",
			FileState:   "enabled",
		}}},
		theme.Dark(), &fakeUnitManager{}, time.Second,
	)
	m, cmd := m.setActive(screens.Services)
	if cmd == nil {
		t.Fatal("activating Services must fire its initial collection")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("Services Init produced %T, want a non-empty tea.BatchMsg", msg)
	}
	m, _ = send(t, m, batch[0]())
	return m
}

// openUnitPaletteAction types filter into the palette and selects the
// first match, returning the command the selection produced.
func openUnitPaletteAction(t *testing.T, m Model, filter string) (Model, tea.Cmd) {
	t.Helper()
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if !m.palette.Open() {
		t.Fatal("ctrl+p should open the palette")
	}
	for _, r := range filter {
		m, _ = send(t, m, runesKey(string(r)))
	}
	return send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
}

func TestPaletteOffersUnitActions(t *testing.T) {
	m := newServicesPaletteModel(t)
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})

	view := m.View()
	for _, want := range []string{
		"Start unit sshd.service",
		"Stop unit sshd.service",
		"Restart unit sshd.service",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the Services palette must offer %q:\n%s", want, view)
		}
	}
}

func TestPaletteUnitStartDispatchesDirectly(t *testing.T) {
	m := newServicesPaletteModel(t)

	calls := 0
	var gotAction screens.UnitAction
	var gotUnit string
	m.unitAction = func(action screens.UnitAction, unit string) error {
		calls++
		gotAction, gotUnit = action, unit
		return nil
	}

	m, cmd := openUnitPaletteAction(t, m, "start")
	if m.confirmOpen {
		t.Fatal("starting a unit is not destructive: it must not open the root confirmation")
	}
	if m.pendingAction != (paletteAction{}) {
		t.Fatalf("direct dispatch must not leave a pending action, got %+v", m.pendingAction)
	}
	if cmd == nil {
		t.Fatal("selecting Start unit must dispatch immediately")
	}
	msg := cmd()
	result, ok := msg.(screens.UnitActionResultMsg)
	if !ok {
		t.Fatalf("dispatch produced %T, want screens.UnitActionResultMsg", msg)
	}
	m, _ = send(t, m, result)

	if calls != 1 || gotAction != screens.UnitStart || gotUnit != "sshd.service" {
		t.Fatalf("dispatch = %d call(s) %v %q, want 1 call start sshd.service",
			calls, gotAction, gotUnit)
	}
	if view := m.View(); !strings.Contains(view, "started sshd.service") {
		t.Fatalf("the result message must reach the services status line:\n%s", view)
	}
}

func TestPaletteUnitStopConfirmsDispatchesAndAudits(t *testing.T) {
	m := newServicesPaletteModel(t)
	dir := os.Getenv("XDG_STATE_HOME")

	calls := 0
	var gotAction screens.UnitAction
	var gotUnit string
	m.unitAction = func(action screens.UnitAction, unit string) error {
		calls++
		gotAction, gotUnit = action, unit
		return nil
	}

	m, _ = openUnitPaletteAction(t, m, "stop")
	if !m.confirmOpen {
		t.Fatal("stop is destructive: selecting it must open the root confirmation")
	}
	if view := m.View(); !strings.Contains(view, "Stop unit sshd.service?") {
		t.Fatalf("the root confirmation must render the unit question:\n%s", view)
	}

	m, cmd := send(t, m, runesKey("y"))
	if m.confirmOpen {
		t.Fatal("confirming must close the root confirmation")
	}
	if cmd == nil {
		t.Fatal("confirming must dispatch the unit action")
	}
	msg := cmd()
	result, ok := msg.(screens.UnitActionResultMsg)
	if !ok {
		t.Fatalf("dispatch produced %T, want screens.UnitActionResultMsg", msg)
	}
	m, _ = send(t, m, result)

	if calls != 1 || gotAction != screens.UnitStop || gotUnit != "sshd.service" {
		t.Fatalf("dispatch = %d call(s) %v %q, want 1 call stop sshd.service",
			calls, gotAction, gotUnit)
	}
	if view := m.View(); !strings.Contains(view, "stopped sshd.service") {
		t.Fatalf("the result message must reach the services status line:\n%s", view)
	}

	entries := readAuditEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("audit log has %d entries, want 1: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Action != "service.stop" || entry.Target != "sshd.service" || entry.Result != "confirmed" {
		t.Errorf("audit entry = %+v, want service.stop/sshd.service/confirmed", entry)
	}
}

func TestPaletteUnitStopCancelAuditsDenial(t *testing.T) {
	m := newServicesPaletteModel(t)
	dir := os.Getenv("XDG_STATE_HOME")

	called := false
	m.unitAction = func(screens.UnitAction, string) error {
		called = true
		return nil
	}

	m, _ = openUnitPaletteAction(t, m, "stop")
	if !m.confirmOpen {
		t.Fatal("stop is destructive: selecting it must open the root confirmation")
	}

	m, cmd := send(t, m, runesKey("n"))
	if m.confirmOpen {
		t.Fatal("denying must close the root confirmation")
	}
	if cmd == nil {
		t.Fatal("denying must yield the cancellation result")
	}
	msg := cmd()
	result, ok := msg.(screens.UnitActionResultMsg)
	if !ok {
		t.Fatalf("denial produced %T, want screens.UnitActionResultMsg", msg)
	}
	if !result.Cancelled || result.Err != nil {
		t.Fatalf("denial result = %+v, want Cancelled with no error", result)
	}
	m, _ = send(t, m, result)

	if called {
		t.Fatal("a denied unit action must never reach the action layer")
	}
	if view := m.View(); !strings.Contains(view, "canceled") {
		t.Fatalf("denial must set the services status line to canceled:\n%s", view)
	}

	entries := readAuditEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("audit log has %d entries, want 1: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Action != "service.stop" || entry.Target != "sshd.service" || entry.Result != "denied" {
		t.Errorf("audit entry = %+v, want service.stop/sshd.service/denied", entry)
	}
}
