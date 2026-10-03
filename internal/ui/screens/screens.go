// Package screens defines the screen contract hosted by serverctl's UI
// shell and the registry of module screens that fill it.
package screens

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

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
	// View renders the screen within the given bounds in terminal cells.
	View(width, height int) string
	// Title is the short module name shown in the header and sidebar.
	Title() string
	// Hints lists the screen-specific keybindings rendered by the help
	// overlay below the global section.
	Hints() []key.Binding
}

// All returns one screen per module, in sidebar order. Until the real
// module screens land (M2+), every entry is a placeholder stub.
func All() []Screen {
	out := make([]Screen, len(catalog))
	for i, s := range catalog {
		out[i] = s
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
