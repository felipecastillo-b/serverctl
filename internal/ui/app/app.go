// Package app wires the root Bubble Tea model of serverctl: it hosts the
// sidebar, the active screen, the refresh ticker, the global keymap, mouse
// input, the help overlay and the command palette.
package app

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/config"
	"github.com/felipecastillo-b/serverctl/internal/ui/help"
	"github.com/felipecastillo-b/serverctl/internal/ui/keys"
	"github.com/felipecastillo-b/serverctl/internal/ui/palette"
	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
	"github.com/felipecastillo-b/serverctl/internal/ui/sidebar"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// tick produces the shared screens.RefreshMsg every configured interval;
// the active screen re-collects its data on it (wired since M2).

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
	// stack holds pushed screens; the top is the last element. Empty
	// means the active module owns the pane.
	stack []screens.Screen
	// palette is the command palette overlay; a field (not a pointer)
	// because Model runs on value receivers and tests mutate fields
	// directly.
	palette palette.Model
	width   int
	height  int
	now     time.Time
}

// New builds the root model against the live kernel interfaces.
func New(cfg config.Config) Model {
	return NewWithSystem(cfg, collectors.New("/"))
}

// NewWithSystem builds the root model reading system data from sys. Tests
// point it at collector fixtures; production always uses New.
func NewWithSystem(cfg config.Config, sys collectors.System) Model {
	global := keys.DefaultGlobal()
	_ = global.ApplyOverrides(cfg.Keys)

	list := screens.All(theme.FromName(cfg.Theme), sys, cadences(cfg))
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
		palette: palette.New(titles, theme.FromName(cfg.Theme)),
		active:  screens.Dashboard,
		now:     time.Now(),
	}
}

// cadences resolves the configured per-chain intervals into the
// screens package's duration shape: config owns the YAML keys and
// seconds, screens own the durations, and this is the only place the
// two meet. Config.Load fills every key (defaults merged per key), so
// every chain resolves; a hand-built Config with no intervals reads
// zero, which the screen constructors replace with the built-in
// defaults.
func cadences(cfg config.Config) screens.Cadences {
	return screens.Cadences{
		Services:          cfg.Cadence(config.IntervalServices),
		Logs:              cfg.Cadence(config.IntervalLogs),
		Storage:           cfg.Cadence(config.IntervalStorage),
		NetworkInterfaces: cfg.Cadence(config.IntervalNetworkInterfaces),
		NetworkSockets:    cfg.Cadence(config.IntervalNetworkSockets),
		Packages:          cfg.Cadence(config.IntervalPackages),
		UsersSessions:     cfg.Cadence(config.IntervalUsersSessions),
		UsersAttempts:     cfg.Cadence(config.IntervalUsersAttempts),
		Containers:        cfg.Cadence(config.IntervalContainers),
	}
}

// Init starts the refresh ticker AND the active screen's initial data
// collection; without the latter the dashboard would render its
// "collecting..." state idle until the first tick.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.tick(), m.screens[m.active].Init())
}

// tick schedules the next refresh tick after the configured interval.
func (m Model) tick() tea.Cmd {
	return tea.Tick(time.Duration(m.cfg.RefreshSeconds)*time.Second,
		func(t time.Time) tea.Msg { return screens.RefreshMsg(t) })
}

// Update routes incoming messages to the shell and the active screen.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case screens.RefreshMsg:
		m.now = time.Time(msg)
		// Forward the refresh tick to the top screen; stub screens ignore
		// it, live screens re-collect their data on it.
		updated, cmd := m.topScreen().Update(msg)
		m.setTop(updated)
		return m, tea.Batch(cmd, m.tick())
	case screens.OpenDetailMsg:
		// A screen pushes a static detail; the app owns the stack, so
		// the pushed screen's Init is the only command to return.
		m.stack = append(m.stack, screens.NewDetail(msg.Title, msg.Lines))
		return m, m.topScreen().Init()
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	default:
		// Every other message (screen data results, action outcomes, ...)
		// belongs to the top screen. Swallowing them here once left the
		// dashboard stuck on "collecting..."; the router MUST forward.
		updated, cmd := m.topScreen().Update(msg)
		m.setTop(updated)
		return m, cmd
	}
}

// topScreen returns the screen that currently owns input and the content
// pane: the last pushed screen when the stack is non-empty, otherwise the
// active module.
func (m Model) topScreen() screens.Screen {
	if n := len(m.stack); n > 0 {
		return m.stack[n-1]
	}
	return m.screens[m.active]
}

// setTop writes updated back to wherever topScreen came from.
func (m Model) setTop(updated screens.Screen) {
	if n := len(m.stack); n > 0 {
		m.stack[n-1] = updated
		return
	}
	m.screens[m.active] = updated
}

// handleKey applies the input rules of ARCHITECTURE.md §6. While the
// help overlay or the command palette is open, it is modal: only the
// overlay's keys act and everything else is swallowed. Otherwise the top
// screen's keymap shadows the global one per key: sort, filter and signal
// bindings are claimed before any global action can fire.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showHelp {
		if key.Matches(msg, m.keys.Help, m.keys.Back) {
			m.showHelp = false
		}
		return m, nil
	}
	if m.palette.Open() {
		return m.handlePaletteKey(msg)
	}

	updated, cmd, handled := m.topScreen().UpdateKey(msg)
	if handled {
		m.setTop(updated)
		return m, cmd
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Palette):
		m.palette.Show()
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
		// esc pops one pushed screen. Emptying the stack hands the pane
		// back to the module, which is re-initialized so its data is
		// fresh; at the top level with no pushed screens this stays a
		// no-op.
		if len(m.stack) > 0 {
			m.stack = m.stack[:len(m.stack)-1]
			if len(m.stack) == 0 {
				return m, m.screens[m.active].Init()
			}
			return m, nil
		}
	}
	return m, nil
}

