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

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/format"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// The Network module's two cadences of ARCHITECTURE.md §3: interfaces
// at 2 s — traffic rates only exist between two reads, so the counter
// window stays tight — and sockets at 5 s, deliberately slower — a
// port table barely churns and every round walks /proc/<pid>/fd for
// the listener PIDs. Two chains, one screen: each re-arms itself and
// dies independently while the screen is inactive.
const (
	netInterval   = 2 * time.Second
	socksInterval = 5 * time.Second
)

// netTickMsg re-arms one interfaces round of the 2 s chain.
type netTickMsg struct{}

// sockTickMsg re-arms one listeners+connections round of the 5 s chain.
type sockTickMsg struct{}

// netDataMsg carries one interfaces round: the listing (rates still
// zero — the screen's tracker fills them), the error that killed the
// round, and at, the instant the round started, from which the
// tracker's elapsed window is measured.
type netDataMsg struct {
	at     time.Time
	ifaces []core.NetworkInterface
	err    error
}

// sockDataMsg carries one sockets round: BOTH listings, each with its
// own error — one failed half never blanks the other (the storageDataMsg
// pattern).
type sockDataMsg struct {
	listeners   []core.ListenPort
	listenErr   error
	connections []core.Connection
	connErr     error
}

// netView is which of the three listings the table renders; `v` cycles
// them. All three ride two collection rounds, so a swap re-renders
// without re-collecting.
type netView int

const (
	netInterfaces netView = iota
	netPorts
	netConnections
)

func (v netView) String() string {
	switch v {
	case netPorts:
		return "ports"
	case netConnections:
		return "connections"
	default:
		return "interfaces"
	}
}

// ifaceSortKey names the column the interfaces table is ordered by.
type ifaceSortKey int

const (
	ifaceSortName ifaceSortKey = iota
	ifaceSortRx
	ifaceSortTx
)

func (k ifaceSortKey) String() string {
	switch k {
	case ifaceSortRx:
		return "rx/s"
	case ifaceSortTx:
		return "tx/s"
	default:
		return "name"
	}
}

// connSortKey names the column the connections table is ordered by.
type connSortKey int

const (
	// connSortState is the default: the attention ladder — live
	// conversations first, the connections analogue of the services
	// screen's failed-units-first ranking.
	connSortState connSortKey = iota
	connSortLocal
)

func (k connSortKey) String() string {
	switch k {
	case connSortLocal:
		return "local"
	default:
		return "state"
	}
}

// network is the Network screen: the interfaces, listening ports and
// connections listings of m5b's collectors, cycled by `v`, with
// per-view filter, sort and cursor-keeping reapply. Read-only by
// design (ARCHITECTURE.md §7): no modal, no actions.
type network struct {
	net     core.NetLister
	socks   core.SocketLister
	theme   theme.Theme
	tracker collectors.NetTracker

	interfaces []core.NetworkInterface // last collected round, rates filled by tracker
	listeners  []core.ListenPort
	conns      []core.Connection

	view netView // which listing the table renders

	ifaceView []core.NetworkInterface // sorted and filtered slice backing the table
	portView  []core.ListenPort
	connView  []core.Connection

	table     table.Model
	filter    textinput.Model
	filtering bool

	ifaceSort ifaceSortKey
	ifaceDesc bool

	portDesc bool // the ports view has a single sort column: port

	connSort connSortKey
	connDesc bool

	lastNet time.Time // start of the previous interfaces round, tracker's window anchor

	netLoaded, listenersLoaded, connsLoaded bool
	netErr, listenErr, connErr              error

	lastWidth int
}

// NewNetwork builds the network screen listing interfaces through net
// and sockets through socks, rendering with th. All() passes the live
// collectors.System for both ports; tests inject fakes
// (ARCHITECTURE.md §2: the UI consumes ports).
func NewNetwork(net core.NetLister, socks core.SocketLister, th theme.Theme) Screen {
	return newNetwork(net, socks, th)
}

