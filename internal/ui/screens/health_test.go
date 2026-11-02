package screens

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// fakeHealthLister serves one static SMART report (or a per-call error)
// instead of reaching the smartctl binary.
type fakeHealthLister struct {
	report core.DiskHealthReport
	err    error
	calls  int
}

func (f *fakeHealthLister) DiskHealth() (core.DiskHealthReport, error) {
	f.calls++
	return f.report, f.err
}

// Compile-time proof that the fake satisfies the m12a port, exactly like
// collectors.System does.
var _ core.DiskHealthLister = (*fakeHealthLister)(nil)

// healthFixture lists sdb before sda on purpose: the screen sorts by device
// name, so the fixture proves the ordering instead of inheriting it. sda is
// a healthy ATA drive with a real power-on counter; sdb is a failing NVMe
// drive with a young counter, so both verdict roles and both attribute
// renderings have rows.
var healthFixture = core.DiskHealthReport{
	Disks: []core.DiskHealth{
		{
			Device: "/dev/sdb", Model: "Samsung 990 PRO", Protocol: "NVMe",
			Passed: false, HasVerdict: true,
			TemperatureC: 41, HasTemp: true,
			PowerOnHours: 2, HasPowerOn: true,
		},
		{
			Device: "/dev/sda", Model: "WDC WD40EFRX", Protocol: "ATA",
			Passed: true, HasVerdict: true,
			TemperatureC: 32, HasTemp: true,
			PowerOnHours: 1234, HasPowerOn: true,
		},
	},
	ToolVersion: "7.5",
}

// driveHealth executes cmd and folds every message it produces into s,
// expanding tea.BatchMsg legs recursively. Callers hand it the collect leg
// only: executing a tea.Tick leg here would stall the test for a real
// cadence interval, so the timer stays runtime territory.
func driveHealth(t *testing.T, s *health, cmd tea.Cmd) *health {
	t.Helper()
	if cmd == nil {
		return s
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, leg := range batch {
			s = driveHealth(t, s, leg)
		}
		return s
	}
	updated, _ := s.Update(msg)
	out, ok := updated.(*health)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	return out
}

// collectOnceHealth drives one fresh collection round through the screen.
func collectOnceHealth(t *testing.T, s *health) *health {
	t.Helper()
	return driveHealth(t, s, s.collect())
}

// newHealthScreen builds the Health screen over the given fake and drives
// the first collection round synchronously. Init batches [collect, tick]
// and that batch is lazy — executing it only yields the legs — so the
// fixture runs the collect leg; the 60 s timer leg would stall the test.
func newHealthScreen(t *testing.T, lister *fakeHealthLister) *health {
	t.Helper()
	s := newHealth(lister, theme.Dark(), defaultHealthInterval)
	cmd := s.Init()
	if cmd == nil {
		t.Fatal("Init must collect and start the cadence")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("Init must batch collect and the cadence tick, got %T len %d", batch, len(batch))
	}
	s = driveHealth(t, s, batch[0])
	if !s.loaded || s.err != nil {
		t.Fatalf("fixture collection: loaded=%v err=%v", s.loaded, s.err)
	}
	return s
}

// healthDevices lists the displayed rows in table order.
func healthDevices(disks []core.DiskHealth) []string {
	out := make([]string, len(disks))
	for i, d := range disks {
		out[i] = d.Device
	}
	return out
}

