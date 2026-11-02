package screens

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/format"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// defaultHealthInterval is the Health refresh cadence: 60 s, the slowest
// module tick. SMART attributes move over hours and days, not seconds, and
// one round may spin idle disks up to read them, so polling faster would
// cost power and noise for data that cannot change between ticks. It is
// the built-in default the config `intervals.health` override replaces.
const defaultHealthInterval = 60 * time.Second

// healthTickMsg re-arms one round of the screen's own cadence.
type healthTickMsg struct{}

// healthMsg carries one collection round: the SMART report — disks plus
// the smartctl version that produced it — and its own error (§5: a failed
// round degrades this screen, never the program).
type healthMsg struct {
	report core.DiskHealthReport
	err    error
}

// health is the Health screen: the host's physical disks with their SMART
// verdict, temperature and power-on time, read through the optional
// smartctl binary (smartmontools). The screen is read-only by design
// (ARCHITECTURE.md §7): it asks the drives how they are doing and never
// starts self-tests, edits attributes or changes settings.
//
// smartctl is not a hard dependency. A host without the package, with a
// smartctl older than 7.0 (no JSON output) or without root/CAP_SYS_RAWIO
// gets a typed, actionable hint in place of the table instead of a
// generic collection error, because those are environment states the
// operator can fix, not transient failures of a round. The hints replace
// the table even when a last-good report exists on purpose: a missing
// binary or missing privilege means the current state is unknown, and
// leaving stale rows under a hint would imply the drives were just read.
type health struct {
	lister core.DiskHealthLister
	theme  theme.Theme

	// interval is the screen's own refresh cadence; lastGood is the
	// instant of its last successful round, the keeps-last-good
	// bookkeeping of §5: a failed later round keeps the last report
	// standing and the status line ages it.
	interval time.Duration
	lastGood time.Time

	table  table.Model
	report core.DiskHealthReport // last good round, sorted by device

	loaded bool
	err    error // error of the last round; the last good report stays (§5)

	lastWidth int
}

// NewHealth builds the Health screen listing through lister and rendering
// with th. All() passes the live collectors.System — which implements
// core.DiskHealthLister over smartctl — and the resolved health cadence;
// tests inject fakes (ARCHITECTURE.md §2: the UI consumes ports). A
// non-positive interval falls back to the built-in §3 default.
func NewHealth(lister core.DiskHealthLister, th theme.Theme, interval time.Duration) Screen {
	return newHealth(lister, th, interval)
}

// newHealth is the concrete constructor; tests use it for white-box state
// assertions.
func newHealth(lister core.DiskHealthLister, th theme.Theme, interval time.Duration) *health {
	if interval <= 0 {
		interval = defaultHealthInterval
	}

	tbl := table.New(
		table.WithColumns(healthColumnsFor(80)),
		table.WithFocused(true),
		table.WithKeyMap(navKeyMap()),
		table.WithStyles(table.Styles{
			Header: lipgloss.NewStyle().Bold(true).Foreground(th.Secondary),
			Selected: lipgloss.NewStyle().
				Foreground(th.Background).Background(th.Primary),
		}),
	)

	return &health{
		lister:    lister,
		theme:     th,
		table:     tbl,
		interval:  interval,
		lastWidth: -1,
	}
}

// healthColumnsFor lays out the table for a given width; MODEL absorbs
// whatever is left (37 cells are fixed: DEVICE 10, HEALTH 9, TEMP 6,
// POWER-ON 12) and is floored at 16. The floor can overflow a very narrow
// terminal, the same trade the storage and processes tables make; the
// shell truncates the rendered line at its edge.
func healthColumnsFor(width int) []table.Column {
	model := width - 37
	if model < 16 {
		model = 16
	}
	return []table.Column{
		{Title: "DEVICE", Width: 10},
		{Title: "MODEL", Width: model},
		{Title: "HEALTH", Width: 9},
		{Title: "TEMP", Width: 6},
		{Title: "POWER-ON", Width: 12},
	}
}

// Init collects the first round immediately and starts the screen's own
// cadence at the configured interval, the storage chain shape: the tick
// dies naturally while the screen is inactive — the router delivers
// healthTickMsg to the ACTIVE screen only, so nobody re-arms it — and
// Init restarts it on re-entry (the §5 cancellable-on-switch story).
func (s *health) Init() tea.Cmd {
	return tea.Batch(s.collect(), s.tick())
}

// tick schedules the next self-armed health tick on the configured
// cadence.
func (s *health) tick() tea.Cmd {
	return tea.Tick(s.interval, func(time.Time) tea.Msg {
		return healthTickMsg{}
	})
}

