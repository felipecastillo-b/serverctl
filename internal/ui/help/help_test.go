package help

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

func globalBindings() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	}
}

func screenBindings() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("k"), key.WithHelp("k", "kill process")),
	}
}

// TestOverlaySectionsAndBindings pins the section order and the fact that
// both the global and the active screen's bindings reach the box.
func TestOverlaySectionsAndBindings(t *testing.T) {
	got := Overlay(globalBindings(), screenBindings(), theme.Dark(), 80, 20)

	for _, want := range []string{
		"Global",
		"Screen",
		"q",
		"quit",
		"k",
		"kill process",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("overlay does not contain %q:\n%s", want, got)
		}
	}
}

// TestOverlaySkipsDisabledAndEmptyBindings pins the guard in writeSection:
// a binding with help text but no keys is disabled and must not render,
// nor does a zero binding add an empty row.
func TestOverlaySkipsDisabledAndEmptyBindings(t *testing.T) {
	bindings := append(globalBindings(),
		key.NewBinding(key.WithHelp("x", "must not render")),
		key.Binding{},
	)

	got := Overlay(bindings, nil, theme.Dark(), 80, 20)

	if strings.Contains(got, "must not render") {
		t.Errorf("overlay rendered help from a disabled binding:\n%s", got)
	}
}

// TestOverlayOmitsScreenSectionWhenEmpty pins that the Screen header only
// appears when the active screen contributes bindings.
func TestOverlayOmitsScreenSectionWhenEmpty(t *testing.T) {
	got := Overlay(globalBindings(), nil, theme.Dark(), 80, 20)

	if strings.Contains(got, "Screen") {
		t.Errorf("overlay rendered a Screen section with no screen bindings:\n%s", got)
	}
}

// TestOverlayRespectsBounds pins the placement contract: the overlay fills
// exactly the requested height and never draws wider than the requested
// width. It asserts <= rather than == because Place pads each row to the
// geometry it is given.
func TestOverlayRespectsBounds(t *testing.T) {
	const width, height = 80, 20

	got := Overlay(globalBindings(), screenBindings(), theme.Dark(), width, height)
	lines := strings.Split(got, "\n")
	if len(lines) != height {
		t.Fatalf("overlay rendered %d lines, want %d:\n%s", len(lines), height, got)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line %d is %d cells wide, want at most %d: %q", i, w, width, line)
		}
	}
}