// newNetwork is the concrete constructor; tests use it for white-box
// state assertions.
func newNetwork(net core.NetLister, socks core.SocketLister, th theme.Theme) *network {
	filter := textinput.New()
	filter.Prompt = "/"
	filter.CharLimit = 64
	filter.Width = 30

	tbl := table.New(
		table.WithColumns(ifaceColumnsFor(80)),
		table.WithFocused(true),
		table.WithKeyMap(navKeyMap()),
		table.WithStyles(table.Styles{
			Header: lipgloss.NewStyle().Bold(true).Foreground(th.Secondary),
			Selected: lipgloss.NewStyle().
				Foreground(th.Background).Background(th.Primary),
		}),
	)

	return &network{
		net:    net,
		socks:  socks,
		theme:  th,
		table:  tbl,
		filter: filter,
		// Name is the natural first sort of an interface listing: a
		// stable, predictable baseline; the rates are one keypress
		// away and start descending (attention direction).
		ifaceSort: ifaceSortName,
		// The connections ladder reads best ascending: live
		// conversations at the top, the TIME_WAIT backlog last.
		connSort:  connSortState,
		lastWidth: -1,
	}
}

// ifaceColumnsFor lays out the interfaces table for a given width;
// the ADDRESSES column absorbs whatever is left (56 cells are fixed).
// RX/TX need 10 cells: format.Bytes renders "100.0 GiB", and a
// narrower column would clip the unit off every interface that moved
// more than 100 GiB since boot. RX/s and TX/s need 10 too — the rate
// cells render "48.8 KiB/s" at scale (the same format.Bytes plus
// "/s"), and the m5a SIZE column lesson applies: the column fits the
// format, not the other way round.
func ifaceColumnsFor(width int) []table.Column {
	addrs := width - 56
	if addrs < 8 {
		addrs = 8
	}
	return []table.Column{
		{Title: "IFACE", Width: 10},
		{Title: "STATE", Width: 6},
		{Title: "ADDRESSES", Width: addrs},
		{Title: "RX", Width: 10},
		{Title: "TX", Width: 10},
		{Title: "RX/s", Width: 10},
		{Title: "TX/s", Width: 10},
	}
}

// portColumnsFor lays out the ports table for a given width; the ADDR
// column absorbs what is left (40 cells are fixed).
func portColumnsFor(width int) []table.Column {
	addr := width - 40
	if addr < 8 {
		addr = 8
	}
	return []table.Column{
		{Title: "PROTO", Width: 6},
		{Title: "ADDR", Width: addr},
		{Title: "PORT", Width: 6},
		{Title: "PID", Width: 8},
		{Title: "PROCESS", Width: 20},
	}
}

// connColumnsFor lays out the connections table for a given width;
// the REMOTE column absorbs what is left (46 are fixed) so LOCAL and
// STATE keep room for a v6 endpoint and a state name.
func connColumnsFor(width int) []table.Column {
	remote := width - 46
	if remote < 8 {
		remote = 8
	}
	return []table.Column{
		{Title: "PROTO", Width: 6},
		{Title: "LOCAL", Width: 28},
		{Title: "REMOTE", Width: remote},
		{Title: "STATE", Width: 12},
	}
}

// columnsFor returns the active view's column set.
func (s *network) columnsFor(width int) []table.Column {
	switch s.view {
	case netPorts:
		return portColumnsFor(width)
	case netConnections:
		return connColumnsFor(width)
	default:
		return ifaceColumnsFor(width)
	}
}

// Init starts BOTH chains: the first interfaces and sockets rounds
// collect immediately, and the two self-armed ticks schedule the
// next rounds of each cadence.
func (s *network) Init() tea.Cmd {
	return tea.Batch(s.collectNet(), s.collectSocks(), s.tickNet(), s.tickSock())
}

// tickNet schedules the next self-armed 2 s tick. The chain dies
// naturally while the screen is inactive — the router delivers
// netTickMsg to the ACTIVE screen only, so nobody re-arms it — and
// Init restarts it on re-entry (the §5 cancellable-on-switch story,
// same as the services and logs screens). sockTick below is its own
// chain: each cadence dies and revives independently of the other.
func (s *network) tickNet() tea.Cmd {
	return tea.Tick(netInterval, func(time.Time) tea.Msg {
		return netTickMsg{}
	})
}

// tickSock schedules the next self-armed 5 s tick of the sockets chain.
func (s *network) tickSock() tea.Cmd {
	return tea.Tick(socksInterval, func(time.Time) tea.Msg {
		return sockTickMsg{}
	})
}

