package screens

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/actions"
	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/confirm"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// fakeLister serves a static unit listing (or an error) instead of
// talking to D-Bus.
type fakeLister struct {
	units []core.Service
	err   error
	calls int
}

func (f *fakeLister) Units() ([]core.Service, error) {
	f.calls++
	return f.units, f.err
}

// fakeManager records the last lifecycle verb instead of touching real
// units.
type fakeManager struct {
	verb unitVerb
	name string
	err  error
}

func (f *fakeManager) Start(name string) error {
	f.verb, f.name = unitStart, name
	return f.err
}

func (f *fakeManager) Stop(name string) error {
	f.verb, f.name = unitStop, name
	return f.err
}

func (f *fakeManager) Restart(name string) error {
	f.verb, f.name = unitRestart, name
	return f.err
}

// Compile-time proof that the fakes satisfy the m4a ports, exactly like
// collectors.System and actions.Actor do.
var (
	_ core.UnitLister  = (*fakeLister)(nil)
	_ core.UnitManager = (*fakeManager)(nil)
)

// unitFixture is a merged-listing snapshot mixing loaded .service
// units with their enablement states and other unit types: only the
// .service units may reach the table.
var unitFixture = []core.Service{
	{Name: "mysql.service", Description: "MySQL database", Load: "loaded", Active: "failed", Sub: "exit-code", FileState: "enabled"},
	{Name: "alpha.service", Description: "activating alpha", Load: "loaded", Active: "activating", Sub: "start", FileState: "static"},
	{Name: "sshd.service", Description: "OpenSSH server", Load: "loaded", Active: "active", Sub: "running", FileState: "enabled"},
	{Name: "nginx.service", Description: "A high performance web server", Load: "loaded", Active: "active", Sub: "running", FileState: "enabled"},
	{Name: "postgres.service", Description: "PostgreSQL database", Load: "loaded", Active: "inactive", Sub: "dead", FileState: "disabled"},
	{Name: "logrotate.timer", Description: "daily rotation", Load: "loaded", Active: "active", Sub: "waiting"},
	{Name: "dbus.socket", Description: "system bus socket", Load: "loaded", Active: "active", Sub: "running"},
}

// dockerOnDisk is the on-disk-only shape the merged collector listing
// reports: disabled and stopped, so systemd never loaded it — the case
// ListUnits alone never surfaces (collectors.mergeUnits).
var dockerOnDisk = core.Service{
	Name:      "docker.service",
	Load:      "unloaded",
	Active:    "inactive",
	Sub:       "dead",
	FileState: "disabled",
}

// newServicesScreen builds the services screen over the given fakes and
// drives the first collection round synchronously. Init batches
// [collect, tick] and that batch is lazy — executing it only yields the
// legs — so the fixture runs the collect leg; the 5 s timer leg stays
// runtime territory, driving it here would stall the test.
func newServicesScreen(t *testing.T, lister core.UnitLister, manager core.UnitManager) *services {
	t.Helper()
	s := newServices(lister, theme.Dark(), manager)
	cmd := s.Init()
	if cmd == nil {
		t.Fatal("Init must collect and start the cadence")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("Init must batch collect and the cadence tick, got %T len %d", batch, len(batch))
	}
	updated, _ := s.Update(batch[0]())
	s, ok = updated.(*services)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if !s.loaded || s.collectErr != nil {
		t.Fatalf("fixture collection: loaded=%v err=%v", s.loaded, s.collectErr)
	}
	// Only the .service units of the fixture may reach the view, so
	// the expected count follows whatever listing the test served.
	want := 0
	for _, u := range s.units {
		if strings.HasSuffix(u.Name, ".service") {
			want++
		}
	}
	if len(s.view) != want {
		t.Fatalf("fixture must keep only .service units: %d rows, want %d", len(s.view), want)
	}
	return s
}

// unitNames lists the unit names of the view in table order.
func unitNames(units []core.Service) []string {
	out := make([]string, len(units))
	for i, u := range units {
		out[i] = u.Name
	}
	return out
}

