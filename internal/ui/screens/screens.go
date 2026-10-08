// Package screens defines the screen contract hosted by serverctl's UI
// shell and the registry of module screens that fill it.
package screens

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/actions"
	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// RefreshMsg is emitted by the app shell's ticker every configured interval
// and forwarded to the active screen; live screens re-collect their data on
// it (ARCHITECTURE.md §5). It lives in this package because app imports
// screens, and the message contract must not create an import cycle.
type RefreshMsg time.Time

// SignalSender is the mutating surface a screen may call, satisfied by
// actions.Actor. Tests inject fakes so screen state machines can be
// exercised without touching real processes (ARCHITECTURE.md §4: actions
// is the only mutating layer).
type SignalSender interface {
	Signal(pid int, sig actions.ProcessSignal) error
}

// ID identifies a module screen. The declaration order matches the sidebar
// order of the modules listed in ARCHITECTURE.md §3.
type ID int

// The module screens of serverctl, in sidebar order.
const (
	Dashboard ID = iota
	Processes
	Services
	Logs
	Storage
	Network
	Packages
	Users
)

// Screen is a module view hosted by the root model. The shell owns layout
// and styling; a screen renders plain content for the bounds it is given.
type Screen interface {
	// Init returns the screen's initial command; data collection is wired
	// by the real screens in M2.
	Init() tea.Cmd
	// Update handles a message routed by the root model and returns the
	// possibly updated screen.
	Update(tea.Msg) (Screen, tea.Cmd)
	// UpdateKey offers a keypress to the screen BEFORE the global keymap:
	// the active screen's keymap shadows the global one (ARCHITECTURE.md
	// §6). handled reports whether the screen consumed the key.
	UpdateKey(tea.KeyMsg) (Screen, tea.Cmd, bool)
	// UpdateMouse offers a mouse event to the screen BEFORE the shell's
	// sidebar hit-test; handled reports consumption. A screen with an
	// open modal must swallow every event.
	UpdateMouse(tea.MouseMsg) (Screen, tea.Cmd, bool)
	// View renders the screen within the given bounds in terminal cells.
	View(width, height int) string
	// Title is the short module name shown in the header and sidebar.
	Title() string
	// Hints lists the screen-specific keybindings rendered by the help
	// overlay below the global section.
	Hints() []key.Binding
}

// All returns one screen per module, in sidebar order. The dashboard is the
// live system overview, processes the live process table, and services the
// live systemd unit table; the remaining modules are placeholder stubs
// until their milestones land.
func All(th theme.Theme, sys collectors.System) []Screen {
	out := make([]Screen, len(catalog))
	for i, s := range catalog {
		switch s.id {
		case Dashboard:
			out[i] = NewDashboard(sys, th)
		case Processes:
			out[i] = NewProcesses(sys, th, actions.Actor{})
		case Services:
			// collectors.System implements core.UnitLister over D-Bus
			// and actions.Actor implements core.UnitManager (m4a).
			out[i] = NewServices(sys, th, actions.Actor{})
		default:
			out[i] = s
		}
	}
	return out
}

// IDs returns the registered screen IDs in sidebar order, matching All().
func IDs() []ID {
	ids := make([]ID, len(catalog))
	for i, s := range catalog {
		ids[i] = s.id
	}
	return ids
}
