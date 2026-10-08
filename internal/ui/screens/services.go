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

	"github.com/felipecastillo-b/serverctl/internal/actions"
	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/confirm"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// servicesInterval is the Services refresh cadence of ARCHITECTURE.md
// §3: 5 s, deliberately slower than the shell-wide 2 s RefreshMsg that
// drives the dashboard and processes screens — unit churn is rare and
// a D-Bus round is not free.
const servicesInterval = 5 * time.Second

// servicesTickMsg re-arms one round of the screen's own cadence.
type servicesTickMsg struct{}

// unitsDataMsg carries one collection round: the loaded unit listing or
// the error that killed it.
type unitsDataMsg struct {
	services []core.Service
	err      error
}

// unitResultMsg reports the outcome of one lifecycle verb.
type unitResultMsg struct {
	verb unitVerb
	name string
	err  error
}

// unitVerb is one of the three whitelisted lifecycle verbs. The manager
// port has no generic method passthrough and neither does this screen.
type unitVerb string

const (
	unitStart   unitVerb = "start"
	unitStop    unitVerb = "stop"
	unitRestart unitVerb = "restart"
)

// action is the audit name of the verb — the same "service.*" namespace
// m4a's Actor writes its executed/failed entries under (§7).
func (v unitVerb) action() string { return "service." + string(v) }

// past is the completed-tense word for the status line.
func (v unitVerb) past() string {
	switch v {
	case unitStop:
		return "stopped"
	case unitRestart:
		return "restarted"
	default:
		return "started"
	}
}

// question is the §7 confirmation prompt for the verb. Only stop and
// restart are gated — starting a unit is not destructive — but the
// mapping keeps the full verb set honest.
func (v unitVerb) question(name string) string {
	switch v {
	case unitStop:
		return fmt.Sprintf("Stop unit %s?", name)
	case unitRestart:
		return fmt.Sprintf("Restart unit %s?", name)
	default:
		return fmt.Sprintf("Start unit %s?", name)
	}
}

// unitSortKey names the column the units table is ordered by.
type unitSortKey int

const (
	// unitSortActive is the default: attentionRank's ladder — failures
	// first, then mid-transition units, then the healthy ones.
	unitSortActive unitSortKey = iota
	unitSortName
)

func (k unitSortKey) String() string {
	switch k {
	case unitSortName:
		return "name"
	default:
		return "active"
	}
}

// services is the systemd units screen: the .service listing collected
// by m4a's collector with sort, filter and §7-gated lifecycle verbs.
type services struct {
	lister  core.UnitLister
	manager core.UnitManager
	theme   theme.Theme

	units []core.Service // last collected listing
	view  []core.Service // sorted and filtered slice backing the table
	table table.Model

	modal     confirm.Model
	modalOpen bool
	modalVerb unitVerb // verb pending on the modal, with its target unit
	modalUnit string

	filter    textinput.Model
	filtering bool

	sortKey unitSortKey
	desc    bool

	status     string
	collectErr error
	loaded     bool

	lastWidth int
}

// NewServices builds the services screen listing units through lister,
// rendering with th and running lifecycle verbs through manager. All()
// passes the live collectors.System and actions.Actor{}; tests inject
// fakes (ARCHITECTURE.md §4: actions is the only mutating layer).
func NewServices(lister core.UnitLister, th theme.Theme, manager core.UnitManager) Screen {
	return newServices(lister, th, manager)
}

// newServices is the concrete constructor; tests use it for white-box
// state assertions.
func newServices(lister core.UnitLister, th theme.Theme, manager core.UnitManager) *services {
	filter := textinput.New()
	filter.Prompt = "/"
	filter.CharLimit = 64
	filter.Width = 30

	tbl := table.New(
		table.WithColumns(unitColumnsFor(80)),
		table.WithFocused(true),
		table.WithKeyMap(navKeyMap()),
		table.WithStyles(table.Styles{
			Header: lipgloss.NewStyle().Bold(true).Foreground(th.Secondary),
			Selected: lipgloss.NewStyle().
				Foreground(th.Background).Background(th.Primary),
		}),
	)

	return &services{
		lister:  lister,
		manager: manager,
		theme:   th,
		table:   tbl,
		filter:  filter,
		// The attention ladder is the natural first sort of a unit
		// monitor: failures first (ARCHITECTURE.md §3).
		sortKey:   unitSortActive,
		lastWidth: -1,
	}
}

