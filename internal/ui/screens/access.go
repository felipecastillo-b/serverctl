package screens

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// The Users module's two cadences of ARCHITECTURE.md §3: sessions at
// 10 s — logins change on human timescales, and a fresh login
// showing up within ten seconds feels live enough — and attempts at
// 30 s — an audit trail accumulates rather than changes, and every
// round re-walks a bounded sshd journal window, which is not a 10 s
// job. Two chains, one screen: each re-arms itself and dies
// independently while the screen is inactive (the network screen's
// two-cadence shape).
const (
	sessionsInterval = 10 * time.Second
	attemptsInterval = 30 * time.Second
)

// sessionsTickMsg re-arms one round of the 10 s chain.
type sessionsTickMsg struct{}

// attemptsTickMsg re-arms one round of the 30 s chain.
type attemptsTickMsg struct{}

// sessionsDataMsg carries one sessions round: the listing plus its
// own error — one failed chain never blanks the other view (§5, the
// network screen's per-half error persistence).
type sessionsDataMsg struct {
	sessions []core.UserSession
	err      error
}

// attemptsDataMsg carries one attempts round: the bounded listing
// plus its own error.
type attemptsDataMsg struct {
	attempts []core.SSHAttempt
	err      error
}

// accessView is which of the two listings the table renders; `v`
// cycles them. Both views ride their own collection chains, so a
// swap re-renders without re-collecting.
type accessView int

const (
	accessSessions accessView = iota
	accessAttempts
)

func (v accessView) String() string {
	if v == accessAttempts {
		return "attempts"
	}
	return "sessions"
}

// accessTimeLayout renders both views' time columns — the logs
// screen's layout, so timestamps read identically across screens.
const accessTimeLayout = "Jan 02 15:04:05"

// access is the Users screen: the login sessions and recent SSH
// attempts of m6b's collectors, cycled by `v`, one table per view
// with per-view loading/error state (§5). Read-only by design
// (ARCHITECTURE.md §7): no modal, no actions.
type access struct {
	sessions core.SessionLister
	attempts core.SSHAttemptLister
	theme    theme.Theme

	view accessView // which listing the table renders

	sessionsList []core.UserSession // last good sessions round
	attemptsList []core.SSHAttempt  // last good attempts round

	table table.Model

	sessionsLoaded, attemptsLoaded bool
	sessionsErr, attemptsErr       error

	lastWidth int
}

// NewAccess builds the Users screen listing sessions through
// sessions and SSH attempts through attempts, rendering with th.
// All() passes the live collectors.System for both ports; tests
// inject fakes (ARCHITECTURE.md §2: the UI consumes ports).
func NewAccess(sessions core.SessionLister, attempts core.SSHAttemptLister, th theme.Theme) Screen {
	return newAccess(sessions, attempts, th)
}

// newAccess is the concrete constructor; tests use it for white-box
// state assertions.
func newAccess(sessions core.SessionLister, attempts core.SSHAttemptLister, th theme.Theme) *access {
	tbl := table.New(
		table.WithColumns(sessionsColumnsFor(80)),
		table.WithFocused(true),
		table.WithKeyMap(navKeyMap()),
		table.WithStyles(table.Styles{
			Header: lipgloss.NewStyle().Bold(true).Foreground(th.Secondary),
			Selected: lipgloss.NewStyle().
				Foreground(th.Background).Background(th.Primary),
		}),
	)

	return &access{
		sessions:  sessions,
		attempts:  attempts,
		theme:     th,
		table:     tbl,
		lastWidth: -1,
	}
}

// sessionsColumnsFor lays out the sessions table for a given width;
// the FROM column absorbs whatever is left (37 cells are fixed).
// LOGIN AT needs 15: accessTimeLayout renders "Jan 02 15:04:05".
func sessionsColumnsFor(width int) []table.Column {
	from := width - 37
	if from < 8 {
		from = 8
	}
	return []table.Column{
		{Title: "USER", Width: 12},
		{Title: "TTY", Width: 10},
		{Title: "FROM", Width: from},
		{Title: "LOGIN AT", Width: 15},
	}
}

