package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/actions"
	"github.com/felipecastillo-b/serverctl/internal/config"
	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
)

// readAuditEntries decodes every JSON line of the audit log under dir.
func readAuditEntries(t *testing.T, dir string) []actions.AuditEntry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "serverctl", "audit.log"))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var entries []actions.AuditEntry
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var entry actions.AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode audit line %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

// openPaletteAction opens the palette on m, filters down to the item
// containing filter, and selects it with enter.
func openPaletteAction(t *testing.T, m Model, filter string) Model {
	t.Helper()
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if !m.palette.Open() {
		t.Fatal("ctrl+p should open the palette")
	}
	for _, r := range filter {
		m, _ = send(t, m, runesKey(string(r)))
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	return m
}

func TestPaletteOffersProcessActionsOnlyOnProcesses(t *testing.T) {
	m, _ := send(t, New(config.Default()), tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if view := m.View(); strings.Contains(view, "Send TERM") {
		t.Fatalf("Dashboard has no process row, the palette must offer no signal action:\n%s", view)
	}

	m = newProcessesModel(t)
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
	view := m.View()
	for _, want := range []string{"Send TERM to pid 2", "Send KILL"} {
		if !strings.Contains(view, want) {
			t.Errorf("the Processes palette must offer %q:\n%s", want, view)
		}
	}
}

func TestPaletteProcessSignalConfirmsDispatchesAndAudits(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newProcessesModel(t)
	dir := os.Getenv("XDG_STATE_HOME")

	calls := 0
	var gotPID int
	var gotSig actions.ProcessSignal
	m.signal = func(pid int, sig actions.ProcessSignal) error {
		calls++
		gotPID, gotSig = pid, sig
		return nil
	}

	m = openPaletteAction(t, m, "term")
	if !m.confirmOpen {
		t.Fatal("selecting a signal action must open the root confirmation")
	}
	if view := m.View(); !strings.Contains(view, "Send TERM") {
		t.Fatalf("the root confirmation must render the question:\n%s", view)
	}

	// The root gate is exclusive: q and tab are swallowed.
	m, cmd := send(t, m, runesKey("q"))
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("q must not quit while the root confirmation is open")
		}
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.active != screens.Processes {
		t.Fatalf("tab must not switch modules while the root confirmation is open, active = %v", m.active)
	}

	m, cmd = send(t, m, runesKey("y"))
	if m.confirmOpen {
		t.Fatal("confirming must close the root confirmation")
	}
	if cmd == nil {
		t.Fatal("confirming must dispatch the signal")
	}
	msg := cmd()
	result, ok := msg.(screens.ProcessSignalResultMsg)
	if !ok {
		t.Fatalf("dispatch produced %T, want screens.ProcessSignalResultMsg", msg)
	}
	m, _ = send(t, m, result)

	if calls != 1 || gotPID != 2 || gotSig != actions.SignalTerm {
		t.Fatalf("dispatch = %d call(s) pid %d sig %v, want pid 2 TERM", calls, gotPID, gotSig)
	}
	if view := m.View(); !strings.Contains(view, "TERM sent to pid 2") {
		t.Fatalf("the result message must reach the processes status line:\n%s", view)
	}

	entries := readAuditEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("audit log has %d entries, want 1: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Action != "process.signal" || entry.Target != "pid 2 (kthreadd)" || entry.Result != "confirmed" {
		t.Errorf("audit entry = %+v, want process.signal/pid 2 (kthreadd)/confirmed", entry)
	}
}

func TestPaletteProcessSignalCancelAuditsDenial(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newProcessesModel(t)
	dir := os.Getenv("XDG_STATE_HOME")

	called := false
	m.signal = func(int, actions.ProcessSignal) error {
		called = true
		return nil
	}

	m = openPaletteAction(t, m, "kill")
	if !m.confirmOpen {
		t.Fatal("selecting KILL must open the root confirmation")
	}

	m, cmd := send(t, m, runesKey("n"))
	if m.confirmOpen {
		t.Fatal("denying must close the root confirmation")
	}
	if cmd == nil {
		t.Fatal("denying must yield the cancellation result")
	}
	msg := cmd()
	result, ok := msg.(screens.ProcessSignalResultMsg)
	if !ok {
		t.Fatalf("denial produced %T, want screens.ProcessSignalResultMsg", msg)
	}
	if !result.Cancelled || result.Err != nil {
		t.Fatalf("denial result = %+v, want Cancelled with no error", result)
	}
	m, _ = send(t, m, result)

	if called {
		t.Fatal("a denied signal must never reach the signaler")
	}
	if view := m.View(); !strings.Contains(view, "cancelled") {
		t.Fatalf("denial must set the processes status line to cancelled:\n%s", view)
	}

	entries := readAuditEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("audit log has %d entries, want 1: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Action != "process.signal" || entry.Target != "pid 2 (kthreadd)" || entry.Result != "denied" {
		t.Errorf("audit entry = %+v, want process.signal/pid 2 (kthreadd)/denied", entry)
	}
}

func TestPaletteConfirmButtonClickCancels(t *testing.T) {
	m := newProcessesModel(t)
	called := false
	m.signal = func(int, actions.ProcessSignal) error {
		called = true
		return nil
	}

	m = openPaletteAction(t, m, "term")
	if !m.confirmOpen {
		t.Fatal("selecting TERM must open the root confirmation")
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

	m, cmd := send(t, m, tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      col,
		Y:      row,
	})
	if m.confirmOpen {
		t.Fatal("clicking no must close the root confirmation")
	}
	if cmd == nil {
		t.Fatal("denying must yield the cancellation result")
	}
	msg := cmd()
	result, ok := msg.(screens.ProcessSignalResultMsg)
	if !ok {
		t.Fatalf("denial produced %T, want screens.ProcessSignalResultMsg", msg)
	}
	m, _ = send(t, m, result)

	if called {
		t.Fatal("a denied signal must never reach the signaler")
	}
	if view := m.View(); !strings.Contains(view, "cancelled") {
		t.Fatalf("clicking no must set the processes status line to cancelled:\n%s", view)
	}
}