// Update stores collection rounds and re-arms whichever cadence fired.
// The shell's global RefreshMsg (the 2 s cadence of the dashboard and
// processes screens) is deliberately ignored: Network runs its own two
// chains (ARCHITECTURE.md §3).
func (s *network) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case netTickMsg:
		return s, tea.Batch(s.collectNet(), s.tickNet())
	case sockTickMsg:
		return s, tea.Batch(s.collectSocks(), s.tickSock())
	case netDataMsg:
		s.netErr = msg.err
		if msg.err == nil {
			// The tracker's window runs from the previous round's
			// start to this one's; a first round (or a clock that
			// went backwards) only baselines, per NetTracker's
			// zero-elapsed contract.
			var elapsed time.Duration
			if !s.lastNet.IsZero() {
				elapsed = msg.at.Sub(s.lastNet)
			}
			s.interfaces = s.tracker.Refresh(msg.ifaces, elapsed)
			s.lastNet = msg.at
			s.netLoaded = true
		}
		s.reapply()
	case sockDataMsg:
		s.listenErr = msg.listenErr
		s.connErr = msg.connErr
		if msg.listenErr == nil {
			s.listeners = msg.listeners
			s.listenersLoaded = true
		}
		if msg.connErr == nil {
			s.conns = msg.connections
			s.connsLoaded = true
		}
		s.reapply()
	}
	return s, nil
}

// collectNet gathers the interface listing asynchronously, stamping
// the round's start instant so the tracker's window is measured from
// collection start, not from message arrival — a stalled UI frame
// must not widen the rate window.
func (s *network) collectNet() tea.Cmd {
	return func() tea.Msg {
		at := time.Now()
		ifaces, err := s.net.Interfaces()
		return netDataMsg{at: at, ifaces: ifaces, err: err}
	}
}

// collectSocks gathers BOTH socket listings asynchronously; the round
// rides back as one sockDataMsg that feeds the ports and connections
// views — toggling `v` re-renders the other listing of the same
// snapshot instead of collecting it on demand.
func (s *network) collectSocks() tea.Cmd {
	return func() tea.Msg {
		msg := sockDataMsg{}
		msg.listeners, msg.listenErr = s.socks.Listeners()
		msg.connections, msg.connErr = s.socks.Connections()
		return msg
	}
}

// UpdateKey applies the screen's keymap, which shadows the global
// one per key (ARCHITECTURE.md §6: q quits, ? helps, tab cycles —
// those still fall through; the view, filter and sort keys are
// claimed here). Filter mode is exclusive: while open it consumes
// every key. The view cycle and the filter key are shared by all
// three views; the sort keys belong to their view — n/r/t order
// interfaces, p orders ports, s/l order connections — so a keypress
// means what the visible table says it means.
func (s *network) UpdateKey(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
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
	case "v":
		s.cycleView()
		return s, nil, true
	case "/":
		s.filtering = true
		s.filter.Focus()
		return s, nil, true
	}

	switch s.view {
	case netPorts:
		if msg.String() == "p" {
			s.portDesc = !s.portDesc
			s.reapply()
			return s, nil, true
		}
	case netConnections:
		switch msg.String() {
		case "s":
			s.applyConnSort(connSortState)
		case "l":
			s.applyConnSort(connSortLocal)
		default:
			return s.claimNav(msg)
		}
		return s, nil, true
	default:
		switch msg.String() {
		case "n":
			s.applyIfaceSort(ifaceSortName)
		case "r":
			s.applyIfaceSort(ifaceSortRx)
		case "t":
			s.applyIfaceSort(ifaceSortTx)
		default:
			return s.claimNav(msg)
		}
		return s, nil, true
	}
	return s.claimNav(msg)
}

// claimNav offers the key to the table's navigation bindings only;
// every other key falls through to the global keymap (the §6
// per-key shadowing rule, not wholesale).
func (s *network) claimNav(msg tea.KeyMsg) (Screen, tea.Cmd, bool) {
	km := s.table.KeyMap
	if !key.Matches(msg, km.LineUp, km.LineDown, km.PageUp, km.PageDown,
		km.HalfPageUp, km.HalfPageDown, km.GotoTop, km.GotoBottom) {
		return s, nil, false
	}
	var cmd tea.Cmd
	s.table, cmd = s.table.Update(msg)
	return s, cmd, true
}

