package screens

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/format"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// defaultContainersInterval is the Containers refresh cadence: 10 s,
// fast enough that a container started or stopped outside the TUI shows
// up while the operator is still looking at the screen, but not the 2 s
// of the live process table — a daemon listing is a socket round trip,
// not a procfs read. It is the built-in default the config
// `intervals.containers` override replaces and the value
// DefaultCadences ships.
const defaultContainersInterval = 10 * time.Second

// containersTickMsg re-arms one round of the screen's own cadence.
type containersTickMsg struct{}

// containersMsg carries one collection round: the listing — including
// the runtime label that produced it — plus its own error (§5: a failed
// round degrades this screen, never the program).
type containersMsg struct {
	list core.ContainerList
	err  error
}

// containerSortKey names the column the containers table is ordered by.
type containerSortKey string

const (
	// containerSortName is the default: alphabetical by container name,
	// the stable identity an operator recognizes fastest.
	containerSortName   containerSortKey = "name"
	containerSortState  containerSortKey = "state"
	containerSortImage  containerSortKey = "image"
	containerSortUptime containerSortKey = "uptime"
)

// containers is the Containers screen: the local Docker-compatible
// daemon's containers as name, state, image and uptime. The screen is
// read-only by design (ARCHITECTURE.md §7): it observes the daemon and
// never starts, stops or removes a container, so there is no modal and
// no action surface. Uptime is display-derived — the age of a running
// container since Created — never a stored or daemon-supplied string,
// which keeps the row honest across daemons that phrase Status
// differently (core.Container documents the same trade).
type containers struct {
	lister core.ContainerLister
	theme  theme.Theme

	// interval is the screen's own refresh cadence; lastGood is the
	// instant of its last successful round, the keeps-last-good
	// bookkeeping of §5: a failed round keeps the last listing
	// standing and the status line ages it.
	interval time.Duration
	lastGood time.Time

	list core.ContainerList // last good round, runtime included
	view []core.Container   // filtered and sorted slice backing the table

	table     table.Model
	filter    textinput.Model
	filtering bool

	sortKey containerSortKey
	desc    bool

	loaded bool
	err    error // error of the last round; the last good listing stays (§5)

	lastWidth int

	// now is the screen's clock for the uptime column; tests pin it so
	// the rendered ages are deterministic.
	now func() time.Time
}

// NewContainers builds the Containers screen listing through lister and
// rendering with th. All() passes the live collectors.System — which
// implements core.ContainerLister over the local unix socket — and the
// resolved containers cadence; tests inject fakes (ARCHITECTURE.md §2:
// the UI consumes ports). A non-positive interval falls back to the
// built-in §3 default.
func NewContainers(lister core.ContainerLister, th theme.Theme, interval time.Duration) Screen {
	return newContainers(lister, th, interval)
}

// newContainers is the concrete constructor; tests use it for white-box
// state assertions.
func newContainers(lister core.ContainerLister, th theme.Theme, interval time.Duration) *containers {
	if interval <= 0 {
		interval = defaultContainersInterval
	}
	filter := textinput.New()
	filter.Prompt = "/"
	filter.CharLimit = 64
	filter.Width = 30

	tbl := table.New(
		table.WithColumns(containerColumnsFor(80)),
		table.WithFocused(true),
		table.WithKeyMap(navKeyMap()),
		table.WithStyles(table.Styles{
			Header: lipgloss.NewStyle().Bold(true).Foreground(th.Secondary),
			Selected: lipgloss.NewStyle().
				Foreground(th.Background).Background(th.Primary),
		}),
	)

	return &containers{
		lister: lister,
		theme:  th,
		table:  tbl,
		filter: filter,
		// Name ascending is the natural first sort of a container
		// listing: the row an operator looks for is the one they
		// named, not the one with the most CPU or age.
		sortKey:   containerSortName,
		desc:      false,
		lastWidth: -1,
		interval:  interval,
		now:       time.Now,
	}
}

// containerColumnsFor lays out the table for a given width; NAME
// absorbs whatever is left (46 cells are fixed: STATE 12, IMAGE 24,
// UPTIME 10) and is floored at 12. The floor can overflow a very
// narrow terminal, the same trade the storage and processes tables
// make; the shell truncates the rendered line at its edge.
func containerColumnsFor(width int) []table.Column {
	name := width - 46
	if name < 12 {
		name = 12
	}
	return []table.Column{
		{Title: "NAME", Width: name},
		{Title: "STATE", Width: 12},
		{Title: "IMAGE", Width: 24},
		{Title: "UPTIME", Width: 10},
	}
}

// Init collects the first round immediately and starts the screen's
// own cadence at the configured interval, the storage chain shape: the
// tick dies naturally while the screen is inactive — the router
// delivers containersTickMsg to the ACTIVE screen only, so nobody
// re-arms it — and Init restarts it on re-entry (the §5
// cancellable-on-switch story).
func (s *containers) Init() tea.Cmd {
	return tea.Batch(s.collect(), s.tick())
}

