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
			CPU:      core.CPUInfo{Model: "Test CPU", Cores: 4, Usage: 50, PerCPU: []float64{50, 50, 50, 50}, Measured: true},
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
			CPU:      core.CPUInfo{Model: "Test CPU", Cores: 4, Usage: 50, PerCPU: []float64{50, 50, 50, 50}, Measured: true},
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

// TestDashboardLoadInlinesWindowsAndNormalizesByCores pins the Load
// section read-A contract: each window label travels with its value so
// no column alignment is implied, the core count sits next to the
// numbers, and the bar normalizes the 1m average against total core
// capacity (0.42 on 2 cores is 21%).
func TestDashboardLoadInlinesWindowsAndNormalizesByCores(t *testing.T) {
	round := dashboardRound()
	round.snapshot.CPU.Cores = 2
	round.snapshot.CPU.PerCPU = []float64{50, 50}

	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen, _ = screen.Update(round)
	view := screen.View(100, 40)

	for _, want := range []string{"2 cores", "· 1m 0.42  5m 0.36  15m 0.30", "21% (1m)"} {
		if !strings.Contains(view, want) {
			t.Errorf("load section is missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Load  1m 5m 15m") {
		t.Errorf("load must not render the detached window legend:\n%s", view)
	}
}

// TestDashboardLoadWithoutCoresFallsBackToPlainWindows pins the
// degraded case: when the CPU section never reported a core count, the
// load section still shows its values but skips the capacity bar
// instead of dividing by zero.
func TestDashboardLoadWithoutCoresFallsBackToPlainWindows(t *testing.T) {
	round := dashboardRound()
	round.snapshot.CPU = core.CPUInfo{}
	round.errs = map[string]error{"cpu": errors.New("boom")}

	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen, _ = screen.Update(round)
	view := screen.View(100, 40)

	if !strings.Contains(view, "· 1m 0.42  5m 0.36  15m 0.30") {
		t.Errorf("load must keep inline windows without a core count:\n%s", view)
	}
	if strings.Contains(view, "% (1m)") {
		t.Errorf("load must not render a capacity readout without cores:\n%s", view)
	}
}

// TestDashboardCPUMeasuresBeforeBaseline pins the first-round honesty
// fix: the tracker can only baseline cumulative counters, so the first
// round renders a measuring note instead of a fake 0.0%.
func TestDashboardCPUMeasuresBeforeBaseline(t *testing.T) {
	// Keep the other sections healthy so the only possible "0.0%" is the
	// CPU's unmeasured baseline, not an empty memory fixture.
	round := dashboardRound()
	round.snapshot.CPU = core.CPUInfo{Model: "Test CPU", Cores: 4, PerCPU: []float64{0, 0, 0, 0}}

	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen, _ = screen.Update(round)

	view := screen.View(100, 40)
	if !strings.Contains(view, "measuring") {
		t.Errorf("first round must render the measuring note:\n%s", view)
	}
	if strings.Contains(view, " 0.0%") {
		t.Errorf("first round must not render a fake 0.0%%:\n%s", view)
	}
}

// TestDashboardCPURendersBarsPerCoreUpToEightCores pins the small-count
// mode: the aggregate and every core share one aligned table, so bars
// and percentages sit in the same columns and indices read as row labels.
func TestDashboardCPURendersBarsPerCoreUpToEightCores(t *testing.T) {
	round := dashboardRound()
	round.snapshot.CPU = core.CPUInfo{
		Model:    "Test CPU",
		Cores:    4,
		Usage:    62.4,
		PerCPU:   []float64{42, 87, 3, 51},
		Measured: true,
	}

	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen, _ = screen.Update(round)
	view := screen.View(100, 40)

	for _, want := range []string{
		"total  █████████████████░░░░░░░░░░░",
		"    0  ███████████░░░░░░░░░░░░░░░░",
		"    1  ████████████████████████░░░░",
		"    2  ░░░░░░░░░░░░░░░░░░░░░░░░░░░░",
		"  42.0%", "  87.0%", "   3.0%", "  51.0%",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("per-core table is missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "0-3") {
		t.Errorf("small-count mode must not render a heatmap index label:\n%s", view)
	}
	if !strings.Contains(view, "62.4%\n\n    0  ") {
		t.Errorf("the total row must be separated from the core rows:\n%s", view)
	}
}

// TestDashboardCPUUsesHeatmapAboveEightCores pins the scalable mode: one
// colored level per core and an index range instead of per-core rows.
func TestDashboardCPUUsesHeatmapAboveEightCores(t *testing.T) {
	round := dashboardRound()
	round.snapshot.CPU = core.CPUInfo{
		Model:    "Test CPU",
		Cores:    9,
		Usage:    54,
		PerCPU:   []float64{0, 100, 50, 25, 75, 90, 10, 60, 80},
		Measured: true,
	}

	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen, _ = screen.Update(round)
	view := screen.View(100, 40)

	if !strings.Contains(view, "0-8") {
		t.Errorf("heatmap must label its core range:\n%s", view)
	}
	if !strings.Contains(view, "▁") {
		t.Errorf("an idle core must render the lowest heat level:\n%s", view)
	}
	if strings.Contains(view, "0 █") {
		t.Errorf("many-core mode must not render per-core bar rows:\n%s", view)
	}
}

// TestDashboardCPUHeatmapWrapsAtNarrowWidth pins the wrap: 32 cores at
// 30 cells split into two labeled rows instead of clipping.
func TestDashboardCPUHeatmapWrapsAtNarrowWidth(t *testing.T) {
	per := make([]float64, 32)
	for i := range per {
		per[i] = float64(i * 3)
	}
	round := dashboardRound()
	round.snapshot.CPU = core.CPUInfo{Model: "Test CPU", Cores: 32, Usage: 40, PerCPU: per, Measured: true}

	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen, _ = screen.Update(round)
	view := screen.View(30, 40)

	for _, want := range []string{"0-22", "23-31"} {
		if !strings.Contains(view, want) {
			t.Errorf("wrapped heatmap is missing row label %q:\n%s", want, view)
		}
	}
}

// TestDashboardMemoryAlignsSwapRows pins the memory read-A: memory and
// swap share one label gutter, one bar width, one percentage column and
// one right-aligned used/total column, so swap gets the same visual
// treatment as memory instead of a muted footnote.
func TestDashboardMemoryAlignsSwapRows(t *testing.T) {
	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen, _ = screen.Update(dashboardDataMsg{
		snapshot: core.ServerSnapshot{
			Memory: core.MemoryInfo{
				Total:     16 << 30,
				Used:      8 << 30,
				Available: 8 << 30,
				SwapTotal: 4 << 30,
				SwapUsed:  1 << 30,
			},
		},
		errs: map[string]error{},
	})

	view := screen.View(100, 40)
	for _, want := range []string{
		"Memory  ██████████████░░░░░░░░░░░░░░",
		"swap    ███████░░░░░░░░░░░░░░░░░░░",
		"  50.0%",
		"  25.0%",
		"8.0 GiB / 16.0 GiB",
		"1.0 GiB / 4.0 GiB",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("memory section is missing %q:\n%s", want, view)
		}
	}
}

// TestDashboardMemoryOmitsSwapRowWithoutSwap pins the no-swap case: the
// section must not render an empty swap row when the host has none.
func TestDashboardMemoryOmitsSwapRowWithoutSwap(t *testing.T) {
	screen := NewDashboard(collectors.New("/nonexistent-fixtures"), theme.Dark())
	screen, _ = screen.Update(dashboardRound())
	view := screen.View(100, 40)

	if strings.Contains(view, "swap") {
		t.Errorf("a host without swap must not render a swap row:\n%s", view)
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