// attemptsColumnsFor lays out the attempts table for a given width;
// the SOURCE IP column absorbs what is left (35 cells are fixed) so
// a v6 address keeps room next to the fixed verdict column.
func attemptsColumnsFor(width int) []table.Column {
	ip := width - 35
	if ip < 8 {
		ip = 8
	}
	return []table.Column{
		{Title: "TIME", Width: 15},
		{Title: "USER", Width: 12},
		{Title: "SOURCE IP", Width: ip},
		{Title: "RESULT", Width: 8},
	}
}

// columnsFor returns the active view's column set.
func (s *access) columnsFor(width int) []table.Column {
	if s.view == accessAttempts {
		return attemptsColumnsFor(width)
	}
	return sessionsColumnsFor(width)
}

// Init starts BOTH chains: the first sessions and attempts rounds
// collect immediately, and the two self-armed ticks schedule the
// next rounds of each cadence.
func (s *access) Init() tea.Cmd {
	return tea.Batch(s.collectSessions(), s.collectAttempts(), s.tickSessions(), s.tickAttempts())
}

// tickSessions schedules the next self-armed 10 s tick. The chain
// dies naturally while the screen is inactive — the router delivers
// sessionsTickMsg to the ACTIVE screen only, so nobody re-arms it —
// and Init restarts it on re-entry (§5's cancellable-on-switch
// story). tickAttempts below is its own chain: each cadence dies and
// revives independently of the other.
func (s *access) tickSessions() tea.Cmd {
	return tea.Tick(sessionsInterval, func(time.Time) tea.Msg {
		return sessionsTickMsg{}
	})
}

// tickAttempts schedules the next self-armed 30 s tick of the
// attempts chain.
func (s *access) tickAttempts() tea.Cmd {
	return tea.Tick(attemptsInterval, func(time.Time) tea.Msg {
		return attemptsTickMsg{}
	})
}

// Update stores collection rounds and re-arms whichever cadence
// fired. The shell's global RefreshMsg (the 2 s cadence of the
// dashboard and processes screens) is deliberately ignored: the
// Users module runs its own two chains (ARCHITECTURE.md §3).
func (s *access) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case sessionsTickMsg:
		return s, tea.Batch(s.collectSessions(), s.tickSessions())
	case attemptsTickMsg:
		return s, tea.Batch(s.collectAttempts(), s.tickAttempts())
	case sessionsDataMsg:
		s.sessionsErr = msg.err
		if msg.err == nil {
			s.sessionsList = msg.sessions
			s.sessionsLoaded = true
		}
		s.reapply()
	case attemptsDataMsg:
		s.attemptsErr = msg.err
		if msg.err == nil {
			s.attemptsList = msg.attempts
			s.attemptsLoaded = true
		}
		s.reapply()
	}
	return s, nil
}

// collectSessions gathers the session listing asynchronously; the
// round rides back as one sessionsDataMsg.
func (s *access) collectSessions() tea.Cmd {
	return func() tea.Msg {
		sessions, err := s.sessions.Sessions()
		return sessionsDataMsg{sessions: sessions, err: err}
	}
}

// collectAttempts gathers the attempt listing asynchronously; the
// round rides back as one attemptsDataMsg.
func (s *access) collectAttempts() tea.Cmd {
	return func() tea.Msg {
		attempts, err := s.attempts.Attempts()
		return attemptsDataMsg{attempts: attempts, err: err}
	}
}

// UpdateKey applies the screen's keymap, which shadows the global
// one per key (ARCHITECTURE.md §6: q quits, ? helps, tab cycles —
// those still fall through; v is claimed here). The table's
// navigation bindings are claimed for the cursor; everything else
// falls through (§6 shadowing is per-key, not wholesale).
func (s *access) UpdateKey(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
	if msg.String() == "v" {
		s.cycleView()
		return s, nil, true
	}
	return s.claimNav(msg)
}