// UpdateMouse handles nothing: Network is a read-only module — no
// modal, no row actions — so every mouse event passes to the shell
// (§6's parity rule wants keyboard equivalents for mouse actions, and
// the keyboard side is complete).
func (s *network) UpdateMouse(msg tea.MouseMsg) (Screen, tea.Cmd, bool) {
	return s, nil, false
}

// cycleView advances the rendered listing: `v` walks interfaces →
// ports → connections. The three views carry different column sets,
// and bubbles' UpdateViewport renders the rows it holds against the
// columns it holds — a row with more cells than the active column
// set panics on the extra cell — so the rows clear before the column
// swap and reapply() rebuilds them for the new view (m5a's SetColumns
// discovery). The cursor lands at the top: the three views list
// different entities, so there is no row identity to preserve across
// the cycle.
func (s *network) cycleView() {
	s.view = (s.view + 1) % 3
	s.table.SetRows(nil)                          // old-shaped rows must not meet the new columns
	s.table.SetColumns(s.columnsFor(s.lastWidth)) // safe on an empty table
	s.reapply()                                   // rows for the new view, cursor to the top
}

// applyIfaceSort switches the interfaces sort column, flipping the
// direction when the same key repeats. Name starts ascending; the
// rate sorts start descending — the attention direction, busiest
// interface first.
func (s *network) applyIfaceSort(k ifaceSortKey) {
	if s.ifaceSort == k {
		s.ifaceDesc = !s.ifaceDesc
	} else {
		s.ifaceSort = k
		s.ifaceDesc = k != ifaceSortName
	}
	s.reapply()
}

// applyConnSort switches the connections sort column, flipping the
// direction when the same key repeats; both columns read best
// ascending first.
func (s *network) applyConnSort(k connSortKey) {
	if s.connSort == k {
		s.connDesc = !s.connDesc
	} else {
		s.connSort = k
		s.connDesc = false
	}
	s.reapply()
}

// reapply re-derives the displayed rows of the ACTIVE view from the
// last rounds, the filter and the sort, keeping the cursor on the
// same row identity when possible: the interface name, the
// (proto, addr, port) of a listener, the full row tuple of a
// connection. The cursor can sit outside the view (an empty table
// parks bubbles' cursor at -1), so bounds-check before reading the
// identity row (m5a's discovery).
func (s *network) reapply() {
	switch s.view {
	case netPorts:
		var keep core.ListenPort
		if cursor := s.table.Cursor(); cursor >= 0 && cursor < len(s.portView) {
			keep = s.portView[cursor]
		}
		s.portView = s.filterAndSortPorts()
		s.table.SetRows(s.rowsFor(s.lastWidth))
		if idx := indexOfPort(s.portView, keep); idx >= 0 {
			s.table.SetCursor(idx)
		} else {
			s.table.SetCursor(0)
		}
	case netConnections:
		var keep core.Connection
		if cursor := s.table.Cursor(); cursor >= 0 && cursor < len(s.connView) {
			keep = s.connView[cursor]
		}
		s.connView = s.filterAndSortConns()
		s.table.SetRows(s.rowsFor(s.lastWidth))
		if idx := indexOfConn(s.connView, keep); idx >= 0 {
			s.table.SetCursor(idx)
		} else {
			s.table.SetCursor(0)
		}
	default:
		name := ""
		if cursor := s.table.Cursor(); cursor >= 0 && cursor < len(s.ifaceView) {
			name = s.ifaceView[cursor].Name
		}
		s.ifaceView = s.filterAndSortIfaces()
		s.table.SetRows(s.rowsFor(s.lastWidth))
		if idx := indexOfIface(s.ifaceView, name); idx >= 0 {
			s.table.SetCursor(idx)
		} else {
			s.table.SetCursor(0)
		}
	}
}

