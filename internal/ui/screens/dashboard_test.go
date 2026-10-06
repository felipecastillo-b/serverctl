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