// handlePaletteKey routes a keypress through the open palette, the same
// modal-first claiming the help overlay applies: the palette key
// toggles it closed, esc closes it, enter jumps to the highlighted
// module via setActive, and every other key is claimed by the palette
// itself (filter text or swallowed). Data messages keep flowing to the
// active screen underneath; the palette only claims keys and the
// render.
func (m Model) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Palette) {
		m.palette.Hide()
		return m, nil
	}

	action, cmd := m.palette.UpdateKey(msg)
	switch action {
	case palette.ActionClose:
		m.palette.Hide()
	case palette.ActionSelect:
		index, ok := m.palette.SelectedIndex()
		if !ok {
			return m, cmd
		}
		m.palette.Hide()
		updated, jumpCmd := m.setActive(screens.IDs()[index])
		return updated, jumpCmd
	}
	return m, cmd
}

// handleMouse offers every press to the active screen first: a screen
// with an open confirmation modal is exclusive and swallows the event
// before the shell acts (ARCHITECTURE.md §7). Screens render inside the
// content pane, below the one-row header and to the right of the sidebar,
// so every event is translated into that pane before delivery: a screen
// hit-tests exactly the cells it rendered. Events over the sidebar or the
// header arrive with negative coordinates and screens ignore them, but an
// open modal still swallows them. Unclaimed presses fall through to the
// sidebar hit-test in raw terminal coordinates: the sidebar column starts
// below the header, so screen row y maps to sidebar row y-1. Mouse input
// stays inert while the help overlay or the palette is open; both overlays
// are keyboard-only.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.showHelp || m.palette.Open() {
		return m, nil
	}

	event := tea.MouseEvent(msg)
	local := msg
	local.X -= sidebar.Width
	local.Y--
	updated, cmd, handled := m.topScreen().UpdateMouse(local)
	if handled {
		m.setTop(updated)
		return m, cmd
	}

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
	// A module switch abandons pushed details: the new module owns the
	// pane from the base of a fresh stack.
	m.stack = nil
	return m, m.screens[m.active].Init()
}

// View renders the shell: header bar, sidebar plus active screen (or
// an overlay: the help listing or the command palette), and the footer
// hint bar.
func (m Model) View() string {
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("terminal too small: need at least %dx%d, got %dx%d",
			minWidth, minHeight, m.width, m.height)
	}

	contentHeight := m.height - 2 // One header row and one footer row.

	var body string
	switch {
	case m.showHelp:
		body = help.Overlay(m.keys.Bindings(), m.topScreen().Hints(),
			m.theme, m.width, contentHeight)
	case m.palette.Open():
		body = m.palette.View(m.width, contentHeight)
	default:
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			m.sidebar.View(m.theme, contentHeight),
			m.topScreen().View(m.contentWidth(), contentHeight),
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

// headerView renders the top bar with the program name and the top
// screen's title, styled with the theme's primary color.
func (m Model) headerView() string {
	title := fmt.Sprintf(" serverctl › %s", m.topScreen().Title())
	return lipgloss.NewStyle().
		Background(m.theme.Primary).
		Foreground(m.theme.Background).
		Bold(true).
		Width(m.width).
		MaxWidth(m.width).
		Render(title)
}

// footerView renders the bottom bar: a compact join of the global
// keybindings as "key description" pairs. Hints are fitted whole; the
// MaxWidth stays only as a belt-and-suspenders no-op after the fitter.
func (m Model) footerView() string {
	bindings := m.keys.Bindings()
	parts := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		h := binding.Help()
		parts = append(parts, h.Key+" "+h.Desc)
	}
	return lipgloss.NewStyle().
		Foreground(m.theme.Muted).
		MaxWidth(m.width).
		Render(fitFooterHints(parts, m.width))
}

// footerSep separates footer hints. Hints are atomic to the fitter, so
// this separator only ever appears between two whole hints.
const footerSep = "  ·  "

// fitFooterHints greedily joins whole hint parts with footerSep into a
// footer line that fits width display columns. The width contract
// includes the line's leading space (the footer bar's left padding) and
// every separator: lipgloss.Width(result) <= width always holds. The
// result carries no trailing separator.
//
// Hints are kept whole because the shell truncates entire lines at the
// terminal edge: clipping a joined line cuts the last hint mid-word.
// Parts are added in order until the first one that does not fit, which
// ends the fit — later hints are dropped whole rather than skipped,
// keeping the footer's stable key order free of misleading gaps. When
// width is zero, or cannot hold the leading space plus the first hint,
// the footer renders empty.
func fitFooterHints(parts []string, width int) string {
	if width <= 0 {
		return ""
	}
	line := ""
	fitted := false
	for _, part := range parts {
		candidate := " " + part
		if fitted {
			candidate = line + footerSep + part
		}
		if lipgloss.Width(candidate) > width {
			break
		}
		line = candidate
		fitted = true
	}
	return line
}
