package collectors_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
)

// utmpRecord builds one 384-byte record in the glibc layout the
// collector parses — the offsets <bits/utmp.h> defines, mirrored here
// so tests craft records without touching the collector's unexported
// constants. Numbers go in native byte order like the parser reads
// them; testdata/run/utmp is the same layout, crafted and verified
// against a decode of the live /run/utmp.
func utmpRecord(typ int16, pid int32, line, id, user, host string, sec, usec int32) []byte {
	rec := make([]byte, 384)
	binary.NativeEndian.PutUint16(rec[0:], uint16(typ))
	binary.NativeEndian.PutUint32(rec[4:], uint32(pid))
	copy(rec[8:], line)
	copy(rec[40:], id)
	copy(rec[44:], user)
	copy(rec[76:], host)
	binary.NativeEndian.PutUint32(rec[340:], uint32(sec))
	binary.NativeEndian.PutUint32(rec[344:], uint32(usec))
	return rec
}

// TestParseUtmpSessions pins the utmp decoding contract: USER_PROCESS
// records become sessions with every field mapped, every other
// record type is the file's bookkeeping and stays out, a torn
// trailing record is a torn write and is ignored, and fixed-width
// fields read up to their first NUL — or whole when they carry none.
// The field mapping mirrors the live-host verification: decoding
// /run/utmp this way reproduced `who` exactly.
func TestParseUtmpSessions(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want []core.UserSession
	}{
		{
			name: "empty file reads no sessions",
			data: nil,
			want: nil,
		},
		{
			name: "one user process record maps every field",
			data: utmpRecord(7, 854, "tty7", ":0", "castillodk", ":0", 1791560974, 105900),
			want: []core.UserSession{
				{User: "castillodk", TTY: "tty7", From: ":0", LoginAt: time.Unix(1791560974, 105900000)},
			},
		},
		{
			name: "bookkeeping records stay out of the listing",
			data: bytes.Join([][]byte{
				// The boot marker carries "reboot" in its user field —
				// it is still not a session.
				utmpRecord(2, 0, "~", "~~", "reboot", "7.2.9-arch1-1", 1791560959, 167465),
				utmpRecord(6, 900, "tty1", "1", "LOGIN", "", 1791560960, 0),       // login prompt
				utmpRecord(8, 11751, "pts/3", "ts/3", "", "", 1791564839, 513536), // dead
				utmpRecord(0, 0, "", "", "", "", 0, 0),                            // empty slot
				utmpRecord(7, 9200, "pts/0", "ts/0", "alice", "192.168.1.50", 1791561004, 210000),
			}, nil),
			want: []core.UserSession{
				{User: "alice", TTY: "pts/0", From: "192.168.1.50", LoginAt: time.Unix(1791561004, 210000000)},
			},
		},
		{
			name: "torn trailing record is ignored",
			data: bytes.Join([][]byte{
				utmpRecord(7, 854, "tty7", ":0", "castillodk", ":0", 1791560974, 105900),
				utmpRecord(7, 9200, "pts/0", "ts/0", "ali", "", 1791561004, 0)[:100], // torn write
			}, nil),
			want: []core.UserSession{
				{User: "castillodk", TTY: "tty7", From: ":0", LoginAt: time.Unix(1791560974, 105900000)},
			},
		},
		{
			name: "record shorter than the layout reads as nothing",
			data: utmpRecord(7, 854, "tty7", ":0", "castillodk", ":0", 1791560974, 105900)[:200],
			want: nil,
		},
		{
			name: "field without a NUL reads whole",
			data: utmpRecord(7, 854, strings.Repeat("t", 32), ":0", strings.Repeat("u", 32), "", 1791560974, 0),
			want: []core.UserSession{
				{User: strings.Repeat("u", 32), TTY: strings.Repeat("t", 32), LoginAt: time.Unix(1791560974, 0)},
			},
		},
		{
			name: "empty user process record still lists",
			data: utmpRecord(7, 100, "tty2", "2", "", "", 1791560980, 0),
			want: []core.UserSession{
				{TTY: "tty2", LoginAt: time.Unix(1791560980, 0)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectors.ParseUtmpSessions(tt.data)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseUtmpSessions = %d sessions, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i].User != tt.want[i].User || got[i].TTY != tt.want[i].TTY || got[i].From != tt.want[i].From {
					t.Errorf("session %d = %+v, want %+v", i, got[i], tt.want[i])
				}
				if !got[i].LoginAt.Equal(tt.want[i].LoginAt) {
					t.Errorf("session %d LoginAt = %v, want %v", i, got[i].LoginAt, tt.want[i].LoginAt)
				}
			}
		})
	}
}

