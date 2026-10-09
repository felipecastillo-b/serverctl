package collectors_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
)

// sockHeader is the shared column header every /proc/net socket file
// opens with; the parser must skip it in every family.
const sockHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

// sockRow renders one /proc/net/tcp-shaped data row for the parser
// tests, keeping the field positions the kernel writes.
func sockRow(sl, local, remote, st, inode string) string {
	return "  " + sl + ": " + local + " " + remote + " " + st +
		" 00000000:00000000 00:00000000 00000000     0        0 " + inode + " 1 00000000deadbeef 100 0 0 10 0\n"
}

// TestParseNetSocketsV4AddressQuirk pins the classic procfs encoding:
// the v4 address column is one big-endian-encoded u32 whose bytes read
// in reverse. 0100007F is 127.0.0.1 and 8492A8C0 is 192.168.146.132
// (both verified against a live /proc/net/tcp); ports decode as plain
// hex u16 (1F90 = 8080, 01BB = 443).
func TestParseNetSocketsV4AddressQuirk(t *testing.T) {
	content := sockHeader +
		sockRow("0", "0100007F:1F90", "8492A8C0:01BB", "01", "48564")
	rows, err := collectors.ParseNetSockets("tcp", content)
	if err != nil {
		t.Fatalf("ParseNetSockets: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.LocalAddr != "127.0.0.1" || row.LocalPort != 8080 {
		t.Errorf("local = %s:%d, want 127.0.0.1:8080", row.LocalAddr, row.LocalPort)
	}
	if row.RemoteAddr != "192.168.146.132" || row.RemotePort != 443 {
		t.Errorf("remote = %s:%d, want 192.168.146.132:443", row.RemoteAddr, row.RemotePort)
	}
	if row.State != "ESTABLISHED" || row.Inode != 48564 {
		t.Errorf("state/inode = %s/%d, want ESTABLISHED/48564", row.State, row.Inode)
	}
}

// TestParseNetSocketsV6AddressQuirk pins the v6 half of the encoding:
// 32 hex digits as four u32 groups, each group's bytes reversed.
// ::1 prints with last group 01000000 (verified against a live
// /proc/net/tcp6 listener on ::1) and 2001:db8::1 prints as
// B80D0120...01000000.
func TestParseNetSocketsV6AddressQuirk(t *testing.T) {
	content := "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		sockRow("0", "00000000000000000000000001000000:0016", "B80D0120000000000000000001000000:0050", "01", "34567")
	rows, err := collectors.ParseNetSockets("tcp6", content)
	if err != nil {
		t.Fatalf("ParseNetSockets: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.LocalAddr != "::1" || row.LocalPort != 22 {
		t.Errorf("local = %s:%d, want [::1]:22", row.LocalAddr, row.LocalPort)
	}
	if row.RemoteAddr != "2001:db8::1" || row.RemotePort != 80 {
		t.Errorf("remote = %s:%d, want 2001:db8::1:80", row.RemoteAddr, row.RemotePort)
	}
	if row.Proto != "tcp6" {
		t.Errorf("proto = %q, want tcp6", row.Proto)
	}
}

// TestParseNetSocketsStateTranslation walks the full TCP state map
// plus the two UDP labels, and pins the raw-hex fallback for a code
// the map does not know.
func TestParseNetSocketsStateTranslation(t *testing.T) {
	tests := []struct {
		proto  string
		code   string
		local  string
		remote string
		want   string
	}{
		{"tcp", "01", "00000000:0000", "00000000:0000", "ESTABLISHED"},
		{"tcp", "02", "00000000:0000", "00000000:0000", "SYN_SENT"},
		{"tcp", "03", "00000000:0000", "00000000:0000", "SYN_RECV"},
		{"tcp", "04", "00000000:0000", "00000000:0000", "FIN_WAIT1"},
		{"tcp", "05", "00000000:0000", "00000000:0000", "FIN_WAIT2"},
		{"tcp", "06", "00000000:0000", "00000000:0000", "TIME_WAIT"},
		{"tcp", "07", "00000000:0000", "00000000:0000", "CLOSE"},
		{"tcp", "08", "00000000:0000", "00000000:0000", "CLOSE_WAIT"},
		{"tcp", "09", "00000000:0000", "00000000:0000", "LAST_ACK"},
		{"tcp", "0A", "00000000:0000", "00000000:0000", "LISTEN"},
		{"tcp", "0B", "00000000:0000", "00000000:0000", "CLOSING"},
		{"tcp", "0C", "00000000:0000", "00000000:0000", "0C"}, // unmapped: raw hex survives
		{"udp", "07", "00000000:0000", "00000000:0000", "UNCONN"},
		{"udp", "01", "00000000:0000", "00000000:0000", "CONNECTED"},
		// v6 families decode 32-digit addresses and translate like
		// their v4 siblings.
		{"udp6", "07", "00000000000000000000000000000000:0000", "00000000000000000000000000000000:0000", "UNCONN"},
		{"tcp6", "0A", "00000000000000000000000000000000:0016", "00000000000000000000000000000000:0000", "LISTEN"},
	}
	for _, tt := range tests {
		t.Run(tt.proto+" "+tt.code, func(t *testing.T) {
			rows, err := collectors.ParseNetSockets(tt.proto, sockHeader+sockRow("0", tt.local, tt.remote, tt.code, "1"))
			if err != nil {
				t.Fatalf("ParseNetSockets: %v", err)
			}
			if rows[0].State != tt.want {
				t.Errorf("state %q translated to %q, want %q", tt.code, rows[0].State, tt.want)
			}
		})
	}
}

// TestParseNetSocketsMalformedRows errors on every shape the kernel
// never writes: a short data row, a non-hex port, a v6 address with
// the wrong digit count, a non-numeric inode, and a first field that
// carries a colon but not a row index.
func TestParseNetSocketsMalformedRows(t *testing.T) {
	bad := []struct{ name, proto, content string }{
		{"short row", "tcp", sockHeader + "   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000\n"},
		{"non-hex port", "tcp", sockHeader + sockRow("0", "0100007F:ZZZZ", "00000000:0000", "0A", "1")},
		{"wrong v6 width", "tcp6", sockHeader + sockRow("0", "0000000000000000:0016", "0000000000000000:0000", "0A", "1")},
		{"non-numeric inode", "tcp", sockHeader + sockRow("0", "0100007F:1F90", "00000000:0000", "0A", "x")},
		{"index not numeric", "tcp", sockHeader + "  x: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 1 1 f 100 0 0 10 0\n"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := collectors.ParseNetSockets(tt.proto, tt.content); err == nil {
				t.Fatal("the malformed row must error the parse")
			}
		})
	}

	// Blank lines — the trailing newline — are tolerated.
	if rows, err := collectors.ParseNetSockets("tcp", sockHeader+sockRow("0", "00000000:0016", "00000000:0000", "0A", "1")+"\n"); err != nil || len(rows) != 1 {
		t.Fatalf("trailing blank line must be tolerated: %v, %d rows", err, len(rows))
	}
}

// TestListenersOnFixture walks the golden /proc/net family end to
// end: the listener split (TCP LISTEN + unconnected UDP), the inode
// join through the fixture pid's fd symlink (exactly one row resolves
// to a PID and name — pid 1234's "vim"; every other socket an
// unprivileged reader cannot see reads PID 0 and ""), and the family
// order (tcp, tcp6, udp, udp6).
func TestListenersOnFixture(t *testing.T) {
	ports, err := collectors.New("testdata").Listeners()
	if err != nil {
		t.Fatalf("Listeners: %v", err)
	}
	want := []core.ListenPort{
		{Proto: "tcp", Addr: "127.0.0.1", Port: 8080, PID: 1234, Process: "vim"},
		{Proto: "tcp", Addr: "0.0.0.0", Port: 22},
		{Proto: "tcp6", Addr: "::", Port: 22},
		{Proto: "udp", Addr: "127.0.0.1", Port: 53},
		{Proto: "udp", Addr: "192.168.146.132", Port: 68},
		{Proto: "udp6", Addr: "::", Port: 53},
	}
	if len(ports) != len(want) {
		t.Fatalf("got %d listeners, want %d:\n%+v", len(ports), len(want), ports)
	}
	for i := range want {
		if ports[i] != want[i] {
			t.Errorf("listener %d = %+v, want %+v", i, ports[i], want[i])
		}
	}
}

// TestConnectionsOnFixture pins the other side of the split: every
// non-LISTEN TCP socket (ESTABLISHED and TIME_WAIT both included) and
// the connected UDP socket carrying CONNECTED as its state label.
func TestConnectionsOnFixture(t *testing.T) {
	conns, err := collectors.New("testdata").Connections()
	if err != nil {
		t.Fatalf("Connections: %v", err)
	}
	want := []core.Connection{
		{Proto: "tcp", LocalAddr: "127.0.0.1", LocalPort: 40010, RemoteAddr: "127.0.0.1", RemotePort: 22, State: "ESTABLISHED"},
		{Proto: "tcp", LocalAddr: "192.168.146.132", LocalPort: 40010, RemoteAddr: "168.235.208.23", RemotePort: 443, State: "ESTABLISHED"},
		{Proto: "tcp", LocalAddr: "127.0.0.1", LocalPort: 57536, RemoteAddr: "127.0.0.1", RemotePort: 8080, State: "TIME_WAIT"},
		{Proto: "udp", LocalAddr: "127.0.0.1", LocalPort: 40010, RemoteAddr: "127.0.0.1", RemotePort: 53, State: "CONNECTED"},
	}
	if len(conns) != len(want) {
		t.Fatalf("got %d connections, want %d:\n%+v", len(conns), len(want), conns)
	}
	for i := range want {
		if conns[i] != want[i] {
			t.Errorf("connection %d = %+v, want %+v", i, conns[i], want[i])
		}
	}
}

// TestListenersDedupesReusePort pins the SO_REUSEPORT collapse: the
// kernel writes one 0A row per socket sharing an endpoint, and the
// listing keeps one row per (proto, address, port) — the first in
// kernel listing order.
func TestListenersDedupesReusePort(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "proc", "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := sockHeader +
		sockRow("0", "0100007F:20FB", "00000000:0000", "0A", "555001") +
		sockRow("1", "0100007F:20FB", "00000000:0000", "0A", "555002")
	if err := os.WriteFile(filepath.Join(dir, "proc", "net", "tcp"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ports, err := collectors.New(dir).Listeners()
	if err != nil {
		t.Fatalf("Listeners: %v", err)
	}
	if len(ports) != 1 || ports[0].Port != 8443 || ports[0].Proto != "tcp" {
		t.Fatalf("SO_REUSEPORT twins must collapse to one row, got %+v", ports)
	}
}

// TestSocketsMissingFamiliesDegrade pins the IPv6-disabled policy:
// /proc/net/{tcp6,udp6} vanish when the kernel boots without IPv6, so
// a missing family file skips its half instead of failing the whole
// listing — while the families that exist still list.
func TestSocketsMissingFamiliesDegrade(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "proc", "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := sockHeader + sockRow("0", "00000000:0016", "00000000:0000", "0A", "99")
	if err := os.WriteFile(filepath.Join(dir, "proc", "net", "tcp"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ports, err := collectors.New(dir).Listeners()
	if err != nil {
		t.Fatalf("a missing tcp6/udp/udp6 must not fail the listing: %v", err)
	}
	if len(ports) != 1 || ports[0].Proto != "tcp" || ports[0].Port != 22 {
		t.Fatalf("the present family must still list: %+v", ports)
	}
}
