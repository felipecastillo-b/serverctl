package screens

import (
	"fmt"
	"math"
	"sort"
	"strconv"
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

// storageInterval is the Storage refresh cadence of ARCHITECTURE.md
// §3: 30 s, the slowest module tick — mounts and disk geometry rarely
// change and every round walks all of /proc/mounts with a statfs per
// row, which is not a 2 s job.
const storageInterval = 30 * time.Second

// storageTickMsg re-arms one round of the screen's own cadence.
type storageTickMsg struct{}

// storageDataMsg carries one collection round: BOTH listings — the
// views share a single round, so toggling between them re-renders
// without re-collecting — with each list carrying its own error, one
// failed half never blanks the other.
type storageDataMsg struct {
	filesystems []core.Partition
	fsErr       error
	disks       []core.Disk
	diskErr     error
}

// storageView is which half of the round the table renders.
type storageView int

const (
	storageFilesystems storageView = iota
	storageDisks
)

func (v storageView) String() string {
	if v == storageDisks {
		return "disks"
	}
	return "filesystems"
}

// fsSortKey names the column the filesystems table is ordered by.
type fsSortKey int

const (
	// fsSortUse is the default: the attention sort, fullest
	// filesystem first — the storage analogue of the services
	// screen's failed-units-first ladder.
	fsSortUse fsSortKey = iota
	fsSortMount
	fsSortSize
)

func (k fsSortKey) String() string {
	switch k {
	case fsSortMount:
		return "mount"
	case fsSortSize:
		return "size"
	default:
		return "use"
	}
}

// storage is the Storage screen: the mounted-filesystems and block
// device listings collected by m5a's collector, with per-view filter,
// sort and cursor-keeping reapply. Read-only by design
// (ARCHITECTURE.md §7): no modal, no actions.
type storage struct {
	lister core.StorageLister
	theme  theme.Theme

	filesystems []core.Partition // last collected round, both halves
	disks       []core.Disk

	view storageView // which half the table renders

	fsView   []core.Partition // sorted and filtered slice backing the table
	diskView []core.Disk

	table     table.Model
	filter    textinput.Model
	filtering bool

	fsSort fsSortKey
	fsDesc bool

	diskDesc bool // the disks view has a single sort column: name

	fsLoaded, disksLoaded bool
	fsErr, diskErr        error

	lastWidth int
}

// NewStorage builds the storage screen listing filesystems and disks
// through lister and rendering with th. All() passes the live
// collectors.System; tests inject fakes (ARCHITECTURE.md §2: the UI
// consumes ports).
func NewStorage(lister core.StorageLister, th theme.Theme) Screen {
	return newStorage(lister, th)
}

// newStorage is the concrete constructor; tests use it for white-box
// state assertions.
func newStorage(lister core.StorageLister, th theme.Theme) *storage {
	filter := textinput.New()
	filter.Prompt = "/"
	filter.CharLimit = 64
	filter.Width = 30

	tbl := table.New(
		table.WithColumns(fsColumnsFor(80)),
		table.WithFocused(true),
		table.WithKeyMap(navKeyMap()),
		table.WithStyles(table.Styles{
			Header: lipgloss.NewStyle().Bold(true).Foreground(th.Secondary),
			Selected: lipgloss.NewStyle().
				Foreground(th.Background).Background(th.Primary),
		}),
	)

	return &storage{
		lister: lister,
		theme:  th,
		table:  tbl,
		filter: filter,
		// The attention sort is the natural first sort of a storage
		// monitor: the fullest filesystem is the one needing eyes.
		fsSort:    fsSortUse,
		fsDesc:    true,
		lastWidth: -1,
	}
}

// fsColumnsFor lays out the filesystems table for a given width; the
// MOUNT column absorbs whatever is left (60 cells are fixed).
// SIZE/USED/AVAIL need 10 cells: format.Bytes renders "100.0 GiB",
// and a narrower column would clip the unit off every filesystem
// larger than 100 GiB.
func fsColumnsFor(width int) []table.Column {
	mount := width - 60
	if mount < 8 {
		mount = 8
	}
	return []table.Column{
		{Title: "DEVICE", Width: 16},
		{Title: "MOUNT", Width: mount},
		{Title: "FSTYPE", Width: 8},
		{Title: "SIZE", Width: 10},
		{Title: "USED", Width: 10},
		{Title: "AVAIL", Width: 10},
		{Title: "USE%", Width: 6},
	}
}

// diskColumnsFor lays out the disks table for a given width; the
// MODEL column absorbs what is left, floored at its nominal 24 cells
// (46 are fixed).
func diskColumnsFor(width int) []table.Column {
	model := width - 46
	if model < 24 {
		model = 24
	}
	return []table.Column{
		{Title: "NAME", Width: 10},
		{Title: "MODEL", Width: model},
		{Title: "SIZE", Width: 10},
		{Title: "PARTS", Width: 6},
		{Title: "READS", Width: 10},
		{Title: "WRITES", Width: 10},
	}
}

// columnsFor returns the active view's column set.
func (s *storage) columnsFor(width int) []table.Column {
	if s.view == storageDisks {
		return diskColumnsFor(width)
	}
	return fsColumnsFor(width)
}

// Init collects the first round immediately — both listings in one
// command — and starts the screen's own 30 s cadence.
func (s *storage) Init() tea.Cmd {
	return tea.Batch(s.collect(), s.tick())
}

// tick schedules the next self-armed storage tick. The chain dies
// naturally while the screen is inactive — the router delivers
// storageTickMsg to the ACTIVE screen only, so nobody re-arms it —
// and Init restarts it on re-entry (the §5 cancellable-on-switch
// story, same as the services and logs screens).
func (s *storage) tick() tea.Cmd {
	return tea.Tick(storageInterval, func(time.Time) tea.Msg {
		return storageTickMsg{}
	})
}

// Update stores collection rounds and re-arms the cadence. The
// shell's global RefreshMsg (the 2 s cadence of the dashboard and
// processes screens) is deliberately ignored: Storage refreshes on
// its own 30 s cadence (ARCHITECTURE.md §3).
func (s *storage) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case storageTickMsg:
		return s, tea.Batch(s.collect(), s.tick())
	case storageDataMsg:
		s.fsErr = msg.fsErr
		s.diskErr = msg.diskErr
		if msg.fsErr == nil {
			s.filesystems = msg.filesystems
			s.fsLoaded = true
		}
		if msg.diskErr == nil {
			s.disks = msg.disks
			s.disksLoaded = true
		}
		s.reapply()
	}
	return s, nil
}

