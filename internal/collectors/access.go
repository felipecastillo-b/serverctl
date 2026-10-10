// access.go reads the "Users / sessions" and "SSH attempts" rows of
// ARCHITECTURE.md §8: the utmp file for login sessions and the sshd
// unit's journal for SSH attempts. Both parsers are pure — bytes or
// lines in, domain models out — so the binary format and the log
// grammar are table-tested against crafted records without a live
// host (§10).
package collectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// Compile-time proof that System satisfies both Users read ports.
var (
	_ core.SessionLister    = System{}
	_ core.SSHAttemptLister = System{}
)

// utmpPath is the login accounting file under the root prefix. The
// canonical spelling is /var/run/utmp, but /var/run is a symlink to
// /run on every systemd host (verified on this Arch box: /var/run ->
// ../run), and the writer — glibc's login accounting — keeps the
// file at /run/utmp. Reading run/utmp through the root prefix covers
// both spellings without trusting the symlink, and keeps fixture
// roots hermetic the way the pacman database walk does.
const utmpPath = "run/utmp"

// utmpRecordSize is the size of one glibc struct utmp record:
// 384 bytes. Verified three ways on this machine, evidence over
// assumptions: glibc's <bits/utmp.h> layout sums to 384 (type 2 +
// pad 2 + pid 4 + line 32 + id 4 + user 32 + host 256 + exit 4 +
// session 4 + tv 8 + addr 16 + reserved 20), the live /run/utmp
// reads 2304 bytes = exactly 6 records, and decoding it with this
// layout reproduces `who` output field for field. The tv fields are
// fixed-width int32 under glibc's __WORDSIZE_TIME64_COMPAT32 rule,
// so the layout is identical on 32- and 64-bit builds.
const utmpRecordSize = 384

// One record's field offsets within its 384 bytes, per glibc's
// <bits/utmp.h> struct utmp layout.
const (
	utmpOffType = 0   // int16: the record type
	utmpOffLine = 8   // char[32]: the terminal device name
	utmpOffUser = 44  // char[32]: the login name
	utmpOffHost = 76  // char[256]: the remote host
	utmpOffSec  = 340 // int32: login time, seconds
	utmpOffUsec = 344 // int32: login time, microseconds
)

// utmpTypeUserProcess is glibc's USER_PROCESS record type: the
// "normal process" of a logged-in user — the one record type `who`
// lists. Every other type is the file's own bookkeeping: the boot
// marker, login prompts, dead processes, empty slots.
const utmpTypeUserProcess = 7

// ParseUtmpSessions decodes utmp file bytes into the login sessions
// they record: every USER_PROCESS record becomes one
// core.UserSession — User from the record's user field, TTY its
// line field, From its host field, LoginAt its tv — and every other
// record type stays out as bookkeeping. Strings are NUL-padded
// fixed-width fields; the bytes before the first NUL are the value.
//
// A trailing partial record — fewer than 384 bytes — is a torn
// write mid-append and is ignored: sessions only ever come from
// complete records. Numbers are read in native byte order, the
// order glibc writes them in, so the parser reads exactly what the
// host's login stack wrote. Pure, so the binary format is
// table-tested against crafted records and a golden fixture.
func ParseUtmpSessions(data []byte) []core.UserSession {
	sessions := make([]core.UserSession, 0, len(data)/utmpRecordSize)
	for off := 0; off+utmpRecordSize <= len(data); off += utmpRecordSize {
		rec := data[off : off+utmpRecordSize]
		if binary.NativeEndian.Uint16(rec[utmpOffType:utmpOffType+2]) != utmpTypeUserProcess {
			continue
		}
		sec := int32(binary.NativeEndian.Uint32(rec[utmpOffSec : utmpOffSec+4]))
		usec := int32(binary.NativeEndian.Uint32(rec[utmpOffUsec : utmpOffUsec+4]))
		sessions = append(sessions, core.UserSession{
			User:    utmpString(rec[utmpOffUser : utmpOffUser+32]),
			TTY:     utmpString(rec[utmpOffLine : utmpOffLine+32]),
			From:    utmpString(rec[utmpOffHost : utmpOffHost+256]),
			LoginAt: time.Unix(int64(sec), int64(usec)*1000),
		})
	}
	return sessions
}

// utmpString reads one NUL-padded fixed-width utmp field: the bytes
// before the first NUL, the whole field when it carries none. utmp
// fields are __attribute_nonstring__ — only the NUL convention
// bounds the value — and the bytes pass into a Go string
// uninterpreted; login names are ASCII in practice.
func utmpString(raw []byte) string {
	if i := bytes.IndexByte(raw, 0); i >= 0 {
		raw = raw[:i]
	}
	return string(raw)
}

// Sessions implements core.SessionLister over the utmp file
// (ARCHITECTURE.md §8, row "Users / sessions"). The file is a plain
// read through the root prefix, so fixture roots stay hermetic and
// the live guard never applies; logind D-Bus — §8's preferred
// source — is the post-MVP adapter this port already fits. A
// missing or unreadable utmp (a container without login accounting,
// a non-utmp init) is an error the sessions view degrades on (§5);
// a readable file with no sessions is a legitimate empty listing.
func (s System) Sessions() ([]core.UserSession, error) {
	data, err := os.ReadFile(s.rootPath(utmpPath))
	if err != nil {
		return nil, fmt.Errorf("read utmp: %w", err)
	}
	return ParseUtmpSessions(data), nil
}

