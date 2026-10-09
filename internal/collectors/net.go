// net.go collects the Network module's interface listing: traffic
// counters from /proc/net/dev joined with the standard library's
// netlink walker for names, addresses and flags (ARCHITECTURE.md §8).
// Everything here is stdlib net — §9's dependency list gains nothing.

package collectors

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// Compile-time proof that System satisfies the Network read port.
var _ core.NetLister = System{}

// NetDevCounters holds the traffic counters of one /proc/net/dev row
// serverctl consumes: cumulative receive and transmit bytes since
// boot. The packet, error and drop columns of the same row stay
// unparsed — the Network screen lists bytes, and a column nobody
// displays is better left unread than half-read.
type NetDevCounters struct {
	Rx uint64
	Tx uint64
}

// ParseNetDev parses /proc/net/dev content into per-interface byte
// counters. The file is two header lines ("Inter-|   Receive ..." and
// " face |bytes ...") followed by one row per interface; a row is
// "name:" and then the sixteen statistic columns, receive bytes first
// and transmit bytes ninth — fields 1 and 9 after the colon, the
// layout net/core/net-procfs.c's dev_seq_printf_stats writes. A colon
// can never appear inside an interface name (dev_valid_name rejects
// it), so splitting at the first colon isolates the name. The file is
// kernel-generated, so a row that cannot carry the two byte counters
// — fewer than 16 fields, a non-numeric counter or an empty name —
// errors the whole parse (the ParseStat policy), while header lines
// (no colon) and blank lines (the trailing newline) are skipped.
func ParseNetDev(content string) (map[string]NetDevCounters, error) {
	out := make(map[string]NetDevCounters)
	for lineno, line := range strings.Split(content, "\n") {
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue // header or blank line: not an interface row
		}
		name := strings.TrimSpace(line[:colon])
		fields := strings.Fields(line[colon+1:])
		if name == "" || len(fields) < 16 {
			return nil, fmt.Errorf("malformed net/dev line %d (%q): %d fields after colon, want at least 16", lineno+1, line, len(fields))
		}
		rx, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("net/dev rx bytes for %s: %w", name, err)
		}
		tx, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("net/dev tx bytes for %s: %w", name, err)
		}
		out[name] = NetDevCounters{Rx: rx, Tx: tx}
	}
	return out, nil
}

// Interfaces lists the host's network interfaces with their traffic
// counters (ARCHITECTURE.md §8): the byte counters come from
// /proc/net/dev through the root prefix, and the name, hardware
// address, flags and addresses come from the standard library's
// net.Interfaces — which walks netlink, a socket API with no root
// prefix, so a fixture System refuses rather than measuring the test
// runner's own interfaces (the Units()/Tail() live guard; ParseNetDev
// and NetTracker stay fixture-testable without it). Addresses list
// every configured family and scope as bare IP strings — the screen
// joins them for display — and an interface whose address enumeration
// fails keeps its other columns: an address disappearing mid-read is
// churn, not a reason to drop the row. RxRate/TxRate stay zero here:
// a rate only exists between two listings and is the consumer's
// NetTracker math.
func (s System) Interfaces() ([]core.NetworkInterface, error) {
	if !s.IsLive() {
		return nil, fmt.Errorf("collectors: interface enumeration requires the live kernel interfaces: System is rooted at %q", s.root)
	}
	raw, err := os.ReadFile(s.proc("net", "dev"))
	if err != nil {
		return nil, fmt.Errorf("read net/dev: %w", err)
	}
	counters, err := ParseNetDev(string(raw))
	if err != nil {
		return nil, err
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("collectors: enumerate interfaces: %w", err)
	}

	out := make([]core.NetworkInterface, 0, len(ifaces))
	for _, ifi := range ifaces {
		iface := core.NetworkInterface{
			Name: ifi.Name,
			Up:   ifi.Flags&net.FlagUp != 0,
		}
		if len(ifi.HardwareAddr) > 0 {
			// net.HardwareAddr renders the aa:bb:cc:dd:ee:ff form
			// itself; loopback and tunnels carry no address.
			iface.MAC = ifi.HardwareAddr.String()
		}
		if addrs, err := ifi.Addrs(); err == nil {
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok {
					iface.Addrs = append(iface.Addrs, ipnet.IP.String())
				}
			}
		}
		iface.Rx, iface.Tx = counters[ifi.Name].Rx, counters[ifi.Name].Tx
		out = append(out, iface)
	}
	return out, nil
}

// NetTracker turns cumulative /proc/net/dev byte counters into live
// bytes-per-second rates between two interface listings, mirroring
// CPUTracker and ProcessTracker: the kernel exposes counters, and a
// rate only exists across two reads. The zero value is ready to use,
// and the first Refresh establishes the baseline and reports zero
// rates — by design, not by failure. The tracker lives in the
// consuming screen; collectors stay pure stateless reads.
type NetTracker struct {
	prev    map[string]NetDevCounters
	started bool
}

// Refresh consumes one interface listing plus the time elapsed since
// the previous Refresh and returns a copy with RxRate/TxRate filled
// in as bytes per second. A zero-or-negative elapsed window — the
// first round, or a caller without a clock yet — only establishes
// the baseline and reports zero rates. An interface unseen in the
// previous listing starts its own baseline (rate 0 this window), an
// interface whose counters went backwards — a down/up resets
// /proc/net/dev to zero — reports 0 for the window rather than a
// negative rate (both directions: a pair where one counter reset and
// the other did not is incoherent), and interfaces missing from the
// current listing leave the tracker state so the map cannot leak (the
// ProcessTracker policy).
func (t *NetTracker) Refresh(current []core.NetworkInterface, elapsed time.Duration) []core.NetworkInterface {
	out := make([]core.NetworkInterface, len(current))
	copy(out, current)

	if !t.started || elapsed <= 0 {
		t.prev = netCountersSnapshot(out)
		t.started = true
		return out
	}

	seconds := elapsed.Seconds()
	for i := range out {
		prev, ok := t.prev[out[i].Name]
		if !ok || out[i].Rx < prev.Rx || out[i].Tx < prev.Tx {
			continue // new name, or counters reset: baseline only
		}
		out[i].RxRate = float64(out[i].Rx-prev.Rx) / seconds
		out[i].TxRate = float64(out[i].Tx-prev.Tx) / seconds
	}
	t.prev = netCountersSnapshot(out)
	return out
}

// netCountersSnapshot captures the per-name byte counters of one listing.
func netCountersSnapshot(ifaces []core.NetworkInterface) map[string]NetDevCounters {
	snap := make(map[string]NetDevCounters, len(ifaces))
	for _, ifi := range ifaces {
		snap[ifi.Name] = NetDevCounters{Rx: ifi.Rx, Tx: ifi.Tx}
	}
	return snap
}