// collect gathers BOTH listings asynchronously; the round rides back
// as one storageDataMsg. One command per round keeps the two views in
// lockstep — toggling `d` re-renders the other half of the same
// snapshot instead of collecting it on demand.
func (s *storage) collect() tea.Cmd {
	return func() tea.Msg {
		msg := storageDataMsg{}
		msg.filesystems, msg.fsErr = s.lister.Filesystems()
		msg.disks, msg.diskErr = s.lister.Disks()
		return msg
	}
}

// UpdateKey applies the screen's keymap, which shadows the global
// one per key (ARCHITECTURE.md §6: q quits, ? helps, tab cycles —
// those still fall through; sort, filter and view keys are claimed
// here). Filter mode is exclusive: while open it consumes every key.
// The view toggle and the filter key are shared by both views; the
// sort keys belong to their view — m/u/s order filesystems, n orders
// disks — so a keypress means what the visible table says it means.
func (s *storage) UpdateKey(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
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
	case "d":
		s.toggleView()
		return s, nil, true
	case "/":
		s.filtering = true
		s.filter.Focus()
		return s, nil, true
	}

	if s.view == storageDisks {
		switch msg.String() {
		case "n":
			s.diskDesc = !s.diskDesc
			s.reapply()
		default:
			return s.claimNav(msg)
		}
		return s, nil, true
	}

	switch msg.String() {
	case "m":
		s.applyFsSort(fsSortMount)
	case "u":
		s.applyFsSort(fsSortUse)
	case "s":
		s.applyFsSort(fsSortSize)
	default:
		return s.claimNav(msg)
	}
	return s, nil, true
}

