package screens

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/actions"
	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/confirm"
	"github.com/felipecastillo-b/serverctl/internal/ui/format"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// processesDataMsg carries one collection round: the full process listing
// with CPU percentages already computed, or the error that killed it.
type processesDataMsg struct {
	procs []core.Process
	err   error
}

// signalResultMsg reports the outcome of one confirmed signal action.
type signalResultMsg struct {
	pid int
	sig actions.ProcessSignal
	err error
}

// sortKey names the column the table is ordered by.
type sortKey int

const (
	sortCPU sortKey = iota
	sortMem
	sortPID
	sortName
)

func (s sortKey) String() string {
	switch s {
	case sortMem:
		return "mem"
	case sortPID:
		return "pid"
	case sortName:
		return "name"
	default:
		return "cpu"
	}
}

// processes is the live process table screen: sort, filter and confirmation
// gated signals over the /proc listing collected by m3a.
type processes struct {
	sys     collectors.System
	theme   theme.Theme
	sender  SignalSender
	tracker collectors.ProcessTracker

	procs []core.Process // last collected listing, CpuPct filled
	view  []core.Process // sorted and filtered slice backing the table
	table table.Model

	modal     confirm.Model
	modalOpen bool
	modalPID  int
	modalSig  actions.ProcessSignal

	filter    textinput.Model
	filtering bool

	sortKey sortKey
	desc    bool

	status     string
	collectErr error
	loaded     bool

	lastWidth int
}

// NewProcesses builds the processes screen collecting from sys, rendering
// with th and delivering signals through sender.
func NewProcesses(sys collectors.System, th theme.Theme, sender SignalSender) Screen {
	return newProcesses(sys, th, sender)
}

// newProcesses is the concrete constructor; tests use it for white-box
// state assertions.
func newProcesses(sys collectors.System, th theme.Theme, sender SignalSender) *processes {
	filter := textinput.New()
	filter.Prompt = "/"
	filter.CharLimit = 64
	filter.Width = 30

	tbl := table.New(
		table.WithColumns(columnsFor(80)),
		table.WithFocused(true),
		table.WithKeyMap(navKeyMap()),
		table.WithStyles(table.Styles{
			Header: lipgloss.NewStyle().Bold(true).Foreground(th.Secondary),
			Selected: lipgloss.NewStyle().
				Foreground(th.Background).Background(th.Primary),
		}),
	)

	return &processes{
		sys:    sys,
		theme:  th,
		sender: sender,
		table:  tbl,
		filter: filter,
		// CPU is the natural first sort of a process monitor, best first.
		sortKey:   sortCPU,
		desc:      true,
		lastWidth: -1,
	}
}

// navKeyMap narrows the table to pure navigation keys: the screen claims
// every other binding (t/k signals, c/m/p/n sorts, / filter) before the
// table sees a key, and the table's upstream defaults would shadow the
// global k/j sidebar keys behind navigation nobody asked for here.
func navKeyMap() table.KeyMap {
	return table.KeyMap{
		LineUp:       key.NewBinding(key.WithKeys("up")),
		LineDown:     key.NewBinding(key.WithKeys("down")),
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdown", " ")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
		GotoTop:      key.NewBinding(key.WithKeys("home")),
		GotoBottom:   key.NewBinding(key.WithKeys("end")),
	}
}

// columnsFor lays out the table for a given width; the NAME column absorbs
// whatever is left.
func columnsFor(width int) []table.Column {
	name := width - 44
	if name < 8 {
		name = 8
	}
	return []table.Column{
		{Title: "CPU%", Width: 6},
		{Title: "MEM", Width: 8},
		{Title: "USER", Width: 10},
		{Title: "START", Width: 6},
		{Title: "PID", Width: 7},
		{Title: "NAME", Width: name},
	}
}

// Init collects the first round immediately.
func (p *processes) Init() tea.Cmd { return p.collect() }

// Update stores collection rounds and signal outcomes.
func (p *processes) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshMsg:
		return p, p.collect()
	case processesDataMsg:
		if msg.err != nil {
			p.collectErr = msg.err
		} else {
			p.collectErr = nil
			p.procs = msg.procs
			p.loaded = true
		}
		p.reapply()
	case signalResultMsg:
		if msg.err != nil {
			p.status = fmt.Sprintf("%s pid %d failed: %v", msg.sig, msg.pid, msg.err)
		} else {
			p.status = fmt.Sprintf("%s sent to pid %d", msg.sig, msg.pid)
		}
	}
	return p, nil
}