// readAudit decodes every JSONL entry of the audit log under dir,
// mirroring how m4a's tests assert the §7 trail.
func readAudit(t *testing.T, dir string) []actions.AuditEntry {
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

func TestServicesScreenRendersFixtureRows(t *testing.T) {
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, &fakeManager{})
	view := s.View(100, 24)
	for _, want := range []string{
		"UNIT", "ACTIVE", "SUB", "ENABLED", "DESCRIPTION",
		"nginx.service", "A high performance web server",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	// Only .service units belong on this screen (ARCHITECTURE.md §3).
	for _, banned := range []string{"logrotate.timer", "dbus.socket"} {
		if strings.Contains(view, banned) {
			t.Errorf("view must exclude non-service unit %q:\n%s", banned, view)
		}
	}
}

func TestServicesSorting(t *testing.T) {
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, &fakeManager{})

	cases := []struct {
		name string
		key  string
		want []string // expected unit order after the keypress
	}{
		{"attention order is the default", "",
			[]string{"mysql.service", "alpha.service", "nginx.service", "sshd.service", "postgres.service"}},
		{"n sorts by name asc", "n",
			[]string{"alpha.service", "mysql.service", "nginx.service", "postgres.service", "sshd.service"}},
		{"n again flips to desc", "n",
			[]string{"sshd.service", "postgres.service", "nginx.service", "mysql.service", "alpha.service"}},
		{"a returns to attention asc", "a",
			[]string{"mysql.service", "alpha.service", "nginx.service", "sshd.service", "postgres.service"}},
		{"a again flips the ladder desc", "a",
			[]string{"postgres.service", "sshd.service", "nginx.service", "alpha.service", "mysql.service"}},
	}

	for idx, tc := range cases {
		if tc.key != "" {
			updated, _, handled := s.UpdateKey(keyMsg(tc.key))
			if !handled {
				t.Fatalf("sort key %q not handled", tc.key)
			}
			s = updated.(*services)
		} else if idx > 0 {
			t.Fatal("table driven misuse: default case must be first")
		}
		if got := unitNames(s.view); !slices.Equal(got, tc.want) {
			t.Errorf("%s: order = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestServicesFilter(t *testing.T) {
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, &fakeManager{})

	if _, _, handled := s.UpdateKey(keyMsg("/")); !handled || !s.filtering {
		t.Fatal("filter mode did not open")
	}
	// "web" matches a description, proving the query spans name and
	// description.
	for _, r := range "web" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if got := unitNames(s.view); !slices.Equal(got, []string{"nginx.service"}) {
		t.Fatalf("filter 'web': %v", got)
	}
	if !strings.Contains(s.View(100, 24), `filter "web"`) {
		t.Error("status line must show the active filter")
	}

	// esc closes the input but keeps the applied filter.
	if _, _, handled := s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled || s.filtering {
		t.Fatal("esc must close filter mode")
	}
	if got := unitNames(s.view); len(got) != 1 {
		t.Fatalf("closed filter must keep rows applied: %v", got)
	}

	// A fresh query: "sql" matches mysql by name and postgres by
	// description. The input still holds "web", so clear it first.
	s.UpdateKey(keyMsg("/"))
	s.filter.SetValue("")
	for _, r := range "sql" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if got := unitNames(s.view); !slices.Equal(got, []string{"mysql.service", "postgres.service"}) {
		t.Fatalf("filter 'sql': %v", got)
	}

	// A no-match query empties the view.
	s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc})
	s.UpdateKey(keyMsg("/"))
	for _, r := range "zzz" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if len(s.view) != 0 {
		t.Fatalf("no-match filter must empty the view: %v", unitNames(s.view))
	}
}

func TestServicesCursorKeptOnRefresh(t *testing.T) {
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, &fakeManager{})
	// Default attention order: mysql, alpha, nginx, sshd, postgres.
	// Move the cursor to alpha.service.
	if _, _, handled := s.UpdateKey(keyMsg("down")); !handled {
		t.Fatal("down must be handled")
	}
	if got := s.view[s.table.Cursor()].Name; got != "alpha.service" {
		t.Fatalf("cursor on %q, want alpha.service", got)
	}

	// A refreshed listing arrives shuffled, as D-Bus ordering allows;
	// reapply must keep the cursor on the same unit.
	shuffled := []core.Service{
		unitFixture[4], unitFixture[3], unitFixture[2],
		unitFixture[1], unitFixture[0], unitFixture[5], unitFixture[6],
	}
	updated, _ := s.Update(unitsDataMsg{services: shuffled})
	s = updated.(*services)
	if got := s.view[s.table.Cursor()].Name; got != "alpha.service" {
		t.Fatalf("cursor drifted to %q after refresh, want alpha.service", got)
	}
}

