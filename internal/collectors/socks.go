// socks.go collects the Network module's socket listings: the
// listening ports and the live connections of
// /proc/net/{tcp,tcp6,udp,udp6} (ARCHITECTURE.md §8). Everything here
// is a file read under the root prefix, so both listings are fully
// hermetic against golden fixtures — no live guard, unlike the
// netlink half of net.go.

package collectors

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// Compile-time proof that System satisfies the Network socket port.
var _ core.SocketLister = System{}

// SockRow is one socket row of /proc/net/{tcp,tcp6,udp,udp6}: the
// decoded local and remote endpoints, the translated state name and
// the socket inode that joins the row to its owning process. It is
// the parser's intermediate shape — core.ListenPort and
// core.Connection are built from it once Listeners and Connections
// have decided which side of their split the row belongs to.
type SockRow struct {
	Proto      string // the family the row was read as: tcp, tcp6, udp, udp6
	LocalAddr  string
	LocalPort  uint16
	RemoteAddr string
	RemotePort uint16
	State      string // translated name: LISTEN, ESTABLISHED, UNCONN, ...
	Inode      uint64
}

// tcpStates maps the hex state codes of /proc/net/{tcp,tcp6} to the
// names netstat and ss print (include/net/tcp_states.h with the
// TCP_ prefix dropped): a TCP socket is a full state machine and
// every code carries its name.
var tcpStates = map[string]string{
	"01": "ESTABLISHED",
	"02": "SYN_SENT",
	"03": "SYN_RECV",
	"04": "FIN_WAIT1",
	"05": "FIN_WAIT2",
	"06": "TIME_WAIT",
	"07": "CLOSE",
	"08": "CLOSE_WAIT",
	"09": "LAST_ACK",
	"0A": "LISTEN",
	"0B": "CLOSING",
}

// udpStates maps the only two states a UDP socket can carry. UDP has
// no connection state machine — the kernel's sk_state is either
// CLOSE (07, no peer pinned by connect(2)) or ESTABLISHED (01, a peer
// is pinned) — and ss prints them UNCONN and ESTAB. The connected
// label here is CONNECTED, the name core.Connection documents for
// the connections view; UNCONN rows are listener material, where the
// label never renders.
var udpStates = map[string]string{
	"07": "UNCONN",
	"01": "CONNECTED",
}

// ParseNetSockets parses one /proc/net/{tcp,tcp6,udp,udp6} file into
// socket rows. proto names the family ("tcp", "tcp6", "udp", "udp6",
// ss's netid spelling) and drives both the address width and the
// state translation. The columns are the hexadecimal ones proc(5)
// documents and the kernel's {tcp,udp}{,6} seq_printf printers write:
// sl, local and remote "address:port", st, the queue/timer triple,
// retrnsmt, uid, timeout and inode — field 10 — with everything after
// the inode left unread (the queues and timers belong to a per-socket
// detail view, not this listing).
//
// The address columns carry the classic procfs encoding quirk,
// verified against a live kernel for both families: an IPv4 address
// is one u32 and an IPv6 address is four u32 groups, each printed
// with %08X — so a group's hex digits read its bytes in REVERSE
// order. 127.0.0.1 prints as 0100007F; ::1 prints as
// 00000000000000000000000001000000, whose last group is 01000000,
// not 00000001. decodeProcAddr undoes exactly that: each group is
// parsed as a big-endian-encoded u32 and decomposed into bytes
// little-endian.
//
// The file is kernel-generated, so a data row that cannot carry the
// ten fields — a short row, a non-hex address or port, a wrong-width
// family, a non-numeric inode — errors the whole parse (the
// ParseStat policy). The header line (whose first field "sl" carries
// no row index) and blank lines (the trailing newline) are skipped.
func ParseNetSockets(proto, content string) ([]SockRow, error) {
	v6 := strings.HasSuffix(proto, "6")
	states := tcpStates
	if strings.HasPrefix(proto, "udp") {
		states = udpStates
	}

	var out []SockRow
	for lineno, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue // blank line: the file's trailing newline
		}
		// Data rows open with the sl row index in "N:" form; the
		// header opens with the bare column name "sl".
		if !strings.HasSuffix(fields[0], ":") {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSuffix(fields[0], ":")); err != nil {
			return nil, fmt.Errorf("malformed %s line %d (%q): %q is not a row index", proto, lineno+1, line, fields[0])
		}
		if len(fields) < 10 {
			return nil, fmt.Errorf("malformed %s line %d (%q): %d fields, want at least 10", proto, lineno+1, line, len(fields))
		}
		localAddr, localPort, err := parseSockEndpoint(fields[1], v6)
		if err != nil {
			return nil, fmt.Errorf("%s line %d local endpoint: %w", proto, lineno+1, err)
		}
		remoteAddr, remotePort, err := parseSockEndpoint(fields[2], v6)
		if err != nil {
			return nil, fmt.Errorf("%s line %d remote endpoint: %w", proto, lineno+1, err)
		}
		state, ok := states[strings.ToUpper(fields[3])]
		if !ok {
			// A code outside the map — 00 on a socket being born —
			// keeps its raw hex: untranslatable, but not invisible.
			state = strings.ToUpper(fields[3])
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s line %d inode %q: %w", proto, lineno+1, fields[9], err)
		}
		out = append(out, SockRow{
			Proto:      proto,
			LocalAddr:  localAddr,
			LocalPort:  localPort,
			RemoteAddr: remoteAddr,
			RemotePort: remotePort,
			State:      state,
			Inode:      inode,
		})
	}
	return out, nil
}

