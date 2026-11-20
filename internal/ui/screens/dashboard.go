package screens

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/format"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// dashboardDataMsg carries one collection round. Each section captures its
// own error independently: a failing collector degrades one panel to "n/a"
// and never takes the screen down (ARCHITECTURE.md §5).
type dashboardDataMsg struct {
	snapshot core.ServerSnapshot
	sensors  []core.SensorReading
	errs     map[string]error
}

// dashboard is the live system overview screen.
type dashboard struct {
	sys     collectors.System
	theme   theme.Theme
	tracker *collectors.CPUTracker
	snap    core.ServerSnapshot
	sensors []core.SensorReading
	errs    map[string]error
	// lastGood stamps each section's last successful round, the
	// per-section keeps-last-good bookkeeping of §5: a failed section
	// keeps its last good values on screen and its note ages them.
	lastGood map[string]time.Time
	loaded   bool
}

// NewDashboard builds the overview screen reading from sys and rendering
// with th.
func NewDashboard(sys collectors.System, th theme.Theme) Screen {
	return &dashboard{
		sys:      sys,
		theme:    th,
		tracker:  &collectors.CPUTracker{},
		lastGood: map[string]time.Time{},
	}
}

// Init collects the first data round immediately.
func (d *dashboard) Init() tea.Cmd { return d.collect() }

// Update re-collects on every refresh tick and stores finished rounds.
func (d *dashboard) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshMsg:
		return d, d.collect()
	case dashboardDataMsg:
		d.loaded = true
		// The errs map is per-round state — a section that recovered
		// drops its error — but the data moves per section only: a
		// failed section keeps its last good values (§5's
		// keeps-last-good rule, the same one every other screen
		// follows) and stamps nothing, so its stale-age note has an
		// anchor. The wholesale snap replace that used to live here
		// was the one place a failed round blanked good data.
		d.errs = msg.errs
		good := time.Now()
		if _, failed := msg.errs["identity"]; !failed {
			d.snap.Hostname = msg.snapshot.Hostname
			d.lastGood["identity"] = good
		}
		if _, failed := msg.errs["kernel"]; !failed {
			d.snap.Kernel = msg.snapshot.Kernel
			d.lastGood["kernel"] = good
		}
		if _, failed := msg.errs["cpu"]; !failed {
			d.snap.CPU = msg.snapshot.CPU
			d.lastGood["cpu"] = good
		}
		if _, failed := msg.errs["memory"]; !failed {
			d.snap.Memory = msg.snapshot.Memory
			d.lastGood["memory"] = good
		}
		if _, failed := msg.errs["load"]; !failed {
			d.snap.Load = msg.snapshot.Load
			d.lastGood["load"] = good
		}
		if _, failed := msg.errs["uptime"]; !failed {
			d.snap.Uptime = msg.snapshot.Uptime
			d.lastGood["uptime"] = good
		}
		if _, failed := msg.errs["sensors"]; !failed {
			d.sensors = msg.sensors
			d.lastGood["sensors"] = good
		}
	}
	return d, nil
}

// collect gathers all sections asynchronously through the Bubble Tea
// command runner; per-section errors ride along instead of aborting.
func (d *dashboard) collect() tea.Cmd {
	return func() tea.Msg {
		var msg dashboardDataMsg
		msg.errs = map[string]error{}
		msg.snapshot.At = time.Now()

		host, err := d.sys.Hostname()
		if err != nil {
			msg.errs["identity"] = err
		} else {
			msg.snapshot.Hostname = host
		}
		if kernel, err := d.sys.Kernel(); err != nil {
			msg.errs["kernel"] = err
		} else {
			msg.snapshot.Kernel = kernel
		}
		if agg, per, err := d.sys.CPUStats(); err != nil {
			msg.errs["cpu"] = err
		} else {
			usage, perUsage, measured := d.tracker.Refresh(agg, per)
			model, modelErr := d.sys.CPUModel()
			if modelErr != nil {
				model = "Unknown CPU"
			}
			msg.snapshot.CPU = core.CPUInfo{Model: model, Cores: len(per), Usage: usage, PerCPU: perUsage, Measured: measured}
		}
		if mem, err := d.sys.Memory(); err != nil {
			msg.errs["memory"] = err
		} else {
			msg.snapshot.Memory = mem
		}
		if load, err := d.sys.LoadAvg(); err != nil {
			msg.errs["load"] = err
		} else {
			msg.snapshot.Load = load
		}
		if up, err := d.sys.Uptime(); err != nil {
			msg.errs["uptime"] = err
		} else {
			msg.snapshot.Uptime = up
		}
		if sensors, err := d.sys.Sensors(); err != nil {
			msg.errs["sensors"] = err
		} else {
			msg.sensors = sensors
		}
		return msg
	}
}