// TestSessionsOnFixture drives Sessions against the golden fixture
// root end to end: the utmp walk reads testdata/run/utmp, the three
// USER_PROCESS records become sessions — the seat login with a
// display in From, two remote logins — and the boot, dead and empty
// bookkeeping records stay out. Hermetic: nothing here reads /run of
// the test runner.
func TestSessionsOnFixture(t *testing.T) {
	sessions, err := collectors.New("testdata").Sessions()
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	want := []struct {
		user, tty, from string
		loginAt         time.Time
	}{
		{"castillodk", "tty7", ":0", time.Unix(1791560974, 105900000)},
		{"alice", "pts/0", "192.168.1.50", time.Unix(1791561004, 210000000)},
		{"bob", "pts/1", "remote.example.com", time.Unix(1791561064, 300000000)},
	}
	if len(sessions) != len(want) {
		t.Fatalf("Sessions = %d sessions, want %d: %+v", len(sessions), len(want), sessions)
	}
	for i, w := range want {
		got := sessions[i]
		if got.User != w.user || got.TTY != w.tty || got.From != w.from {
			t.Errorf("session %d = %+v, want %q %q %q", i, got, w.user, w.tty, w.from)
		}
		if !got.LoginAt.Equal(w.loginAt) {
			t.Errorf("session %d LoginAt = %v, want %v", i, got.LoginAt, w.loginAt)
		}
	}
}

// TestSessionsOnFixtureReadsUtmpOnly pins the fixture dispatch: a
// System rooted at a fixture directory serves Sessions from that
// root's utmp file and never reaches logind — the bus is live-only,
// so a leaked dial would return the test runner's sessions (or fail)
// instead of this crafted row. Hermetic proof that the fallback is
// also the fixture path.
func TestSessionsOnFixtureReadsUtmpOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "run"), 0o755); err != nil {
		t.Fatalf("create fixture run dir: %v", err)
	}
	record := utmpRecord(7, 4242, "pts/7", "ts/7", "fixture-only", "198.51.100.20", 1791562000, 500000)
	if err := os.WriteFile(filepath.Join(root, "run", "utmp"), record, 0o644); err != nil {
		t.Fatalf("write fixture utmp: %v", err)
	}

	sessions, err := collectors.New(root).Sessions()
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Sessions = %d sessions, want 1: %+v", len(sessions), sessions)
	}
	got := sessions[0]
	if got.User != "fixture-only" || got.TTY != "pts/7" || got.From != "198.51.100.20" {
		t.Errorf("session = %+v, want the fixture record", got)
	}
	if want := time.Unix(1791562000, 500000000); !got.LoginAt.Equal(want) {
		t.Errorf("LoginAt = %v, want %v", got.LoginAt, want)
	}
}

// TestSessionsMissingUtmpRefusesOnFixtureSystem pins the hermeticity
// guard: a root without a utmp file must ERROR, never fall through to
// the test runner's own /run/utmp. On a machine WITH the file (the
// dev box) the test doubles as proof the root prefix actually
// prefixes: a leaky read would return the host's real sessions
// instead of an error.
func TestSessionsMissingUtmpRefusesOnFixtureSystem(t *testing.T) {
	if _, err := collectors.New(t.TempDir()).Sessions(); err == nil {
		t.Fatal("a fixture System without a utmp file must error, not read the host /run/utmp")
	}
}