// parseSockEndpoint splits one "address:port" endpoint column into the
// decoded IP string and port, undoing the procfs quirk documented on
// ParseNetSockets.
func parseSockEndpoint(col string, v6 bool) (string, uint16, error) {
	colon := strings.LastIndex(col, ":")
	if colon < 0 {
		return "", 0, fmt.Errorf("%q: no port", col)
	}
	addrHex, portHex := col[:colon], col[colon+1:]
	if addrHex == "" || portHex == "" {
		return "", 0, fmt.Errorf("%q: empty address or port", col)
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return "", 0, fmt.Errorf("port %q: %w", portHex, err)
	}
	addr, err := decodeProcAddr(addrHex, v6)
	if err != nil {
		return "", 0, err
	}
	return addr, uint16(port), nil
}

// decodeProcAddr turns one procfs address column into the bare IP
// string. v4 columns are 8 hex digits (one u32), v6 columns 32 (four
// u32 groups); per the quirk each group is a big-endian-encoded u32
// whose little-endian decomposition is the address byte order —
// 0100007F reads back as the bytes 7F 00 00 01.
func decodeProcAddr(hex string, v6 bool) (string, error) {
	if v6 {
		if len(hex) != 32 {
			return "", fmt.Errorf("v6 address %q: want 32 hex digits, got %d", hex, len(hex))
		}
		var ip [16]byte
		for g := range 4 {
			group, err := strconv.ParseUint(hex[g*8:(g+1)*8], 16, 32)
			if err != nil {
				return "", fmt.Errorf("v6 address %q: %w", hex, err)
			}
			for i := range 4 {
				ip[g*4+i] = byte(group >> (8 * i))
			}
		}
		return net.IP(ip[:]).String(), nil
	}
	if len(hex) != 8 {
		return "", fmt.Errorf("v4 address %q: want 8 hex digits, got %d", hex, len(hex))
	}
	u, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return "", fmt.Errorf("v4 address %q: %w", hex, err)
	}
	var ip [4]byte
	for i := range 4 {
		ip[i] = byte(u >> (8 * i))
	}
	return net.IP(ip[:]).String(), nil
}

// isListener reports which side of the listings' split a row belongs
// to on the listener side: TCP rows listen in state LISTEN, and UDP
// rows are listeners when the kernel marks them unconnected (UNCONN)
// with an empty remote endpoint — a peer-less socket is the
// bind-and-wait shape; a connected UDP socket belongs to Connections.
func (r SockRow) isListener() bool {
	if strings.HasPrefix(r.Proto, "tcp") {
		return r.State == "LISTEN"
	}
	return r.State == "UNCONN" && r.RemotePort == 0
}

// isConnection is the other side of the split: every TCP socket that
// is not a listener — the established and closing states, TIME_WAIT
// included the way `ss -t` lists it — and the connected UDP sockets.
// A row in neither list (a hypothetical UNCONN UDP socket with a
// remote endpoint) cannot occur on a live kernel.
func (r SockRow) isConnection() bool {
	if strings.HasPrefix(r.Proto, "tcp") {
		return r.State != "LISTEN"
	}
	return r.State == "CONNECTED"
}

// sockFamilies is the /proc/net family the socket port walks, in
// listing order: TCP before UDP, IPv4 before IPv6, so a golden
// fixture reads in the order the collector lists.
var sockFamilies = []struct{ file, proto string }{
	{"tcp", "tcp"},
	{"tcp6", "tcp6"},
	{"udp", "udp"},
	{"udp6", "udp6"},
}

