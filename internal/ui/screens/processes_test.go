package screens

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/actions"
	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/confirm"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// fakeSender records the last signal request instead of touching real
// processes.
type fakeSender struct {
	pid int
	sig actions.ProcessSignal
	err error
}

func (f *fakeSender) Signal(pid int, sig actions.ProcessSignal) error {
	f.pid, f.sig = pid, sig
	return f.err
}

func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// newFixtureScreen builds the processes screen on the golden /proc
// fixtures with a recording sender, and runs the first collection.
func newFixtureScreen(t *testing.T) (*processes, *fakeSender) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fake := &fakeSender{}
	p := newProcesses(collectors.New("../../collectors/testdata"), theme.Dark(), fake)
	cmd := p.Init()
	if cmd == nil {
		t.Fatal("Init must collect")
	}
	updated, _ := p.Update(cmd())
	p, ok := updated.(*processes)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if !p.loaded || len(p.procs) != 3 {
		t.Fatalf("fixture collection: loaded=%v procs=%d err=%v", p.loaded, len(p.procs), p.collectErr)
	}
	return p, fake
}

func TestProcessesScreenRendersFixtureRows(t *testing.T) {
	p, _ := newFixtureScreen(t)
	view := p.View(100, 24)
	for _, want := range []string{"CPU%", "kthreadd", "vim", "weird (name) here"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func pidsOf(procs []core.Process) []int {
	out := make([]int, len(procs))
	for i, p := range procs {
		out[i] = p.PID
	}
	return out
}

func TestProcessesSorting(t *testing.T) {
	p, _ := newFixtureScreen(t)
	// Controlled listing: the fixture tracker always reports 0% (static
	// jiffies), so CPU sort orders are only observable with these.
	p.procs = []core.Process{
		{PID: 10, Name: "alpha", CpuPct: 5, RSS: 100},
		{PID: 20, Name: "beta", CpuPct: 50, RSS: 900},
		{PID: 30, Name: "gamma", CpuPct: 20, RSS: 500},
	}
	p.reapply()

	cases := []struct {
		name string
		key  string
		want []int // expected PID order after the keypress
	}{
		{"cpu desc is the default", "", []int{20, 30, 10}},
		{"m sorts by mem desc", "m", []int{20, 30, 10}},
		{"p sorts by pid asc", "p", []int{10, 20, 30}},
		{"p again flips to desc", "p", []int{30, 20, 10}},
		{"n sorts by name asc", "n", []int{10, 20, 30}},
		{"c returns to cpu desc", "c", []int{20, 30, 10}},
	}

	for idx, tc := range cases {
		if tc.key != "" {
			updated, _, handled := p.UpdateKey(keyMsg(tc.key))
			if !handled {
				t.Fatalf("sort key %q not handled", tc.key)
			}
			p = updated.(*processes)
		} else if idx > 0 {
			t.Fatal("table driven misuse: default case must be first")
		}
		for i, wantPID := range tc.want {
			if p.view[i].PID != wantPID {
				t.Errorf("%s: order = %v, want %v", tc.name, pidsOf(p.view), tc.want)
				break
			}
		}
	}
}

func TestProcessesFilter(t *testing.T) {
	p, _ := newFixtureScreen(t)

	if _, _, handled := p.UpdateKey(keyMsg("/")); !handled || !p.filtering {
		t.Fatal("filter mode did not open")
	}
	for _, r := range "vim" {
		p.UpdateKey(keyMsg(string(r)))
	}
	if len(p.view) != 1 || p.view[0].Name != "vim" {
		t.Fatalf("filter 'vim': %v", pidsOf(p.view))
	}
	if !strings.Contains(p.View(100, 24), "filter \"vim\"") {
		t.Error("status line must show the active filter")
	}

	// esc closes the input but keeps the applied filter.
	if _, _, handled := p.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled || p.filtering {
		t.Fatal("esc must close filter mode")
	}
	if len(p.view) != 1 {
		t.Fatalf("closed filter must keep rows applied: %v", pidsOf(p.view))
	}

	// A no-match query empties the view.
	p.UpdateKey(keyMsg("/"))
	for _, r := range "zzz" {
		p.UpdateKey(keyMsg(string(r)))
	}
	if len(p.view) != 0 {
		t.Fatalf("no-match filter must empty the view: %v", pidsOf(p.view))
	}
}

func TestProcessesSignalConfirmed(t *testing.T) {
	p, fake := newFixtureScreen(t)
	// Fixture order is PID-ascending under the stable cpu sort; the
	// cursor starts on kthreadd (PID 2).
	if _, _, handled := p.UpdateKey(keyMsg("k")); !handled || !p.modalOpen {
		t.Fatal("k must open the KILL confirmation")
	}
	question := fmt.Sprintf("Send %s to pid %d (%s)?", actions.SignalKill, 2, "kthreadd")
	if got := p.modal.Decision(); got != confirm.Pending {
		t.Fatalf("modal decision before answer = %v", got)
	}
	if out := p.View(60, 20); !strings.Contains(out, question) {
		t.Errorf("modal view must carry the question:\n%s", out)
	}

	updated, cmd, handled := p.UpdateKey(keyMsg("y"))
	p = updated.(*processes)
	if !handled || p.modalOpen {
		t.Fatal("y must close the modal")
	}
	if cmd == nil {
		t.Fatal("confirmation must dispatch the signal")
	}
	p.Update(cmd())
	if fake.pid != 2 || fake.sig != actions.SignalKill {
		t.Errorf("sender got pid=%d sig=%v, want 2 KILL", fake.pid, fake.sig)
	}
	if !strings.Contains(p.status, "KILL sent to pid 2") {
		t.Errorf("status = %q, want the sent confirmation", p.status)
	}
}

func TestProcessesSignalDenied(t *testing.T) {
	p, fake := newFixtureScreen(t)
	if _, _, handled := p.UpdateKey(keyMsg("t")); !handled || !p.modalOpen {
		t.Fatal("t must open the TERM confirmation")
	}
	updated, cmd, handled := p.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc})
	p = updated.(*processes)
	if !handled || p.modalOpen {
		t.Fatal("esc must deny and close the modal")
	}
	if cmd != nil {
		t.Error("denial must not dispatch anything")
	}
	if fake.pid != 0 {
		t.Errorf("sender must not be called on denial, got pid %d", fake.pid)
	}
	if p.status != "cancelled" {
		t.Errorf("status = %q, want cancelled", p.status)
	}
}

