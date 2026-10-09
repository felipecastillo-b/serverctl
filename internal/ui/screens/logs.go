package screens

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// logsInterval is the Logs refresh cadence of ARCHITECTURE.md §3:
// "follow, ~1 s" — deliberately faster than the shell-wide 2 s
// RefreshMsg, because a journal tail is worth watching live and a
// local sdjournal round is cheap.
const logsInterval = time.Second

// The follow pipeline's batch sizes: the first load shows the newest
// 200 lines, each 1 s round fetches at most 500 more, and the buffer
// keeps the newest 1000 so a live tail never grows without bound.
const (
	logsTailCount   = 200
	logsFollowCount = 500
	logsBufferCap   = 1000
)

// logTimeLayout is the per-line timestamp format of the log view.
const logTimeLayout = "Jan 02 15:04:05"

// logsTickMsg re-arms one round of the follow cadence.
type logsTickMsg struct{}

// entriesMsg carries one journal round: the entries a Tail or After
// returned, the new head cursor, or the error that killed the round.
type entriesMsg struct {
	entries []core.JournalEntry
	cursor  string
	err     error
}

// logs is the journal tail screen: the live log view collected through
// the JournalReader port with follow/pause scrolling and a
// client-side filter over unit and message.
type logs struct {
	reader core.JournalReader
	theme  theme.Theme

	entries []core.JournalEntry // consumed buffer, oldest first
	view    []core.JournalEntry // filtered slice backing the viewport
	vp      viewport.Model

	filter    textinput.Model
	filtering bool

	// follow keeps the newest line in sight; scrolling up pauses it
	// and f resumes (ARCHITECTURE.md §3's "follow" is the module's
	// whole point, so the state is explicit, not inferred from the
	// scroll offset).
	follow bool

	cursor string // newest consumed journal cursor, "" until one lands

	loaded     bool
	collectErr error
}

// NewLogs builds the logs screen tailing the journal through reader
// and rendering with th. All() passes the live collectors.System;
// tests inject fakes (ARCHITECTURE.md §2: the UI consumes ports).
func NewLogs(reader core.JournalReader, th theme.Theme) Screen {
	return newLogs(reader, th)
}

// newLogs is the concrete constructor; tests use it for white-box
// state assertions.
func newLogs(reader core.JournalReader, th theme.Theme) *logs {
	filter := textinput.New()
	filter.Prompt = "/"
	filter.CharLimit = 64
	filter.Width = 30

	// The viewport is this screen's §9 rationale in one component:
	// a growing, scrollable log window is what viewports are for.
	vp := viewport.New(80, 10)
	vp.KeyMap = viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
		Down:         key.NewBinding(key.WithKeys("down")),
		Up:           key.NewBinding(key.WithKeys("up")),
	}

	return &logs{
		reader: reader,
		theme:  th,
		vp:     vp,
		filter: filter,
		// A log tail starts at the bottom and stays there until the
		// user scrolls away.
		follow: true,
	}
}

// Init starts the follow pipeline: the first entry into the screen
// tails the journal head, and re-entry continues strictly after the
// stored cursor — the router keeps screen instances alive across tab
// switches, and a fresh tail would re-append the same newest window
// the buffer already holds. Both paths batch with the cadence tick.
func (s *logs) Init() tea.Cmd {
	collect := s.tail()
	if s.cursor != "" {
		collect = s.followUp()
	}
	return tea.Batch(collect, s.tick())
}

// tick schedules the next self-armed follow tick. The chain dies
// naturally while the screen is inactive — the router delivers
// logsTickMsg to the ACTIVE screen only, so nobody re-arms it — and
// Init restarts it on re-entry. That is the §5 cancellable-on-switch
// story: leaving stops the polling, re-entering resumes live.
func (s *logs) tick() tea.Cmd {
	return tea.Tick(logsInterval, func(time.Time) tea.Msg {
		return logsTickMsg{}
	})
}

// tail fetches the newest journal window; the round rides back as an
// entriesMsg.
func (s *logs) tail() tea.Cmd {
	return func() tea.Msg {
		entries, cursor, err := s.reader.Tail("", logsTailCount)
		return entriesMsg{entries: entries, cursor: cursor, err: err}
	}
}

// followUp fetches whatever was published since the last consumed
// cursor. The cursor is captured when the command is created, not
// when it runs, so an asynchronous round can never race a newer
// cursor into the wrong position.
func (s *logs) followUp() tea.Cmd {
	unit, cursor, limit := "", s.cursor, logsFollowCount
	return func() tea.Msg {
		entries, cur, err := s.reader.After(unit, cursor, limit)
		return entriesMsg{entries: entries, cursor: cur, err: err}
	}
}

// Update stores journal rounds and re-arms the follow cadence. The
// shell's global RefreshMsg (the 2 s cadence of the dashboard and
// processes screens) is deliberately ignored: Logs follows on its own
// 1 s cadence (ARCHITECTURE.md §3).
func (s *logs) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case logsTickMsg:
		return s, tea.Batch(s.followUp(), s.tick())
	case entriesMsg:
		if msg.err != nil {
			s.collectErr = msg.err
			if errors.Is(msg.err, core.ErrStaleCursor) {
				// The port's re-Tail signal: drop the position so the
				// next round tails the head instead of wedging on a
				// cursor the journal rotated away.
				s.cursor = ""
			}
			return s, nil
		}
		s.collectErr = nil
		s.loaded = true
		s.entries = append(s.entries, msg.entries...)
		if excess := len(s.entries) - logsBufferCap; excess > 0 {
			// A live tail keeps the newest window: trim the oldest.
			s.entries = s.entries[excess:]
		}
		s.cursor = msg.cursor
		s.refill()
	}
	return s, nil
}