// claimNav offers the key to the table's navigation bindings only;
// every other key falls through to the global keymap (the §6
// per-key shadowing rule, not wholesale).
func (s *storage) claimNav(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
	km := s.table.KeyMap
	if !key.Matches(msg, km.LineUp, km.LineDown, km.PageUp, km.PageDown,
		km.HalfPageUp, km.HalfPageDown, km.GotoTop, km.GotoBottom) {
		return s, nil, false
	}
	var cmd tea.Cmd
	s.table, cmd = s.table.Update(msg)
	return s, cmd, true
}

// UpdateMouse handles nothing: Storage is m5a's read-only module — no
// modal, no row actions — so every mouse event passes to the shell
// (§6's parity rule wants keyboard equivalents for mouse actions, and
// the keyboard side is complete).
func (s *storage) UpdateMouse(msg tea.MouseMsg) (Screen, tea.Cmd, bool) {
	return s, nil, false
}

// toggleView swaps the rendered half of the last round: `d` between
// FILESYSTEMS and DISKS. The two views carry different column sets,
// and bubbles' UpdateViewport renders the rows it holds against the
// columns it holds — a row with more cells than the active column
// set panics on the extra cell — so the rows clear before the column
// swap and reapply() rebuilds them for the new view. The cursor lands
// at the top: the two views list different entities, so there is no
// row identity to preserve across the swap.
func (s *storage) toggleView() {
	if s.view == storageDisks {
		s.view = storageFilesystems
	} else {
		s.view = storageDisks
	}
	s.table.SetRows(nil)                          // old-shaped rows must not meet the new columns
	s.table.SetColumns(s.columnsFor(s.lastWidth)) // safe on an empty table
	s.reapply()                                   // rows for the new view, cursor to the top
}

// applyFsSort switches the filesystems sort column, flipping the
// direction when the same key repeats. Use% starts descending — the
// attention direction; mount and size start ascending.
func (s *storage) applyFsSort(k fsSortKey) {
	if s.fsSort == k {
		s.fsDesc = !s.fsDesc
	} else {
		s.fsSort = k
		s.fsDesc = k == fsSortUse
	}
	s.reapply()
}

// reapply re-derives the displayed rows of the ACTIVE view from the
// last round, the filter and the sort, keeping the cursor on the same
// row identity when possible: the mount point in the filesystems
// view, the disk name in the disks view.
func (s *storage) reapply() {
	if s.view == storageDisks {
		name := ""
		// The cursor can sit outside the view (an empty table parks
		// bubbles' cursor at -1), so bounds-check before reading the
		// identity row.
		if cursor := s.table.Cursor(); cursor >= 0 && cursor < len(s.diskView) {
			name = s.diskView[cursor].Name
		}
		s.diskView = s.filterAndSortDisks()
		s.table.SetRows(s.rowsFor(s.lastWidth))
		if idx := indexOfDisk(s.diskView, name); idx >= 0 {
			s.table.SetCursor(idx)
		} else {
			s.table.SetCursor(0)
		}
		return
	}

	mount := ""
	if cursor := s.table.Cursor(); cursor >= 0 && cursor < len(s.fsView) {
		mount = s.fsView[cursor].Mount
	}
	s.fsView = s.filterAndSortFilesystems()
	s.table.SetRows(s.rowsFor(s.lastWidth))
	if idx := indexOfMount(s.fsView, mount); idx >= 0 {
		s.table.SetCursor(idx)
	} else {
		s.table.SetCursor(0)
	}
}