// sshdUnit is the systemd unit whose journal carries sshd's
// authentication lines. Verified on this Arch host: the unit file
// is sshd.service (ssh.service is the Debian/Ubuntu spelling).
// Hosts running sshd under socket activation log per-connection
// entries under the sshd@.service template instead — those carry a
// different _SYSTEMD_UNIT and do not match; the standalone daemon
// is the Arch default this adapter targets.
const sshdUnit = "sshd.service"

// sshAttemptsLimit caps the attempts one round returns: 200, enough
// to fill a screen of audit history without an unbounded listing.
const sshAttemptsLimit = 200

// sshAttemptsJournalWindow is how many recent sshd journal entries
// one round reads to find them: the journal mixes attempt lines with
// connection chatter, so the window is wider than the cap — a
// busy box's most recent 1000 entries hold 200 attempts with room
// to spare, and a quiet box returns everything it has.
const sshAttemptsJournalWindow = 1000

// Attempts implements core.SSHAttemptLister over the sshd unit's
// journal, through the M4c JournalReader machinery — Tail's unit
// match and live-only guard included, so a fixture System refuses
// rather than guessing where entries would come from (the wtmp and
// auth-log fallbacks of §8 are post-MVP adapters).
//
// Unprivileged reality, probed live on this host as a user outside
// the systemd-journal and adm groups: the journal opens fine and the
// sshd match simply returns zero entries with NO error — journald
// shows a reader only their own session's entries
// (core.SSHAttempt documents the access model). The error state
// this method can return — the journal cannot be opened at all —
// is the real degradation; an empty listing is not one.
func (s System) Attempts() ([]core.SSHAttempt, error) {
	entries, _, err := s.Tail(sshdUnit, sshAttemptsJournalWindow)
	if err != nil {
		return nil, err
	}
	return attemptsFromEntries(entries), nil
}

// attemptsFromEntries distills journal entries — oldest first, the
// order Tail returns — into the most recent attempts, newest first,
// capped at sshAttemptsLimit. Non-attempt lines (key exchange,
// disconnects) stay out; Time is the journal entry's own timestamp,
// the line itself carries none. Pure, so the bound and the ordering
// are testable without a journal.
func attemptsFromEntries(entries []core.JournalEntry) []core.SSHAttempt {
	attempts := make([]core.SSHAttempt, 0, min(len(entries), sshAttemptsLimit))
	for i := len(entries) - 1; i >= 0 && len(attempts) < sshAttemptsLimit; i-- {
		attempt, ok := ParseSSHAttemptLine(entries[i].Message)
		if !ok {
			continue
		}
		attempt.Time = entries[i].Time
		attempts = append(attempts, attempt)
	}
	return attempts
}

// ParseSSHAttemptLine decodes one sshd journal MESSAGE line into the
// attempt it records; ok is false for every line that is not one.
// sshd's authentication lines read, per openssh's auth log grammar:
//
//	Accepted publickey for alice from 192.168.1.50 port 51422 ssh2
//	Failed password for invalid user admin from 203.0.113.7 port 41002 ssh2
//	Failed publickey for root from 203.0.113.7 port 41003 ssh2 [preauth]
//
// The parse is tolerant where openssh drifts and strict where the
// grammar never has: the method token is any single word (methods
// gain spellings — keyboard-interactive/pam — not new shapes), the
// "ssh2" and "[preauth]" trailers are ignored, and a username may
// span tokens ("invalid user spaced name" is a real brute-force
// shape). Required: the verdict word, the literal for/from/port
// structure, a non-empty username, and a token after "port". The
// other lines sshd writes ("Received disconnect from ...",
// "Connection closed by ...") never satisfy them. A username
// containing the literal word "from" misparses — the first from
// wins, the honest cost of reading names with spaces. Time is NOT
// in the line: it is the journal entry's own timestamp, stamped by
// the caller. Pure, so the grammar is table-tested against fixture
// lines.
func ParseSSHAttemptLine(line string) (core.SSHAttempt, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[2] != "for" {
		return core.SSHAttempt{}, false
	}

	var attempt core.SSHAttempt
	switch fields[0] {
	case "Accepted":
		attempt.Success = true
	case "Failed":
		attempt.Success = false
	default:
		return core.SSHAttempt{}, false
	}

	// "invalid user" prefixes the username of every attempt against
	// an account sshd does not know; the user field still names the
	// account the client asked for.
	start := 3
	if fields[3] == "invalid" && len(fields) >= 5 && fields[4] == "user" {
		start = 5
	}

	// The username runs from start to the first "from"; every token
	// between them belongs to the name.
	from := -1
	for i := start; i < len(fields); i++ {
		if fields[i] == "from" {
			from = i
			break
		}
	}
	// No "from", or a username of zero tokens ("... for from ..."):
	// not an attempt.
	if from <= start {
		return core.SSHAttempt{}, false
	}

	// "<ip> port <n>" closes the grammar; the port's value token
	// must exist, its digits are sshd's own business.
	if from+3 >= len(fields) || fields[from+2] != "port" {
		return core.SSHAttempt{}, false
	}

	attempt.User = strings.Join(fields[start:from], " ")
	attempt.SourceIP = fields[from+1]
	return attempt, true
}