// socketRows reads and parses the whole /proc/net socket family
// through the root prefix. A missing file skips its family:
// /proc/net/tcp6 and udp6 are absent with IPv6 disabled at boot, and
// a listing without that family is still worth showing (the Sensors
// policy — only a masked procfs or a fixture can lack any of them).
// A file that is present but corrupt fails the walk.
func (s System) socketRows() ([]SockRow, error) {
	var out []SockRow
	for _, fam := range sockFamilies {
		raw, err := os.ReadFile(s.proc("net", fam.file))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read net/%s: %w", fam.file, err)
		}
		rows, err := ParseNetSockets(fam.proto, string(raw))
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// Listeners lists the sockets bound and waiting for peers: TCP
// sockets in LISTEN state and unconnected UDP sockets, one row per
// (proto, address, port). Duplicate rows are real — verified against
// a live kernel, where two SO_REUSEPORT listeners on one endpoint
// produce two identical 0A rows in /proc/net/tcp — so they collapse
// onto the first row in kernel listing order rather than stacking
// visually identical rows (`ss -tlnH` stacks them; one row per
// endpoint reads better in a table).
//
// PID and Process are best-effort, resolved by walking every
// readable <root>/proc/<pid>/fd readlink for the socket's inode —
// the same walk `ss -p` makes, on the module's 5 s cadence. An
// unprivileged reader only sees the descriptors of processes it can
// own, so sockets of other users' processes or of the kernel read as
// PID 0 and Process "" instead of vanishing (core.ListenPort). An
// inode several processes share (fork inheritance) resolves to the
// lowest PID holding it.
func (s System) Listeners() ([]core.ListenPort, error) {
	rows, err := s.socketRows()
	if err != nil {
		return nil, err
	}
	owners := s.socketOwners()
	comm := make(map[int]string)

	var out []core.ListenPort
	seen := make(map[core.ListenPort]bool)
	for _, row := range rows {
		if !row.isListener() {
			continue
		}
		port := core.ListenPort{Proto: row.Proto, Addr: row.LocalAddr, Port: row.LocalPort}
		if seen[port] {
			continue // SO_REUSEPORT twin: one row per endpoint
		}
		seen[port] = true
		if pid, ok := owners[row.Inode]; ok {
			port.PID = pid
			port.Process = s.commOf(pid, comm)
		}
		out = append(out, port)
	}
	return out, nil
}

// Connections lists the socket pairs in flight: every TCP socket that
// is not a listener, plus the connected UDP sockets, whose State
// reads CONNECTED (core.Connection). No PID is joined here — the
// connections view lists traffic, and a TIME_WAIT row owns no
// process anyway.
func (s System) Connections() ([]core.Connection, error) {
	rows, err := s.socketRows()
	if err != nil {
		return nil, err
	}
	out := make([]core.Connection, 0, len(rows))
	for _, row := range rows {
		if !row.isConnection() {
			continue
		}
		out = append(out, core.Connection{
			Proto:      row.Proto,
			LocalAddr:  row.LocalAddr,
			LocalPort:  row.LocalPort,
			RemoteAddr: row.RemoteAddr,
			RemotePort: row.RemotePort,
			State:      row.State,
		})
	}
	return out, nil
}

// socketOwners walks <root>/proc/<pid>/fd readlinks and maps every
// socket inode to the lowest PID holding it: a socket's only /proc
// identity is the "socket:[inode]" symlink target in its owner's fd
// directory. Read errors — another user's /proc/<pid>/fd (the
// unprivileged reality), a process that died mid-walk, a pid without
// an fd directory — skip that directory, never the walk. PIDs walk in
// ascending order so the first claim of a shared inode is already the
// lowest, making the mapping deterministic across runs.
func (s System) socketOwners() map[uint64]int {
	owners := make(map[uint64]int)
	entries, err := os.ReadDir(s.proc())
	if err != nil {
		return owners // no /proc to walk: no owners to find
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if pid, err := strconv.Atoi(entry.Name()); err == nil {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)

	for _, pid := range pids {
		fdDir := s.proc(strconv.Itoa(pid), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if inode, ok := parseSocketLink(target); ok {
				if _, claimed := owners[inode]; !claimed {
					owners[inode] = pid
				}
			}
		}
	}
	return owners
}

// parseSocketLink extracts the inode from one "socket:[N]" fd symlink
// target; anything else — a pipe, a file, a readlink that raced a
// dying process — reports false.
func parseSocketLink(target string) (uint64, bool) {
	const prefix = "socket:["
	if !strings.HasPrefix(target, prefix) || !strings.HasSuffix(target, "]") {
		return 0, false
	}
	inode, err := strconv.ParseUint(target[len(prefix):len(target)-1], 10, 64)
	if err != nil {
		return 0, false
	}
	return inode, true
}

// commOf resolves a PID to its process name — the comm field of
// /proc/<pid>/stat through the same ParseStat readProcess uses —
// through cache, so a daemon owning several listeners reads its stat
// once per round. An unreadable or malformed stat (the process died
// mid-walk) reads as "", the unprivileged best-effort shape.
func (s System) commOf(pid int, cache map[int]string) string {
	if name, ok := cache[pid]; ok {
		return name
	}
	name := ""
	if raw, err := os.ReadFile(s.proc(strconv.Itoa(pid), "stat")); err == nil {
		if stat, err := ParseStat(string(raw)); err == nil {
			name = stat.Name
		}
	}
	cache[pid] = name
	return name
}