func TestHealthInitRendersReport(t *testing.T) {
	lister := &fakeHealthLister{report: healthFixture}
	s := newHealthScreen(t, lister)
	if lister.calls != 1 {
		t.Fatalf("Init must collect exactly once, got %d", lister.calls)
	}
	if !s.loaded || s.err != nil {
		t.Fatalf("loaded=%v err=%v", s.loaded, s.err)
	}
	if got, want := healthDevices(s.report.Disks), []string{"/dev/sda", "/dev/sdb"}; !slices.Equal(got, want) {
		t.Fatalf("rows must be sorted by device: got %v, want %v", got, want)
	}
	if s.report.ToolVersion != "7.5" {
		t.Errorf("report version = %q, want 7.5", s.report.ToolVersion)
	}

	view := s.View(100, 24)
	for _, want := range []string{
		"DEVICE", "MODEL", "HEALTH", "TEMP", "POWER-ON",
		"/dev/sda", "WDC WD40EFRX", "PASSED", "32°C", "51d 10h",
		"/dev/sdb", "Samsung 990 PRO", "FAILED", "41°C", "2h 0m",
		"2 disks · 1 passed · 1 failed · smartctl 7.5",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	if strings.Index(view, "/dev/sda") > strings.Index(view, "/dev/sdb") {
		t.Errorf("sda must render before sdb:\n%s", view)
	}
	if strings.Contains(view, "unknown") {
		t.Errorf("a fully-answered report must not claim unknown disks:\n%s", view)
	}
}

// TestHealthCollectingState pins the pre-round footer: the screen is
// collecting, not empty and not failed.
func TestHealthCollectingState(t *testing.T) {
	s := newHealth(&fakeHealthLister{}, theme.Dark(), defaultHealthInterval)
	out := s.View(100, 24)
	if !strings.Contains(out, "collecting...") {
		t.Errorf("fresh screen must show collecting..., got:\n%s", out)
	}
	if strings.Contains(out, "0 SMART devices") {
		t.Errorf("a screen before its first round must not claim an empty report:\n%s", out)
	}
	if strings.Contains(out, "n/a") {
		t.Errorf("a pending first round is not an error:\n%s", out)
	}
	if strings.Contains(out, "DEVICE") {
		t.Errorf("a screen before its first round must not render the table:\n%s", out)
	}
}

// TestHealthTypedHints pins the environment-state block: each sentinel
// renders its two actionable lines instead of the generic n/a path, even
// when wrapped by the collector.
func TestHealthTypedHints(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want []string
	}{
		{
			name: "missing tool",
			err:  fmt.Errorf("health: %w", core.ErrSmartctlMissing),
			want: []string{"smartctl is not installed.", "Install the smartmontools package."},
		},
		{
			name: "unprivileged",
			err:  fmt.Errorf("health: %w", core.ErrSmartUnprivileged),
			want: []string{"SMART needs root or CAP_SYS_RAWIO.", "serverctl never escalates privileges."},
		},
		{
			name: "too old",
			err:  fmt.Errorf("health: %w", core.ErrSmartctlUnsupported),
			want: []string{"smartctl 7.0 or newer is required.", "Upgrade smartmontools for JSON output."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lister := &fakeHealthLister{err: tc.err}
			s := newHealth(lister, theme.Dark(), defaultHealthInterval)
			s = collectOnceHealth(t, s)

			out := s.View(100, 24)
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("hint missing %q:\n%s", want, out)
				}
			}
			if !strings.Contains(out, "health unavailable") {
				t.Errorf("hint state must end with health unavailable:\n%s", out)
			}
			if strings.Contains(out, "n/a") {
				t.Errorf("a typed state must not use the generic n/a path:\n%s", out)
			}
			if strings.Contains(out, "DEVICE") {
				t.Errorf("a typed state must replace the table, not sit under it:\n%s", out)
			}
			if s.loaded {
				t.Error("a failed first round must not mark the screen loaded")
			}
		})
	}
}

// TestHealthUnsupportedHintNamesVersion pins the one version literal the
// upgrade hint promises, so the message cannot silently drift from the -j
// boundary.
func TestHealthUnsupportedHintNamesVersion(t *testing.T) {
	lines, ok := hintLines(core.ErrSmartctlUnsupported)
	if !ok || len(lines) != 2 {
		t.Fatalf("hintLines = %v, %v", lines, ok)
	}
	if !strings.Contains(lines[0], "7.0") {
		t.Errorf("unsupported hint must name 7.0, got %q", lines[0])
	}
}

// TestHealthFirstRoundError pins the failed-first-round state: the error in
// the status line and no body rows, because there is no last good report to
// keep.
func TestHealthFirstRoundError(t *testing.T) {
	s := newHealth(&fakeHealthLister{err: errors.New("scan failed")},
		theme.Dark(), defaultHealthInterval)
	s = collectOnceHealth(t, s)

	if s.loaded {
		t.Fatal("a failed first round must not mark the screen loaded")
	}
	out := s.View(100, 24)
	if !strings.Contains(out, "n/a (scan failed)") {
		t.Errorf("failed round must show its collection error:\n%s", out)
	}
	if strings.Contains(out, "DEVICE") {
		t.Errorf("failed first round must not render a table:\n%s", out)
	}
	if strings.Contains(out, "health unavailable") {
		t.Errorf("a generic error is not a typed environment state:\n%s", out)
	}
}