// claimNav offers the key to the table's navigation bindings only;
// every other key falls through to the global keymap (the §6
// per-key shadowing rule).
func (s *access) claimNav(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
	km := s.table.KeyMap
	if !key.Matches(msg, km.LineUp, km.LineDown, km.PageUp, km.PageDown,
		km.HalfPageUp, km.HalfPageDown, km.GotoTop, km.GotoBottom) {
		return s, nil, false
	}
	var cmd tea.Cmd
	s.table, cmd = s.table.Update(msg)
	return s, cmd, true
}

// UpdateMouse handles nothing: the Users module is read-only — no
// modal, no row actions — so every mouse event passes to the shell
// (§6's parity rule wants keyboard equivalents for mouse actions,
// and the keyboard side is complete).
func (s *access) UpdateMouse(tea.MouseMsg) (Screen, tea.Cmd, bool) {
	return s, nil, false
}

// cycleView advances the rendered listing: `v` walks sessions →
// attempts. Both views carry four columns, so a stale row can never
// out-arity the incoming column set — but the rows still clear
// before the swap and reapply() rebuilds them, the network screen's
// cycle shape, which keeps a future column count change from
// panicking on stale rows (m5a's SetColumns discovery). The cursor
// lands at the top: the two views list different entities, so there
// is no row identity to preserve across the cycle.
func (s *access) cycleView() {
	s.view = (s.view + 1) % 2
	s.table.SetRows(nil)                          // old-shaped rows must not meet the new columns
	s.table.SetColumns(s.columnsFor(s.lastWidth)) // safe on an empty table
	s.reapply()                                   // rows for the new view, cursor to the top
}

// reapply re-derives the displayed rows of the ACTIVE view from its
// last round, keeping the cursor on the same row identity when that
// row survives the round — the whole session tuple, the whole
// attempt tuple; the network screen's cursor-keeping shape. The
// cursor can sit outside the view (an empty table parks bubbles'
// cursor at -1), so bounds-check before reading the identity row
// (m5a's discovery).
func (s *access) reapply() {
	if s.view == accessAttempts {
		var keep core.SSHAttempt
		if cursor := s.table.Cursor(); cursor >= 0 && cursor < len(s.attemptsList) {
			keep = s.attemptsList[cursor]
		}
		s.table.SetRows(s.rowsFor(s.lastWidth))
		if idx := indexOfAttempt(s.attemptsList, keep); idx >= 0 {
			s.table.SetCursor(idx)
		} else {
			s.table.SetCursor(0)
		}
		return
	}
	var keep core.UserSession
	if cursor := s.table.Cursor(); cursor >= 0 && cursor < len(s.sessionsList) {
		keep = s.sessionsList[cursor]
	}
	s.table.SetRows(s.rowsFor(s.lastWidth))
	if idx := indexOfSession(s.sessionsList, keep); idx >= 0 {
		s.table.SetCursor(idx)
	} else {
		s.table.SetCursor(0)
	}
}

// indexOfSession finds a session row by identity: the whole row —
// user, tty, from and login instant together name one session.
func indexOfSession(sessions []core.UserSession, keep core.UserSession) int {
	for i, sess := range sessions {
		if sess == keep {
			return i
		}
	}
	return -1
}

// indexOfAttempt finds an attempt row by identity: the whole row.
func indexOfAttempt(attempts []core.SSHAttempt, keep core.SSHAttempt) int {
	for i, a := range attempts {
		if a == keep {
			return i
		}
	}
	return -1
}

// sessionRowsFor renders sessions as table rows, clipped to their
// column widths. FROM passes utmp's host field through — ":0" seat
// logins and remote hosts alike, core.UserSession documents the
// shapes.
func sessionRowsFor(sessions []core.UserSession, width int) []table.Row {
	cols := sessionsColumnsFor(width)
	rows := make([]table.Row, 0, len(sessions))
	for _, sess := range sessions {
		rows = append(rows, table.Row{
			clip(sess.User, cols[0].Width),
			clip(sess.TTY, cols[1].Width),
			clip(sess.From, cols[2].Width),
			clip(sess.LoginAt.Format(accessTimeLayout), cols[3].Width),
		})
	}
	return rows
}