// unitColumnsFor lays out the units table for a given width; the
// DESCRIPTION column absorbs whatever is left.
func unitColumnsFor(width int) []table.Column {
	// 28 unit + 10 active + 10 sub are fixed.
	desc := width - 48
	if desc < 8 {
		desc = 8
	}
	return []table.Column{
		{Title: "UNIT", Width: 28},
		{Title: "ACTIVE", Width: 10},
		{Title: "SUB", Width: 10},
		{Title: "DESCRIPTION", Width: desc},
	}
}

// Init collects the first round immediately and starts the screen's
// own 5 s cadence.
func (s *services) Init() tea.Cmd {
	return tea.Batch(s.collect(), s.tick())
}

// tick schedules the next self-armed services tick. The chain dies
// naturally while the screen is inactive — the router delivers
// servicesTickMsg to the ACTIVE screen only, so nobody re-arms it —
// and Init restarts it on re-entry. That is the §5
// cancellable-on-switch story for now: leaving and re-entering the
// screen collects immediately, and the next pending tick is the manual
// refresh.
func (s *services) tick() tea.Cmd {
	return tea.Tick(servicesInterval, func(time.Time) tea.Msg {
		return servicesTickMsg{}
	})
}

// Update stores collection rounds, re-arms the cadence and reports
// lifecycle outcomes. The shell's global RefreshMsg (the 2 s cadence of
// the dashboard and processes screens) is deliberately ignored: Services
// refreshes on its own 5 s cadence (ARCHITECTURE.md §3).
func (s *services) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case servicesTickMsg:
		return s, tea.Batch(s.collect(), s.tick())
	case unitsDataMsg:
		if msg.err != nil {
			s.collectErr = msg.err
		} else {
			s.collectErr = nil
			s.units = msg.services
			s.loaded = true
		}
		s.reapply()
	case unitResultMsg:
		if msg.err != nil {
			s.status = fmt.Sprintf("%s unit %s failed: %v", msg.verb, msg.name, msg.err)
		} else {
			s.status = fmt.Sprintf("%s %s", msg.verb.past(), msg.name)
		}
	}
	return s, nil
}

// collect gathers the unit listing asynchronously; the round rides back
// as a unitsDataMsg.
func (s *services) collect() tea.Cmd {
	return func() tea.Msg {
		msg := unitsDataMsg{}
		msg.services, msg.err = s.lister.Units()
		return msg
	}
}