func TestServicesStartIsDirect(t *testing.T) {
	manager := &fakeManager{}
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, manager)

	updated, cmd, handled := s.UpdateKey(keyMsg("s"))
	s = updated.(*services)
	if !handled {
		t.Fatal("'s' must be handled")
	}
	if s.modalOpen {
		t.Fatal("starting is not destructive: 's' must not open the confirmation modal")
	}
	if cmd == nil {
		t.Fatal("'s' must dispatch the start verb immediately")
	}
	s.Update(cmd())
	if manager.verb != unitStart || manager.name != "mysql.service" {
		t.Errorf("manager got %s %q, want start mysql.service", manager.verb, manager.name)
	}
	if s.status != "started mysql.service" {
		t.Errorf("status = %q, want the start confirmation", s.status)
	}
}

func TestServicesLifecycleGates(t *testing.T) {
	cases := []struct {
		name    string
		verbKey string // opens the gate
		verb    unitVerb
		answer  string // key that resolves the modal
		deny    bool   // answer denies instead of confirming
	}{
		{"stop confirmed", "x", unitStop, "y", false},
		{"restart confirmed", "r", unitRestart, "y", false},
		{"stop denied by n", "x", unitStop, "n", true},
		{"restart denied by esc", "r", unitRestart, "esc", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_STATE_HOME", dir)

			manager := &fakeManager{}
			s := newServicesScreen(t, &fakeLister{units: unitFixture}, manager)

			// The cursor sits on the first row of the attention order.
			unit := "mysql.service"
			updated, _, handled := s.UpdateKey(keyMsg(tc.verbKey))
			s = updated.(*services)
			if !handled || !s.modalOpen {
				t.Fatalf("%q must open the confirmation modal", tc.verbKey)
			}
			if got := s.modal.Decision(); got != confirm.Pending {
				t.Fatalf("modal decision before the answer = %v, want pending", got)
			}
			question := tc.verb.question(unit)
			if out := s.View(60, 20); !strings.Contains(out, question) {
				t.Errorf("modal view must carry %q:\n%s", question, out)
			}

			var answer tea.KeyMsg
			if tc.answer == "esc" {
				answer = tea.KeyMsg{Type: tea.KeyEsc}
			} else {
				answer = keyMsg(tc.answer)
			}
			updated, cmd, handled := s.UpdateKey(answer)
			s = updated.(*services)
			if !handled || s.modalOpen {
				t.Fatal("the answer must close the modal")
			}

			if tc.deny {
				if cmd != nil {
					t.Error("a denial must not dispatch anything")
				}
				if manager.verb != "" {
					t.Errorf("manager must not be called on denial, got %s %q", manager.verb, manager.name)
				}
				if s.status != "canceled" {
					t.Errorf("status = %q, want canceled", s.status)
				}
			} else {
				if cmd == nil {
					t.Fatal("confirmation must dispatch the verb")
				}
				s.Update(cmd())
				if manager.verb != tc.verb || manager.name != unit {
					t.Errorf("manager got %s %q, want %s %q", manager.verb, manager.name, tc.verb, unit)
				}
				if want := tc.verb.past() + " " + unit; s.status != want {
					t.Errorf("status = %q, want %q", s.status, want)
				}
			}

			// §7: the modal states are the screen's to audit; executed
			// and failed belong to the Actor (not exercised here — the
			// fake manager writes nothing).
			entries := readAudit(t, dir)
			if len(entries) != 1 {
				t.Fatalf("audit log has %d lines, want 1", len(entries))
			}
			got := entries[0]
			wantResult := "confirmed"
			if tc.deny {
				wantResult = "denied"
			}
			if got.Action != tc.verb.action() || got.Target != unit ||
				got.Result != wantResult || got.Detail != "" {
				t.Errorf("audit = %+v, want action %q target %q result %q",
					got, tc.verb.action(), unit, wantResult)
			}
		})
	}
}

func TestServicesNoSelection(t *testing.T) {
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, &fakeManager{})
	s.units = nil
	s.reapply()
	updated, cmd, handled := s.UpdateKey(keyMsg("s"))
	s = updated.(*services)
	if !handled || cmd != nil || s.modalOpen {
		t.Fatal("acting with no rows must be a local no-op")
	}
	if s.status != "no unit selected" {
		t.Errorf("status = %q, want no unit selected", s.status)
	}
	updated, cmd, handled = s.UpdateKey(keyMsg("x"))
	s = updated.(*services)
	if !handled || cmd != nil || s.modalOpen {
		t.Fatal("stop with no rows must be a local no-op too")
	}
}

func TestServicesModalSwallowsGlobalKeys(t *testing.T) {
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, &fakeManager{})
	s.UpdateKey(keyMsg("x"))

	// q is global-quit, but a §7 modal is exclusive: swallowed, no quit.
	updated, cmd, handled := s.UpdateKey(keyMsg("q"))
	s = updated.(*services)
	if !handled || cmd != nil || !s.modalOpen {
		t.Fatal("the open modal must swallow q")
	}
}