// collect gathers the listing and the machine's total jiffies
// asynchronously; the tracker turns both into live CPU percentages.
func (p *processes) collect() tea.Cmd {
	return func() tea.Msg {
		var msg processesDataMsg
		procs, err := p.sys.Processes()
		if err != nil {
			msg.err = err
			return msg
		}
		agg, _, err := p.sys.CPUStats()
		if err != nil {
			msg.err = err
			return msg
		}
		msg.procs = p.tracker.Refresh(procs, agg.Total())
		return msg
	}
}

// UpdateKey applies the screen's keymap, which shadows the global one
// (ARCHITECTURE.md §6). Modal and filter modes are exclusive: while open
// they consume every key.
func (p *processes) UpdateKey(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
	if p.modalOpen {
		p.modal.UpdateKey(msg)
		return p, p.resolveModal(), true
	}
	if p.filtering {
		switch msg.String() {
		case "esc", "enter":
			p.filtering = false
			p.filter.Blur()
			return p, nil, true
		}
		var cmd tea.Cmd
		p.filter, cmd = p.filter.Update(msg)
		p.reapply()
		return p, cmd, true
	}

	switch msg.String() {
	case "/":
		p.filtering = true
		p.filter.Focus()
		return p, nil, true
	case "t":
		return p, p.requestSignal(actions.SignalTerm), true
	case "k":
		return p, p.requestSignal(actions.SignalKill), true
	case "c":
		p.applySort(sortCPU)
	case "m":
		p.applySort(sortMem)
	case "p":
		p.applySort(sortPID)
	case "n":
		p.applySort(sortName)
	default:
		// Only the navigation keys the table actually consumes are
		// claimed; every other key falls through to the global keymap
		// (q quits, ? helps, tab cycles — ARCHITECTURE.md §6 shadowing
		// is per-key, not wholesale).
		km := p.table.KeyMap
		if !key.Matches(msg, km.LineUp, km.LineDown, km.PageUp, km.PageDown,
			km.HalfPageUp, km.HalfPageDown, km.GotoTop, km.GotoBottom) {
			return p, nil, false
		}
		var cmd tea.Cmd
		p.table, cmd = p.table.Update(msg)
		return p, cmd, true
	}
	return p, nil, true
}

// UpdateMouse routes presses to the open confirmation modal only. Table
// row clicks need viewport offset access that bubbles/table v1.0.0 does
// not expose; they land with a later milestone.
func (p *processes) UpdateMouse(msg tea.MouseMsg) (Screen, tea.Cmd, bool) {
	if !p.modalOpen {
		return p, nil, false
	}
	p.modal.UpdateMouse(msg)
	return p, p.resolveModal(), true
}

// requestSignal opens the §7 confirmation modal for sig on the selected
// process.
func (p *processes) requestSignal(sig actions.ProcessSignal) tea.Cmd {
	if len(p.view) == 0 {
		p.status = "no process selected"
		return nil
	}
	row := p.view[p.table.Cursor()]
	p.modal.Reset(fmt.Sprintf("Send %s to pid %d (%s)?", sig, row.PID, row.Name))
	p.modalOpen = true
	p.modalPID = row.PID
	p.modalSig = sig
	p.status = ""
	return nil
}

// resolveModal closes the modal once decided and dispatches the confirmed
// signal; a denial just records the cancellation.
func (p *processes) resolveModal() tea.Cmd {
	switch p.modal.Decision() {
	case confirm.Confirmed:
		pid, sig := p.modalPID, p.modalSig
		p.modalOpen = false
		return p.signal(pid, sig)
	case confirm.Denied:
		p.modalOpen = false
		p.status = "cancelled"
	}
	return nil
}

// signal delivers the whitelisted action asynchronously; the outcome rides
// back as a signalResultMsg for the status line.
func (p *processes) signal(pid int, sig actions.ProcessSignal) tea.Cmd {
	return func() tea.Msg {
		return signalResultMsg{pid: pid, sig: sig, err: p.sender.Signal(pid, sig)}
	}
}

// applySort switches the sort column, flipping the direction when the
// same key repeats.
func (p *processes) applySort(k sortKey) {
	if p.sortKey == k {
		p.desc = !p.desc
	} else {
		p.sortKey = k
		p.desc = k == sortCPU || k == sortMem // best first; pid/name ascend
	}
	p.reapply()
}

// reapply re-derives the displayed rows from the current listing, filter
// and sort, keeping the cursor on the same process when possible.
func (p *processes) reapply() {
	pid := 0
	if len(p.view) > 0 {
		pid = p.view[p.table.Cursor()].PID
	}

	p.view = p.filterAndSort()
	p.table.SetRows(rowsFor(p.view, p.lastWidth))
	if idx := indexOfPID(p.view, pid); idx >= 0 {
		p.table.SetCursor(idx)
	} else {
		p.table.SetCursor(0)
	}
}

