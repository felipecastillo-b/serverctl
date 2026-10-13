package screens

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// collectOnce runs the dashboard's collect command synchronously and feeds
// the result back through Update, mimicking one refresh round.
func collectOnce(t *testing.T, s Screen) Screen {
	t.Helper()
	cmd := s.Init()
	if cmd == nil {
		t.Fatal("Init must return the collection command")
	}
	updated, _ := s.Update(cmd())
	return updated
}

func TestDashboardDegradesOnBrokenSystem(t *testing.T) {
	// A system rooted at a path with no procfs: every section must render
	// its "n/a" state instead of failing. Collection must not panic.
	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen = collectOnce(t, screen)

	view := screen.View(100, 40)
	if !strings.Contains(view, "n/a") {
		t.Errorf("broken system should render n/a sections, got:\n%s", view)
	}
}

func TestDashboardRendersCollectedData(t *testing.T) {
	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())

	screen, _ = screen.Update(dashboardDataMsg{
		snapshot: core.ServerSnapshot{
			Hostname: "buildbox",
			Kernel:   core.KernelInfo{Release: "6.11.5-arch1-1", HardwareModel: "PowerEdge R240"},
			CPU:      core.CPUInfo{Model: "Test CPU", Cores: 4, Usage: 50, PerCPU: []float64{50, 50, 50, 50}},
			Memory:   core.MemoryInfo{Total: 16 << 30, Used: 8 << 30, Available: 8 << 30},
			Load:     core.LoadAvg{Load1: 0.42, Load5: 0.36, Load15: 0.30},
			Uptime:   3*24*time.Hour + 4*time.Hour,
		},
		sensors: []core.SensorReading{{Name: "coretemp", Label: "Package id 0", Celsius: 43}},
		errs:    map[string]error{},
	})

	view := screen.View(100, 40)
	for _, want := range []string{"buildbox", "PowerEdge R240", "6.11.5-arch1-1", "3d 4h", "50.0%", "GiB", "0.42", "coretemp", "43"} {
		if !strings.Contains(view, want) {
			t.Errorf("render is missing %q:\n%s", want, view)
		}
	}
}

func TestDashboardShowsSectionErrorNextToHealthySections(t *testing.T) {
	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())

	screen, _ = screen.Update(dashboardDataMsg{
		snapshot: core.ServerSnapshot{Hostname: "buildbox", Uptime: time.Hour},
		errs:     map[string]error{"cpu": errors.New("boom")},
	})

	view := screen.View(100, 40)
	if !strings.Contains(view, "n/a (boom)") {
		t.Errorf("failed section must render its error state:\n%s", view)
	}
	if !strings.Contains(view, "buildbox") {
		t.Errorf("healthy sections must keep rendering:\n%s", view)
	}
}

func TestDashboardRefreshMsgTriggersCollection(t *testing.T) {
	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	_, cmd := screen.Update(RefreshMsg(time.Now()))
	if cmd == nil {
		t.Error("RefreshMsg must trigger a new collection command")
	}
}

func TestDashboardNoSensorsMessage(t *testing.T) {
	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen, _ = screen.Update(dashboardDataMsg{errs: map[string]error{}})

	view := screen.View(100, 40)
	if !strings.Contains(view, "no sensors detected") {
		t.Errorf("missing sensors must degrade to a message, got:\n%s", view)
	}
}

// dashboardRound builds one collection round with every section filled
// and healthy; tests tweak the fields they need.
func dashboardRound() dashboardDataMsg {
	return dashboardDataMsg{
		snapshot: core.ServerSnapshot{
			Hostname: "buildbox",
			Kernel:   core.KernelInfo{Release: "6.11.5-arch1-1", HardwareModel: "PowerEdge R240"},
			CPU:      core.CPUInfo{Model: "Test CPU", Cores: 4, Usage: 50, PerCPU: []float64{50, 50, 50, 50}},
			Memory:   core.MemoryInfo{Total: 16 << 30, Used: 8 << 30, Available: 8 << 30},
			Load:     core.LoadAvg{Load1: 0.42, Load5: 0.36, Load15: 0.30},
			Uptime:   3 * 24 * time.Hour,
		},
		sensors: []core.SensorReading{{Name: "coretemp", Label: "Package id 0", Celsius: 43}},
		errs:    map[string]error{},
	}
}