func TestServicesNavKeysClaimedAndOthersFallThrough(t *testing.T) {
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, &fakeManager{})

	if _, _, handled := s.UpdateKey(keyMsg("down")); !handled {
		t.Fatal("navigation keys must be claimed by the screen")
	}
	if s.table.Cursor() != 1 {
		t.Errorf("cursor = %d, want 1 after down", s.table.Cursor())
	}

	// Keys outside the screen keymap fall through to the global map.
	for _, k := range []string{"q", "?", "j", "tab"} {
		if _, _, handled := s.UpdateKey(keyMsg(k)); handled {
			t.Errorf("key %q must fall through to the global keymap", k)
		}
	}
}

func TestServicesMouseConfirmsModal(t *testing.T) {
	// Confirming writes the §7 audit line; keep it out of the real
	// state directory.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	manager := &fakeManager{}
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, manager)
	s.UpdateKey(keyMsg("x"))
	if !s.modalOpen {
		t.Fatal("precondition: modal open")
	}
	// Render once so the modal records its size, then click the yes
	// button. Its cell rect follows the confirm package's centered-box
	// layout (widest line = question; buttons on the last content row).
	question := "Stop unit mysql.service?"
	s.View(60, 20)
	totalW := len(question) + 2*3 + 2 // padX both sides plus the border
	totalH := 3 + 2*1 + 2             // three content lines, padY, border
	ox := (60 - totalW) / 2
	oy := (20 - totalH) / 2
	buttonY := oy + 1 + 1 + 2
	buttonX := ox + 1 + 3

	miss := tea.MouseMsg(tea.MouseEvent{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 1, Y: 1})
	updated, cmd, handled := s.UpdateMouse(miss)
	s = updated.(*services)
	if !handled || !s.modalOpen || cmd != nil {
		t.Fatal("a click outside the buttons must be swallowed without deciding")
	}

	yes := tea.MouseMsg(tea.MouseEvent{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: buttonX + 1, Y: buttonY})
	updated, cmd, handled = s.UpdateMouse(yes)
	s = updated.(*services)
	if !handled || s.modalOpen {
		t.Fatal("clicking yes must close the modal")
	}
	if cmd == nil {
		t.Fatal("clicking yes must dispatch the stop verb")
	}
	s.Update(cmd())
	if manager.verb != unitStop || manager.name != "mysql.service" {
		t.Errorf("manager got %s %q, want stop mysql.service", manager.verb, manager.name)
	}
	if !strings.Contains(s.status, "stopped mysql.service") {
		t.Errorf("status = %q, want the stop confirmation", s.status)
	}
}

func TestServicesTickRearmsOwnCadence(t *testing.T) {
	lister := &fakeLister{units: unitFixture}
	s := newServicesScreen(t, lister, &fakeManager{})
	if lister.calls != 1 {
		t.Fatalf("fixture must have collected once, got %d calls", lister.calls)
	}

	// The shell's 2 s RefreshMsg drives the dashboard and processes
	// screens; the Services screen must ignore it and keep its own 5 s
	// cadence (ARCHITECTURE.md §3).
	updated, cmd := s.Update(RefreshMsg{})
	s = updated.(*services)
	if cmd != nil {
		t.Error("RefreshMsg must not trigger a services collection")
	}
	if lister.calls != 1 {
		t.Errorf("RefreshMsg must not re-collect: %d calls", lister.calls)
	}

	// The screen's own tick both re-collects and re-arms the next tick.
	updated, cmd = s.Update(servicesTickMsg{})
	s = updated.(*services)
	if cmd == nil {
		t.Fatal("the tick must re-arm the cadence")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the tick must re-arm collect and the next tick, got %T len %d", batch, len(batch))
	}
	s.Update(batch[0]())
	if lister.calls != 2 {
		t.Errorf("the re-armed collect must fetch a fresh listing: %d calls", lister.calls)
	}
}

func TestServicesListerErrorShowsNA(t *testing.T) {
	s := newServices(&fakeLister{err: errors.New("no system bus")}, theme.Dark(), &fakeManager{})
	updated, _ := s.Update(s.collect()())
	s = updated.(*services)
	if s.collectErr == nil || s.loaded {
		t.Fatalf("error round must set collectErr: err=%v loaded=%v", s.collectErr, s.loaded)
	}
	if out := s.View(100, 24); !strings.Contains(out, "n/a (no system bus)") {
		t.Errorf("view must show the collection error:\n%s", out)
	}
	if len(s.view) != 0 {
		t.Errorf("an error round must leave the table empty, got %d rows", len(s.view))
	}
}