// filterAndSort applies the filter query to the collected listing and
// sorts the survivors by the active column.
func (p *processes) filterAndSort() []core.Process {
	query := strings.ToLower(strings.TrimSpace(p.filter.Value()))
	out := make([]core.Process, 0, len(p.procs))
	for _, proc := range p.procs {
		if query != "" &&
			!strings.Contains(strings.ToLower(proc.Name), query) &&
			!strings.Contains(strings.ToLower(proc.User), query) &&
			!strings.Contains(strings.ToLower(proc.Cmdline), query) &&
			!strings.Contains(strconv.Itoa(proc.PID), query) {
			continue
		}
		out = append(out, proc)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		less := func() bool {
			switch p.sortKey {
			case sortMem:
				return a.RSS < b.RSS
			case sortPID:
				return a.PID < b.PID
			case sortName:
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			default:
				return a.CpuPct < b.CpuPct
			}
		}()
		if p.desc {
			return !less && !equalRank(a, b, p.sortKey)
		}
		return less
	})
	return out
}

// equalRank reports whether two processes tie on the active sort column;
// stable order keeps the PID order of the listing for ties.
func equalRank(a, b core.Process, k sortKey) bool {
	switch k {
	case sortMem:
		return a.RSS == b.RSS
	case sortPID:
		return a.PID == b.PID
	case sortName:
		return strings.EqualFold(a.Name, b.Name)
	default:
		return a.CpuPct == b.CpuPct
	}
}

func indexOfPID(procs []core.Process, pid int) int {
	for i, proc := range procs {
		if proc.PID == pid {
			return i
		}
	}
	return -1
}

// rowsFor renders the displayed processes as table rows clipped to their
// column widths so alignment never breaks on long names.
func rowsFor(procs []core.Process, width int) []table.Row {
	cols := columnsFor(width)
	rows := make([]table.Row, 0, len(procs))
	for _, proc := range procs {
		rows = append(rows, table.Row{
			fmt.Sprintf("%.1f", proc.CpuPct),
			clip(format.Bytes(proc.RSS), cols[1].Width),
			clip(proc.User, cols[2].Width),
			startAt(proc.StartTime),
			clip(strconv.Itoa(proc.PID), cols[4].Width),
			clip(proc.Name, cols[5].Width),
		})
	}
	return rows
}

// clip truncates a cell to n display cells.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// startAt renders a process start time compactly: clock time today, date
// otherwise.
func startAt(t time.Time) string {
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("Jan02")
}

// Title implements Screen.
func (p *processes) Title() string { return "Processes" }

// Hints implements Screen: the processes keymap as the help overlay shows
// it below the global section.
func (p *processes) Hints() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "send TERM")),
		key.NewBinding(key.WithKeys("k"), key.WithHelp("k", "send KILL")),
		key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "sort by cpu")),
		key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "sort by mem")),
		key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "sort by pid")),
		key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "sort by name")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	}
}

// View renders the table with a filter line and a status line, or the
// confirmation modal when one is open.
func (p *processes) View(width, height int) string {
	if p.modalOpen {
		return p.modal.View(width, height)
	}

	if width != p.lastWidth {
		p.lastWidth = width
		p.table.SetColumns(columnsFor(width))
		p.table.SetWidth(width)
		// Column widths changed: the rows must be reclipped.
		p.table.SetRows(rowsFor(p.view, width))
	}

	body := height - 1 // status line
	if p.filtering {
		body--
	}
	if body > 0 {
		p.table.SetHeight(body)
	}

	muted := lipgloss.NewStyle().Foreground(p.theme.Muted)
	danger := lipgloss.NewStyle().Foreground(p.theme.Danger)

	lines := []string{p.table.View()}
	if p.filtering {
		lines = append(lines, p.filter.View())
	}
	lines = append(lines, p.statusLine(muted, danger))

	// The table renders its own rows; the status line is plain, so the
	// composed height matches body+2 by construction.
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// statusLine composes the one-line footer: collection errors first, else
// the listing summary plus the last action outcome.
func (p *processes) statusLine(muted, danger lipgloss.Style) string {
	if p.collectErr != nil {
		return danger.Render(fmt.Sprintf("n/a (%v)", p.collectErr))
	}
	if !p.loaded {
		return danger.Render("collecting...")
	}
	parts := []string{
		fmt.Sprintf("%d processes · sort %s %s", len(p.view), p.sortKey, sortArrow(p.desc)),
	}
	if q := strings.TrimSpace(p.filter.Value()); q != "" {
		parts = append(parts, fmt.Sprintf("filter %q", q))
	}
	if p.status != "" {
		parts = append(parts, p.status)
	}
	return muted.Render(strings.Join(parts, " · "))
}

func sortArrow(desc bool) string {
	if desc {
		return "↓"
	}
	return "↑"
}
