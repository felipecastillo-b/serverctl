package screens

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// packagesInterval is the Packages refresh cadence: 30 s, the slowest
// module tick — installed counts change only when the administrator
// runs a package transaction, which is ARCHITECTURE.md §3's
// on-demand territory, and the m6 doc settles it on the storage-style
// 30 s self-tick so the count visibly refreshes instead of living
// forever.
const packagesInterval = 30 * time.Second

// packagesTickMsg re-arms one round of the screen's own cadence.
type packagesTickMsg struct{}

// packagesDataMsg carries one collection round: the count plus its own
// error (§5: a failed round degrades this panel, never the program).
type packagesDataMsg struct {
	count core.PackageCount
	err   error
}

// packages is the Packages screen: the installed package count with
// the distro and manager that produced it. Its whole dataset is one
// summary row, so it mirrors the dashboard's plain-lines lead instead
// of the storage table on purpose: a table's filter, sort and
// navigation machinery has nothing to operate on over a single row,
// and the §6 keyboard side stays complete trivially — no mouse
// actions and no screen-local bindings. Read-only by design
// (ARCHITECTURE.md §7): no modal, no actions.
type packages struct {
	counter core.PackageCounter
	theme   theme.Theme

	count  core.PackageCount // last good round
	loaded bool
	err    error // error of the last round; the last good count stays (§5)
}

// NewPackages builds the Packages screen counting through counter and
// rendering with th. All() passes the live collectors.System; tests
// inject fakes (ARCHITECTURE.md §2: the UI consumes ports).
func NewPackages(counter core.PackageCounter, th theme.Theme) Screen {
	return newPackages(counter, th)
}

// newPackages is the concrete constructor; tests use it for white-box
// state assertions.
func newPackages(counter core.PackageCounter, th theme.Theme) *packages {
	return &packages{counter: counter, theme: th}
}

// Init collects the first round immediately and starts the screen's
// own 30 s cadence, the storage chain shape: the tick dies naturally
// while the screen is inactive — the router delivers packagesTickMsg
// to the ACTIVE screen only, so nobody re-arms it — and Init restarts
// it on re-entry (the §5 cancellable-on-switch story).
func (s *packages) Init() tea.Cmd {
	return tea.Batch(s.collect(), s.tick())
}

// tick schedules the next self-armed packages tick.
func (s *packages) tick() tea.Cmd {
	return tea.Tick(packagesInterval, func(time.Time) tea.Msg {
		return packagesTickMsg{}
	})
}

// Update stores collection rounds and re-arms the cadence. The
// shell's global RefreshMsg (the 2 s cadence of the dashboard and
// processes screens) is deliberately ignored: Packages refreshes on
// its own slow cadence (ARCHITECTURE.md §3).
func (s *packages) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case packagesTickMsg:
		return s, tea.Batch(s.collect(), s.tick())
	case packagesDataMsg:
		s.err = msg.err
		if msg.err == nil {
			s.count = msg.count
			s.loaded = true
		}
	}
	return s, nil
}

// collect gathers the installed count asynchronously; the round rides
// back as one packagesDataMsg.
func (s *packages) collect() tea.Cmd {
	return func() tea.Msg {
		count, err := s.counter.Count()
		return packagesDataMsg{count: count, err: err}
	}
}

// UpdateKey never claims a key: one summary row leaves nothing to
// navigate, filter or sort, so the global keymap (§6) serves every
// keypress.
func (s *packages) UpdateKey(tea.KeyMsg) (Screen, tea.Cmd, bool) { return s, nil, false }

// UpdateMouse never claims a mouse event: with no screen-local
// actions there is nothing for a click to hit, and §6's parity rule
// is satisfied by the empty set of mouse actions.
func (s *packages) UpdateMouse(tea.MouseMsg) (Screen, tea.Cmd, bool) { return s, nil, false }

// Title implements Screen.
func (s *packages) Title() string { return "Packages" }

// Hints implements Screen: no screen-local keybindings exist to list.
func (s *packages) Hints() []key.Binding { return nil }

// pkgLabel pads one field label so the three summary values align in
// a single column.
func pkgLabel(name string, style lipgloss.Style) string {
	return style.Width(10).Render(name)
}

// View renders the summary lines with a status line. The body appears
// once a round has landed; a failed later round keeps the last good
// lines standing while the status line carries the error — §5's
// stale-but-shown state — and before the first round only the status
// line's collecting state renders, the storage screen's empty-table
// shape.
func (s *packages) View(width, height int) string {
	label := lipgloss.NewStyle().Foreground(s.theme.Secondary)
	muted := lipgloss.NewStyle().Foreground(s.theme.Muted)
	danger := lipgloss.NewStyle().Foreground(s.theme.Danger)

	var lines []string
	if s.loaded {
		lines = append(lines,
			pkgLabel("Distro", label)+s.count.Distro,
			pkgLabel("Manager", label)+s.count.Manager,
			pkgLabel("Installed", label)+strconv.Itoa(s.count.Installed),
		)
	}
	lines = append(lines, s.statusLine(muted, danger))

	for i, line := range lines {
		if width > 0 && lipgloss.Width(line) > width {
			lines[i] = lipgloss.NewStyle().MaxWidth(width).Render(line)
		}
	}
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// statusLine composes the one-line footer: the last round's error
// first, the collecting state until the first round lands, else the
// listing summary — the count with the manager and distro that
// produced it, the storage footer's shape.
func (s *packages) statusLine(muted, danger lipgloss.Style) string {
	if s.err != nil {
		return danger.Render(fmt.Sprintf("n/a (%v)", s.err))
	}
	if !s.loaded {
		return danger.Render("collecting...")
	}
	summary := fmt.Sprintf("%d packages · %s", s.count.Installed, s.count.Manager)
	if s.count.Distro != "" {
		summary = fmt.Sprintf("%s on %s", summary, s.count.Distro)
	}
	return muted.Render(summary)
}
