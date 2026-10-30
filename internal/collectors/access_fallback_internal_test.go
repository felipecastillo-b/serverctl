package collectors

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// fixedAuthNow is the reference instant for year-less syslog stamps:
// 2026-10-09 12:00:00 UTC. The auth-log grammar carries no year, so
// every parser test injects one instead of reading the wall clock.
var fixedAuthNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// TestParseAuthLogTime pins the year inference: a stamp in the past
// keeps now's year, a stamp more than a day ahead is last year's
// (the Dec/Jan rollover), and anything that is not a syslog stamp is
// rejected.
func TestParseAuthLogTime(t *testing.T) {
	tests := []struct {
		name  string
		stamp string
		now   time.Time
		want  time.Time
		ok    bool
	}{
		{
			name:  "same-day stamp keeps the current year",
			stamp: "Oct 9 13:49:30",
			now:   fixedAuthNow,
			want:  time.Date(2026, 10, 9, 13, 49, 30, 0, time.UTC),
			ok:    true,
		},
		{
			name:  "earlier month keeps the current year",
			stamp: "Jan 2 03:04:05",
			now:   fixedAuthNow,
			want:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			ok:    true,
		},
		{
			name:  "december stamp read in january is last year",
			stamp: "Dec 31 23:59:59",
			now:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			want:  time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC),
			ok:    true,
		},
		{
			name:  "space-padded single-digit day parses",
			stamp: "Jan  2 15:04:05",
			now:   fixedAuthNow,
			want:  time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC),
			ok:    true,
		},
		{name: "missing time", stamp: "Jan 2", now: fixedAuthNow, ok: false},
		{name: "full ISO timestamp is not a syslog stamp", stamp: "2026-10-09T12:00:00", now: fixedAuthNow, ok: false},
		{name: "bad month", stamp: "Foo 2 15:04:05", now: fixedAuthNow, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseAuthLogTime(tt.stamp, tt.now)
			if ok != tt.ok {
				t.Fatalf("parseAuthLogTime(%q) ok = %v, want %v (got %v)", tt.stamp, ok, tt.ok, got)
			}
			if ok && !got.Equal(tt.want) {
				t.Errorf("parseAuthLogTime(%q) = %v, want %v", tt.stamp, got, tt.want)
			}
		})
	}
}