// filterAndSortIfaces narrows the collected listing to rows whose name
// or addresses contain the filter query (case-insensitive substring),
// then sorts the survivors by the active column: name, receive rate
// or transmit rate, with the name as the alphabetical tie-break.
func (s *network) filterAndSortIfaces() []core.NetworkInterface {
	query := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	out := make([]core.NetworkInterface, 0, len(s.interfaces))
	for _, ifi := range s.interfaces {
		if query != "" && !ifaceMatches(ifi, query) {
			continue
		}
		out = append(out, ifi)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		less := func() bool {
			switch s.ifaceSort {
			case ifaceSortRx:
				if a.RxRate != b.RxRate {
					return a.RxRate < b.RxRate
				}
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			case ifaceSortTx:
				if a.TxRate != b.TxRate {
					return a.TxRate < b.TxRate
				}
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			default:
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
		}()
		if s.ifaceDesc {
			return !less && !equalIfaceRow(a, b, s.ifaceSort)
		}
		return less
	})
	return out
}

// ifaceMatches reports whether a filter query hits an interface's
// name or any of its addresses.
func ifaceMatches(ifi core.NetworkInterface, query string) bool {
	if strings.Contains(strings.ToLower(ifi.Name), query) {
		return true
	}
	for _, addr := range ifi.Addrs {
		if strings.Contains(strings.ToLower(addr), query) {
			return true
		}
	}
	return false
}

// filterAndSortPorts narrows the collected listeners to rows whose
// protocol, address or process name contains the filter query, then
// sorts by port; the single sort column ties on the endpoint so the
// desc flip is a full reversal.
func (s *network) filterAndSortPorts() []core.ListenPort {
	query := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	out := make([]core.ListenPort, 0, len(s.listeners))
	for _, p := range s.listeners {
		if query != "" &&
			!strings.Contains(strings.ToLower(p.Proto), query) &&
			!strings.Contains(strings.ToLower(p.Addr), query) &&
			!strings.Contains(strings.ToLower(p.Process), query) {
			continue
		}
		out = append(out, p)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		less := a.Port < b.Port
		if a.Port == b.Port {
			less = sockKey(a.Proto, a.Addr, int(a.Port)) < sockKey(b.Proto, b.Addr, int(b.Port))
		}
		if s.portDesc {
			return !less && !equalPortRow(a, b)
		}
		return less
	})
	return out
}

// filterAndSortConns narrows the collected connections to rows whose
// protocol, local endpoint, remote endpoint or state contains the
// filter query, then sorts the survivors by the active column: the
// state attention ladder or the local endpoint.
func (s *network) filterAndSortConns() []core.Connection {
	query := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	out := make([]core.Connection, 0, len(s.conns))
	for _, c := range s.conns {
		if query != "" &&
			!strings.Contains(strings.ToLower(c.Proto), query) &&
			!strings.Contains(strings.ToLower(sockEndpoint(c.LocalAddr, c.LocalPort)), query) &&
			!strings.Contains(strings.ToLower(sockEndpoint(c.RemoteAddr, c.RemotePort)), query) &&
			!strings.Contains(strings.ToLower(c.State), query) {
			continue
		}
		out = append(out, c)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		less := func() bool {
			switch s.connSort {
			case connSortLocal:
				return sockKey(a.Proto, a.LocalAddr, int(a.LocalPort)) <
					sockKey(b.Proto, b.LocalAddr, int(b.LocalPort))
			default: // connSortState: the attention ladder first,
				// the local endpoint as the tie-break per rung.
				if ra, rb := connRank(a.State), connRank(b.State); ra != rb {
					return ra < rb
				}
				return sockKey(a.Proto, a.LocalAddr, int(a.LocalPort)) <
					sockKey(b.Proto, b.LocalAddr, int(b.LocalPort))
			}
		}()
		if s.connDesc {
			return !less && !equalConnRow(a, b, s.connSort)
		}
		return less
	})
	return out
}

// connRank maps a connection state to its rank under the state sort.
// The comparator is an attention bias, mirroring the services screen's
// attentionRank: live conversations come first — ESTABLISHED, and
// CONNECTED, UDP's label for the same thing — then handshakes in
// flight, then the closing family, then the TIME_WAIT backlog, then
// closed sockets, and anything untranslatable last. Ascending puts
// live traffic at the top of the table; the desc flip inverts the
// whole ladder, tie-breaks included.
func connRank(state string) int {
	switch state {
	case "ESTABLISHED", "CONNECTED":
		return 0
	case "SYN_SENT", "SYN_RECV":
		return 1
	case "FIN_WAIT1", "FIN_WAIT2", "CLOSE_WAIT", "LAST_ACK", "CLOSING":
		return 2
	case "TIME_WAIT":
		return 3
	case "CLOSE":
		return 4
	default:
		return 5
	}
}