// tick schedules the next self-armed containers tick on the configured
// cadence.
func (s *containers) tick() tea.Cmd {
	return tea.Tick(s.interval, func(time.Time) tea.Msg {
		return containersTickMsg{}
	})
}

// Update stores collection rounds and re-arms the cadence. A failed
// round keeps the last good listing on screen and only records the
// error, so the next success replaces it (§5's keeps-last-good state).
// The shell's global RefreshMsg (the 2 s cadence of the dashboard and
// processes screens) is deliberately ignored: Containers refreshes on
// its own slower cadence (ARCHITECTURE.md §3).
func (s *containers) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case containersTickMsg:
		return s, tea.Batch(s.collect(), s.tick())
	case containersMsg:
		if msg.err != nil {
			s.err = msg.err
			return s, nil
		}
		s.list = msg.list
		s.loaded = true
		s.lastGood = s.now()
		s.err = nil
		s.reapply()
	}
	return s, nil
}

// collect gathers the container listing asynchronously; the round —
// listing and error together — rides back as one containersMsg.
func (s *containers) collect() tea.Cmd {
	return func() tea.Msg {
		list, err := s.lister.Containers()
		return containersMsg{list: list, err: err}
	}
}

// UpdateKey applies the screen's keymap, which shadows the global one
// per key (ARCHITECTURE.md §6: q quits, ? helps, tab cycles — those
// still fall through; sort and filter keys are claimed here). Filter
// mode is exclusive while open: every key goes to the textinput, and
// esc closes the input but leaves the query applied, exactly like the
// storage and processes filters. Once closed, a second esc clears the
// query, so the key only shadows the global back binding when there is
// something to clear.
func (s *containers) UpdateKey(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
	if s.filtering {
		if msg.String() == "esc" {
			s.filtering = false
			s.filter.Blur()
			return s, nil, true
		}
		var cmd tea.Cmd
		s.filter, cmd = s.filter.Update(msg)
		s.reapply()
		return s, cmd, true
	}

	switch msg.String() {
	case "/":
		s.filtering = true
		s.filter.Focus()
		return s, nil, true
	case "n":
		s.applySort(containerSortName)
	case "s":
		s.applySort(containerSortState)
	case "i":
		s.applySort(containerSortImage)
	case "u":
		s.applySort(containerSortUptime)
	case "esc":
		if strings.TrimSpace(s.filter.Value()) == "" {
			return s, nil, false
		}
		s.filter.SetValue("")
		s.reapply()
	default:
		return s.claimNav(msg)
	}
	return s, nil, true
}

// claimNav offers the key to the table's navigation bindings only;
// every other key falls through to the global keymap (the §6 per-key
// shadowing rule, not wholesale). Without this claim the table would
// render a cursor nobody could move: navKeyMap narrows the table's
// bindings to arrows/pgup/home, and the shell never forwards an
// unclaimed key to the screen again.
func (s *containers) claimNav(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
	km := s.table.KeyMap
	if !key.Matches(msg, km.LineUp, km.LineDown, km.PageUp, km.PageDown,
		km.HalfPageUp, km.HalfPageDown, km.GotoTop, km.GotoBottom) {
		return s, nil, false
	}
	var cmd tea.Cmd
	s.table, cmd = s.table.Update(msg)
	return s, cmd, true
}

// UpdateMouse handles nothing: Containers is a read-only module — no
// modal, no row actions — so every mouse event passes to the shell
// (§6's parity rule wants keyboard equivalents for mouse actions, and
// the keyboard side is complete).
func (s *containers) UpdateMouse(tea.MouseMsg) (Screen, tea.Cmd, bool) {
	return s, nil, false
}

// applySort switches the sort column, flipping the direction when the
// same key repeats. A new column starts ascending: the first press of
// a sort key answers "order by this, A to Z", not "reverse it".
func (s *containers) applySort(k containerSortKey) {
	if s.sortKey == k {
		s.desc = !s.desc
	} else {
		s.sortKey = k
		s.desc = false
	}
	s.reapply()
}

// reapply re-derives the displayed rows from the last good listing,
// the filter and the sort, keeping the cursor on the same container ID
// when the row survives. The filter spans name and image — the two
// fields an operator matches a workload by — as a case-insensitive
// substring; an empty query keeps every row.
func (s *containers) reapply() {
	selected := ""
	// An empty table parks bubbles' cursor at -1, so bounds-check
	// before reading the identity row.
	if cursor := s.table.Cursor(); cursor >= 0 && cursor < len(s.view) {
		selected = s.view[cursor].ID
	}

	query := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	out := make([]core.Container, 0, len(s.list.Containers))
	for _, c := range s.list.Containers {
		if query != "" &&
			!strings.Contains(strings.ToLower(c.Name), query) &&
			!strings.Contains(strings.ToLower(c.Image), query) {
			continue
		}
		out = append(out, c)
	}

	// Stable sort on purpose: equal rows keep the daemon's listing
	// order, and the descending direction compares the pair swapped
	// instead of reversing the ascending result, so ties keep that
	// same order in both directions.
	sort.SliceStable(out, func(i, j int) bool {
		if s.desc {
			return containerLess(out[j], out[i], s.sortKey)
		}
		return containerLess(out[i], out[j], s.sortKey)
	})

	s.view = out
	s.table.SetRows(containerRowsFor(out, s.lastWidth, s.now()))
	if idx := containerIndexOf(out, selected); idx >= 0 {
		s.table.SetCursor(idx)
	} else {
		s.table.SetCursor(0)
	}
}