func TestServicesManagerErrorShowsFailure(t *testing.T) {
	// A plain failure keeps the raw rendering; auth failures get the
	// polkit hint instead (TestServicesAuthFailureHintsAtPrivileges).
	manager := &fakeManager{err: errors.New("unit not found")}
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, manager)

	updated, cmd, handled := s.UpdateKey(keyMsg("s"))
	s = updated.(*services)
	if !handled || cmd == nil {
		t.Fatal("'s' must dispatch the start verb")
	}
	s.Update(cmd())
	if !strings.Contains(s.status, "failed") || !strings.Contains(s.status, "unit not found") {
		t.Errorf("status = %q, want the failure with the error text", s.status)
	}
	if strings.Contains(s.status, "needs privileges") {
		t.Errorf("status = %q, non-auth errors must keep the raw rendering", s.status)
	}
}

// TestServicesOnDiskOnlyUnitRenders drives the docker case end to end:
// an on-disk-only unit arrives with the merged listing and must reach
// the table with its enablement state in the ENABLED column.
func TestServicesOnDiskOnlyUnitRenders(t *testing.T) {
	listing := append(slices.Clone(unitFixture), dockerOnDisk)
	s := newServicesScreen(t, &fakeLister{units: listing}, &fakeManager{})

	// The row keeps the unloaded/inactive/dead triple the collector
	// reported; only the ACTIVE, SUB and ENABLED cells render.
	if idx := indexOfUnit(s.view, "docker.service"); idx < 0 {
		t.Fatalf("docker.service missing from the view: %v", unitNames(s.view))
	} else if s.view[idx] != dockerOnDisk {
		t.Errorf("docker row = %+v, want %+v", s.view[idx], dockerOnDisk)
	}
	for _, want := range []string{"ENABLED", "docker.service", "disabled"} {
		if out := s.View(100, 24); !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

// TestServicesFilterMatchesEnablementState proves the filter query
// spans FileState: "disabled" must surface the on-disk-only unit whose
// name and (empty) description say nothing about enablement.
func TestServicesFilterMatchesEnablementState(t *testing.T) {
	listing := append(slices.Clone(unitFixture), dockerOnDisk)
	s := newServicesScreen(t, &fakeLister{units: listing}, &fakeManager{})

	s.UpdateKey(keyMsg("/"))
	for _, r := range "disabled" {
		s.UpdateKey(keyMsg(string(r)))
	}
	// Both disabled units match — the on-disk-only docker and the
	// loaded-but-disabled postgres — attention order tie-breaking
	// their equal inactive rank alphabetically.
	if got := unitNames(s.view); !slices.Equal(got, []string{"docker.service", "postgres.service"}) {
		t.Fatalf("filter 'disabled': %v", got)
	}
}

// TestServicesAuthFailureHintsAtPrivileges renders the polkit case:
// stopping a unit over SSH is not an active local session, so polkit
// demands interactive authentication serverctl cannot provide. The
// status line must say what to do, not dump the raw D-Bus error.
func TestServicesAuthFailureHintsAtPrivileges(t *testing.T) {
	manager := &fakeManager{err: errors.New("dbus: Interactive authentication required to manage units")}
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, manager)

	updated, cmd, handled := s.UpdateKey(keyMsg("s"))
	s = updated.(*services)
	if !handled || cmd == nil {
		t.Fatal("'s' must dispatch the start verb")
	}
	s.Update(cmd())
	if !strings.Contains(s.status, "needs privileges") {
		t.Errorf("status = %q, want the polkit hint", s.status)
	}
	if !strings.Contains(s.status, "polkit rule") {
		t.Errorf("status = %q, want the actionable rule pointer", s.status)
	}
	if strings.Contains(s.status, "Interactive authentication") {
		t.Errorf("status = %q, the raw error dump is not actionable and must go", s.status)
	}
}

// TestNeedsPrivileges pins the auth-failure detector: the polkit
// refusal wordings, case-insensitively, and the negative cases.
func TestNeedsPrivileges(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil is not an auth failure", nil, false},
		{"polkit interactive authentication", errors.New("Interactive authentication required."), true},
		{"lowercase access denied", errors.New("org.freedesktop.DBus.Error.AccessDenied: access denied"), true},
		{"polkit daemon missing", errors.New("The name org.freedesktop.PolicyKit1 was not provided by any .service files"), true},
		{"plain failure stays plain", errors.New("unit not found"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsPrivileges(tt.err); got != tt.want {
				t.Errorf("needsPrivileges(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