// UpdateKey applies the screen's keymap, which shadows the global one
// per key (ARCHITECTURE.md §6). Filter mode is exclusive: while open
// it consumes every key.
func (s *logs) UpdateKey(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
	if s.filtering {
		switch msg.String() {
		case "esc", "enter":
			s.filtering = false
			s.filter.Blur()
			return s, nil, true
		}
		var cmd tea.Cmd
		s.filter, cmd = s.filter.Update(msg)
		s.refill()
		return s, cmd, true
	}

	switch msg.String() {
	case "/":
		s.filtering = true
		s.filter.Focus()
		return s, nil, true
	case "f":
		s.follow = !s.follow
		return s, nil, true
	default:
		// Only the navigation keys the viewport actually consumes are
		// claimed; every other key falls through to the global keymap
		// (q quits, ? helps, tab cycles — ARCHITECTURE.md §6 shadowing
		// is per-key, not wholesale).
		km := s.vp.KeyMap
		if !key.Matches(msg, km.PageDown, km.PageUp, km.HalfPageUp, km.HalfPageDown, km.Down, km.Up) {
			return s, nil, false
		}
		var cmd tea.Cmd
		s.vp, cmd = s.vp.Update(msg)
		// Scrolling toward older lines pauses following: the newest
		// line is no longer what the user is reading.
		if key.Matches(msg, km.PageUp, km.HalfPageUp, km.Up) {
			s.follow = false
		}
		return s, cmd, true
	}
}

// UpdateMouse handles nothing in v1: §6's parity rule wants keyboard
// equivalents for mouse actions, not the reverse — the scroll and
// follow keys cover everything a wheel would do. Wheel and click
// handling lands with a later milestone's mouse story.
func (s *logs) UpdateMouse(msg tea.MouseMsg) (Screen, tea.Cmd, bool) {
	return s, nil, false
}

// refill re-derives the viewport's content from the buffer and the
// filter.
func (s *logs) refill() {
	s.view = s.filterEntries()
	s.vp.SetContent(strings.Join(s.logLines(), "\n"))
}

// filterEntries narrows the buffer to entries whose unit or message
// contains the filter query, case-insensitive substring. The journal
// stays unfiltered server-side ("" unit): the client-side filter is
// this slice's job.
func (s *logs) filterEntries() []core.JournalEntry {
	query := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	if query == "" {
		return s.entries
	}
	out := make([]core.JournalEntry, 0, len(s.entries))
	for _, e := range s.entries {
		if !strings.Contains(strings.ToLower(e.Unit), query) &&
			!strings.Contains(strings.ToLower(e.Message), query) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// logLines renders the view as `Jan 02 15:04:05 · unit · message`
// lines, colored by syslog priority: err and worse (0–3) danger,
// warning (4) secondary, everything else plain.
func (s *logs) logLines() []string {
	danger := lipgloss.NewStyle().Foreground(s.theme.Danger)
	secondary := lipgloss.NewStyle().Foreground(s.theme.Secondary)
	out := make([]string, len(s.view))
	for i, e := range s.view {
		line := fmt.Sprintf("%s · %s · %s", e.Time.Format(logTimeLayout), e.Unit, e.Message)
		switch {
		case e.Priority <= 3:
			out[i] = danger.Render(line)
		case e.Priority == 4:
			out[i] = secondary.Render(line)
		default:
			out[i] = line
		}
	}
	return out
}

// Title implements Screen.
func (s *logs) Title() string { return "Logs" }

// Hints implements Screen: the logs keymap as the help overlay shows
// it below the global section.
func (s *logs) Hints() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow")),
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "scroll")),
		key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/pgdn", "page")),
	}
}

// View renders the log viewport with a filter line and a status line.
func (s *logs) View(width, height int) string {
	body := height - 1 // status line
	if s.filtering {
		body--
	}
	if width > 0 {
		s.vp.Width = width
	}
	if body > 0 {
		s.vp.Height = body
	}
	// Following keeps the newest line in sight on every render; a
	// paused viewport keeps its offset — SetContent preserves YOffset
	// when content grows, so pauses survive new arrivals.
	if s.follow {
		s.vp.GotoBottom()
	}

	muted := lipgloss.NewStyle().Foreground(s.theme.Muted)
	danger := lipgloss.NewStyle().Foreground(s.theme.Danger)

	lines := []string{s.vp.View()}
	if s.filtering {
		lines = append(lines, s.filter.View())
	}
	lines = append(lines, s.statusLine(muted, danger))

	// The viewport renders its own rows; the status line is plain, so
	// the composed height matches body+2 by construction.
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// statusLine composes the one-line footer: collection errors first,
// else the line summary with the follow state, the active filter and
// — for a genuinely empty buffer — journald's unprivileged visibility
// explained (a filter that matches nothing is the user's own doing
// and gets no hint; core.JournalEntry documents the access model).
func (s *logs) statusLine(muted, danger lipgloss.Style) string {
	if s.collectErr != nil {
		return danger.Render(fmt.Sprintf("n/a (%v)", s.collectErr))
	}
	if !s.loaded {
		return danger.Render("collecting...")
	}
	followState := "paused"
	if s.follow {
		followState = "following"
	}
	parts := []string{fmt.Sprintf("%d lines · %s", len(s.view), followState)}
	if q := strings.TrimSpace(s.filter.Value()); q != "" {
		parts = append(parts, fmt.Sprintf("filter %q", q))
	}
	if len(s.entries) == 0 {
		parts = append(parts, "no entries — system-wide journal needs systemd-journal group membership")
	}
	return muted.Render(strings.Join(parts, " · "))
}