// sockKey renders one sortable identity string for an endpoint.
func sockKey(proto, addr string, port int) string {
	return proto + " " + addr + " " + strconv.Itoa(port)
}

// equalIfaceRow reports whether two interface rows tie on the active
// sort column; stable order keeps the listing order for ties.
func equalIfaceRow(a, b core.NetworkInterface, k ifaceSortKey) bool {
	if !strings.EqualFold(a.Name, b.Name) {
		return false
	}
	switch k {
	case ifaceSortRx:
		return a.RxRate == b.RxRate
	case ifaceSortTx:
		return a.TxRate == b.TxRate
	default:
		return true
	}
}

// equalPortRow reports whether two listener rows tie on the port
// sort column (same endpoint, different owner).
func equalPortRow(a, b core.ListenPort) bool {
	return a.Port == b.Port && sockKey(a.Proto, a.Addr, int(a.Port)) == sockKey(b.Proto, b.Addr, int(b.Port))
}

// equalConnRow reports whether two connection rows tie on the active
// sort column; the state sort ties on rank and local endpoint.
func equalConnRow(a, b core.Connection, k connSortKey) bool {
	switch k {
	case connSortState:
		return connRank(a.State) == connRank(b.State) &&
			sockKey(a.Proto, a.LocalAddr, int(a.LocalPort)) == sockKey(b.Proto, b.LocalAddr, int(b.LocalPort))
	default:
		return sockKey(a.Proto, a.LocalAddr, int(a.LocalPort)) == sockKey(b.Proto, b.LocalAddr, int(b.LocalPort))
	}
}

func indexOfIface(ifaces []core.NetworkInterface, name string) int {
	for i, ifi := range ifaces {
		if ifi.Name == name {
			return i
		}
	}
	return -1
}

func indexOfPort(ports []core.ListenPort, keep core.ListenPort) int {
	for i, p := range ports {
		if p.Proto == keep.Proto && p.Addr == keep.Addr && p.Port == keep.Port {
			return i
		}
	}
	return -1
}

func indexOfConn(conns []core.Connection, keep core.Connection) int {
	for i, c := range conns {
		if c == keep {
			return i
		}
	}
	return -1
}

// rateCell renders one bytes-per-second cell in the "%.1f B/s" shape
// below 1 KiB/s, and format.Bytes plus "/s" above, so a saturating
// link reads "48.8 KiB/s" instead of an unreadable eight-digit B/s
// figure. The byte count rounds to the nearest whole byte first —
// truncation would drop 4095.9 back under the KiB boundary — and the
// 10-cell columns clip whatever still does not fit.
func rateCell(rate float64) string {
	if rate < 1024 {
		return fmt.Sprintf("%.1f B/s", rate)
	}
	return format.Bytes(uint64(rate+0.5)) + "/s"
}

// sockAddr renders a bare socket address the way ss does: IPv6
// addresses bracketed, IPv4 and wildcards plain.
func sockAddr(addr string) string {
	if strings.Contains(addr, ":") {
		return "[" + addr + "]"
	}
	return addr
}

// sockEndpoint renders one address:port endpoint the way ss does,
// brackets included for IPv6.
func sockEndpoint(addr string, port uint16) string {
	if strings.Contains(addr, ":") {
		return fmt.Sprintf("[%s]:%d", addr, port)
	}
	return fmt.Sprintf("%s:%d", addr, port)
}

// ifaceRowsFor renders the displayed interfaces as table rows
// clipped to their column widths: counters as format.Bytes, rates via
// rateCell, addresses comma-joined in the slack column.
func ifaceRowsFor(ifaces []core.NetworkInterface, width int) []table.Row {
	cols := ifaceColumnsFor(width)
	rows := make([]table.Row, 0, len(ifaces))
	for _, ifi := range ifaces {
		state := "down"
		if ifi.Up {
			state = "up"
		}
		rows = append(rows, table.Row{
			clip(ifi.Name, cols[0].Width),
			clip(state, cols[1].Width),
			clip(strings.Join(ifi.Addrs, ","), cols[2].Width),
			clip(format.Bytes(ifi.Rx), cols[3].Width),
			clip(format.Bytes(ifi.Tx), cols[4].Width),
			clip(rateCell(ifi.RxRate), cols[5].Width),
			clip(rateCell(ifi.TxRate), cols[6].Width),
		})
	}
	return rows
}

