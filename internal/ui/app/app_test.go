package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/config"
	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
)

// send routes one message through Update and returns the updated root model.
func send(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want app.Model", updated)
	}
	return got, cmd
}

// runesKey builds the KeyMsg produced by typing the given characters.
func runesKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestScreenCyclingMovesSidebarSelection(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyMsg
		want screens.ID
	}{
		{"tab from Dashboard activates Processes", tea.KeyMsg{Type: tea.KeyTab}, screens.Processes},
		{"shift+tab from Dashboard wraps to Users", tea.KeyMsg{Type: tea.KeyShiftTab}, screens.Users},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := send(t, New(config.Default()), tt.key)
			if m.active != tt.want {
				t.Errorf("active screen = %v, want %v", m.active, tt.want)
			}
			if got := m.sidebar.Selected(); got != tt.want {
				t.Errorf("sidebar selection = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHelpOverlayOpensSwallowsAndCloses(t *testing.T) {
	m, _ := send(t, New(config.Default()), runesKey("?"))
	if !m.showHelp {
		t.Fatal("? should open the help overlay")
	}

	m, _ = send(t, m, runesKey("x"))
	if !m.showHelp {
		t.Fatal("unrelated keys must be swallowed while help is open")
	}

	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showHelp {
		t.Fatal("esc should close the help overlay")
	}

	m, _ = send(t, m, runesKey("?"))
	if !m.showHelp {
		t.Fatal("? should reopen the help overlay")
	}
	m, _ = send(t, m, runesKey("?"))
	if m.showHelp {
		t.Fatal("? should also close the help overlay")
	}
}

func TestQuitKeyReturnsQuitCommand(t *testing.T) {
	_, cmd := send(t, New(config.Default()), runesKey("q"))
	if cmd == nil {
		t.Fatal("q should return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("q command produced %T, want tea.QuitMsg", cmd())
	}
}

func TestEnterActivatesSidebarSelection(t *testing.T) {
	m, _ := send(t, New(config.Default()), tea.KeyMsg{Type: tea.KeyDown})
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.active != screens.Processes {
		t.Fatalf("enter after down should activate Processes, got %v", m.active)
	}
}

func TestMousePressActivatesSidebarEntry(t *testing.T) {
	press := func(x, y int) tea.MouseMsg {
		return tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: x, Y: y}
	}
	tests := []struct {
		name string
		x, y int
		want screens.ID
	}{
		{"left press on the second entry activates Processes", 2, 2, screens.Processes},
		{"press below the last entry keeps the active screen", 2, 50, screens.Dashboard},
		{"press outside the sidebar keeps the active screen", 30, 2, screens.Dashboard},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := send(t, New(config.Default()), press(tt.x, tt.y))
			if m.active != tt.want {
				t.Errorf("active screen = %v, want %v", m.active, tt.want)
			}
		})
	}
}

func TestWindowSizeUpdatesDimensions(t *testing.T) {
	m, _ := send(t, New(config.Default()), tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.width != 120 || m.height != 40 {
		t.Fatalf("dimensions = %dx%d, want 120x40", m.width, m.height)
	}
}

func TestRefreshTickStoresTimeAndReschedules(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	m, cmd := send(t, New(config.Default()), screens.RefreshMsg(now))
	if !m.now.Equal(now) {
		t.Fatalf("now = %v, want %v", m.now, now)
	}
	if cmd == nil {
		t.Fatal("a tick must reschedule the next one")
	}
}

// TestDataMessagesReachTheActiveScreen is the regression test for the
// dashboard stuck on "collecting...": the router once swallowed every
// message it did not recognize, so screen data never arrived. This drives
// one full collection round against the collector fixtures and asserts the
// rendered dashboard shows fixture data.
func TestDataMessagesReachTheActiveScreen(t *testing.T) {
	fixtureRoot := filepath.Join("..", "..", "collectors", "testdata")
	m := NewWithSystem(config.Default(), collectors.New(fixtureRoot))

	m, _ = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, cmd := send(t, m, screens.RefreshMsg(time.Now()))
	if cmd == nil {
		t.Fatal("a refresh must trigger data collection")
	}

	// The batch carries the next tick plus the collect command; run every
	// part and feed resulting messages back through the router, like the
	// real Bubble Tea runtime would.
	for _, msg := range unwrapBatch(cmd()) {
		m, _ = send(t, m, msg)
	}

	if view := m.View(); !strings.Contains(view, "buildbox") {
		t.Errorf("dashboard must render fixture hostname after one round, got:\n%s", view)
	}
}

// unwrapBatch flattens a tea.BatchMsg so tests can replay every command's
// message through Update in order.
func unwrapBatch(msg tea.Msg) []tea.Msg {
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, cmd := range batch {
			if cmd != nil {
				out = append(out, unwrapBatch(cmd())...)
			}
		}
		return out
	}
	return []tea.Msg{msg}
}