// TestParseSSHAttemptLine pins the sshd line grammar: both verdicts
// count, the method token is free-form, invalid-user and [preauth]
// variants parse, IPv6 sources survive token splitting, spaced
// usernames join, and the connection-chatter lines sshd writes never
// pass as attempts.
func TestParseSSHAttemptLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want core.SSHAttempt
		ok   bool
	}{
		{
			name: "accepted publickey",
			line: "Accepted publickey for alice from 192.168.1.50 port 51422 ssh2",
			want: core.SSHAttempt{User: "alice", SourceIP: "192.168.1.50", Success: true},
			ok:   true,
		},
		{
			name: "accepted password",
			line: "Accepted password for bob from 10.0.0.9 port 49776 ssh2",
			want: core.SSHAttempt{User: "bob", SourceIP: "10.0.0.9", Success: true},
			ok:   true,
		},
		{
			name: "accepted keyboard-interactive method token",
			line: "Accepted keyboard-interactive/pam for carol from 192.168.1.50 port 51430 ssh2",
			want: core.SSHAttempt{User: "carol", SourceIP: "192.168.1.50", Success: true},
			ok:   true,
		},
		{
			name: "failed password for a real account",
			line: "Failed password for root from 203.0.113.7 port 41000 ssh2",
			want: core.SSHAttempt{User: "root", SourceIP: "203.0.113.7"},
			ok:   true,
		},
		{
			name: "failed password for an invalid user strips the prefix",
			line: "Failed password for invalid user admin from 203.0.113.7 port 41002 ssh2",
			want: core.SSHAttempt{User: "admin", SourceIP: "203.0.113.7"},
			ok:   true,
		},
		{
			name: "failed publickey with preauth trailer",
			line: "Failed publickey for invalid user oracle from 198.51.100.9 port 51234 ssh2 [preauth]",
			want: core.SSHAttempt{User: "oracle", SourceIP: "198.51.100.9"},
			ok:   true,
		},
		{
			name: "failed none for an invalid user",
			line: "Failed none for invalid user test from 198.51.100.9 port 51236 ssh2 [preauth]",
			want: core.SSHAttempt{User: "test", SourceIP: "198.51.100.9"},
			ok:   true,
		},
		{
			name: "IPv6 source survives token splitting",
			line: "Accepted publickey for dana from 2001:db8::1 port 51440 ssh2",
			want: core.SSHAttempt{User: "dana", SourceIP: "2001:db8::1", Success: true},
			ok:   true,
		},
		{
			name: "spaced username joins into one field",
			line: "Failed password for spaced user name from 203.0.113.8 port 42000 ssh2",
			want: core.SSHAttempt{User: "spaced user name", SourceIP: "203.0.113.8"},
			ok:   true,
		},
		{
			name: "spaced username behind invalid user",
			line: "Failed password for invalid user admin admin2 from 203.0.113.8 port 42002 ssh2",
			want: core.SSHAttempt{User: "admin admin2", SourceIP: "203.0.113.8"},
			ok:   true,
		},
		{name: "empty line", line: "", ok: false},
		{name: "disconnect chatter", line: "Received disconnect from 203.0.113.7 port 41000:11: Bye Bye [preauth]", ok: false},
		{name: "authenticated disconnect chatter", line: "Disconnected from invalid user admin 203.0.113.7 port 41002:11: Bye Bye [preauth]", ok: false},
		{name: "connection closed chatter", line: "Connection closed by authenticating user root 203.0.113.7 port 41004 [preauth]", ok: false},
		{name: "missing for literal", line: "Failed password root from 1.2.3.4 port 22 ssh2", ok: false},
		{name: "missing port literal", line: "Failed password for root from 1.2.3.4 ssh2", ok: false},
		{name: "truncated after port", line: "Failed password for root from 1.2.3.4 port", ok: false},
		{name: "empty username", line: "Accepted publickey for from 1.2.3.4 port 22 ssh2", ok: false},
		{name: "empty invalid username", line: "Failed password for invalid user from 1.2.3.4 port 22 ssh2", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := collectors.ParseSSHAttemptLine(tt.line)
			if ok != tt.ok {
				t.Fatalf("ParseSSHAttemptLine(%q) ok = %v, want %v (got %+v)", tt.line, ok, tt.ok, got)
			}
			if !ok {
				return
			}
			if got != tt.want {
				t.Errorf("ParseSSHAttemptLine(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}

// TestParseSSHAttemptLineOnSample pins the classification against the
// sshd line samples kept under testdata, the shapes the live journal
// read parses: eight attempts — four accepted, four failed — and
// three chatter lines that stay out.
func TestParseSSHAttemptLineOnSample(t *testing.T) {
	raw, err := os.ReadFile("testdata/sshd-lines.txt")
	if err != nil {
		t.Fatalf("read sample lines: %v", err)
	}
	accepted, failed, skipped := 0, 0, 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		attempt, ok := collectors.ParseSSHAttemptLine(line)
		if !ok {
			skipped++
			continue
		}
		if attempt.Success {
			accepted++
		} else {
			failed++
		}
	}
	if accepted != 4 || failed != 4 || skipped != 3 {
		t.Errorf("sample classification = %d accepted, %d failed, %d skipped; want 4, 4, 3",
			accepted, failed, skipped)
	}
}

// writeUnderRoot writes data at root/rel, creating parent directories.
func writeUnderRoot(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parents of %s: %v", rel, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestAttemptsFallbackFromAuthLog drives the public Attempts chain end
// to end on a fixture root: a non-live System skips the journal (no
// root-prefix journal fixtures exist), reads auth.log under the root,
// and returns its attempts newest first with both verdicts.
func TestAttemptsFallbackFromAuthLog(t *testing.T) {
	root := t.TempDir()
	writeUnderRoot(t, root, "var/log/auth.log", []byte(strings.Join([]string{
		"Oct  9 12:00:00 server sshd[1]: Accepted publickey for alice from 192.168.1.50 port 51422 ssh2",
		"Oct  9 13:49:30 server sshd[2]: Failed password for invalid user admin from 203.0.113.7 port 41002 ssh2",
		"",
	}, "\n")))

	attempts, err := collectors.New(root).Attempts()
	if err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("Attempts = %d attempts, want 2: %+v", len(attempts), attempts)
	}
	if got := attempts[0]; got.User != "admin" || got.SourceIP != "203.0.113.7" || got.Success {
		t.Errorf("newest attempt = %+v, want admin/203.0.113.7/failed", got)
	}
	if got := attempts[1]; got.User != "alice" || got.SourceIP != "192.168.1.50" || !got.Success {
		t.Errorf("older attempt = %+v, want alice/192.168.1.50/accepted", got)
	}
}

// TestAttemptsFallbackFromSecureLog pins the Red Hat spelling: with no
// auth.log, the chain reads var/log/secure.
func TestAttemptsFallbackFromSecureLog(t *testing.T) {
	root := t.TempDir()
	writeUnderRoot(t, root, "var/log/secure", []byte(
		"Oct  9 13:49:30 server sshd[2]: Failed password for root from 203.0.113.7 port 41000 ssh2\n"))

	attempts, err := collectors.New(root).Attempts()
	if err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("Attempts = %d attempts, want 1: %+v", len(attempts), attempts)
	}
	if got := attempts[0]; got.User != "root" || got.SourceIP != "203.0.113.7" || got.Success {
		t.Errorf("attempt = %+v, want root/203.0.113.7/failed", got)
	}
}

// TestAttemptsFallbackFromWtmp pins the last resort: with no auth log,
// wtmp supplies best-effort successful remote logins — USER_PROCESS
// records with a non-empty host — and local or bookkeeping records stay
// out.
func TestAttemptsFallbackFromWtmp(t *testing.T) {
	root := t.TempDir()
	writeUnderRoot(t, root, "var/log/wtmp", bytes.Join([][]byte{
		utmpRecord(2, 0, "~", "~~", "reboot", "6.1.0", 1791560000, 0),
		utmpRecord(7, 900, "pts/0", "ts/0", "alice", "192.168.1.50", 1791561004, 0),
		utmpRecord(7, 901, "tty7", ":0", "seat", "", 1791561064, 0),
	}, nil))

	attempts, err := collectors.New(root).Attempts()
	if err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("Attempts = %d attempts, want 1: %+v", len(attempts), attempts)
	}
	want := core.SSHAttempt{Time: time.Unix(1791561004, 0), User: "alice", SourceIP: "192.168.1.50", Success: true}
	if attempts[0] != want {
		t.Errorf("attempt = %+v, want %+v", attempts[0], want)
	}
}

// TestAttemptsEmptyAuthLogStopsChain pins the error-only fallback
// policy end to end: a readable auth log with no attempts is an
// authoritative empty listing, so a wtmp holding rows is NOT consulted.
func TestAttemptsEmptyAuthLogStopsChain(t *testing.T) {
	root := t.TempDir()
	writeUnderRoot(t, root, "var/log/auth.log", nil)
	writeUnderRoot(t, root, "var/log/wtmp", utmpRecord(
		7, 900, "pts/0", "ts/0", "alice", "192.168.1.50", 1791561004, 0))

	attempts, err := collectors.New(root).Attempts()
	if err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	if len(attempts) != 0 {
		t.Errorf("Attempts = %d attempts, want the empty auth log to win: %+v", len(attempts), attempts)
	}
}

// TestAttemptsWithoutSourcesErrors pins the all-sources-fail shape: a
// root without any attempt source errors and names both file paths, so
// the screen can say which sources the collector tried.
func TestAttemptsWithoutSourcesErrors(t *testing.T) {
	_, err := collectors.New(t.TempDir()).Attempts()
	if err == nil {
		t.Fatal("a root with no attempt sources must error")
	}
	for _, want := range []string{"auth.log", "secure", "wtmp"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}
}

// TestAttemptsOnFixtureSystemWithoutFilesErrors keeps the shared
// fixture root honest: testdata has no journal, auth log or wtmp, and a
// non-live System must never fall through to the test runner's own
// files.
func TestAttemptsOnFixtureSystemWithoutFilesErrors(t *testing.T) {
	if _, err := collectors.New("testdata").Attempts(); err == nil {
		t.Fatal("a fixture System without attempt files must error, not read the host")
	}
}