// containerLess orders two containers on the active key. Name, image
// and state compare case-insensitively; uptime orders by Created, so
// the oldest container is "smallest" and an ascending sort reads
// oldest first, with the repeated key flipping to youngest first.
func containerLess(a, b core.Container, k containerSortKey) bool {
	switch k {
	case containerSortImage:
		return strings.ToLower(a.Image) < strings.ToLower(b.Image)
	case containerSortState:
		return strings.ToLower(a.State) < strings.ToLower(b.State)
	case containerSortUptime:
		return a.Created.Before(b.Created)
	default: // containerSortName
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	}
}

func containerIndexOf(list []core.Container, id string) int {
	for i, c := range list {
		if c.ID == id {
			return i
		}
	}
	return -1
}

// containerUptime renders the display-derived age of a container: the
// time since Created for a running row, and an em dash for every other
// state (and for a daemon that reported no creation instant). Exited
// containers have an age too, but "uptime" would be a lie, so the cell
// renders a dash rather than a duration nobody asked for.
func containerUptime(c core.Container, now time.Time) string {
	if c.State != "running" || c.Created.IsZero() {
		return "—"
	}
	return format.Duration(now.Sub(c.Created))
}

// containerRowsFor renders the displayed containers as table rows,
// clipped to their column widths so alignment never breaks on long
// names or image references.
func containerRowsFor(list []core.Container, width int, now time.Time) []table.Row {
	cols := containerColumnsFor(width)
	rows := make([]table.Row, 0, len(list))
	for _, c := range list {
		rows = append(rows, table.Row{
			clip(c.Name, cols[0].Width),
			clip(c.State, cols[1].Width),
			clip(c.Image, cols[2].Width),
			clip(containerUptime(c, now), cols[3].Width),
		})
	}
	return rows
}

// Title implements Screen.
func (s *containers) Title() string { return "Containers" }

// Hints implements Screen: the containers keymap as the help overlay
// shows it below the global section.
func (s *containers) Hints() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "sort by name")),
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort by state")),
		key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "sort by image")),
		key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "sort by uptime")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
	}
}

// View renders the table with a filter line and a status line.
func (s *containers) View(width, height int) string {
	if width != s.lastWidth {
		s.lastWidth = width
		s.table.SetColumns(containerColumnsFor(width))
		s.table.SetWidth(width)
		// Column widths changed: the rows must be reclipped.
		s.table.SetRows(containerRowsFor(s.view, width, s.now()))
	}

	body := height - 1 // status line
	if s.filtering {
		body--
	}
	if body > 0 {
		s.table.SetHeight(body)
	}

	muted := lipgloss.NewStyle().Foreground(s.theme.Muted)
	danger := lipgloss.NewStyle().Foreground(s.theme.Danger)

	lines := []string{s.table.View()}
	if s.filtering {
		lines = append(lines, s.filter.View())
	}
	lines = append(lines, s.statusLine(muted, danger))

	// The table renders its own rows; the status line is plain, so the
	// composed height matches body+2 by construction.
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// statusLine composes the one-line footer: the last round's error
// first — with the muted age of the listing still on screen while it
// stays (§5's keeps-last-good state) — the muted collecting state
// until the first round lands, else the listing summary: the total and
// running counts of the FILTERED view, the runtime label when the
// collector named one, the active sort and the active filter.
func (s *containers) statusLine(muted, danger lipgloss.Style) string {
	if s.err != nil {
		return errStatus(s.err, s.lastGood, muted, danger)
	}
	if !s.loaded {
		return loadingStatus(muted)
	}

	running := 0
	for _, c := range s.view {
		if c.State == "running" {
			running++
		}
	}
	summary := fmt.Sprintf("%d containers · %d running", len(s.view), running)
	if s.list.Runtime != "" {
		summary = fmt.Sprintf("%s · %s", summary, s.list.Runtime)
	}

	parts := []string{
		muted.Render(summary),
		muted.Render(fmt.Sprintf("sort %s %s", s.sortKey, sortArrow(s.desc))),
	}
	if q := strings.TrimSpace(s.filter.Value()); q != "" {
		parts = append(parts, muted.Render(fmt.Sprintf("filter %q", q)))
	}
	return strings.Join(parts, " · ")
}
