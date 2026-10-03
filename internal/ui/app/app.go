// Package app wires the root Bubble Tea model of serverctl: it hosts the
// sidebar, the active screen, the refresh ticker, the global keymap, mouse
// input and the help overlay.
package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/config"
	"github.com/felipecastillo-b/serverctl/internal/ui/help"
	"github.com/felipecastillo-b/serverctl/internal/ui/keys"
	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
	"github.com/felipecastillo-b/serverctl/internal/ui/sidebar"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// tickMsg is emitted by the refresh ticker. Screens use it to re-collect
// their data (wired in M2); the stub screens ignore it.
type tickMsg time.Time

const (
	// minWidth and minHeight bound the smallest terminal the shell lays
	// out; below them a placeholder is rendered instead of a broken TUI.
	minWidth  = 40
	minHeight = 8
)

// Model is the root tea.Model of the UI shell.
type Model struct {
	cfg      config.Config
	theme    theme.Theme
	keys     keys.Global
	sidebar  sidebar.Model
	screens  []screens.Screen
	active   screens.ID
	showHelp bool
	width    int
	height   int
	now      time.Time
}

// New builds the root model from an already validated configuration: the
// caller (main) exits on invalid key overrides, so ApplyOverrides cannot
// fail here and its error is intentionally discarded.
func New(cfg config.Config) Model {
	global := keys.DefaultGlobal()
	_ = global.ApplyOverrides(cfg.Keys)

	list := screens.All()
	titles := make([]string, len(list))
	for i, s := range list {
		titles[i] = s.Title()
	}

	return Model{
		cfg:     cfg,
		theme:   theme.FromName(cfg.Theme),
		keys:    global,
		sidebar: sidebar.New(screens.IDs(), titles),
		screens: list,
		active:  screens.Dashboard,
		now:     time.Now(),
	}
}

// Init starts the refresh ticker.
func (m Model) Init() tea.Cmd {
	return m.tick()
}

// tick schedules the next refresh tick after the configured interval.
func (m Model) tick() tea.Cmd {
	return tea.Tick(time.Duration(m.cfg.RefreshSeconds)*time.Second,
		func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Update routes incoming messages to the shell and the active screen.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tickMsg:
		m.now = time.Time(msg)
		// Forward the refresh tick to the active screen; the stub screens
		// ignore it until their collectors land in M2.
		updated, cmd := m.screens[m.active].Update(msg)
		m.screens[m.active] = updated
		return m, tea.Batch(cmd, m.tick())
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

// handleKey applies the input rules of ARCHITECTURE.md §6. While the help
// overlay is open it is modal: only its close keys act and everything else
// is swallowed.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showHelp {
		if key.Matches(msg, m.keys.Help, m.keys.Back) {
			m.showHelp = false
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.showHelp = true
	case key.Matches(msg, m.keys.NextScreen):
		updated, cmd := m.setActive(m.cycle(1))
		return updated, cmd
	case key.Matches(msg, m.keys.PrevScreen):
		updated, cmd := m.setActive(m.cycle(-1))
		return updated, cmd
	case key.Matches(msg, m.keys.Up):
		m.sidebar.MoveUp()
	case key.Matches(msg, m.keys.Down):
		m.sidebar.MoveDown()
	case key.Matches(msg, m.keys.Select):
		updated, cmd := m.setActive(m.sidebar.Selected())
		return updated, cmd
	case key.Matches(msg, m.keys.Back):
		// No-op at the top level: esc pops the screen stack once screens
		// can push detail views (ARCHITECTURE.md §6, M2).
	}
	return m, nil
}

// handleMouse activates sidebar entries on left-button press. The sidebar
// column starts below the one-row header, so screen row y maps to sidebar
// row y-1. Like the keyboard, it is inert while the help overlay is open.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.showHelp {
		return m, nil
	}
	event := tea.MouseEvent(msg)
	if event.Action != tea.MouseActionPress || event.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if event.X < sidebar.Width && event.Y >= 1 {
		if id, ok := m.sidebar.SelectAtRow(event.Y - 1); ok {
			updated, cmd := m.setActive(id)
			return updated, cmd
		}
	}
	return m, nil
}

// cycle returns the screen ID delta positions away from the active one,
// wrapping around both ends of the registry.
func (m Model) cycle(delta int) screens.ID {
	n := len(m.screens)
	return screens.ID(((int(m.active)+delta)%n + n) % n)
}

// setActive switches the active screen, moves the sidebar selection to
// match, and returns the new screen's initial command.
func (m Model) setActive(id screens.ID) (Model, tea.Cmd) {
	m.active = id
	m.sidebar.SetSelected(id)
	return m, m.screens[m.active].Init()
}

// View renders the shell: header bar, sidebar plus active screen (or the
// help overlay), and the footer hint bar.
func (m Model) View() string {
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("terminal too small: need at least %dx%d, got %dx%d",
			minWidth, minHeight, m.width, m.height)
	}

	contentHeight := m.height - 2 // One header row and one footer row.

	var body string
	if m.showHelp {
		body = help.Overlay(m.keys.Bindings(), m.screens[m.active].Hints(),
			m.theme, m.width, contentHeight)
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			m.sidebar.View(m.theme, contentHeight),
			m.screens[m.active].View(m.contentWidth(), contentHeight),
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left, m.headerView(), body, m.footerView())
}

// contentWidth is the width available to the active screen next to the
// sidebar, floored at zero.
func (m Model) contentWidth() int {
	if w := m.width - sidebar.Width; w > 0 {
		return w
	}
	return 0
}

// headerView renders the top bar with the program name and the active
// screen's title, styled with the theme's primary color.
func (m Model) headerView() string {
	title := fmt.Sprintf(" serverctl › %s", m.screens[m.active].Title())
	return lipgloss.NewStyle().
		Background(m.theme.Primary).
		Foreground(m.theme.Background).
		Bold(true).
		Width(m.width).
		MaxWidth(m.width).
		Render(title)
}

// footerView renders the bottom bar: a compact join of the global
// keybindings as "key description" pairs.
func (m Model) footerView() string {
	parts := make([]string, 0, len(m.keys.Bindings()))
	for _, binding := range m.keys.Bindings() {
		h := binding.Help()
		parts = append(parts, h.Key+" "+h.Desc)
	}
	// MaxWidth truncates the hints at the terminal edge; Width is avoided
	// on purpose because it wraps long content instead of truncating it.
	return lipgloss.NewStyle().
		Foreground(m.theme.Muted).
		MaxWidth(m.width).
		Render(" " + strings.Join(parts, "  ·  "))
}