// UpdateKey applies the screen's keymap, which shadows the global one
// (ARCHITECTURE.md §6). Modal and filter modes are exclusive: while open
// they consume every key.
func (s *services) UpdateKey(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
	if s.modalOpen {
		s.modal.UpdateKey(msg)
		return s, s.resolveModal(), true
	}
	if s.filtering {
		switch msg.String() {
		case "esc", "enter":
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
	case "s":
		return s, s.startUnit(), true
	case "x":
		return s, s.requestLifecycle(unitStop), true
	case "r":
		return s, s.requestLifecycle(unitRestart), true
	case "n":
		s.applySort(unitSortName)
	case "a":
		s.applySort(unitSortActive)
	default:
		// Only the navigation keys the table actually consumes are
		// claimed; every other key falls through to the global keymap
		// (q quits, ? helps, tab cycles — ARCHITECTURE.md §6 shadowing
		// is per-key, not wholesale).
		km := s.table.KeyMap
		if !key.Matches(msg, km.LineUp, km.LineDown, km.PageUp, km.PageDown,
			km.HalfPageUp, km.HalfPageDown, km.GotoTop, km.GotoBottom) {
			return s, nil, false
		}
		var cmd tea.Cmd
		s.table, cmd = s.table.Update(msg)
		return s, cmd, true
	}
	return s, nil, true
}

// UpdateMouse routes presses to the open confirmation modal only. Table
// row clicks need viewport offset access that bubbles/table v1.0.0 does
// not expose; they land with a later milestone.
func (s *services) UpdateMouse(msg tea.MouseMsg) (Screen, tea.Cmd, bool) {
	if !s.modalOpen {
		return s, nil, false
	}
	s.modal.UpdateMouse(msg)
	return s, s.resolveModal(), true
}

// startUnit starts the cursor's unit directly: starting a service is
// not destructive, so §7's confirmation gate does not apply to it. The
// manager call runs as a tea.Cmd; m4a's Actor audits the executed or
// failed attempt itself.
func (s *services) startUnit() tea.Cmd {
	if len(s.view) == 0 {
		s.status = "no unit selected"
		return nil
	}
	return s.runUnit(unitStart, s.view[s.table.Cursor()].Name)
}

// requestLifecycle opens the §7 confirmation modal for verb on the
// selected unit: stop and restart can take a machine down, so they only
// run behind an explicit answer.
func (s *services) requestLifecycle(verb unitVerb) tea.Cmd {
	if len(s.view) == 0 {
		s.status = "no unit selected"
		return nil
	}
	name := s.view[s.table.Cursor()].Name
	s.modal.Reset(verb.question(name))
	s.modalOpen = true
	s.modalVerb = verb
	s.modalUnit = name
	s.status = ""
	return nil
}

// resolveModal closes the modal once decided. §7 wants every attempt
// traceable, split by who can see it: the screen records the modal
// states the backend cannot (denied and confirmed), while m4a's Actor
// audits the executed/failed outcome of the dispatched call itself. A
// failed "confirmed" record is left to that Actor path — a broken sink
// resurfaces folded into the dispatched call's result error — but a
// denial dispatches nothing, so its record failure must surface here
// instead of vanishing.
func (s *services) resolveModal() tea.Cmd {
	switch s.modal.Decision() {
	case confirm.Confirmed:
		verb, name := s.modalVerb, s.modalUnit
		s.modalOpen = false
		_ = actions.Record(verb.action(), name, "confirmed", "")
		return s.runUnit(verb, name)
	case confirm.Denied:
		verb, name := s.modalVerb, s.modalUnit
		s.modalOpen = false
		s.status = "canceled"
		if err := actions.Record(verb.action(), name, "denied", ""); err != nil {
			s.status = fmt.Sprintf("canceled, audit log write failed: %v", err)
		}
	}
	return nil
}

// runUnit dispatches one lifecycle verb through the manager port; the
// outcome rides back as a unitResultMsg for the status line.
func (s *services) runUnit(verb unitVerb, name string) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch verb {
		case unitStop:
			err = s.manager.Stop(name)
		case unitRestart:
			err = s.manager.Restart(name)
		default:
			err = s.manager.Start(name)
		}
		return unitResultMsg{verb: verb, name: name, err: err}
	}
}

// applySort switches the sort column, flipping the direction when the
// same key repeats.
func (s *services) applySort(k unitSortKey) {
	if s.sortKey == k {
		s.desc = !s.desc
	} else {
		s.sortKey = k
		s.desc = false // attention and alphabetical both read best ascending
	}
	s.reapply()
}

// reapply re-derives the displayed rows from the current listing,
// filter and sort, keeping the cursor on the same unit when possible.
func (s *services) reapply() {
	name := ""
	if len(s.view) > 0 {
		name = s.view[s.table.Cursor()].Name
	}

	s.view = s.filterAndSort()
	s.table.SetRows(unitRowsFor(s.view, s.lastWidth))
	if idx := indexOfUnit(s.view, name); idx >= 0 {
		s.table.SetCursor(idx)
	} else {
		s.table.SetCursor(0)
	}
}

