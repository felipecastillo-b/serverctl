package screens

import (
	"fmt"
	"strings"
	"testing"

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
