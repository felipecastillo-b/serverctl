package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/config"
	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
)

// TestCadencesResolveConfigIntervals pins the config→Cadences
// conversion, the one place the YAML keys meet the screen durations:
// every chain resolves from its own intervals key.
func TestCadencesResolveConfigIntervals(t *testing.T) {
	cfg := config.Default()
	cfg.Intervals = map[string]int{
		config.IntervalServices:          3,
		config.IntervalLogs:              4,
		config.IntervalStorage:           6,
		config.IntervalNetworkInterfaces: 7,
		config.IntervalNetworkSockets:    8,
		config.IntervalPackages:          9,
		config.IntervalUsersSessions:     11,
		config.IntervalUsersAttempts:     12,
	}
	want := screens.Cadences{
		Services:          3 * time.Second,
		Logs:              4 * time.Second,
		Storage:           6 * time.Second,
		NetworkInterfaces: 7 * time.Second,
		NetworkSockets:    8 * time.Second,
		Packages:          9 * time.Second,
		UsersSessions:     11 * time.Second,
		UsersAttempts:     12 * time.Second,
	}
	if got := cadences(cfg); got != want {
		t.Errorf("cadences() = %+v, want %+v", got, want)
	}
}

// TestCadencesZeroWithoutIntervals pins the fallback path: a Config
// with no intervals reads zero everywhere, which the screen
// constructors replace with the built-in defaults — a hand-built
// Config cannot stall a chain on a zero tick.
func TestCadencesZeroWithoutIntervals(t *testing.T) {
	var cfg config.Config
	if got := cadences(cfg); got != (screens.Cadences{}) {
		t.Errorf("cadences on an interval-less config = %+v, want the zero struct", got)
	}
}

// copyTree copies the fixture tree into dst so a test can fail one
// section mid-run by deleting its file; the shared fixture tree
// itself is read-only state (ARCHITECTURE.md §10).
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// The fixture tree carries /proc shapes like dangling fd
		// symlinks; only regular files are copyable data.
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture tree: %v", err)
	}
}

// TestDashboardStaleAgeSurfacesEndToEnd drives the §5 exit criterion
// through the real router with no pty: a good round renders fixture
// data, the next round fails the memory section (its meminfo file
// vanishes between rounds), and the dashboard keeps that section's
// last good values on screen under the round error plus their age —
// the keeps-last-good fix reaching the shell (the send/unwrapBatch
// pattern replays the runtime's message flow). The window is wide
// because the fixture path inflates the error text; the assertion
// targets the stale state, not the shell's width clipping.
func TestDashboardStaleAgeSurfacesEndToEnd(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "collectors", "testdata"), root)
	m := NewWithSystem(config.Default(), collectors.New(root))
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 400, Height: 30})

	// Round 1: every section healthy, fixture data flows.
	m, cmd := send(t, m, screens.RefreshMsg(time.Now()))
	for _, msg := range unwrapBatch(cmd()) {
		m, _ = send(t, m, msg)
	}
	if view := m.View(); !strings.Contains(view, "buildbox") {
		t.Fatalf("the first round must render the fixture hostname:\n%s", view)
	}

	// Round 2: the memory section's source vanishes; its last good
	// values stay on screen under the error and their age.
	if err := os.Remove(filepath.Join(root, "proc", "meminfo")); err != nil {
		t.Fatalf("remove meminfo: %v", err)
	}
	m, cmd = send(t, m, screens.RefreshMsg(time.Now()))
	for _, msg := range unwrapBatch(cmd()) {
		m, _ = send(t, m, msg)
	}
	view := m.View()
	if !strings.Contains(view, "meminfo") || !strings.Contains(view, "n/a (") {
		t.Errorf("the failed section must surface its error:\n%s", view)
	}
	if !strings.Contains(view, "stale") {
		t.Errorf("the failed section must age its last good values:\n%s", view)
	}
	if !strings.Contains(view, "buildbox") {
		t.Errorf("the healthy sections must keep rendering:\n%s", view)
	}
}