// TestProcessesSignalConfirmedAuditsDecision pins the §7 split for the
// screen-local signal modal: the screen records the confirmed decision,
// while actions.Actor audits the delivery outcome.
func TestProcessesSignalConfirmedAuditsDecision(t *testing.T) {
	p, fake := newFixtureScreen(t)
	dir := os.Getenv("XDG_STATE_HOME")

	if _, _, handled := p.UpdateKey(keyMsg("k")); !handled || !p.modalOpen {
		t.Fatal("k must open the KILL confirmation")
	}
	updated, cmd, handled := p.UpdateKey(keyMsg("y"))
	p = updated.(*processes)
	if !handled || cmd == nil {
		t.Fatal("y must confirm and dispatch the signal")
	}
	p.Update(cmd())
	if fake.pid != 2 || fake.sig != actions.SignalKill {
		t.Fatalf("sender got pid=%d sig=%v, want 2 KILL", fake.pid, fake.sig)
	}

	entries := readAudit(t, dir)
	if len(entries) != 1 {
		t.Fatalf("audit log has %d entries, want 1: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Action != "process.signal" || entry.Result != "confirmed" ||
		entry.Target != "pid 2 (kthreadd)" {
		t.Errorf("audit entry = %+v, want process.signal/confirmed/pid 2 (kthreadd)", entry)
	}
}

// TestProcessesSignalDeniedAuditsDecision pins the denial half: an
// unconfirmed signal dispatches nothing and still leaves an audit line.
func TestProcessesSignalDeniedAuditsDecision(t *testing.T) {
	p, fake := newFixtureScreen(t)
	dir := os.Getenv("XDG_STATE_HOME")

	if _, _, handled := p.UpdateKey(keyMsg("t")); !handled || !p.modalOpen {
		t.Fatal("t must open the TERM confirmation")
	}
	_, cmd, handled := p.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled || cmd != nil {
		t.Fatal("esc must deny without dispatching anything")
	}
	if fake.pid != 0 {
		t.Fatalf("sender must not be called on denial, got pid %d", fake.pid)
	}

	entries := readAudit(t, dir)
	if len(entries) != 1 {
		t.Fatalf("audit log has %d entries, want 1: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Action != "process.signal" || entry.Result != "denied" ||
		entry.Target != "pid 2 (kthreadd)" {
		t.Errorf("audit entry = %+v, want process.signal/denied/pid 2 (kthreadd)", entry)
	}
}

func TestProcessesModalSwallowsGlobalKeys(t *testing.T) {
	p, _ := newFixtureScreen(t)
	p.UpdateKey(keyMsg("t"))

	// q is global-quit, but a §7 modal is exclusive: swallowed, no quit.
	updated, cmd, handled := p.UpdateKey(keyMsg("q"))
	p = updated.(*processes)
	if !handled || cmd != nil || !p.modalOpen {
		t.Fatal("the open modal must swallow q")
	}
}

func TestProcessesNoSelection(t *testing.T) {
	p, _ := newFixtureScreen(t)
	p.procs = nil
	p.reapply()
	updated, cmd, handled := p.UpdateKey(keyMsg("t"))
	p = updated.(*processes)
	if !handled || cmd != nil || p.modalOpen {
		t.Fatal("signalling with no rows must be a local no-op")
	}
	if p.status != "no process selected" {
		t.Errorf("status = %q", p.status)
	}
}

// TestProcessesEnterOpensDetail pins the drill-down: enter on the
// selected row emits the static detail for that process.
func TestProcessesEnterOpensDetail(t *testing.T) {
	p, _ := newFixtureScreen(t)
	row := p.view[p.table.Cursor()]

	_, cmd, handled := p.UpdateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("enter must be claimed by the processes screen")
	}
	if cmd == nil {
		t.Fatal("enter on a selected row must emit the detail command")
	}
	msg, ok := cmd().(OpenDetailMsg)
	if !ok {
		t.Fatalf("enter command produced %T, want OpenDetailMsg", cmd())
	}
	for _, want := range []string{fmt.Sprintf("%d", row.PID), row.Name} {
		if !strings.Contains(msg.Title, want) {
			t.Errorf("title %q missing %q", msg.Title, want)
		}
	}
	body := strings.Join(msg.Lines, "\n")
	for _, want := range []string{"PID:", "Command:", row.User} {
		if !strings.Contains(body, want) {
			t.Errorf("detail lines missing %q:\n%s", want, body)
		}
	}
}

func TestProcessesNavKeysClaimedAndOthersFallThrough(t *testing.T) {
	p, _ := newFixtureScreen(t)

	if _, _, handled := p.UpdateKey(keyMsg("down")); !handled {
		t.Fatal("navigation keys must be claimed by the screen")
	}
	if p.table.Cursor() != 1 {
		t.Errorf("cursor = %d, want 1 after down", p.table.Cursor())
	}

	// Keys outside the screen keymap fall through to the global map.
	for _, k := range []string{"q", "?", "j", "tab"} {
		if _, _, handled := p.UpdateKey(keyMsg(k)); handled {
			t.Errorf("key %q must fall through to the global keymap", k)
		}
	}
}

func TestProcessesMouseConfirmsModal(t *testing.T) {
	p, fake := newFixtureScreen(t)
	p.UpdateKey(keyMsg("k"))
	if !p.modalOpen {
		t.Fatal("precondition: modal open")
	}
	// Render once so the modal records its size, then click the yes
	// button. Its cell rect follows the confirm package's centered-box
	// layout (widest line = question; buttons on the last content row).
	question := fmt.Sprintf("Send %s to pid %d (%s)?", actions.SignalKill, 2, "kthreadd")
	p.View(60, 20)
	totalW := len(question) + 2*3 + 2 // padX both sides plus the border
	totalH := 3 + 2*1 + 2             // three content lines, padY, border
	ox := (60 - totalW) / 2
	oy := (20 - totalH) / 2
	buttonY := oy + 1 + 1 + 2
	buttonX := ox + 1 + 3

	miss := tea.MouseMsg(tea.MouseEvent{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 1, Y: 1})
	updated, cmd, handled := p.UpdateMouse(miss)
	p = updated.(*processes)
	if !handled || !p.modalOpen || cmd != nil {
		t.Fatal("a click outside the buttons must be swallowed without deciding")
	}

	yes := tea.MouseMsg(tea.MouseEvent{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: buttonX + 1, Y: buttonY})
	updated, cmd, handled = p.UpdateMouse(yes)
	p = updated.(*processes)
	if !handled || p.modalOpen {
		t.Fatal("clicking yes must close the modal")
	}
	if cmd == nil {
		t.Fatal("clicking yes must dispatch the signal")
	}
	p.Update(cmd())
	if fake.pid != 2 || fake.sig != actions.SignalKill {
		t.Errorf("sender got pid=%d sig=%v", fake.pid, fake.sig)
	}
}

func TestProcessesClickSelectsRow(t *testing.T) {
	p, _ := newFixtureScreen(t)
	p.View(100, 24)
	if len(p.view) < 2 {
		t.Fatalf("fixture needs at least two rows, got %d", len(p.view))
	}
	if p.table.Cursor() != 0 {
		t.Fatalf("precondition: cursor = %d, want 0", p.table.Cursor())
	}

	updated, cmd, handled := p.UpdateMouse(mousePress(tea.MouseButtonLeft, 2, 2))
	p = updated.(*processes)
	if !handled {
		t.Fatal("a left press on a table row must be claimed")
	}
	if cmd != nil {
		t.Error("selecting a row must not emit a command")
	}
	if p.table.Cursor() != 1 {
		t.Errorf("cursor = %d, want the clicked row 1", p.table.Cursor())
	}

	cursor := p.table.Cursor()
	updated, cmd, handled = p.UpdateMouse(mousePress(tea.MouseButtonLeft, 2, 0))
	p = updated.(*processes)
	if handled || cmd != nil {
		t.Errorf("header press: handled=%v cmd=%v, want unclaimed without a command", handled, cmd)
	}
	if p.table.Cursor() != cursor {
		t.Errorf("header press moved the cursor to %d, want %d", p.table.Cursor(), cursor)
	}

	updated, cmd, handled = p.UpdateMouse(mousePress(tea.MouseButtonLeft, 2, 23))
	p = updated.(*processes)
	if handled || cmd != nil {
		t.Errorf("status-line press: handled=%v cmd=%v, want unclaimed without a command", handled, cmd)
	}
	if p.table.Cursor() != cursor {
		t.Errorf("status-line press moved the cursor to %d, want %d", p.table.Cursor(), cursor)
	}
}

// TestProcessesStaleRoundAgesListing pins the §5 keeps-last-good
// state on a RefreshMsg-driven screen: a failed round keeps the last
// good listing standing while the status line carries the error plus
// the muted age of that listing, and the next good round clears both.
func TestProcessesStaleRoundAgesListing(t *testing.T) {
	p, _ := newFixtureScreen(t)

	// The next round dies; the fixture listing stays on screen.
	updated, _ := p.Update(processesDataMsg{err: errors.New("/proc vanished")})
	p = updated.(*processes)
	p.lastGood = time.Now().Add(-45 * time.Second)
	out := p.View(100, 24)
	for _, want := range []string{"n/a (/proc vanished)", "stale 45s", "kthreadd"} {
		if !strings.Contains(out, want) {
			t.Errorf("stale round must keep the error, the age and the listing, missing %q:\n%s", want, out)
		}
	}

	// Recovery: a good round clears the error and the stale fragment.
	updated, _ = p.Update(p.collect()())
	p = updated.(*processes)
	out = p.View(100, 24)
	if strings.Contains(out, "stale") || strings.Contains(out, "/proc vanished") {
		t.Errorf("a recovered round must clear the error and the age:\n%s", out)
	}
}
