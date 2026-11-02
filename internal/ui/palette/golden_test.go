package palette

import (
	"fmt"
	"os"
	"testing"

	"github.com/charmbracelet/x/exp/golden"

	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
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

// TestGoldenPalette freezes the open command palette at 80x24: the
// centered rounded box, title, filter input, the full catalog in order
// and the hint line.
func TestGoldenPalette(t *testing.T) {
	p := New([]string{
		"Dashboard",
		"Processes",
		"Services",
		"Logs",
		"Storage",
		"Network",
		"Packages",
		"Users",
		"Containers",
		"Send TERM to pid 2 (kthreadd)",
	}, theme.Dark())
	p.Show()
	golden.RequireEqual(t, []byte(p.View(80, 24)))
}