// filterAndSortFilesystems narrows the collected listing to rows whose
// device, mount point or fstype contains the filter query
// (case-insensitive substring), then sorts the survivors by the
// active column: UsedPct, mount path or total size, with the mount
// path as the alphabetical tie-break.
func (s *storage) filterAndSortFilesystems() []core.Partition {
	query := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	out := make([]core.Partition, 0, len(s.filesystems))
	for _, part := range s.filesystems {
		if query != "" &&
			!strings.Contains(strings.ToLower(part.Device), query) &&
			!strings.Contains(strings.ToLower(part.Mount), query) &&
			!strings.Contains(strings.ToLower(part.FSType), query) {
			continue
		}
		out = append(out, part)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		less := func() bool {
			switch s.fsSort {
			case fsSortMount:
				return a.Mount < b.Mount
			case fsSortSize:
				if a.Total != b.Total {
					return a.Total < b.Total
				}
				return a.Mount < b.Mount
			default: // fsSortUse
				if a.UsedPct != b.UsedPct {
					return a.UsedPct < b.UsedPct
				}
				return a.Mount < b.Mount
			}
		}()
		if s.fsDesc {
			return !less && !equalFsRow(a, b, s.fsSort)
		}
		return less
	})
	return out
}

// filterAndSortDisks narrows the collected listing to rows whose name
// or model contains the filter query, then sorts by name; the single
// sort column has no tie-break beyond the stable listing order.
func (s *storage) filterAndSortDisks() []core.Disk {
	query := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	out := make([]core.Disk, 0, len(s.disks))
	for _, disk := range s.disks {
		if query != "" &&
			!strings.Contains(strings.ToLower(disk.Name), query) &&
			!strings.Contains(strings.ToLower(disk.Model), query) {
			continue
		}
		out = append(out, disk)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if s.diskDesc {
			return !strings.EqualFold(a.Name, b.Name) &&
				strings.ToLower(a.Name) > strings.ToLower(b.Name)
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out
}

// equalFsRow reports whether two filesystem rows tie on the active
// sort column; stable order keeps the listing order for ties.
func equalFsRow(a, b core.Partition, k fsSortKey) bool {
	if !strings.EqualFold(a.Mount, b.Mount) {
		return false
	}
	switch k {
	case fsSortSize:
		return a.Total == b.Total
	case fsSortUse:
		return a.UsedPct == b.UsedPct
	default:
		return true
	}
}

func indexOfMount(parts []core.Partition, mount string) int {
	for i, part := range parts {
		if part.Mount == mount {
			return i
		}
	}
	return -1
}

func indexOfDisk(disks []core.Disk, name string) int {
	for i, disk := range disks {
		if disk.Name == name {
			return i
		}
	}
	return -1
}

// usePctColor maps a usage percentage to its theme color: Danger at
// 90% and above, Secondary from 75%, the default text color below.
func usePctColor(pct float64, th theme.Theme) lipgloss.Color {
	switch {
	case pct >= 90:
		return th.Danger
	case pct >= 75:
		return th.Secondary
	default:
		return th.Foreground
	}
}

// usePctCell renders one USE% cell: df's integer display — the raw
// percentage rounded UP to the next integer (coreutils df.c), 0
// staying 0 — colored by threshold. In non-TTY contexts lipgloss
// degrades the color to plain text, so tests see "45%".
func usePctCell(pct float64, th theme.Theme) string {
	value := int(math.Ceil(pct))
	return lipgloss.NewStyle().Foreground(usePctColor(pct, th)).Render(fmt.Sprintf("%d%%", value))
}

// fsRowsFor renders the displayed filesystems as table rows clipped
// to their column widths so alignment never breaks on long mount
// points (the file systems view renders df's SIZE/USED/AVAIL as
// format.Bytes and USE% as df's rounded-up integer).
func fsRowsFor(parts []core.Partition, width int, th theme.Theme) []table.Row {
	cols := fsColumnsFor(width)
	rows := make([]table.Row, 0, len(parts))
	for _, part := range parts {
		rows = append(rows, table.Row{
			clip(part.Device, cols[0].Width),
			clip(part.Mount, cols[1].Width),
			clip(part.FSType, cols[2].Width),
			clip(format.Bytes(part.Total), cols[3].Width),
			clip(format.Bytes(part.Used), cols[4].Width),
			clip(format.Bytes(part.Avail), cols[5].Width),
			usePctCell(part.UsedPct, th),
		})
	}
	return rows
}

// diskRowsFor renders the displayed disks as table rows, clipped.
func diskRowsFor(disks []core.Disk, width int) []table.Row {
	cols := diskColumnsFor(width)
	rows := make([]table.Row, 0, len(disks))
	for _, disk := range disks {
		rows = append(rows, table.Row{
			clip(disk.Name, cols[0].Width),
			clip(disk.Model, cols[1].Width),
			clip(format.Bytes(disk.SizeBytes), cols[2].Width),
			clip(strconv.Itoa(len(disk.Partitions)), cols[3].Width),
			clip(strconv.FormatUint(disk.Reads, 10), cols[4].Width),
			clip(strconv.FormatUint(disk.Writes, 10), cols[5].Width),
		})
	}
	return rows
}

// Title implements Screen.
func (s *storage) Title() string { return "Storage" }

// Hints implements Screen: the storage keymap as the help overlay
// shows it below the global section. Sort keys are listed with their
// view in the hint text since m/u/s and n belong to different views.
func (s *storage) Hints() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "disks/filesystems view")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "sort by use%")),
		key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "sort by mount")),
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort by size")),
		key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "sort by name (disks)")),
	}
}