// filterAndSort narrows the collected listing to .service units whose
// name or description matches the filter query, then sorts the
// survivors by the active column.
func (s *services) filterAndSort() []core.Service {
	query := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	out := make([]core.Service, 0, len(s.units))
	for _, unit := range s.units {
		if !strings.HasSuffix(unit.Name, ".service") {
			continue
		}
		if query != "" &&
			!strings.Contains(strings.ToLower(unit.Name), query) &&
			!strings.Contains(strings.ToLower(unit.Description), query) {
			continue
		}
		out = append(out, unit)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		less := func() bool {
			switch s.sortKey {
			case unitSortName:
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			default: // unitSortActive: the attention ladder first,
				// unit names as the alphabetical tie-break per rung.
				if ra, rb := attentionRank(a.Active), attentionRank(b.Active); ra != rb {
					return ra < rb
				}
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
		}()
		if s.desc {
			return !less && !equalUnitRank(a, b, s.sortKey)
		}
		return less
	})
	return out
}

// attentionRank maps a systemd active state to its rank under the
// active-state sort. The comparator is an attention bias: failed units
// come first — they are the ones needing eyes — then units still
// activating (a change in flight), then the healthy active ones, and
// finally every other state (inactive, deactivating, reloading, ...)
// in plain alphabetical order. Ascending puts failures at the top of
// the table; the desc flip inverts the whole ladder, names included.
func attentionRank(active string) int {
	switch strings.ToLower(active) {
	case "failed":
		return 0
	case "activating":
		return 1
	case "active":
		return 2
	default:
		return 3
	}
}

// equalUnitRank reports whether two units tie on the active sort column;
// stable order keeps the listing order for ties.
func equalUnitRank(a, b core.Service, k unitSortKey) bool {
	switch k {
	case unitSortActive:
		return attentionRank(a.Active) == attentionRank(b.Active) &&
			strings.EqualFold(a.Name, b.Name)
	default:
		return strings.EqualFold(a.Name, b.Name)
	}
}

func indexOfUnit(units []core.Service, name string) int {
	for i, unit := range units {
		if unit.Name == name {
			return i
		}
	}
	return -1
}

// unitRowsFor renders the displayed units as table rows clipped to
// their column widths so alignment never breaks on long descriptions.
func unitRowsFor(units []core.Service, width int) []table.Row {
	cols := unitColumnsFor(width)
	rows := make([]table.Row, 0, len(units))
	for _, unit := range units {
		rows = append(rows, table.Row{
			clip(unit.Name, cols[0].Width),
			clip(unit.Active, cols[1].Width),
			clip(unit.Sub, cols[2].Width),
			clip(unit.Description, cols[3].Width),
		})
	}
	return rows
}

// failedCount counts the failed units of the displayed view so the
// status line can call them out.
func failedCount(units []core.Service) int {
	n := 0
	for _, unit := range units {
		if strings.EqualFold(unit.Active, "failed") {
			n++
		}
	}
	return n
}

// Title implements Screen.
func (s *services) Title() string { return "Services" }

// Hints implements Screen: the services keymap as the help overlay
// shows it below the global section.
func (s *services) Hints() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "start")),
		key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "stop")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "restart")),
		key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "sort by name")),
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "sort by state")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	}
}

// View renders the table with a filter line and a status line, or the
// confirmation modal when one is open.
func (s *services) View(width, height int) string {
	if s.modalOpen {
		return s.modal.View(width, height)
	}

	if width != s.lastWidth {
		s.lastWidth = width
		s.table.SetColumns(unitColumnsFor(width))
		s.table.SetWidth(width)
		// Column widths changed: the rows must be reclipped.
		s.table.SetRows(unitRowsFor(s.view, width))
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

// statusLine composes the one-line footer: collection errors first,
// else the listing summary — with the failed units called out — plus
// the last action outcome.
func (s *services) statusLine(muted, danger lipgloss.Style) string {
	if s.collectErr != nil {
		return danger.Render(fmt.Sprintf("n/a (%v)", s.collectErr))
	}
	if !s.loaded {
		return danger.Render("collecting...")
	}
	parts := []string{
		fmt.Sprintf("%d services · sort %s %s", len(s.view), s.sortKey, sortArrow(s.desc)),
	}
	if failed := failedCount(s.view); failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if q := strings.TrimSpace(s.filter.Value()); q != "" {
		parts = append(parts, fmt.Sprintf("filter %q", q))
	}
	if s.status != "" {
		parts = append(parts, s.status)
	}
	return muted.Render(strings.Join(parts, " · "))
}
