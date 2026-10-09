package screens

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// fakeNetLister serves a static interface listing (or a per-list
// error) instead of reading /proc/net/dev and netlink.
type fakeNetLister struct {
	ifaces []core.NetworkInterface
	err    error
	calls  int
}

func (f *fakeNetLister) Interfaces() ([]core.NetworkInterface, error) {
	f.calls++
	return f.ifaces, f.err
}

// fakeSockLister serves static listener and connection listings (or
// per-list errors) instead of reading /proc/net/{tcp,tcp6,udp,udp6}.
type fakeSockLister struct {
	listeners   []core.ListenPort
	conns       []core.Connection
	listenErr   error
	connErr     error
	listenCalls int
	connCalls   int
}

func (f *fakeSockLister) Listeners() ([]core.ListenPort, error) {
	f.listenCalls++
	return f.listeners, f.listenErr
}

func (f *fakeSockLister) Connections() ([]core.Connection, error) {
	f.connCalls++
	return f.conns, f.connErr
}

// Compile-time proof that the fakes satisfy the m5b ports, exactly
// like collectors.System does.
var (
	_ core.NetLister    = (*fakeNetLister)(nil)
	_ core.SocketLister = (*fakeSockLister)(nil)
)

// netFixture mixes a big-counter loopback, a working ethernet link
// with both address families, and a down tunnel with nothing
// configured, so the name sort, the address filter and the up/down
// column all have real rows.
var netFixture = []core.NetworkInterface{
	{Name: "lo", Up: true, Addrs: []string{"127.0.0.1", "::1"}, Rx: 5123456789, Tx: 5123456789},
	{Name: "eth0", Up: true, MAC: "20:1a:06:cf:66:4e", Addrs: []string{"192.168.1.10", "fe80::1"}, Rx: 123456789, Tx: 98765432},
	{Name: "tun0", Up: false},
}

// portsFixture mirrors the collector fixture shapes: a resolved
// listener pair, a resolved udp listener, and one unresolved row
// (PID 0) exercising the "-" rendering.
var portsFixture = []core.ListenPort{
	{Proto: "tcp", Addr: "0.0.0.0", Port: 22, PID: 1, Process: "systemd"},
	{Proto: "tcp6", Addr: "::", Port: 22, PID: 1, Process: "systemd"},
	{Proto: "udp", Addr: "127.0.0.1", Port: 53, PID: 999, Process: "dnsmasq"},
	{Proto: "tcp", Addr: "127.0.0.1", Port: 8080},
}

// connsFixture spans the state attention ladder: a live TCP
// conversation, a connected UDP flow (same rank, tie-broken by
// local endpoint), a closing socket, and a TIME_WAIT leftover.
var connsFixture = []core.Connection{
	{Proto: "tcp", LocalAddr: "192.168.1.10", LocalPort: 40010, RemoteAddr: "142.250.79.46", RemotePort: 443, State: "ESTABLISHED"},
	{Proto: "udp", LocalAddr: "127.0.0.1", LocalPort: 40010, RemoteAddr: "127.0.0.1", RemotePort: 53, State: "CONNECTED"},
	{Proto: "tcp", LocalAddr: "127.0.0.1", LocalPort: 51144, RemoteAddr: "127.0.0.1", RemotePort: 8080, State: "CLOSE_WAIT"},
	{Proto: "tcp", LocalAddr: "127.0.0.1", LocalPort: 57536, RemoteAddr: "127.0.0.1", RemotePort: 8080, State: "TIME_WAIT"},
}