// Update stores collection rounds and re-arms the cadence. A successful
// round sorts a copy of the report by device, keeping the scan order for
// equal names, and stamps lastGood. A failed round keeps the last good
// report on screen and only records the error, so the next success
// replaces it (§5's keeps-last-good state). The shell's global RefreshMsg
// (the 2 s cadence of the dashboard and processes screens) is deliberately
// ignored: Health refreshes on its own slower cadence (ARCHITECTURE.md
// §3).
func (s *health) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case healthTickMsg:
		return s, tea.Batch(s.collect(), s.tick())
	case healthMsg:
		if msg.err != nil {
			s.err = msg.err
			return s, nil
		}
		disks := make([]core.DiskHealth, len(msg.report.Disks))
		copy(disks, msg.report.Disks)
		// Stable and case-insensitive: device names are the stable
		// identity an operator recognizes fastest, and equal names keep
		// the scan order instead of shuffling between rounds.
		sort.SliceStable(disks, func(i, j int) bool {
			return strings.ToLower(disks[i].Device) < strings.ToLower(disks[j].Device)
		})
		s.report = core.DiskHealthReport{
			Disks:       disks,
			ToolVersion: msg.report.ToolVersion,
		}
		s.loaded = true
		s.lastGood = time.Now()
		s.err = nil
		s.setRows()
	}
	return s, nil
}

// collect gathers the SMART report asynchronously; the round — report and
// error together — rides back as one healthMsg.
func (s *health) collect() tea.Cmd {
	return func() tea.Msg {
		report, err := s.lister.DiskHealth()
		return healthMsg{report: report, err: err}
	}
}

// setRows re-renders the table from the last good report at the last known
// width, or the built-in 80-column layout before the first View.
func (s *health) setRows() {
	width := s.lastWidth
	if width < 0 {
		width = 80
	}
	s.table.SetRows(healthRowsFor(s.report.Disks, width, s.theme))
}

// UpdateKey applies the screen's keymap, which shadows the global one per
// key (ARCHITECTURE.md §6: q quits, ? helps, tab cycles — those still fall
// through). Health has no sort or filter: the disk set is small, always
// sorted by device, and the table's navigation is the whole screen-local
// surface. esc and every letter therefore fall through untouched.
func (s *health) UpdateKey(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
	return s.claimNav(msg)
}

// claimNav offers the key to the table's navigation bindings only; every
// other key falls through to the global keymap (the §6 per-key shadowing
// rule, not wholesale). Without this claim the table would render a cursor
// nobody could move: navKeyMap narrows the table's bindings to
// arrows/pgup/home, and the shell never forwards an unclaimed key to the
// screen again.
func (s *health) claimNav(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
	km := s.table.KeyMap
	if !key.Matches(msg, km.LineUp, km.LineDown, km.PageUp, km.PageDown,
		km.HalfPageUp, km.HalfPageDown, km.GotoTop, km.GotoBottom) {
		return s, nil, false
	}
	var cmd tea.Cmd
	s.table, cmd = s.table.Update(msg)
	return s, cmd, true
}

// UpdateMouse handles nothing: Health is a read-only module — no modal, no
// row actions — so every mouse event passes to the shell (§6's parity rule
// wants keyboard equivalents for mouse actions, and the keyboard side is
// complete).
func (s *health) UpdateMouse(tea.MouseMsg) (Screen, tea.Cmd, bool) {
	return s, nil, false
}

// Title implements Screen.
func (s *health) Title() string { return "Health" }

// Hints implements Screen: navigation is the global table behaviour and
// health has no screen-local keybindings of its own.
func (s *health) Hints() []key.Binding { return nil }

// healthVerdict renders the HEALTH cell in its role: PASSED in the success
// role, FAILED in the danger role, and UNKNOWN muted for a disk that did
// not answer — a SCSI device without smart_status or an unreadable drive.
// A failing disk is data, not a collection error, so FAILED is a normal
// row paint. The plain word is clipped before styling so an escape
// sequence is never cut mid-cell.
func healthVerdict(d core.DiskHealth, th theme.Theme, width int) string {
	verdict, color := "UNKNOWN", th.Muted
	switch {
	case !d.HasVerdict:
	case d.Passed:
		verdict, color = "PASSED", th.Success
	default:
		verdict, color = "FAILED", th.Danger
	}
	return lipgloss.NewStyle().Foreground(color).Render(clip(verdict, width))
}

// healthModelCell renders the MODEL cell, an em dash when smartctl and
// sysfs both failed to name the drive.
func healthModelCell(d core.DiskHealth) string {
	if d.Model == "" {
		return "—"
	}
	return d.Model
}

// healthTempCell renders the TEMP cell, an em dash when the drive reported
// no temperature. A real 0 °C reading stays "0°C" because HasTemp says the
// source answered.
func healthTempCell(d core.DiskHealth) string {
	if !d.HasTemp {
		return "—"
	}
	return fmt.Sprintf("%d°C", d.TemperatureC)
}

// healthPowerOnCell renders the POWER-ON cell as a compact duration, an em
// dash when the drive reported no hour counter.
func healthPowerOnCell(d core.DiskHealth) string {
	if !d.HasPowerOn {
		return "—"
	}
	return format.Duration(time.Duration(d.PowerOnHours) * time.Hour)
}

