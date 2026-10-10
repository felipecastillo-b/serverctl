package screens

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// fakePackageCounter serves one static count (or a per-call error)
// instead of walking the pacman local database.
type fakePackageCounter struct {
	count core.PackageCount
	err   error
	calls int
}

func (f *fakePackageCounter) Count() (core.PackageCount, error) {
	f.calls++
	return f.count, f.err
}

// Compile-time proof that the fake satisfies the m6a port, exactly
// like collectors.System does.
var _ core.PackageCounter = (*fakePackageCounter)(nil)

// pkgFixture mirrors a live Arch round: 588 installed packages,
// labeled with the distro and manager that counted them.
var pkgFixture = core.PackageCount{Distro: "arch", Manager: "pacman", Installed: 588}

// newPackagesScreen builds the packages screen over the given fake
// and drives the first collection round synchronously. Init batches
// [collect, tick] and that batch is lazy — executing it only yields
// the legs — so the fixture runs the collect leg; the 30 s timer leg
// stays runtime territory, driving it here would stall the test.
func newPackagesScreen(t *testing.T, counter *fakePackageCounter) *packages {
	t.Helper()
	s := newPackages(counter, theme.Dark())
	cmd := s.Init()
	if cmd == nil {
		t.Fatal("Init must collect and start the cadence")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("Init must batch collect and the cadence tick, got %T len %d", batch, len(batch))
	}
	updated, _ := s.Update(batch[0]())
	s, ok = updated.(*packages)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if !s.loaded || s.err != nil {
		t.Fatalf("fixture collection: loaded=%v err=%v", s.loaded, s.err)
	}
	return s
}

func TestPackagesRendersSummary(t *testing.T) {
	s := newPackagesScreen(t, &fakePackageCounter{count: pkgFixture})
	view := s.View(100, 24)
	for _, want := range []string{
		"Distro", "arch",
		"Manager", "pacman",
		"Installed", "588",
		"588 packages · pacman on arch",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

// TestPackagesDistrolessSummary pins the label rendering when the
// collector could not read an os-release: the summary keeps the
// manager and drops the "on <distro>" tail instead of showing an
// empty label (core.PackageCount documents the best-effort trade).
func TestPackagesDistrolessSummary(t *testing.T) {
	s := newPackagesScreen(t, &fakePackageCounter{
		count: core.PackageCount{Manager: "pacman", Installed: 5},
	})
	view := s.View(100, 24)
	if !strings.Contains(view, "5 packages · pacman") {
		t.Errorf("view must show the manager-only summary:\n%s", view)
	}
	if strings.Contains(view, " on ") {
		t.Errorf("view must not render an empty distro label:\n%s", view)
	}
}

// TestPackagesErrorAndCollectingStates pins the §5 status-line ladder:
// collecting until the first round lands, the error of a failed first
// round, and — the stale case — the last good summary standing under a
// failed round's error, the shape the storage screen keeps for its
// rows.
func TestPackagesErrorAndCollectingStates(t *testing.T) {
	// Before the first round: collecting.
	fresh := newPackages(&fakePackageCounter{}, theme.Dark())
	if out := fresh.View(100, 24); !strings.Contains(out, "collecting...") {
		t.Errorf("fresh screen must show collecting..., got:\n%s", out)
	}

	// A failed first round: the error state, no body lines.
	failed := &fakePackageCounter{err: errors.New("no pacman database")}
	s := newPackages(failed, theme.Dark())
	updated, _ := s.Update(s.collect()())
	s = updated.(*packages)
	if s.loaded {
		t.Fatal("a failed round must not mark the screen loaded")
	}
	out := s.View(100, 24)
	if !strings.Contains(out, "n/a (no pacman database)") {
		t.Errorf("failed round must show its collection error:\n%s", out)
	}
	if strings.Contains(out, "588") {
		t.Errorf("failed first round must not render a body:\n%s", out)
	}

	// A good round followed by a failed one: the last good summary
	// stays rendered (stale, §5) while the status line carries the
	// new error.
	s = newPackagesScreen(t, &fakePackageCounter{count: pkgFixture})
	failed = &fakePackageCounter{err: errors.New("db vanished")}
	s.counter = failed
	updated, _ = s.Update(s.collect()())
	s = updated.(*packages)
	out = s.View(100, 24)
	if !strings.Contains(out, "588") || !strings.Contains(out, "arch") {
		t.Errorf("stale round must keep the last good summary lines:\n%s", out)
	}
	if !strings.Contains(out, "n/a (db vanished)") {
		t.Errorf("stale round must show the new error in the status line:\n%s", out)
	}
}

func TestPackagesTickRearmsOwnCadence(t *testing.T) {
	counter := &fakePackageCounter{count: pkgFixture}
	s := newPackagesScreen(t, counter)
	if counter.calls != 1 {
		t.Fatalf("fixture must have collected once, got %d", counter.calls)
	}

	// The shell's 2 s RefreshMsg drives the dashboard and processes
	// screens; Packages must ignore it and keep its own 30 s cadence
	// (ARCHITECTURE.md §3).
	updated, cmd := s.Update(RefreshMsg{})
	s = updated.(*packages)
	if cmd != nil {
		t.Error("RefreshMsg must not trigger a packages collection")
	}
	if counter.calls != 1 {
		t.Errorf("RefreshMsg must not re-collect: %d", counter.calls)
	}

	// The screen's own tick re-collects and re-arms.
	updated, cmd = s.Update(packagesTickMsg{})
	s = updated.(*packages)
	if cmd == nil {
		t.Fatal("the tick must re-arm the cadence")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the tick must re-arm collect and the next tick, got %T len %d", batch, len(batch))
	}
	s.Update(batch[0]())
	if counter.calls != 2 {
		t.Errorf("the re-armed collect must fetch a fresh count: %d", counter.calls)
	}
}

// TestPackagesKeysFallThrough pins the §6 story for a one-row
// summary: no screen-local bindings exist, so every key and mouse
// event passes to the shell.
func TestPackagesKeysFallThrough(t *testing.T) {
	s := newPackagesScreen(t, &fakePackageCounter{count: pkgFixture})
	for _, k := range []string{"q", "?", "tab", "down", "/"} {
		if _, _, handled := s.UpdateKey(keyMsg(k)); handled {
			t.Errorf("key %q must fall through to the global keymap", k)
		}
	}
	if _, _, handled := s.UpdateMouse(tea.MouseMsg{}); handled {
		t.Error("mouse events must fall through to the shell")
	}
}