// Title implements Screen.
func (d *dashboard) Title() string { return "Dashboard" }

// UpdateKey never claims a key: the dashboard binds no screen-local keys.
func (d *dashboard) UpdateKey(tea.KeyMsg) (Screen, tea.Cmd, bool) { return d, nil, false }

// UpdateMouse never claims a mouse event.
func (d *dashboard) UpdateMouse(tea.MouseMsg) (Screen, tea.Cmd, bool) { return d, nil, false }

// Hints implements Screen: the dashboard binds no screen-local keys in M2.
func (d *dashboard) Hints() []key.Binding { return nil }

// View renders the overview panels within the given bounds.
func (d *dashboard) View(width, height int) string {
	muted := lipgloss.NewStyle().Foreground(d.theme.Muted)
	label := lipgloss.NewStyle().Foreground(d.theme.Secondary)
	danger := lipgloss.NewStyle().Foreground(d.theme.Danger)

	var lines []string
	lines = append(lines, d.identityLine(muted, danger)...)
	lines = append(lines, "")
	lines = append(lines, d.cpuBlock(label, muted, danger, width)...)
	lines = append(lines, "")
	lines = append(lines, d.memoryBlock(label, muted, danger, width)...)
	lines = append(lines, "")
	lines = append(lines, d.loadBlock(label, muted, danger, width)...)
	lines = append(lines, "")
	lines = append(lines, d.sensorsBlock(label, muted, danger, width)...)

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

// sectionNote renders one dashboard section's status annotation and
// reports whether its values render. Keeps-last-good (§5): a failed
// section keeps its last good values on screen and its note carries
// the round error plus the muted age of those values; a section that
// failed before any good round has no values to show and renders the
// error alone; before the first round the muted collecting note
// stands alone. An empty note means the section is fresh.
func (d *dashboard) sectionNote(section string, muted, danger lipgloss.Style) (note string, showValues bool) {
	if err, failed := d.errs[section]; failed {
		note = danger.Render(statusErrorText(err))
		if good, ok := d.lastGood[section]; ok {
			return note + " " + muted.Render(staleSince(good)), true
		}
		return note, false
	}
	if !d.loaded {
		return loadingStatus(muted), false
	}
	return "", true
}

func (d *dashboard) identityLine(muted, danger lipgloss.Style) []string {
	idNote, idValues := d.sectionNote("identity", muted, danger)
	if !idValues {
		return []string{idNote}
	}
	hostLine := fmt.Sprintf("%s · %s", d.snap.Hostname, d.snap.Kernel.HardwareModel)
	if idNote != "" {
		hostLine += " " + idNote
	}

	var kernelText string
	if kNote, kValues := d.sectionNote("kernel", muted, danger); !kValues {
		kernelText = kNote
	} else {
		kernelText = fmt.Sprintf("kernel %s", d.snap.Kernel.Release)
		if kNote != "" {
			kernelText += " " + kNote
		}
		if uNote, uValues := d.sectionNote("uptime", muted, danger); !uValues {
			kernelText += " · " + uNote
		} else {
			kernelText += fmt.Sprintf(" · up %s", format.Duration(d.snap.Uptime))
			if uNote != "" {
				kernelText += " " + uNote
			}
		}
	}
	return []string{hostLine, muted.Render(kernelText)}
}

func (d *dashboard) cpuBlock(label, muted, danger lipgloss.Style, width int) []string {
	note, showValues := d.sectionNote("cpu", muted, danger)
	if !showValues {
		return []string{label.Render("CPU"), note}
	}
	cpu := d.snap.CPU
	head := label.Render("CPU") + muted.Render(fmt.Sprintf("  %s · %s", cpu.Model, coreCountText(cpu.Cores)))
	if note != "" {
		head += " " + note
	}
	// A fresh tracker has no previous sample: the zero usage below is a
	// missing baseline, not an idle CPU. Say so instead of lying.
	if !cpu.Measured {
		return []string{head, muted.Render("measuring…")}
	}
	aggregate := fmt.Sprintf("%s %.1f%%", renderBar(cpu.Usage, barWidth(width), d.theme), cpu.Usage)
	switch {
	case len(cpu.PerCPU) == 0:
		return []string{head, aggregate}
	case len(cpu.PerCPU) <= maxCPUBarCores:
		return append([]string{head}, cpuCoreTable(cpu.Usage, cpu.PerCPU, width, muted, d.theme)...)
	default:
		lines := []string{head, aggregate}
		return append(lines, cpuCoreHeatmap(cpu.PerCPU, width, muted, d.theme)...)
	}
}

// maxCPUBarCores is the boundary between per-core rows (clear and exact,
// but one line per core) and the scalable heatmap: 8 rows still leave
// room for the rest of the dashboard on a 24-row terminal.
const maxCPUBarCores = 8

// cpuCoreTable renders the aggregate plus one aligned row per core: a
// fixed label gutter keeps every bar and percentage in the same columns,
// so a core index reads as a row label instead of a floating number.
func cpuCoreTable(total float64, per []float64, width int, muted lipgloss.Style, th theme.Theme) []string {
	const totalLabel = "total"
	labelW := len(totalLabel)
	barW := coreTableBarWidth(width)
	row := func(label string, pct float64) string {
		return muted.Render(label) + "  " + renderBar(pct, barW, th) + " " + fmt.Sprintf("%5.1f%%", pct)
	}
	lines := []string{row(totalLabel, total), ""}
	for i, pct := range per {
		lines = append(lines, row(fmt.Sprintf("%*d", labelW, i), pct))
	}
	return lines
}

// coreTableBarWidth reserves the label gutter, the gap and the widest
// readout ("100.0%") so the table never overflows its pane.
func coreTableBarWidth(contentWidth int) int {
	w := contentWidth - len("total") - 2 - 7
	if w > 28 {
		w = 28
	}
	if w < 4 {
		w = 4
	}
	return w
}

// heatLevels maps a core's usage to one of eight increasing block glyphs;
// the glyph itself encodes magnitude, so the map still reads without
// color.
var heatLevels = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// usageLevel buckets a percentage into the eight heat levels.
func usageLevel(pct float64) int {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return int(pct/100*float64(len(heatLevels)-1) + 0.5)
}

// cpuCoreHeatmap renders one colored glyph per core, wrapping into rows
// with an index range so many-core hosts never clip.
func cpuCoreHeatmap(per []float64, width int, muted lipgloss.Style, th theme.Theme) []string {
	n := len(per)
	// Reserve the widest possible "start-end" label.
	labelWidth := len(fmt.Sprintf("%d-%d", n-1, n-1))
	perRow := width - labelWidth - 2
	if perRow < 1 {
		perRow = 1
	}

	lines := make([]string, 0, (n+perRow-1)/perRow)
	for start := 0; start < n; start += perRow {
		end := start + perRow
		if end > n {
			end = n
		}
		var row strings.Builder
		for i := start; i < end; i++ {
			color := usageColor(per[i], th)
			row.WriteString(lipgloss.NewStyle().Foreground(color).Render(string(heatLevels[usageLevel(per[i])])))
		}
		label := fmt.Sprintf("%d-%d", start, end-1)
		lines = append(lines, row.String()+"  "+muted.Render(label))
	}
	return lines
}

func (d *dashboard) memoryBlock(label, muted, danger lipgloss.Style, width int) []string {
	note, showValues := d.sectionNote("memory", muted, danger)
	if !showValues {
		return []string{label.Render("Memory"), note}
	}
	lines := memoryTable(d.snap.Memory, width, label, muted, d.theme)
	if note != "" {
		lines = append(lines, note)
	}
	return lines
}

// memoryTable renders memory and swap as one aligned table: a label
// gutter, equal-width bars, one percentage column and one right-aligned
// used/total column, so the two rows compare directly instead of swap
// being a muted footnote.
func memoryTable(mem core.MemoryInfo, width int, label, muted lipgloss.Style, th theme.Theme) []string {
	memBytes := fmt.Sprintf("%s / %s", format.Bytes(mem.Used), format.Bytes(mem.Total))
	bytesW := len(memBytes)
	swapBytes := ""
	if mem.SwapTotal > 0 {
		swapBytes = fmt.Sprintf("%s / %s", format.Bytes(mem.SwapUsed), format.Bytes(mem.SwapTotal))
		if len(swapBytes) > bytesW {
			bytesW = len(swapBytes)
		}
	}
	barW := memoryBarWidth(width, bytesW)
	row := func(name string, style lipgloss.Style, used, total uint64, bytesText string) string {
		pct := percentOf(used, total)
		return style.Render(fmt.Sprintf("%-*s", len("Memory"), name)) + "  " +
			renderBar(pct, barW, th) + " " +
			fmt.Sprintf("%5.1f%%", pct) + "  " +
			fmt.Sprintf("%*s", bytesW, bytesText)
	}
	lines := []string{row("Memory", label, mem.Used, mem.Total, memBytes)}
	if mem.SwapTotal > 0 {
		lines = append(lines, row("swap", muted, mem.SwapUsed, mem.SwapTotal, swapBytes))
	}
	return lines
}

// percentOf returns used/total as a percentage, or zero without capacity.
func percentOf(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

// memoryBarWidth reserves the label gutter, gaps, the percentage column
// and the widest used/total readout, and caps the bar like every other
// dashboard bar.
func memoryBarWidth(contentWidth, bytesW int) int {
	w := contentWidth - len("Memory") - 2 - 1 - 6 - 2 - bytesW
	if w > 28 {
		w = 28
	}
	if w < 4 {
		w = 4
	}
	return w
}

// loadBlock renders the load average section: each window travels
// inline with its value (no detached column legend), the core count sits
// beside the numbers because a load average is only interpretable
// against capacity, and the bar normalizes the 1-minute average against
// all cores (load == cores is 100% capacity). Without a core count the
// section degrades to the plain inline windows instead of dividing by
// zero.
func (d *dashboard) loadBlock(label, muted, danger lipgloss.Style, width int) []string {
	note, showValues := d.sectionNote("load", muted, danger)
	if !showValues {
		return []string{label.Render("Load"), note}
	}
	l := d.snap.Load
	cores := d.snap.CPU.Cores
	head := label.Render("Load")
	if cores > 0 {
		head += muted.Render("  " + coreCountText(cores))
	}
	head += muted.Render(fmt.Sprintf(" · 1m %.2f  5m %.2f  15m %.2f", l.Load1, l.Load5, l.Load15))
	if note != "" {
		head += " " + note
	}
	if cores <= 0 {
		return []string{head}
	}
	pct := l.Load1 / float64(cores) * 100
	return []string{
		head,
		fmt.Sprintf("%s %.0f%% (1m)", renderBar(pct, barWidth(width), d.theme), pct),
	}
}

func (d *dashboard) sensorsBlock(label, muted, danger lipgloss.Style, width int) []string {
	note, showValues := d.sectionNote("sensors", muted, danger)
	if !showValues {
		return []string{label.Render("Sensors"), note}
	}
	if len(d.sensors) == 0 {
		empty := label.Render("Sensors")
		if note != "" {
			empty += " " + note
		}
		return []string{empty, muted.Render("no sensors detected")}
	}
	head := label.Render("Sensors")
	if len(d.sensors) > 1 {
		max := maxTemperature(d.sensors)
		head += muted.Render("  max ") +
			lipgloss.NewStyle().Foreground(temperatureColor(max, d.theme)).Render(fmt.Sprintf("%.0f°C", max))
	}
	if note != "" {
		head += " " + note
	}
	return append([]string{head}, sensorRows(d.sensors, width, muted, d.theme)...)
}

// maxTemperature returns the hottest reading of a non-empty slice.
func maxTemperature(sensors []core.SensorReading) float64 {
	max := sensors[0].Celsius
	for _, s := range sensors[1:] {
		if s.Celsius > max {
			max = s.Celsius
		}
	}
	return max
}

// sensorRows renders one aligned row per sensor: the chip name only when
// it changes, the label in a fixed column, and the temperature
// right-aligned in its own column and colored by threshold.
func sensorRows(sensors []core.SensorReading, width int, muted lipgloss.Style, th theme.Theme) []string {
	nameW, labelW := sensorColumns(sensors, width)
	rows := make([]string, 0, len(sensors))
	lastName := ""
	for _, s := range sensors {
		name := ""
		if s.Name != lastName {
			name = clipRunes(s.Name, nameW)
			lastName = s.Name
		}
		label := clipRunes(s.Label, labelW)
		temp := lipgloss.NewStyle().
			Foreground(temperatureColor(s.Celsius, th)).
			Render(fmt.Sprintf("%3.0f°C", s.Celsius))
		rows = append(rows, muted.Render(fmt.Sprintf("%-*s", nameW, name))+
			"  "+fmt.Sprintf("%-*s", labelW, label)+"  "+temp)
	}
	return rows
}

// sensorColumns sizes the name and label columns for the current pane,
// shrinking the label first and then the name so the temperature cell
// always survives.
func sensorColumns(sensors []core.SensorReading, width int) (nameW, labelW int) {
	for _, s := range sensors {
		if w := utf8.RuneCountInString(s.Name); w > nameW {
			nameW = w
		}
		if w := utf8.RuneCountInString(s.Label); w > labelW {
			labelW = w
		}
	}
	if nameW == 0 {
		nameW = 1
	}
	if labelW == 0 {
		labelW = 1
	}
	if width <= 0 {
		return nameW, labelW
	}
	// Two column gaps plus the widest temperature cell ("100°C").
	avail := width - 4 - 5
	if avail < 2 {
		avail = 2
	}
	for nameW+labelW > avail {
		if labelW > 3 {
			labelW--
		} else if nameW > 3 {
			nameW--
		} else {
			break
		}
	}
	return nameW, labelW
}

// temperatureColor maps a reading to the theme roles: 70°C warns and
// 85°C is dangerous. CPU, GPU and NVMe chips throttle at different
// points, so these are advisory dashboard heuristics, not limits.
func temperatureColor(celsius float64, th theme.Theme) lipgloss.Color {
	if celsius >= 85 {
		return th.Danger
	}
	if celsius >= 70 {
		return th.Warning
	}
	return th.Success
}

// barWidth keeps bars compact but usable: never wider than 28 cells, and
// always leaving room for the numeric readout that follows.
func barWidth(contentWidth int) int {
	if contentWidth > 48 {
		return 28
	}
	if w := contentWidth - 20; w > 4 {
		return w
	}
	return 4
}

// renderBar draws a percentage bar: filled cells in the theme's success or
// warning color as load rises, remainder with the muted role.
func renderBar(pct float64, width int, th theme.Theme) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct / 100 * float64(width))
	fill := lipgloss.NewStyle().Foreground(usageColor(pct, th)).Render(strings.Repeat("█", filled))
	empty := lipgloss.NewStyle().Foreground(barEmptyColor(th)).Render(strings.Repeat("░", width-filled))
	return fill + empty
}

// usageColor maps a percentage to the shared success/warning/danger roles:
// 70% warns and 90% is dangerous, the same thresholds every bar uses.
func usageColor(pct float64, th theme.Theme) lipgloss.Color {
	if pct >= 90 {
		return th.Danger
	}
	if pct >= 70 {
		return th.Warning
	}
	return th.Success
}

// coreCountText renders the logical-CPU count with the correct singular.
func coreCountText(cores int) string {
	if cores == 1 {
		return "1 core"
	}
	return fmt.Sprintf("%d cores", cores)
}

// barEmptyColor is the role of a bar's empty-cell run: muted. The
// border role the bar once used nearly vanishes on the light palette
// (1.4:1 against its background) while the muted role reads on both
// palettes, which the theme tests pin by contrast.
func barEmptyColor(th theme.Theme) lipgloss.Color { return th.Muted }