// newNetworkScreen builds the network screen over the given fakes
// and drives the first collection round synchronously. Init batches
// [collectNet, collectSocks, tickNet, tickSock] and that batch is
// lazy — executing it only yields the legs — so the fixture runs the
// two collect legs; the 2 s and 5 s timer legs stay runtime
// territory, driving them here would stall the test.
func newNetworkScreen(t *testing.T, net *fakeNetLister, socks *fakeSockLister) *network {
	t.Helper()
	s := newNetwork(net, socks, theme.Dark())
	cmd := s.Init()
	if cmd == nil {
		t.Fatal("Init must start both chains and collect both listings")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 4 {
		t.Fatalf("Init must batch both collects and both tick legs, got %T len %d", batch, len(batch))
	}
	netMsg := batch[0]()
	if _, ok := netMsg.(netDataMsg); !ok {
		t.Fatalf("the first leg must collect interfaces, got %T", netMsg)
	}
	updated, _ := s.Update(netMsg)
	s, ok = updated.(*network)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	sockMsg := batch[1]()
	if _, ok := sockMsg.(sockDataMsg); !ok {
		t.Fatalf("the second leg must collect sockets, got %T", sockMsg)
	}
	updated, _ = s.Update(sockMsg)
	s, ok = updated.(*network)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if !s.netLoaded || !s.listenersLoaded || !s.connsLoaded {
		t.Fatalf("fixture collection: net=%v ports=%v conns=%v",
			s.netLoaded, s.listenersLoaded, s.connsLoaded)
	}
	if s.netErr != nil || s.listenErr != nil || s.connErr != nil {
		t.Fatalf("fixture collection errors: %v %v %v", s.netErr, s.listenErr, s.connErr)
	}
	return s
}

// ifaceNames lists the interface names of the interfaces view in
// table order.
func ifaceNames(ifaces []core.NetworkInterface) []string {
	out := make([]string, len(ifaces))
	for i, ifi := range ifaces {
		out[i] = ifi.Name
	}
	return out
}

// connStates lists the states of the connections view in table order.
func connStates(conns []core.Connection) []string {
	out := make([]string, len(conns))
	for i, c := range conns {
		out[i] = c.State
	}
	return out
}

func TestNetworkRendersInterfacesView(t *testing.T) {
	s := newNetworkScreen(t, &fakeNetLister{ifaces: netFixture}, &fakeSockLister{listeners: portsFixture, conns: connsFixture})
	view := s.View(100, 24)
	for _, want := range []string{
		"IFACE", "STATE", "ADDRESSES", "RX", "TX", "RX/s", "TX/s",
		"lo", "eth0", "tun0", "down",
		"127.0.0.1,::1", "192.168.1.10,fe80::1",
		"4.8 GiB", "117.7 MiB",
		// First round only baselines the tracker: rates render zero.
		"0.0 B/s",
		"3 interfaces · sort name ↑",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	// The sockets half of the rounds stays out of this view's table.
	for _, banned := range []string{"LISTEN", "systemd", "dnsmasq", "ESTABLISHED"} {
		if strings.Contains(view, banned) {
			t.Errorf("interfaces view must not show socket data %q:\n%s", banned, view)
		}
	}
}

func TestNetworkViewCyclesWithV(t *testing.T) {
	net := &fakeNetLister{ifaces: netFixture}
	socks := &fakeSockLister{listeners: portsFixture, conns: connsFixture}
	s := newNetworkScreen(t, net, socks)
	if net.calls != 1 || socks.listenCalls != 1 || socks.connCalls != 1 {
		t.Fatalf("one round must collect each listing once, got net=%d listen=%d conn=%d",
			net.calls, socks.listenCalls, socks.connCalls)
	}

	// v: interfaces → ports.
	updated, _, handled := s.UpdateKey(keyMsg("v"))
	s = updated.(*network)
	if !handled || s.view != netPorts {
		t.Fatal("'v' must cycle to the ports view")
	}
	view := s.View(100, 24)
	for _, want := range []string{
		"PROTO", "ADDR", "PORT", "PID", "PROCESS",
		"[::]", "22", "dnsmasq", "-",
		"4 ports · sort port ↑",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("ports view missing %q:\n%s", want, view)
		}
	}

	// v: ports → connections.
	updated, _, handled = s.UpdateKey(keyMsg("v"))
	s = updated.(*network)
	if !handled || s.view != netConnections {
		t.Fatal("'v' must cycle to the connections view")
	}
	view = s.View(100, 24)
	for _, want := range []string{
		"PROTO", "LOCAL", "REMOTE", "STATE",
		"192.168.1.10:40010", "142.250.79.46:443",
		"ESTABLISHED", "TIME_WAIT",
		"4 connections · sort state ↑",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("connections view missing %q:\n%s", want, view)
		}
	}

	// v: connections → interfaces, and the full cycle re-collects
	// nothing — every view rides the same two rounds.
	updated, _, handled = s.UpdateKey(keyMsg("v"))
	s = updated.(*network)
	if !handled || s.view != netInterfaces {
		t.Fatal("'v' must cycle back to the interfaces view")
	}
	if !strings.Contains(s.View(100, 24), "3 interfaces · sort name ↑") {
		t.Errorf("cycling back must re-render the interfaces:\n%s", s.View(100, 24))
	}
	if net.calls != 1 || socks.listenCalls != 1 || socks.connCalls != 1 {
		t.Fatalf("cycling views must not re-collect, got net=%d listen=%d conn=%d",
			net.calls, socks.listenCalls, socks.connCalls)
	}

	// Keys outside the screen keymap fall through to the global map
	// (q quits, ? helps, tab cycles).
	for _, k := range []string{"q", "?", "tab"} {
		if _, _, handled := s.UpdateKey(keyMsg(k)); handled {
			t.Errorf("key %q must fall through to the global keymap", k)
		}
	}
}