// TestDashboardFailedSectionKeepsLastGoodValues pins the per-section
// keeps-last-good fix (§5): a failed section keeps its last good
// values on screen under the round error plus their age, while the
// sections whose round succeeded move to the new values — the
// dashboard used to overwrite the whole snapshot with the fresh
// round's zeros, the one screen that lost last-good data on failure.
func TestDashboardFailedSectionKeepsLastGoodValues(t *testing.T) {
	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen, _ = screen.Update(dashboardRound())

	// The next round fails CPU but succeeds elsewhere; its snapshot
	// carries fresh values only for the sections that succeeded.
	failed := dashboardRound()
	failed.snapshot.CPU = core.CPUInfo{}
	failed.snapshot.Memory = core.MemoryInfo{Total: 32 << 30, Used: 4 << 30}
	failed.errs = map[string]error{"cpu": errors.New("boom")}
	screen, _ = screen.Update(failed)
	d := screen.(*dashboard)

	// Backdate the CPU section's last good round so the age reads
	// deterministically.
	d.lastGood["cpu"] = time.Now().Add(-45 * time.Second)
	view := d.View(100, 40)

	for _, want := range []string{"Test CPU", "50.0%", "n/a (boom)", "stale 45s"} {
		if !strings.Contains(view, want) {
			t.Errorf("failed CPU section must keep its last good values under the error and age, missing %q:\n%s", want, view)
		}
	}
	// The sections whose round succeeded moved to the new values.
	for _, want := range []string{"4.0 GiB / 32.0 GiB", "buildbox", "6.11.5-arch1-1"} {
		if !strings.Contains(view, want) {
			t.Errorf("healthy sections must render the new round, missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "16.0 GiB") {
		t.Errorf("the memory section must move to the new round, not keep the old one:\n%s", view)
	}
}

// TestDashboardRecoveredSectionClearsStaleNote pins the recovery half:
// once a failed section's round succeeds again, the error and the
// stale fragment disappear and the fresh values render plain.
func TestDashboardRecoveredSectionClearsStaleNote(t *testing.T) {
	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen, _ = screen.Update(dashboardRound())

	failed := dashboardRound()
	failed.snapshot.CPU = core.CPUInfo{}
	failed.errs = map[string]error{"cpu": errors.New("boom")}
	screen, _ = screen.Update(failed)

	screen, _ = screen.Update(dashboardRound())
	d := screen.(*dashboard)
	view := d.View(100, 40)
	if strings.Contains(view, "n/a (boom)") {
		t.Errorf("recovered section must drop its error note:\n%s", view)
	}
	if strings.Contains(view, "stale") {
		t.Errorf("recovered section must drop its stale note:\n%s", view)
	}
	if !strings.Contains(view, "Test CPU") {
		t.Errorf("recovered section must render the fresh values:\n%s", view)
	}
}

// TestDashboardFirstRoundFailureHasNoStaleNote pins the no-last-good
// case: a section that fails before any good round has no values to
// keep, so only the error renders — no age for data that never was.
func TestDashboardFirstRoundFailureHasNoStaleNote(t *testing.T) {
	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	failed := dashboardRound()
	failed.snapshot.CPU = core.CPUInfo{}
	failed.errs = map[string]error{"cpu": errors.New("boom")}
	screen, _ = screen.Update(failed)

	view := screen.View(100, 40)
	if !strings.Contains(view, "n/a (boom)") {
		t.Errorf("failed section must render its error:\n%s", view)
	}
	if strings.Contains(view, "stale") {
		t.Errorf("a section with no last good round must not claim a stale age:\n%s", view)
	}
}

// TestRenderBarEmptyCellsUseMutedRole pins the light-theme readability
// fix: the bar's empty-cell run uses the muted role, which the theme
// tests prove readable on both palettes — the border role the bar once
// used nearly vanishes on the light background.
func TestRenderBarEmptyCellsUseMutedRole(t *testing.T) {
	for _, th := range []theme.Theme{theme.Dark(), theme.Light()} {
		if got := barEmptyColor(th); got != th.Muted {
			t.Errorf("%s: bar empty color = %v, want the muted role %v", th.Name, got, th.Muted)
		}
		if got := barEmptyColor(th); got == th.Border {
			t.Errorf("%s: the border role must not return for bar empty cells", th.Name)
		}
	}
	// A half-filled bar renders both runs.
	bar := renderBar(50, 10, theme.Dark())
	if !strings.Contains(bar, "█") || !strings.Contains(bar, "░") {
		t.Errorf("a 50%% bar must render filled and empty cells, got %q", bar)
	}
}