// View renders the active table with a filter line and a status line.
func (s *storage) View(width, height int) string {
	if width != s.lastWidth {
		s.lastWidth = width
		s.table.SetColumns(s.columnsFor(width))
		s.table.SetWidth(width)
		// Column widths changed: the rows must be reclipped.
		s.table.SetRows(s.rowsFor(s.lastWidth))
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

// rowsFor renders the active view's rows.
func (s *storage) rowsFor(width int) []table.Row {
	if s.view == storageDisks {
		return diskRowsFor(s.diskView, width)
	}
	return fsRowsFor(s.fsView, width, s.theme)
}

// statusLine composes the one-line footer: the ACTIVE view's
// collection error first, its collecting state until the first round
// lands, else the view's listing summary with sort direction and the
// active filter.
func (s *storage) statusLine(muted, danger lipgloss.Style) string {
	if s.view == storageDisks {
		if s.diskErr != nil {
			return danger.Render(fmt.Sprintf("n/a (%v)", s.diskErr))
		}
		if !s.disksLoaded {
			return danger.Render("collecting...")
		}
		parts := []string{
			fmt.Sprintf("%d disks · sort name %s", len(s.diskView), sortArrow(s.diskDesc)),
		}
		if q := strings.TrimSpace(s.filter.Value()); q != "" {
			parts = append(parts, fmt.Sprintf("filter %q", q))
		}
		return muted.Render(strings.Join(parts, " · "))
	}

	if s.fsErr != nil {
		return danger.Render(fmt.Sprintf("n/a (%v)", s.fsErr))
	}
	if !s.fsLoaded {
		return danger.Render("collecting...")
	}
	parts := []string{
		fmt.Sprintf("%d filesystems · sort %s %s", len(s.fsView), s.fsSort, sortArrow(s.fsDesc)),
	}
	if q := strings.TrimSpace(s.filter.Value()); q != "" {
		parts = append(parts, fmt.Sprintf("filter %q", q))
	}
	return muted.Render(strings.Join(parts, " · "))
}