// TestNetworkTwoChainsReArmIndependently pins the module's two
// cadences (§3: interfaces 2 s, sockets 5 s): each tick re-collects
// and re-arms its OWN half only, and the shell's global RefreshMsg
// drives neither.
func TestNetworkTwoChainsReArmIndependently(t *testing.T) {
	net := &fakeNetLister{ifaces: netFixture}
	socks := &fakeSockLister{listeners: portsFixture, conns: connsFixture}
	s := newNetworkScreen(t, net, socks)

	// The shell's 2 s RefreshMsg drives the dashboard and processes
	// screens; Network runs its own two chains and must ignore it.
	updated, cmd := s.Update(RefreshMsg{})
	s = updated.(*network)
	if cmd != nil {
		t.Error("RefreshMsg must not trigger a network collection")
	}
	if net.calls != 1 || socks.listenCalls != 1 {
		t.Errorf("RefreshMsg must not re-collect: net=%d listen=%d", net.calls, socks.listenCalls)
	}

	// The 2 s chain: one collect leg plus its own re-arm, nothing of
	// the sockets half.
	updated, cmd = s.Update(netTickMsg{})
	s = updated.(*network)
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the net tick must re-arm its own collect and tick, got %T len %d", batch, len(batch))
	}
	updated, _ = s.Update(batch[0]())
	s = updated.(*network)
	if net.calls != 2 || socks.listenCalls != 1 || socks.connCalls != 1 {
		t.Fatalf("the net tick must re-collect interfaces only: net=%d listen=%d conn=%d",
			net.calls, socks.listenCalls, socks.connCalls)
	}

	// The 5 s chain: both socket listings, no interfaces round.
	updated, cmd = s.Update(sockTickMsg{})
	s = updated.(*network)
	batch, ok = cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the sock tick must re-arm its own collect and tick, got %T len %d", batch, len(batch))
	}
	sockMsg := batch[0]()
	if _, ok := sockMsg.(sockDataMsg); !ok {
		t.Fatalf("the sock tick's collect leg must carry a sockDataMsg, got %T", sockMsg)
	}
	s.Update(sockMsg)
	if net.calls != 2 || socks.listenCalls != 2 || socks.connCalls != 2 {
		t.Fatalf("the sock tick must re-collect sockets only: net=%d listen=%d conn=%d",
			net.calls, socks.listenCalls, socks.connCalls)
	}
}

