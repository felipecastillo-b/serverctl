package screens

import (
	"fmt"
	"strings"
	"time"

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
			usage, perUsage := d.tracker.Refresh(agg, per)
			model, modelErr := d.sys.CPUModel()
			if modelErr != nil {
				model = "Unknown CPU"
			}
			msg.snapshot.CPU = core.CPUInfo{Model: model, Cores: len(per), Usage: usage, PerCPU: perUsage}
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
	lines = append(lines, d.loadBlock(label, muted, danger)...)
	lines = append(lines, "")
	lines = append(lines, d.sensorsBlock(label, muted, danger)...)

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
		note = danger.Render(fmt.Sprintf("n/a (%v)", err))
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
	head := label.Render("CPU") + muted.Render(fmt.Sprintf("  %s · %d cores", cpu.Model, cpu.Cores))
	if note != "" {
		head += " " + note
	}
	bar := renderBar(cpu.Usage, barWidth(width), d.theme)
	lines := []string{
		head,
		fmt.Sprintf("%s %.1f%%", bar, cpu.Usage),
	}
	if len(cpu.PerCPU) > 0 {
		parts := make([]string, len(cpu.PerCPU))
		for i, p := range cpu.PerCPU {
			parts[i] = fmt.Sprintf("%.0f%%", p)
		}
		lines = append(lines, muted.Render(strings.Join(parts, " ")))
	}
	return lines
}

func (d *dashboard) memoryBlock(label, muted, danger lipgloss.Style, width int) []string {
	note, showValues := d.sectionNote("memory", muted, danger)
	if !showValues {
		return []string{label.Render("Memory"), note}
	}
	mem := d.snap.Memory
	pct := 0.0
	if mem.Total > 0 {
		pct = float64(mem.Used) / float64(mem.Total) * 100
	}
	head := label.Render("Memory")
	if note != "" {
		head += " " + note
	}
	lines := []string{
		head,
		fmt.Sprintf("%s %s / %s", renderBar(pct, barWidth(width), d.theme), format.Bytes(mem.Used), format.Bytes(mem.Total)),
	}
	if mem.SwapTotal > 0 {
		lines = append(lines, muted.Render(fmt.Sprintf("swap %s / %s", format.Bytes(mem.SwapUsed), format.Bytes(mem.SwapTotal))))
	}
	return lines
}

func (d *dashboard) loadBlock(label, muted, danger lipgloss.Style) []string {
	note, showValues := d.sectionNote("load", muted, danger)
	if !showValues {
		return []string{label.Render("Load"), note}
	}
	l := d.snap.Load
	head := label.Render("Load") + muted.Render("  1m 5m 15m")
	if note != "" {
		head += " " + note
	}
	return []string{
		head,
		fmt.Sprintf("%.2f %.2f %.2f", l.Load1, l.Load5, l.Load15),
	}
}

func (d *dashboard) sensorsBlock(label, muted, danger lipgloss.Style) []string {
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
	if note != "" {
		head += " " + note
	}
	lines := []string{head}
	for _, s := range d.sensors {
		lines = append(lines, fmt.Sprintf("%s %s  %.0f°C", s.Name, s.Label, s.Celsius))
	}
	return lines
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
	color := th.Success
	if pct >= 90 {
		color = th.Danger
	} else if pct >= 70 {
		color = th.Warning
	}
	fill := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled))
	empty := lipgloss.NewStyle().Foreground(barEmptyColor(th)).Render(strings.Repeat("░", width-filled))
	return fill + empty
}

// barEmptyColor is the role of a bar's empty-cell run: muted. The
// border role the bar once used nearly vanishes on the light palette
// (1.4:1 against its background) while the muted role reads on both
// palettes, which the theme tests pin by contrast.
func barEmptyColor(th theme.Theme) lipgloss.Color { return th.Muted }