// TestParseAuthLogLine pins the syslog envelope around the sshd
// message: the program tag is the OpenSSH family (pid brackets
// optional), the message keeps ParseSSHAttemptLine's grammar, the
// stamp supplies the time, and every other program or malformed line
// stays out. One case runs with a January clock to prove the year
// rollover survives the full line parse.
func TestParseAuthLogLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		now  time.Time
		want core.SSHAttempt
		ok   bool
	}{
		{
			name: "accepted publickey with pid",
			line: "Oct  9 13:49:30 server sshd[4242]: Accepted publickey for alice from 192.168.1.50 port 51422 ssh2",
			now:  fixedAuthNow,
			want: core.SSHAttempt{Time: time.Date(2026, 10, 9, 13, 49, 30, 0, time.UTC), User: "alice", SourceIP: "192.168.1.50", Success: true},
			ok:   true,
		},
		{
			name: "accepted password without pid brackets",
			line: "Oct  9 13:50:04 server sshd: Accepted password for bob from 10.0.0.9 port 49776 ssh2",
			now:  fixedAuthNow,
			want: core.SSHAttempt{Time: time.Date(2026, 10, 9, 13, 50, 4, 0, time.UTC), User: "bob", SourceIP: "10.0.0.9", Success: true},
			ok:   true,
		},
		{
			name: "failed password for an invalid user under sshd-session",
			line: "Oct  9 13:51:00 server sshd-session[99]: Failed password for invalid user admin from 203.0.113.7 port 41002 ssh2",
			now:  fixedAuthNow,
			want: core.SSHAttempt{Time: time.Date(2026, 10, 9, 13, 51, 0, 0, time.UTC), User: "admin", SourceIP: "203.0.113.7"},
			ok:   true,
		},
		{
			name: "failed publickey under sshd-auth",
			line: "Oct  9 13:52:00 server sshd-auth[7]: Failed publickey for root from 203.0.113.7 port 41003 ssh2 [preauth]",
			now:  fixedAuthNow,
			want: core.SSHAttempt{Time: time.Date(2026, 10, 9, 13, 52, 0, 0, time.UTC), User: "root", SourceIP: "203.0.113.7"},
			ok:   true,
		},
		{
			name: "two-digit day",
			line: "Oct 19 08:00:01 server sshd[12]: Failed password for root from 198.51.100.9 port 41000 ssh2",
			now:  time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
			want: core.SSHAttempt{Time: time.Date(2026, 10, 19, 8, 0, 1, 0, time.UTC), User: "root", SourceIP: "198.51.100.9"},
			ok:   true,
		},
		{
			name: "december line under a january clock is last year",
			line: "Dec 31 23:59:59 server sshd[1]: Accepted password for alice from 10.0.0.1 port 22 ssh2",
			now:  time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			want: core.SSHAttempt{Time: time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC), User: "alice", SourceIP: "10.0.0.1", Success: true},
			ok:   true,
		},
		{
			name: "non-sshd program with attempt-shaped message is ignored",
			line: "Oct  9 13:49:30 server CRON[1234]: Accepted password for mallory from 10.0.0.1 port 22 ssh2",
			now:  fixedAuthNow,
			ok:   false,
		},
		{
			name: "sshd chatter is not an attempt",
			line: "Oct  9 13:49:30 server sshd[4242]: Received disconnect from 203.0.113.7 port 41000:11: Bye Bye [preauth]",
			now:  fixedAuthNow,
			ok:   false,
		},
		{
			name: "malformed short line",
			line: "Oct  9 13:49:30 server",
			now:  fixedAuthNow,
			ok:   false,
		},
		{
			name: "malformed program tag",
			line: "Oct  9 13:49:30 server sshd[12: Failed password for root from 1.2.3.4 port 22 ssh2",
			now:  fixedAuthNow,
			ok:   false,
		},
		{
			name: "bad timestamp",
			line: "Foo 9 13:49:30 server sshd[1]: Failed password for root from 1.2.3.4 port 22 ssh2",
			now:  fixedAuthNow,
			ok:   false,
		},
		{name: "empty line", line: "", now: fixedAuthNow, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseAuthLogLine(tt.line, tt.now)
			if ok != tt.ok {
				t.Fatalf("parseAuthLogLine(%q) ok = %v, want %v (got %+v)", tt.line, ok, tt.ok, got)
			}
			if !ok {
				return
			}
			if !got.Time.Equal(tt.want.Time) {
				t.Errorf("Time = %v, want %v", got.Time, tt.want.Time)
			}
			if got.User != tt.want.User || got.SourceIP != tt.want.SourceIP || got.Success != tt.want.Success {
				t.Errorf("attempt = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestAttemptsFromAuthLogOrderAndCap pins the listing contract over a
// log: file order is oldest first, the result is newest first, chatter
// and non-sshd lines are skipped without disturbing the order, and the
// cap keeps the newest attempts.
func TestAttemptsFromAuthLogOrderAndCap(t *testing.T) {
	lines := []string{
		"Oct  9 10:00:00 server sshd[1]: Accepted publickey for oldest from 10.0.0.1 port 22 ssh2",
		"Oct  9 10:00:30 server CRON[2]: session opened for user root",
		"Oct  9 10:01:00 server sshd[3]: Failed password for middle from 10.0.0.2 port 22 ssh2",
		"Oct  9 10:02:00 server sshd[4]: Accepted password for newest from 10.0.0.3 port 22 ssh2",
	}
	got := attemptsFromAuthLog([]byte(strings.Join(lines, "\n")), fixedAuthNow)
	if len(got) != 3 {
		t.Fatalf("attemptsFromAuthLog = %d attempts, want 3: %+v", len(got), got)
	}
	for i, wantUser := range []string{"newest", "middle", "oldest"} {
		if got[i].User != wantUser {
			t.Errorf("attempt %d user = %q, want %q — the listing must be newest first", i, got[i].User, wantUser)
		}
	}

	var many []string
	for i := 0; i < sshAttemptsLimit+5; i++ {
		many = append(many, "Oct  9 10:00:00 server sshd[1]: Failed password for user"+
			strconv.Itoa(i)+" from 10.0.0.1 port 22 ssh2")
	}
	capped := attemptsFromAuthLog([]byte(strings.Join(many, "\n")), fixedAuthNow)
	if len(capped) != sshAttemptsLimit {
		t.Fatalf("attemptsFromAuthLog = %d attempts, want the cap %d", len(capped), sshAttemptsLimit)
	}
	if capped[0].User != "user"+strconv.Itoa(sshAttemptsLimit+4) {
		t.Errorf("newest kept attempt = %q, want the freshest line", capped[0].User)
	}
	if capped[len(capped)-1].User != "user5" {
		t.Errorf("oldest kept attempt = %q, want user5 — overflow must drop from the old end", capped[len(capped)-1].User)
	}
}

// TestTailLinesDropsPartialFirstLine pins the bounded auth-log read:
// when the window starts mid-file, everything before the first newline
// is dropped — a truncated syslog record must never reach the parser —
// while a file shorter than the budget reads whole.
func TestTailLinesDropsPartialFirstLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.log")

	first := "Jan  1 00:00:01 server sshd[1]: Failed password for old from 1.1.1.1 port 22 ssh2\n"
	second := "Jan  1 00:00:02 server sshd[2]: Accepted password for new from 10.0.0.9 port 23 ssh2\n"
	if err := os.WriteFile(path, []byte(first+second), 0o644); err != nil {
		t.Fatalf("write fixture log: %v", err)
	}

	// The budget leaves the window starting ten bytes before line one's
	// newline, so the read begins mid-line; the remainder of line one
	// must be dropped and only the complete second line retained.
	budget := int64(len(second) + 10)
	got, err := tailLines(path, budget)
	if err != nil {
		t.Fatalf("tailLines: %v", err)
	}
	if string(got) != second {
		t.Errorf("tailLines = %q, want only the complete second line %q", got, second)
	}
	if bytes.Contains(got, []byte("old")) {
		t.Error("the partial first line leaked into the tail window")
	}

	whole, err := tailLines(path, int64(len(first+second)+100))
	if err != nil {
		t.Fatalf("tailLines whole-file read: %v", err)
	}
	if string(whole) != first+second {
		t.Errorf("tailLines whole file = %q, want every byte", whole)
	}
}

// TestTailRecordsAlignsRecords pins the wtmp window: the read is
// aligned by deriving the skip from the file offset, so a budget that
// does not land on a record boundary still yields whole records and a
// torn trailing record stays out.
func TestTailRecordsAlignsRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wtmp")

	recs := bytes.Join([][]byte{
		wtmpTestRecord(utmpTypeUserProcess, "old", "10.0.0.1", 100),
		wtmpTestRecord(utmpTypeUserProcess, "middle", "10.0.0.2", 200),
		wtmpTestRecord(utmpTypeUserProcess, "new", "10.0.0.3", 300),
	}, nil)
	torn := []byte("partial record")
	if err := os.WriteFile(path, append(recs, torn...), 0o644); err != nil {
		t.Fatalf("write fixture wtmp: %v", err)
	}

	// A budget of two records plus a bit starts inside the first of the
	// three complete records; aligning to the next boundary must leave
	// exactly the last two records.
	got, err := tailRecords(path, int64(2*utmpRecordSize+100))
	if err != nil {
		t.Fatalf("tailRecords: %v", err)
	}
	attempts := attemptsFromWtmp(got)
	if len(attempts) != 2 {
		t.Fatalf("tailRecords window = %d attempts, want the last 2: %+v", len(attempts), attempts)
	}
	if attempts[0].User != "new" || attempts[1].User != "middle" {
		t.Errorf("tailRecords window = %+v, want new then middle", attempts)
	}
}

// wtmpTestRecord builds one 384-byte glibc utmp record in the internal
// test (the external suite has its own builder): enough fields for the
// wtmp mapper — type, user, host and login seconds.
func wtmpTestRecord(typ int16, user, host string, sec int32) []byte {
	rec := make([]byte, utmpRecordSize)
	binary.NativeEndian.PutUint16(rec[utmpOffType:utmpOffType+2], uint16(typ))
	copy(rec[utmpOffUser:utmpOffUser+32], user)
	copy(rec[utmpOffHost:utmpOffHost+256], host)
	binary.NativeEndian.PutUint32(rec[utmpOffSec:utmpOffSec+4], uint32(sec))
	return rec
}

// TestAttemptsFromWtmp pins the wtmp fallback's best-effort contract:
// USER_PROCESS records with a non-empty host become successful
// attempts with LoginAt as Time and the host field as SourceIP; local
// sessions (no host) and every other record type stay out; the listing
// is newest first and capped.
func TestAttemptsFromWtmp(t *testing.T) {
	data := bytes.Join([][]byte{
		wtmpTestRecord(2, "reboot", "6.1.0", 50),             // boot marker
		wtmpTestRecord(utmpTypeUserProcess, "seat", "", 100), // local session: no host
		wtmpTestRecord(utmpTypeUserProcess, "alice", "192.168.1.50", 200),
		wtmpTestRecord(8, "", "", 250), // dead process
		wtmpTestRecord(utmpTypeUserProcess, "bob", "remote.example.com", 300),
	}, nil)

	got := attemptsFromWtmp(data)
	want := []core.SSHAttempt{
		{Time: time.Unix(300, 0), User: "bob", SourceIP: "remote.example.com", Success: true},
		{Time: time.Unix(200, 0), User: "alice", SourceIP: "192.168.1.50", Success: true},
	}
	if len(got) != len(want) {
		t.Fatalf("attemptsFromWtmp = %d attempts, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("attempt %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Cap: the newest sshAttemptsLimit remote logins survive, oldest
	// overflow drops.
	var many [][]byte
	for i := 0; i < sshAttemptsLimit+5; i++ {
		many = append(many, wtmpTestRecord(utmpTypeUserProcess, "u"+strconv.Itoa(i), "10.0.0.1", int32(i)))
	}
	capped := attemptsFromWtmp(bytes.Join(many, nil))
	if len(capped) != sshAttemptsLimit {
		t.Fatalf("attemptsFromWtmp = %d attempts, want the cap %d", len(capped), sshAttemptsLimit)
	}
	if capped[0].User != "u"+strconv.Itoa(sshAttemptsLimit+4) {
		t.Errorf("newest kept attempt = %q, want the freshest record", capped[0].User)
	}
	if capped[len(capped)-1].User != "u5" {
		t.Errorf("oldest kept attempt = %q, want u5 — overflow must drop from the old end", capped[len(capped)-1].User)
	}

	if got := attemptsFromWtmp(nil); len(got) != 0 {
		t.Errorf("attemptsFromWtmp(nil) = %d attempts, want 0", len(got))
	}
}