// TestHealthStaleRoundKeepsLastGood pins §5's keeps-last-good state: a
// failed later round leaves the previous report standing under the error
// plus the muted age of that report, and the next good round clears both.
func TestHealthStaleRoundKeepsLastGood(t *testing.T) {
	s := newHealthScreen(t, &fakeHealthLister{report: healthFixture})

	s.lister = &fakeHealthLister{err: errors.New("disk read failed")}
	s = collectOnceHealth(t, s)
	s.lastGood = time.Now().Add(-45 * time.Second)

	out := s.View(100, 24)
	for _, want := range []string{"n/a (disk read failed)", "stale 45s", "/dev/sda", "PASSED"} {
		if !strings.Contains(out, want) {
			t.Errorf("stale round must keep the report under the error and age, missing %q:\n%s", want, out)
		}
	}
	if !s.loaded {
		t.Error("a failed later round must keep the screen loaded")
	}

	// Recovery: the good report replaces the stale one and clears the
	// error and the age.
	s.lister = &fakeHealthLister{report: healthFixture}
	s = collectOnceHealth(t, s)
	out = s.View(100, 24)
	if strings.Contains(out, "stale") || strings.Contains(out, "disk read failed") {
		t.Errorf("a recovered round must clear the error and the age:\n%s", out)
	}
}

// TestHealthHintReplacesLastGoodTable pins the deliberate exception to
// keeps-last-good: an environment state (missing tool, missing privilege)
// hides even a good report, so stale rows never imply the drives were just
// read.
func TestHealthHintReplacesLastGoodTable(t *testing.T) {
	s := newHealthScreen(t, &fakeHealthLister{report: healthFixture})

	s.lister = &fakeHealthLister{err: core.ErrSmartctlMissing}
	s = collectOnceHealth(t, s)

	if !s.loaded || len(s.report.Disks) != 2 {
		t.Fatalf("the last good report must stay in state: loaded=%v disks=%d", s.loaded, len(s.report.Disks))
	}
	out := s.View(100, 24)
	for _, want := range []string{"smartctl is not installed.", "Install the smartmontools package.", "health unavailable"} {
		if !strings.Contains(out, want) {
			t.Errorf("hint missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"/dev/sda", "/dev/sdb", "PASSED", "DEVICE"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("hint state must replace the table, found %q:\n%s", unwanted, out)
		}
	}
}

// TestHealthEmptyReport pins that an empty successful round is a zero-row
// listing, never an error or a loading state, and still names its tool.
func TestHealthEmptyReport(t *testing.T) {
	s := newHealthScreen(t, &fakeHealthLister{
		report: core.DiskHealthReport{ToolVersion: "7.5"},
	})
	out := s.View(100, 24)
	if !strings.Contains(out, "0 SMART devices · smartctl 7.5") {
		t.Errorf("empty round must show the zero-device summary and version:\n%s", out)
	}
	if strings.Contains(out, "collecting...") || strings.Contains(out, "n/a") {
		t.Errorf("empty round must render neither loading nor error:\n%s", out)
	}
}

// TestHealthVerdictAndAttributeCells pins the cell rendering: FAILED and
// UNKNOWN roles, em dashes for absent attributes and model, and the
// power-on duration format.
func TestHealthVerdictAndAttributeCells(t *testing.T) {
	report := core.DiskHealthReport{
		Disks: []core.DiskHealth{
			{
				Device: "/dev/sda", Model: "ATA Disk", Protocol: "ATA",
				Passed: true, HasVerdict: true,
				TemperatureC: 32, HasTemp: true,
				PowerOnHours: 1234, HasPowerOn: true,
			},
			{
				Device: "/dev/sdb", Model: "NVMe Disk", Protocol: "NVMe",
				Passed: false, HasVerdict: true,
				TemperatureC: 41, HasTemp: true,
			},
			{Device: "/dev/sdc"}, // no verdict, no model, no attributes
		},
		ToolVersion: "7.5",
	}
	s := newHealthScreen(t, &fakeHealthLister{report: report})

	rows := healthRowsFor(s.report.Disks, 100, theme.Dark())
	cases := []struct {
		name string
		row  table.Row
		want []string
	}{
		{"passed", rows[0], []string{"/dev/sda", "ATA Disk", "PASSED", "32°C", "51d 10h"}},
		{"failed", rows[1], []string{"/dev/sdb", "NVMe Disk", "FAILED", "41°C", "—"}},
		{"unknown", rows[2], []string{"/dev/sdc", "—", "UNKNOWN", "—", "—"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := []string(tc.row); !slices.Equal(got, tc.want) {
				t.Errorf("row = %v, want %v", got, tc.want)
			}
		})
	}

	view := s.View(100, 24)
	if !strings.Contains(view, "3 disks · 1 passed · 1 failed · 1 unknown · smartctl 7.5") {
		t.Errorf("status line must count every verdict role:\n%s", view)
	}
}

// TestHealthKeysFallThrough pins §6's per-key shadowing: navigation is
// claimed, while esc, letters and tab pass to the shell — Health has no
// sort or filter to shadow them.
func TestHealthKeysFallThrough(t *testing.T) {
	s := newHealthScreen(t, &fakeHealthLister{report: healthFixture})

	if _, _, handled := s.UpdateKey(keyMsg("down")); !handled {
		t.Fatal("navigation keys must be claimed by the screen")
	}
	if s.table.Cursor() != 1 {
		t.Errorf("cursor = %d, want 1 after down", s.table.Cursor())
	}
	if _, _, handled := s.UpdateKey(keyMsg("up")); !handled {
		t.Error("up must be claimed by the screen")
	}
	if s.table.Cursor() != 0 {
		t.Errorf("cursor = %d, want 0 after up", s.table.Cursor())
	}
	if _, _, handled := s.UpdateKey(tea.KeyMsg{Type: tea.KeyEnd}); !handled {
		t.Error("end must be claimed by the screen")
	}
	if s.table.Cursor() != 1 {
		t.Errorf("cursor = %d, want 1 after end", s.table.Cursor())
	}

	for _, k := range []string{"q", "?", "tab", "x", "j", "k", "s", "n", "/"} {
		if _, _, handled := s.UpdateKey(keyMsg(k)); handled {
			t.Errorf("key %q must fall through to the global keymap", k)
		}
	}
	if _, _, handled := s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc}); handled {
		t.Error("esc must fall through to the shell")
	}
	if _, _, handled := s.UpdateMouse(tea.MouseMsg{}); handled {
		t.Error("mouse events must fall through to the shell")
	}
}