// portRowsFor renders the displayed listeners as table rows, clipped.
// An unresolved owner — another user's socket, or the kernel's —
// reads "-" in both columns rather than the misleading "0".
func portRowsFor(ports []core.ListenPort, width int) []table.Row {
	cols := portColumnsFor(width)
	rows := make([]table.Row, 0, len(ports))
	for _, p := range ports {
		pid, process := "-", "-"
		if p.PID > 0 {
			pid = strconv.Itoa(p.PID)
			process = p.Process
		}
		rows = append(rows, table.Row{
			clip(p.Proto, cols[0].Width),
			clip(sockAddr(p.Addr), cols[1].Width),
			clip(strconv.Itoa(int(p.Port)), cols[2].Width),
			clip(pid, cols[3].Width),
			clip(process, cols[4].Width),
		})
	}
	return rows
}

// connRowsFor renders the displayed connections as table rows,
// clipped, with both endpoints in ss's bracketed form.
func connRowsFor(conns []core.Connection, width int) []table.Row {
	cols := connColumnsFor(width)
	rows := make([]table.Row, 0, len(conns))
	for _, c := range conns {
		rows = append(rows, table.Row{
			clip(c.Proto, cols[0].Width),
			clip(sockEndpoint(c.LocalAddr, c.LocalPort), cols[1].Width),
			clip(sockEndpoint(c.RemoteAddr, c.RemotePort), cols[2].Width),
			clip(c.State, cols[3].Width),
		})
	}
	return rows
}

// Title implements Screen.
func (s *network) Title() string { return "Network" }

// Hints implements Screen: the network keymap as the help overlay
// shows it below the global section. Sort keys are listed with their
// view in the hint text since n/r/t, p and s/l belong to different
// views.
func (s *network) Hints() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "interfaces/ports/connections view")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "sort by name (interfaces)")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "sort by rx/s (interfaces)")),
		key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "sort by tx/s (interfaces)")),
		key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "sort by port (ports)")),
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort by state (connections)")),
		key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "sort by local (connections)")),
	}
}

// View renders the active table with a filter line and a status line.
func (s *network) View(width, height int) string {
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
func (s *network) rowsFor(width int) []table.Row {
	switch s.view {
	case netPorts:
		return portRowsFor(s.portView, width)
	case netConnections:
		return connRowsFor(s.connView, width)
	default:
		return ifaceRowsFor(s.ifaceView, width)
	}
}

// statusLine composes the one-line footer: the ACTIVE view's
// collection error first, its collecting state until the first round
// lands, else the view's listing summary with sort direction and the
// active filter. Each view reads its own half's state — a failed
// interfaces round never blanks the ports listing.
func (s *network) statusLine(muted, danger lipgloss.Style) string {
	var err error
	var loaded bool
	var summary string
	switch s.view {
	case netPorts:
		err, loaded = s.listenErr, s.listenersLoaded
		summary = fmt.Sprintf("%d ports · sort port %s", len(s.portView), sortArrow(s.portDesc))
	case netConnections:
		err, loaded = s.connErr, s.connsLoaded
		summary = fmt.Sprintf("%d connections · sort %s %s", len(s.connView), s.connSort, sortArrow(s.connDesc))
	default:
		err, loaded = s.netErr, s.netLoaded
		summary = fmt.Sprintf("%d interfaces · sort %s %s", len(s.ifaceView), s.ifaceSort, sortArrow(s.ifaceDesc))
	}

	if err != nil {
		return danger.Render(fmt.Sprintf("n/a (%v)", err))
	}
	if !loaded {
		return danger.Render("collecting...")
	}
	parts := []string{summary}
	if q := strings.TrimSpace(s.filter.Value()); q != "" {
		parts = append(parts, fmt.Sprintf("filter %q", q))
	}
	return muted.Render(strings.Join(parts, " · "))
}