// attemptRowsFor renders attempts as table rows, clipped. The
// RESULT cell reads "ok" or "failed"; the failed count rides the
// status line.
func attemptRowsFor(attempts []core.SSHAttempt, width int) []table.Row {
	cols := attemptsColumnsFor(width)
	rows := make([]table.Row, 0, len(attempts))
	for _, a := range attempts {
		result := "failed"
		if a.Success {
			result = "ok"
		}
		rows = append(rows, table.Row{
			clip(a.Time.Format(accessTimeLayout), cols[0].Width),
			clip(a.User, cols[1].Width),
			clip(a.SourceIP, cols[2].Width),
			clip(result, cols[3].Width),
		})
	}
	return rows
}

// rowsFor renders the active view's rows.
func (s *access) rowsFor(width int) []table.Row {
	if s.view == accessAttempts {
		return attemptRowsFor(s.attemptsList, width)
	}
	return sessionRowsFor(s.sessionsList, width)
}

// Title implements Screen.
func (s *access) Title() string { return "Users" }

// Hints implements Screen: the access keymap as the help overlay
// shows it below the global section. The navigation keys are listed
// because the screen claims them — the help overlay is where the
// user learns why up/down move the table here and the sidebar
// elsewhere (the logs screen's convention).
func (s *access) Hints() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "sessions/attempts view")),
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "move")),
		key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/pgdn", "page")),
	}
}

// View renders the active table with a status line (the network
// screen's shape: table, then the active view's footer).
func (s *access) View(width, height int) string {
	if width != s.lastWidth {
		s.lastWidth = width
		s.table.SetColumns(s.columnsFor(width))
		s.table.SetWidth(width)
		// Column widths changed: the rows must be reclipped.
		s.table.SetRows(s.rowsFor(s.lastWidth))
	}

	body := height - 1 // status line
	if body > 0 {
		s.table.SetHeight(body)
	}

	muted := lipgloss.NewStyle().Foreground(s.theme.Muted)
	danger := lipgloss.NewStyle().Foreground(s.theme.Danger)

	lines := []string{s.table.View(), s.statusLine(muted, danger)}

	// The table renders its own rows; the status line is plain, so the
	// composed height matches body+1 by construction.
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// statusLine composes the one-line footer: the ACTIVE view's
// collection error first, its collecting state until the first round
// lands, else the view's summary. Each view reads its own half's
// state — a failed sessions round never blanks the attempts listing
// (§5). A zero-attempts round appends journald's access-model hint:
// the common cause is unprivileged visibility — a reader outside the
// systemd-journal (or adm) group sees only their own session's
// entries (probed live: as a wheel-only user the sshd unit match
// returns zero entries with NO error) — and a reader with group
// membership but a quiet sshd sees the same hint, the imprecision
// the logs screen accepts for its empty buffer.
func (s *access) statusLine(muted, danger lipgloss.Style) string {
	if s.view == accessAttempts {
		if s.attemptsErr != nil {
			return danger.Render(fmt.Sprintf("n/a (%v)", s.attemptsErr))
		}
		if !s.attemptsLoaded {
			return danger.Render("collecting...")
		}
		failed := 0
		for _, a := range s.attemptsList {
			if !a.Success {
				failed++
			}
		}
		parts := []string{fmt.Sprintf("%d attempts · %d failed", len(s.attemptsList), failed)}
		if len(s.attemptsList) == 0 {
			parts = append(parts, "sshd journal needs systemd-journal group membership")
		}
		return muted.Render(strings.Join(parts, " · "))
	}

	if s.sessionsErr != nil {
		return danger.Render(fmt.Sprintf("n/a (%v)", s.sessionsErr))
	}
	if !s.sessionsLoaded {
		return danger.Render("collecting...")
	}
	return muted.Render(fmt.Sprintf("%d sessions · utmp", len(s.sessionsList)))
}