func TestHealthTitleAndHints(t *testing.T) {
	s := newHealth(&fakeHealthLister{}, theme.Dark(), defaultHealthInterval)
	if got := s.Title(); got != "Health" {
		t.Errorf("Title() = %q, want Health", got)
	}
	if hints := s.Hints(); len(hints) != 0 {
		t.Errorf("Hints() = %v, want nil: navigation is the global table behaviour", hints)
	}
}

// TestHealthTickRearmsOwnCadence pins the self-ticked slow chain: the
// shell's RefreshMsg is ignored, and the screen's own tick collects a fresh
// round and re-arms.
func TestHealthTickRearmsOwnCadence(t *testing.T) {
	lister := &fakeHealthLister{report: healthFixture}
	s := newHealthScreen(t, lister)
	if lister.calls != 1 {
		t.Fatalf("fixture must have collected once, got %d", lister.calls)
	}

	updated, cmd := s.Update(RefreshMsg{})
	s = updated.(*health)
	if cmd != nil {
		t.Error("RefreshMsg must not trigger a health collection")
	}
	if lister.calls != 1 {
		t.Errorf("RefreshMsg must not re-collect: %d", lister.calls)
	}

	updated, cmd = s.Update(healthTickMsg{})
	s = updated.(*health)
	if cmd == nil {
		t.Fatal("the tick must re-arm the cadence")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the tick must re-arm collect and the next tick, got %T len %d", batch, len(batch))
	}
	// Only lister.calls matters here; the returned model is the same
	// pointer, so the leg can run without reassigning s.
	driveHealth(t, s, batch[0])
	if lister.calls != 2 {
		t.Errorf("the re-armed collect must fetch a fresh report: %d", lister.calls)
	}
}
