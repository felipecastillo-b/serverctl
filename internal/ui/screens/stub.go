package screens

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// catalog is the module registry in sidebar order; entry i serves ID(i).
// The descriptions mirror the data sources of ARCHITECTURE.md §3.
var catalog = []stub{
	{id: Dashboard, title: "Dashboard", description: "System overview: CPU, memory, load and uptime at a glance."},
	{id: Processes, title: "Processes", description: "Live process list collected from /proc."},
	{id: Services, title: "Services", description: "systemd unit states queried over D-Bus."},
	{id: Logs, title: "Logs", description: "Systemd journal entries, followed live."},
	{id: Storage, title: "Storage", description: "Mounts, filesystem usage and disk activity."},
	{id: Network, title: "Network", description: "Interfaces, addresses and traffic counters."},
	{id: Packages, title: "Packages", description: "Installed package count, distro and package manager."},
	{id: Users, title: "Users", description: "Logged-in users and active sessions."},
}

// stub is a placeholder screen shown while the real module screens are
// built (M2+). It renders static plain text and ignores every message.
type stub struct {
	id          ID
	title       string
	description string
}

// Init returns no command: stubs have nothing to collect.
func (s stub) Init() tea.Cmd { return nil }

// Update intentionally ignores all messages; refresh handling arrives with
// the real screens in M2.
func (s stub) Update(tea.Msg) (Screen, tea.Cmd) { return s, nil }

// UpdateKey never claims a key: stubs expose no screen-local bindings.
func (s stub) UpdateKey(tea.KeyMsg) (Screen, tea.Cmd, bool) { return s, nil, false }

// UpdateMouse never claims a mouse event.
func (s stub) UpdateMouse(tea.MouseMsg) (Screen, tea.Cmd, bool) { return s, nil, false }

// View renders the placeholder as plain text clipped to the given bounds.
// Styling is the app shell's job, not the screen's.
func (s stub) View(width, height int) string {
	lines := []string{
		s.title,
		"",
		s.description,
		"",
		"This module is not implemented yet.",
	}
	if width > 0 {
		for i, line := range lines {
			if len(line) > width {
				lines[i] = line[:width]
			}
		}
	}
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// Title returns the module name shown in the header and sidebar.
func (s stub) Title() string { return s.title }

// Hints is empty: stubs have no screen-specific keybindings yet.
func (s stub) Hints() []key.Binding { return nil }