// healthRowsFor renders the already-sorted disks as table rows, clipped to
// their column widths so alignment never breaks on long device paths or
// model names.
func healthRowsFor(disks []core.DiskHealth, width int, th theme.Theme) []table.Row {
	cols := healthColumnsFor(width)
	rows := make([]table.Row, 0, len(disks))
	for _, d := range disks {
		rows = append(rows, table.Row{
			clip(d.Device, cols[0].Width),
			clip(healthModelCell(d), cols[1].Width),
			healthVerdict(d, th, cols[2].Width),
			clip(healthTempCell(d), cols[3].Width),
			clip(healthPowerOnCell(d), cols[4].Width),
		})
	}
	return rows
}

// hintLines maps the SMART unavailability sentinels to a two-line
// actionable hint: what is missing and what the operator can do about it.
// These states are environment facts — the optional tool is absent, too
// old, or the process lacks the privilege to read the drives — so they get
// a dedicated block instead of the generic "n/a (...)" degradation path,
// and they replace the table even when a last-good report exists (see the
// health type doc).
func hintLines(err error) ([]string, bool) {
	switch {
	case errors.Is(err, core.ErrSmartctlMissing):
		return []string{
			"smartctl is not installed.",
			"Install the smartmontools package.",
		}, true
	case errors.Is(err, core.ErrSmartUnprivileged):
		return []string{
			"SMART needs root or CAP_SYS_RAWIO.",
			"serverctl never escalates privileges.",
		}, true
	case errors.Is(err, core.ErrSmartctlUnsupported):
		return []string{
			"smartctl 7.0 or newer is required.",
			"Upgrade smartmontools for JSON output.",
		}, true
	default:
		return nil, false
	}
}

// View renders the table with a status line. A typed hint state renders
// its two actionable lines in the body instead of the table, even when a
// last-good report exists, because a missing binary or missing privilege
// is an environment state and not stale drift of the data; the status
// line says "health unavailable" rather than pretending a round failed.
// A generic first-round error renders the error alone, a generic later
// error keeps the last good table under the error and its stale age, and
// an empty successful report is a zero-row listing, never an error.
func (s *health) View(width, height int) string {
	if width != s.lastWidth {
		s.lastWidth = width
		s.table.SetColumns(healthColumnsFor(width))
		s.table.SetWidth(width)
		// Column widths changed: the rows must be reclipped.
		s.table.SetRows(healthRowsFor(s.report.Disks, width, s.theme))
	}

	body := height - 1 // status line
	if body > 0 {
		s.table.SetHeight(body)
	}

	warning := lipgloss.NewStyle().Foreground(s.theme.Warning)
	muted := lipgloss.NewStyle().Foreground(s.theme.Muted)
	danger := lipgloss.NewStyle().Foreground(s.theme.Danger)

	if hints, ok := hintLines(s.err); ok {
		lines := make([]string, 0, len(hints)+1)
		for _, hint := range hints {
			lines = append(lines, warning.MaxWidth(width).Render(hint))
		}
		lines = append(lines, muted.Render("health unavailable"))
		if height > 0 && len(lines) > height {
			lines = lines[:height]
		}
		return strings.Join(lines, "\n")
	}

	var lines []string
	if s.loaded {
		lines = append(lines, s.table.View())
	}
	lines = append(lines, s.statusLine(muted, danger))

	// The table renders its own rows; the status line is plain, so the
	// composed height matches body+1 by construction.
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// statusLine composes the one-line footer: the last round's error first —
// with the muted age of the report still on screen while it stays (§5's
// keeps-last-good state) — the muted collecting state until the first
// round lands, else the report summary: disk and verdict counts plus the
// smartctl version when the source named one. The counts come from the
// last good report, so a stale table never claims a fresh verdict.
func (s *health) statusLine(muted, danger lipgloss.Style) string {
	if s.err != nil {
		return errStatus(s.err, s.lastGood, muted, danger)
	}
	if !s.loaded {
		return loadingStatus(muted)
	}
	if len(s.report.Disks) == 0 {
		summary := "0 SMART devices"
		if s.report.ToolVersion != "" {
			summary += " · smartctl " + s.report.ToolVersion
		}
		return muted.Render(summary)
	}

	passed, failed, unknown := 0, 0, 0
	for _, d := range s.report.Disks {
		switch {
		case !d.HasVerdict:
			unknown++
		case d.Passed:
			passed++
		default:
			failed++
		}
	}

	summary := fmt.Sprintf("%d disks · %d passed", len(s.report.Disks), passed)
	if failed > 0 {
		summary += fmt.Sprintf(" · %d failed", failed)
	}
	if unknown > 0 {
		summary += fmt.Sprintf(" · %d unknown", unknown)
	}
	if s.report.ToolVersion != "" {
		summary += " · smartctl " + s.report.ToolVersion
	}
	return muted.Render(summary)
}
