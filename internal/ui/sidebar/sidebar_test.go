package sidebar

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

var (
	testIDs    = []screens.ID{screens.Dashboard, screens.Processes, screens.Services}
	testTitles = []string{"Dashboard", "Processes", "Services"}
)

func testSidebar() Model {
	return New(testIDs, testTitles)
}

// TestSelectedAndMovement pins the initial selection and the clamping at
// both ends of the list.
func TestSelectedAndMovement(t *testing.T) {
	m := testSidebar()

	if got := m.Selected(); got != screens.Dashboard {
		t.Fatalf("new sidebar selected %v, want %v", got, screens.Dashboard)
	}

	m.MoveDown()
	m.MoveDown()
	if got := m.Selected(); got != screens.Services {
		t.Fatalf("two MoveDown calls selected %v, want %v", got, screens.Services)
	}
	m.MoveDown()
	if got := m.Selected(); got != screens.Services {
		t.Errorf("MoveDown past the last entry selected %v, want %v", got, screens.Services)
	}

	m.MoveUp()
	m.MoveUp()
	if got := m.Selected(); got != screens.Dashboard {
		t.Fatalf("two MoveUp calls selected %v, want %v", got, screens.Dashboard)
	}
	m.MoveUp()
	if got := m.Selected(); got != screens.Dashboard {
		t.Errorf("MoveUp past the first entry selected %v, want %v", got, screens.Dashboard)
	}
}

// TestSetSelectedIgnoresUnknown pins that a known ID moves the selection
// while an unknown one leaves it untouched.
func TestSetSelectedIgnoresUnknown(t *testing.T) {
	m := testSidebar()

	m.SetSelected(screens.Processes)
	if got := m.Selected(); got != screens.Processes {
		t.Fatalf("SetSelected(Processes) selected %v, want Processes", got)
	}

	m.SetSelected(screens.ID(99))
	if got := m.Selected(); got != screens.Processes {
		t.Errorf("SetSelected(unknown) changed the selection to %v, want Processes", got)
	}
}

// TestSelectAtRowHitTest pins the mouse hit-test: a drawn row selects its
// entry, out-of-range rows report false and keep the selection.
func TestSelectAtRowHitTest(t *testing.T) {
	m := testSidebar()

	got, ok := m.SelectAtRow(1)
	if !ok || got != screens.Processes {
		t.Fatalf("SelectAtRow(1) = (%v, %v), want (Processes, true)", got, ok)
	}
	if m.Selected() != screens.Processes {
		t.Fatalf("SelectAtRow(1) selected %v, want Processes", m.Selected())
	}

	for _, row := range []int{-1, len(testIDs)} {
		if _, ok := m.SelectAtRow(row); ok {
			t.Errorf("SelectAtRow(%d) reported a hit, want false", row)
		}
		if got := m.Selected(); got != screens.Processes {
			t.Errorf("SelectAtRow(%d) changed the selection to %v, want Processes", row, got)
		}
	}
}

// TestEmptySidebar pins the defensive zero-value behavior: no entries means
// Dashboard, and movement or hit-testing never panics.
func TestEmptySidebar(t *testing.T) {
	m := New(nil, nil)

	if got := m.Selected(); got != screens.Dashboard {
		t.Fatalf("empty sidebar selected %v, want Dashboard", got)
	}

	m.MoveUp()
	m.MoveDown()
	if got := m.Selected(); got != screens.Dashboard {
		t.Errorf("moving an empty sidebar selected %v, want Dashboard", got)
	}
	if _, ok := m.SelectAtRow(0); ok {
		t.Error("SelectAtRow(0) on an empty sidebar reported a hit, want false")
	}
}

// TestViewRendersSelectionAndClips pins the render contract: every title
// appears at a comfortable height, a short height clips the listing to its
// bounds, the selected row carries the mark, and no row exceeds the fixed
// sidebar width.
func TestViewRendersSelectionAndClips(t *testing.T) {
	m := testSidebar()

	tall := m.View(theme.Dark(), 10)
	for _, title := range testTitles {
		if !strings.Contains(tall, title) {
			t.Errorf("height-10 view does not contain %q:\n%s", title, tall)
		}
	}

	short := m.View(theme.Dark(), 2)
	lines := strings.Split(short, "\n")
	if len(lines) != 2 {
		t.Fatalf("height-2 view rendered %d lines, want 2:\n%s", len(lines), short)
	}
	if !strings.Contains(short, testTitles[0]) || !strings.Contains(short, testTitles[1]) {
		t.Errorf("height-2 view dropped one of the first two titles:\n%s", short)
	}
	if strings.Contains(short, testTitles[2]) {
		t.Errorf("height-2 view kept the clipped third title %q:\n%s", testTitles[2], short)
	}
	if !strings.Contains(short, "▎") {
		t.Errorf("view does not carry the selection mark:\n%s", short)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > Width {
			t.Errorf("line %d is %d cells wide, want at most %d: %q", i, w, Width, line)
		}
	}
}