// TestNetworkRatesRefreshThroughTracker pins the rate pipeline: the
// first round baselines the screen's NetTracker, and the next round's
// counters — measured across the rounds' start instants — become
// bytes per second in the listing.
func TestNetworkRatesRefreshThroughTracker(t *testing.T) {
	s := newNetworkScreen(t, &fakeNetLister{ifaces: netFixture}, &fakeSockLister{})

	round2 := slices.Clone(netFixture)
	round2[1].Rx += 100000 // eth0: 50 KiB/s over the 2 s window
	round2[1].Tx += 8192   // eth0: 4 KiB/s over the 2 s window
	updated, _ := s.Update(netDataMsg{at: time.Now().Add(2 * time.Second), ifaces: round2})
	s = updated.(*network)

	for _, ifi := range s.ifaceView {
		if ifi.Name != "eth0" {
			// Unchanged counters keep zero rates.
			if ifi.RxRate != 0 || ifi.TxRate != 0 {
				t.Errorf("%s: unchanged counters must keep zero rates, got rx=%v tx=%v", ifi.Name, ifi.RxRate, ifi.TxRate)
			}
			continue
		}
		// The window is 2 s plus whatever microseconds the test
		// harness spent between the two instants, so the rates sit a
		// hair under the nominal 50000 and 4096.
		if math.Abs(ifi.RxRate-50000) > 500 || math.Abs(ifi.TxRate-4096) > 50 {
			t.Errorf("eth0 rates = rx %v tx %v, want about 50000 and 4096", ifi.RxRate, ifi.TxRate)
		}
	}
	view := s.View(100, 24)
	if !strings.Contains(view, "48.8 KiB/s") || !strings.Contains(view, "4.0 KiB/s") {
		t.Errorf("the refreshed rates must render in the cells:\n%s", view)
	}
	if !strings.Contains(view, "0.0 B/s") {
		t.Errorf("unchanged interfaces must keep their zero-rate cells:\n%s", view)
	}
}

