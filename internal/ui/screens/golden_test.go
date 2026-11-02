package screens

import (
	"fmt"
	"os"
	"testing"

	"github.com/charmbracelet/x/exp/golden"
)

// TestMain pins the renderer to the plain profile before any view is
// composed: the goldens freeze layout at a fixed geometry, not color,
// and NO_COLOR keeps lipgloss from emitting ANSI escapes. It must run
// before m.Run so the renderer's lazy profile detection sees it.
func TestMain(m *testing.M) {
	if err := os.Setenv("NO_COLOR", "1"); err != nil {
		fmt.Fprintln(os.Stderr, "set NO_COLOR:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// TestGoldenServicesScreen freezes the services table at 100x24: header,
// five .service rows in default name order, and the listing summary.
func TestGoldenServicesScreen(t *testing.T) {
	s := newServicesScreen(t, &fakeLister{units: unitFixture}, &fakeManager{})
	golden.RequireEqual(t, []byte(s.View(100, 24)))
}

// TestGoldenContainersScreen freezes the containers table at 100x24.
// The fixture pins the screen clock, so the uptime cells are stable.
func TestGoldenContainersScreen(t *testing.T) {
	s := newContainersScreen(t, &fakeContainerLister{list: containerRound})
	golden.RequireEqual(t, []byte(s.View(100, 24)))
}

// TestGoldenStorageScreen freezes the filesystems table at 100x24:
// header, four rows in use% order, and the filesystems summary.
func TestGoldenStorageScreen(t *testing.T) {
	s := newStorageScreen(t, &fakeStorageLister{filesystems: fsFixture, disks: diskFixture})
	golden.RequireEqual(t, []byte(s.View(100, 24)))
}

// TestGoldenLogsScreen freezes the journal view at 100x24: the three
// fixture lines with their fixed timestamps and the following summary.
func TestGoldenLogsScreen(t *testing.T) {
	s := newLogsScreen(t, &fakeJournal{tail: logFixture, tailCursor: "c3"})
	golden.RequireEqual(t, []byte(s.View(100, 24)))
}

// TestGoldenDetailScreen freezes the generic detail body at 80x20. The
// detail only renders the title and lines it was handed, so the frame
// covers the wrap/clip path without any collector input.
func TestGoldenDetailScreen(t *testing.T) {
	d := NewDetail("Process 2 (kthreadd)", []string{
		"PID:       2",
		"Parent:    0",
		"User:      root",
		"State:     S",
		"Command:   [kthreadd]",
	})
	golden.RequireEqual(t, []byte(d.View(80, 20)))
}
