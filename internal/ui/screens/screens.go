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

// Cadences carries the refresh intervals of the eight self-ticked
// collection chains of ARCHITECTURE.md §3 from the config layer to
// the screen constructors. All() is the injection point: the app
// resolves the configured intervals into this struct so the screens
// package stays self-contained — it knows durations, never YAML
// keys. The shell-wide RefreshMsg screens (dashboard, processes) are
// not here: they ride the config's refresh_seconds, not intervals.
//
// A zero field means "use the built-in default for that chain", which
// the constructors apply, so a partially wired Cadences can never
// stall a screen on a zero tick.
type Cadences struct {
	Services          time.Duration
	Logs              time.Duration
	Storage           time.Duration
	NetworkInterfaces time.Duration
	NetworkSockets    time.Duration
	Packages          time.Duration
	UsersSessions     time.Duration
	UsersAttempts     time.Duration
}

// DefaultCadences returns the built-in per-chain intervals, the §3
// cadences the screens shipped with before they became configurable:
// one constant per chain.
func DefaultCadences() Cadences {
	return Cadences{
		Services:          defaultServicesInterval,
		Logs:              defaultLogsInterval,
		Storage:           defaultStorageInterval,
		NetworkInterfaces: defaultNetInterval,
		NetworkSockets:    defaultSocksInterval,
		Packages:          defaultPackagesInterval,
		UsersSessions:     defaultSessionsInterval,
		UsersAttempts:     defaultAttemptsInterval,
	}
}

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

// All returns one screen per module, in sidebar order, wired to the
// live collectors.System and the given cadences: the dashboard
// (system overview), processes, services (systemd units), logs
// (journal tail), storage (mounts and disks), network (interfaces
// and sockets), packages (installed count) and users (login sessions
// and SSH attempts). A module whose milestone has not landed yet
// would keep its catalog stub; with m6b all eight are live.
//
// All() is the intervals injection point of ARCHITECTURE.md §3: the
// app resolves the config's `intervals` section into cad, and each
// constructor receives its own chain's cadence (Network and Access
// receive both of theirs).
func All(th theme.Theme, sys collectors.System, cad Cadences) []Screen {
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
			out[i] = NewServices(sys, th, actions.Actor{}, cad.Services)
		case Logs:
			// collectors.System implements core.JournalReader over
			// sdjournal (m4c).
			out[i] = NewLogs(sys, th, cad.Logs)
		case Storage:
			// collectors.System implements core.StorageLister over
			// /proc/mounts, statfs and /proc/diskstats (m5a).
			out[i] = NewStorage(sys, th, cad.Storage)
		case Network:
			// collectors.System implements core.NetLister over
			// /proc/net/dev + netlink and core.SocketLister over
			// /proc/net/{tcp,tcp6,udp,udp6} (m5b). Both chain
			// intervals: interfaces and sockets.
			out[i] = NewNetwork(sys, sys, th, cad.NetworkInterfaces, cad.NetworkSockets)
		case Packages:
			// collectors.System implements core.PackageCounter over
			// the pacman local database with a `pacman -Qq` fixed-argv
			// fallback (m6a).
			out[i] = NewPackages(sys, th, cad.Packages)
		case Users:
			// collectors.System implements core.SessionLister over
			// logind D-Bus with a utmp fallback, and
			// core.SSHAttemptLister over the sshd unit's journal
			// (m6b). Both chain intervals: sessions and attempts.
			out[i] = NewAccess(sys, sys, th, cad.UsersSessions, cad.UsersAttempts)
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