func TestNetworkInterfaceSorting(t *testing.T) {
	s := newNetworkScreen(t, &fakeNetLister{ifaces: netFixture}, &fakeSockLister{})

	// The first round only baselines the tracker, so every rate reads
	// zero and the rate sorts would compare ties. Drive a second
	// round with distinct rates: lo busiest, eth0 middle, tun0 quiet.
	rated := slices.Clone(netFixture)
	rated[0].Rx += 100000
	rated[0].Tx += 8192
	rated[1].Rx += 50000
	rated[1].Tx += 4000
	updated, _ := s.Update(netDataMsg{at: time.Now().Add(2 * time.Second), ifaces: rated})
	s = updated.(*network)

	cases := []struct {
		name string
		key  string
		want []string
	}{
		{"name asc is the default", "", []string{"eth0", "lo", "tun0"}},
		{"r sorts by rx/s, busiest first", "r", []string{"lo", "eth0", "tun0"}},
		{"r again flips to quietest first", "r", []string{"tun0", "eth0", "lo"}},
		{"t sorts by tx/s, busiest first", "t", []string{"lo", "eth0", "tun0"}},
		{"n returns to the name sort", "n", []string{"eth0", "lo", "tun0"}},
	}

	for idx, tc := range cases {
		if tc.key != "" {
			updated, _, handled := s.UpdateKey(keyMsg(tc.key))
			if !handled {
				t.Fatalf("sort key %q not handled", tc.key)
			}
			s = updated.(*network)
		} else if idx > 0 {
			t.Fatal("table driven misuse: default case must be first")
		}
		if got := ifaceNames(s.ifaceView); !slices.Equal(got, tc.want) {
			t.Errorf("%s: order = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNetworkInterfaceFilter(t *testing.T) {
	s := newNetworkScreen(t, &fakeNetLister{ifaces: netFixture}, &fakeSockLister{})

	if _, _, handled := s.UpdateKey(keyMsg("/")); !handled || !s.filtering {
		t.Fatal("filter mode did not open")
	}
	// "fe80" matches an address, proving the query spans name and
	// addresses.
	for _, r := range "fe80" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if got := ifaceNames(s.ifaceView); !slices.Equal(got, []string{"eth0"}) {
		t.Fatalf("filter 'fe80': %v", got)
	}
	if !strings.Contains(s.View(100, 24), `filter "fe80"`) {
		t.Error("status line must show the active filter")
	}

	// esc closes the input but keeps the applied filter (storage's
	// behavior).
	if _, _, handled := s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled || s.filtering {
		t.Fatal("esc must close filter mode")
	}
	if got := ifaceNames(s.ifaceView); !slices.Equal(got, []string{"eth0"}) {
		t.Fatalf("closed filter must keep rows applied: %v", got)
	}

	// A no-match query empties the view — and the cursor parks at -1
	// without a panic on the next reapply (m5a's discovery).
	s.UpdateKey(keyMsg("/"))
	s.filter.SetValue("")
	for _, r := range "zzz" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if len(s.ifaceView) != 0 {
		t.Fatalf("no-match filter must empty the view: %v", ifaceNames(s.ifaceView))
	}
	s.reapply() // must not panic on the empty table
}

func TestNetworkPortsSortAndFilter(t *testing.T) {
	s := newNetworkScreen(t, &fakeNetLister{ifaces: netFixture}, &fakeSockLister{listeners: portsFixture, conns: connsFixture})
	s.UpdateKey(keyMsg("v"))

	// Default port order asc; the 22/22 pair ties onto the endpoint.
	want := []int{22, 22, 53, 8080}
	got := make([]int, len(s.portView))
	for i, p := range s.portView {
		got[i] = int(p.Port)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ports default order = %v, want %v", got, want)
	}

	// p flips the single ports sort column.
	if _, _, handled := s.UpdateKey(keyMsg("p")); !handled {
		t.Fatal("'p' must be handled in the ports view")
	}
	slices.Reverse(got)
	for i, p := range s.portView {
		if got[i] != int(p.Port) {
			t.Fatalf("after p = %v, want %v", s.portView, got)
		}
	}

	// The filter spans proto, addr and process: "dnsmasq" hits a
	// process only.
	s.UpdateKey(keyMsg("/"))
	for _, r := range "dnsmasq" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if len(s.portView) != 1 || s.portView[0].Port != 53 {
		t.Fatalf("filter 'dnsmasq': %+v", s.portView)
	}
	if !strings.Contains(s.View(100, 24), `filter "dnsmasq"`) {
		t.Error("status line must show the active filter in the ports view")
	}
}

func TestNetworkConnectionsSortAndFilter(t *testing.T) {
	s := newNetworkScreen(t, &fakeNetLister{ifaces: netFixture}, &fakeSockLister{listeners: portsFixture, conns: connsFixture})
	s.UpdateKey(keyMsg("v"))
	s.UpdateKey(keyMsg("v"))

	// The default state sort is the attention ladder: live
	// conversations first (ESTABLISHED before CONNECTED by the
	// local-endpoint tie-break), then the closing family, then the
	// TIME_WAIT backlog.
	ladder := []string{"ESTABLISHED", "CONNECTED", "CLOSE_WAIT", "TIME_WAIT"}
	if got := connStates(s.connView); !slices.Equal(got, ladder) {
		t.Fatalf("state ladder = %v, want %v", got, ladder)
	}

	// s flips the whole ladder.
	if _, _, handled := s.UpdateKey(keyMsg("s")); !handled {
		t.Fatal("'s' must be handled in the connections view")
	}
	slices.Reverse(ladder)
	if got := connStates(s.connView); !slices.Equal(got, ladder) {
		t.Fatalf("flipped ladder = %v, want %v", got, ladder)
	}

	// l orders by local endpoint instead.
	if _, _, handled := s.UpdateKey(keyMsg("l")); !handled {
		t.Fatal("'l' must be handled in the connections view")
	}
	localKeys := make([]string, len(s.connView))
	for i, c := range s.connView {
		localKeys[i] = sockKey(c.Proto, c.LocalAddr, int(c.LocalPort))
	}
	if !slices.IsSorted(localKeys) {
		t.Fatalf("local sort must be ascending: %v", localKeys)
	}

	// The filter spans proto, both endpoints and state: "443" hits a
	// remote endpoint, "estab" a state.
	s.UpdateKey(keyMsg("/"))
	for _, r := range "443" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if len(s.connView) != 1 || s.connView[0].State != "ESTABLISHED" {
		t.Fatalf("filter '443': %+v", s.connView)
	}
}

// TestNetworkErrorAndCollectingStates pins the three status-line
// states per view: the error of the ACTIVE view's half — one failed
// half never blanks the other — the collecting state until the
// first round lands, and the listing summary.
func TestNetworkErrorAndCollectingStates(t *testing.T) {
	// Before any round: collecting.
	fresh := newNetwork(&fakeNetLister{}, &fakeSockLister{}, theme.Dark())
	if out := fresh.View(100, 24); !strings.Contains(out, "collecting...") {
		t.Errorf("fresh screen must show collecting..., got:\n%s", out)
	}

	// A round with a failed interfaces half and healthy sockets.
	net := &fakeNetLister{err: errors.New("no netlink")}
	socks := &fakeSockLister{listeners: portsFixture, conns: connsFixture}
	s := newNetwork(net, socks, theme.Dark())
	cmd := s.Init()
	batch := cmd().(tea.BatchMsg)
	updated, _ := s.Update(batch[0]())
	s = updated.(*network)
	if s.netLoaded || s.netErr == nil {
		t.Fatalf("interfaces round state: loaded=%v err=%v", s.netLoaded, s.netErr)
	}
	if out := s.View(100, 24); !strings.Contains(out, "n/a (no netlink)") {
		t.Errorf("interfaces view must show its collection error:\n%s", out)
	}
	// The sockets half of the same screen collects fine and renders
	// normally — the ports view never inherits the interfaces error.
	updated, _ = s.Update(batch[1]())
	s = updated.(*network)
	s.UpdateKey(keyMsg("v")) // ports
	if out := s.View(100, 24); !strings.Contains(out, "4 ports · sort port ↑") || strings.Contains(out, "n/a") {
		t.Errorf("the ports view must stay healthy despite the interfaces error:\n%s", out)
	}
	// The interfaces error persists until a successful interfaces
	// round clears it (the storageDataMsg per-half policy).
	s.UpdateKey(keyMsg("v")) // connections
	s.UpdateKey(keyMsg("v")) // back to interfaces
	if out := s.View(100, 24); !strings.Contains(out, "n/a (no netlink)") {
		t.Errorf("the interfaces error must persist until its own round succeeds:\n%s", out)
	}
	updated, _ = s.Update(netDataMsg{at: time.Now(), ifaces: netFixture})
	s = updated.(*network)
	if out := s.View(100, 24); strings.Contains(out, "n/a") || !strings.Contains(out, "3 interfaces · sort name ↑") {
		t.Errorf("a successful interfaces round must clear the error:\n%s", out)
	}

	// A failed listeners half: the ports view errors, the connections
	// view of the same round stays healthy.
	socks = &fakeSockLister{listenErr: errors.New("no proc net tcp"), conns: connsFixture}
	s = newNetwork(&fakeNetLister{ifaces: netFixture}, socks, theme.Dark())
	cmd = s.Init()
	batch = cmd().(tea.BatchMsg)
	updated, _ = s.Update(batch[1]())
	s = updated.(*network)
	s.UpdateKey(keyMsg("v")) // ports
	if out := s.View(100, 24); !strings.Contains(out, "n/a (no proc net tcp)") {
		t.Errorf("ports view must show its collection error:\n%s", out)
	}
	s.UpdateKey(keyMsg("v")) // connections
	if out := s.View(100, 24); strings.Contains(out, "n/a") || !strings.Contains(out, "connections · sort state") {
		t.Errorf("the connections view must not inherit the listeners error:\n%s", out)
	}

	// An empty round renders as a zero-row listing, not an error.
	empty := newNetworkScreen(t, &fakeNetLister{}, &fakeSockLister{})
	if out := empty.View(100, 24); !strings.Contains(out, "0 interfaces") {
		t.Errorf("empty round must show the zero-row summary:\n%s", out)
	}
}

func TestNetworkCursorKeptOnRefresh(t *testing.T) {
	s := newNetworkScreen(t, &fakeNetLister{ifaces: netFixture}, &fakeSockLister{})
	// Default name order: eth0, lo, tun0. Move to lo.
	if _, _, handled := s.UpdateKey(keyMsg("down")); !handled {
		t.Fatal("down must be handled")
	}
	if got := s.ifaceView[s.table.Cursor()].Name; got != "lo" {
		t.Fatalf("cursor on %q, want lo", got)
	}

	// A refreshed round bumps lo's counters and the cursor must stay
	// on it even though the rows re-derive.
	refreshed := slices.Clone(netFixture)
	refreshed[0].Rx += 1 << 20
	updated, _ := s.Update(netDataMsg{at: time.Now().Add(2 * time.Second), ifaces: refreshed})
	s = updated.(*network)
	if got := s.ifaceView[s.table.Cursor()].Name; got != "lo" {
		t.Fatalf("cursor drifted to %q after refresh, want lo", got)
	}
}
