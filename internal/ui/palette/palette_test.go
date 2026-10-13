package palette

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// newShown builds a palette over the catalog titles and opens it.
func newShown(t *testing.T) *Model {
	t.Helper()
	m := New(catalogTitles, theme.Dark())
	m.Show()
	if !m.Open() {
		t.Fatal("Show must open the palette")
	}
	return &m
}

// typeRunes feeds the characters of s to the palette one keypress at a
// time, the way a terminal delivers them.
func typeRunes(t *testing.T, m *Model, s string) {
	t.Helper()
	for _, r := range s {
		action, _ := m.UpdateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if action != ActionNone {
			t.Fatalf("typing %q must only edit the filter, got action %v", r, action)
		}
	}
}

// press sends one non-rune key and returns the reported action.
func press(t *testing.T, m *Model, typ tea.KeyType) Action {
	t.Helper()
	action, _ := m.UpdateKey(tea.KeyMsg{Type: typ})
	return action
}

func TestShowListsEveryModuleInCatalogOrder(t *testing.T) {
	m := newShown(t)

	if got, ok := m.SelectedIndex(); !ok || got != 0 {
		t.Fatalf("the cursor starts on the first result, got index %d (ok=%v)", got, ok)
	}

	view := m.View(100, 30)
	last := -1
	for _, title := range catalogTitles {
		i := strings.Index(view, title)
		if i < 0 {
			t.Fatalf("the open palette must list %q:\n%s", title, view)
		}
		if i < last {
			t.Errorf("%q renders before the previous module, breaking catalog order:\n%s",
				title, view)
		}
		last = i
	}
}

func TestTypingFiltersResults(t *testing.T) {
	m := newShown(t)

	typeRunes(t, m, "proc")
	view := m.View(100, 30)
	if strings.Contains(view, "Network") {
		t.Errorf("a filtered palette must not list non-matching modules:\n%s", view)
	}
	if got, ok := m.SelectedIndex(); !ok || got != 1 {
		t.Fatalf("after filtering to Processes the highlight is on index %d (ok=%v), want 1",
			got, ok)
	}

	// An impossible query renders the empty state honestly and leaves
	// nothing to select or jump to.
	typeRunes(t, m, "z")
	if _, ok := m.SelectedIndex(); ok {
		t.Fatal("no result may stay selectable with zero matches")
	}
	if view := m.View(100, 30); !strings.Contains(view, "no matching module") {
		t.Errorf("zero matches must render the empty state:\n%s", view)
	}
	if action := press(t, m, tea.KeyEnter); action != ActionNone {
		t.Errorf("enter with zero matches must be a no-op, got action %v", action)
	}
}

func TestCursorWrapsBothEnds(t *testing.T) {
	m := newShown(t) // cursor 0 of 8 results

	press(t, m, tea.KeyUp)
	if got, ok := m.SelectedIndex(); !ok || got != 7 {
		t.Fatalf("up from the first result wraps to the last, got index %d (ok=%v)", got, ok)
	}

	press(t, m, tea.KeyDown)
	if got, ok := m.SelectedIndex(); !ok || got != 0 {
		t.Fatalf("down from the last result wraps to the first, got index %d (ok=%v)", got, ok)
	}
}

func TestEnterSelectsHighlightedResult(t *testing.T) {
	m := newShown(t)
	typeRunes(t, m, "proc")

	if action := press(t, m, tea.KeyEnter); action != ActionSelect {
		t.Fatalf("enter must report a selection, got action %v", action)
	}
	if got, ok := m.SelectedIndex(); !ok || got != 1 {
		t.Fatalf("selected index = %d (ok=%v), want Processes (1)", got, ok)
	}
}

func TestEscReportsClose(t *testing.T) {
	m := newShown(t)

	if action := press(t, m, tea.KeyEsc); action != ActionClose {
		t.Fatalf("esc must report a close, got action %v", action)
	}

	m.Hide()
	if m.Open() {
		t.Fatal("Hide must close the palette")
	}
	if view := m.View(100, 30); view != "" {
		t.Errorf("a closed palette must render nothing, got:\n%s", view)
	}
}

func TestOpenPaletteClaimsKeysTheScreensWouldActOn(t *testing.T) {
	m := newShown(t)

	// Keys that act globally when no modal is open are claimed by the
	// open palette instead: runes feed the filter, the rest is
	// swallowed. None of them may close or select.
	//
	// "q?" matches no module title, so the honest empty state proves
	// both runes reached the filter instead of leaking to the shell.
	// (The rendered filter value itself cannot be substring-matched:
	// the focused cursor wraps the last character in escape codes.)
	typeRunes(t, m, "q?")
	if view := m.View(100, 30); !strings.Contains(view, "no matching module") {
		t.Errorf("typed runes must reach the filter and narrow the results:\n%s", view)
	}
	for _, typ := range []tea.KeyType{tea.KeyTab, tea.KeyPgUp, tea.KeyCtrlU} {
		if action := press(t, m, typ); action != ActionNone {
			t.Errorf("%v must be swallowed by the open palette, got action %v", typ, action)
		}
	}
	if !m.Open() {
		t.Fatal("claimed keys must leave the palette open")
	}
}
