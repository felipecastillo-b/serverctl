// access.go reads the "Users / sessions" and "SSH attempts" rows of
// ARCHITECTURE.md §8: logind D-Bus for login sessions, with the utmp
// file as its fallback (access_logind.go holds the D-Bus half), and
// the sshd-process journal — with auth-log and wtmp file fallbacks —
// for SSH attempts. The parsers are pure — bytes or lines in, domain
// models out — so the binary format and the log grammar are
// table-tested against crafted records without a live host (§10).
package collectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-systemd/v22/sdjournal"

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

// Sessions implements core.SessionLister over the host's login
// sessions (ARCHITECTURE.md §8, row "Users / sessions"). A live
// System reads logind over D-Bus — the preferred source, because it
// is the login manager's own registry rather than a file parse — and
// falls back to the utmp file on ANY logind error: a host without a
// bus, a container without logind, a permission the bus refuses. The
// fallback is error-driven, never emptiness-driven: an empty logind
// listing is a legitimate "nobody is logged in" answer, exactly as a
// readable utmp with no USER_PROCESS records is.
//
// A fixture System stays on utmp only: D-Bus has no root-prefix
// fixtures, so the bus is never dialed under a test root and the
// golden listing stays hermetic. When both sources fail, the error
// carries both causes — the utmp failure wrapped, the logind failure
// in the message — so the sessions view (§5) shows which paths the
// collector tried.
func (s System) Sessions() ([]core.UserSession, error) {
	if !s.IsLive() {
		return s.utmpSessions()
	}
	sessions, logindErr := s.logindSessions()
	if logindErr == nil {
		return sessions, nil
	}
	sessions, utmpErr := s.utmpSessions()
	if utmpErr == nil {
		return sessions, nil
	}
	return nil, fmt.Errorf("collectors: sessions: logind unavailable (%v) and utmp fallback failed: %w", logindErr, utmpErr)
}

// utmpSessions implements the utmp fallback of Sessions. The file is
// a plain read through the root prefix, so fixture roots stay
// hermetic and the live guard never applies. A missing or unreadable
// utmp (a container without login accounting, a non-utmp init) is an
// error the sessions view degrades on (§5); a readable file with no
// sessions is a legitimate empty listing.
func (s System) utmpSessions() ([]core.UserSession, error) {
	data, err := os.ReadFile(s.rootPath(utmpPath))
	if err != nil {
		return nil, fmt.Errorf("read utmp: %w", err)
	}
	return ParseUtmpSessions(data), nil
}

// SSH attempt sources. The journal is preferred; the file fallbacks
// cover hosts whose journal the reader cannot open and hosts without
// journald at all. Any ONE readable source is authoritative: an empty
// listing never triggers the next source, because "no attempts" is a
// legitimate answer, not a failure.
const (
	// authLogPath and secureLogPath are Debian/Ubuntu's and Red Hat's
	// spellings of the same OpenSSH authentication log.
	authLogPath   = "var/log/auth.log"
	secureLogPath = "var/log/secure"

	// wtmpPath is the login accounting file: successful logins only,
	// so this fallback can never report a failed attempt.
	wtmpPath = "var/log/wtmp"
)

// sshdIdentifiers matches the OpenSSH process family by syslog
// identifier instead of unit name. Exact _SYSTEMD_UNIT matches cannot
// cover sshd@N.service socket instances and miss Debian's ssh.service
// spelling; SYSLOG_IDENTIFIER travels with the process, so standalone
// and socket-activated units land in one OR-ed set. journald ORs
// matches on the same field (tailWithMatches documents the semantics).
// OpenSSH 9.8+ splits authentication into sshd-session (some builds
// also sshd-auth), each logging under its own identifier.
var sshdIdentifiers = []string{
	sdjournal.SD_JOURNAL_FIELD_SYSLOG_IDENTIFIER + "=sshd",
	sdjournal.SD_JOURNAL_FIELD_SYSLOG_IDENTIFIER + "=sshd-session",
	sdjournal.SD_JOURNAL_FIELD_SYSLOG_IDENTIFIER + "=sshd-auth",
}

// sshAttemptsLimit caps the attempts one round returns: 200, enough
// to fill a screen of audit history without an unbounded listing.
const sshAttemptsLimit = 200

// sshAttemptsJournalWindow is how many recent sshd journal entries
// one round reads to find them: the journal mixes attempt lines with
// connection chatter, so the window is wider than the cap — a
// busy box's most recent 1000 entries hold 200 attempts with room
// to spare, and a quiet box returns everything it has.
const sshAttemptsJournalWindow = 1000

// sshAttemptsTailBudget bounds each file fallback read to the last
// 512 KiB — thousands of auth lines or roughly 1300 wtmp records, far
// more than the 200-row listing shows — so a multi-gigabyte log is
// never read whole on a 30 s round.
const sshAttemptsTailBudget int64 = 512 * 1024

// Attempts implements core.SSHAttemptLister over a source chain, in
// preference order.
//
// A live System reads the journal first, filtered by the sshd process
// family's SYSLOG_IDENTIFIER values, which covers standalone
// sshd.service, Debian's ssh.service and socket-activated
// sshd@N.service instances. Only a journal ERROR falls through to
// the files: an empty journal result is a legitimate empty listing —
// an unprivileged reader sees only their own session's entries with
// NO error (core.SSHAttempt documents the access model) — and must
// not be replaced by a lower source.
//
// The file fallbacks read, in order, the first readable auth log
// (/var/log/auth.log, then /var/log/secure), then wtmp. The auth log
// carries both verdicts; wtmp carries successful logins only and
// cannot attribute them to SSH with certainty, so its rows are
// best-effort successes. A fixture System skips the journal entirely
// (there are no root-prefix journal fixtures) and goes straight to
// the file fallbacks, keeping tests hermetic. When every source
// fails, the error names each cause.
func (s System) Attempts() ([]core.SSHAttempt, error) {
	return s.attemptsAt(time.Now())
}

// attemptsAt is Attempts with the clock injected: traditional syslog
// timestamps carry no year, so the auth-log parser needs a reference
// instant to infer it.
func (s System) attemptsAt(now time.Time) ([]core.SSHAttempt, error) {
	if s.IsLive() {
		entries, _, journalErr := s.tailWithMatches(sshAttemptsJournalWindow, sshdIdentifiers...)
		if journalErr == nil {
			return attemptsFromEntries(entries), nil
		}
		attempts, fileErr := s.fileAttempts(now)
		if fileErr == nil {
			return attempts, nil
		}
		return nil, fmt.Errorf("collectors: ssh attempts: journal unavailable (%v) and file fallbacks failed: %w", journalErr, fileErr)
	}
	return s.fileAttempts(now)
}

// fileAttempts tries the file sources in order and returns the first
// readable one, even when it holds no attempts. Only open/read errors
// accumulate; the first source that opens is authoritative.
func (s System) fileAttempts(now time.Time) ([]core.SSHAttempt, error) {
	attempts, authErr := s.authLogAttempts(now)
	if authErr == nil {
		return attempts, nil
	}
	attempts, wtmpErr := s.wtmpAttempts()
	if wtmpErr == nil {
		return attempts, nil
	}
	return nil, fmt.Errorf("auth log (%v) and wtmp (%v)", authErr, wtmpErr)
}

// authLogAttempts reads the first readable auth log under the root
// prefix and parses its trailing window. A missing auth.log on a Red
// Hat host is expected: secure is its spelling. When neither opens,
// the error names both failures.
func (s System) authLogAttempts(now time.Time) ([]core.SSHAttempt, error) {
	var errs []string
	for _, name := range []string{authLogPath, secureLogPath} {
		data, err := tailLines(s.rootPath(name), sshAttemptsTailBudget)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		return attemptsFromAuthLog(data, now), nil
	}
	return nil, fmt.Errorf("no readable auth log (%s)", strings.Join(errs, "; "))
}

// wtmpAttempts reads the trailing window of wtmp under the root
// prefix and distills its remote logins. wtmp records only successful
// logins, so this source is the last resort — it can enrich the
// listing but never report a failure.
func (s System) wtmpAttempts() ([]core.SSHAttempt, error) {
	data, err := tailRecords(s.rootPath(wtmpPath), sshAttemptsTailBudget)
	if err != nil {
		return nil, fmt.Errorf("read wtmp: %w", err)
	}
	return attemptsFromWtmp(data), nil
}

// readTail reads at most the last budget bytes of path and returns
// them with the file offset the read began at. offset > 0 means the
// read started mid-file, so the first entry may be partial.
func readTail(path string, budget int64) (data []byte, offset int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if start := fi.Size() - budget; start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return nil, 0, err
		}
		offset = start
	}
	data, err = io.ReadAll(io.LimitReader(f, budget))
	if err != nil {
		return nil, 0, err
	}
	return data, offset, nil
}

// tailLines reads the trailing budget bytes of a line-oriented log and
// drops the leading partial line when the read started mid-file: a cut
// through the middle of a line must never feed a truncated syslog
// record to the parser.
func tailLines(path string, budget int64) ([]byte, error) {
	data, offset, err := readTail(path, budget)
	if err != nil {
		return nil, err
	}
	if offset == 0 {
		return data, nil
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return data[i+1:], nil
	}
	return nil, nil // the window holds one partial line and nothing else
}

// tailRecords reads the trailing budget bytes of a fixed-record file
// and drops the leading partial record when the read started mid-file.
// Record boundaries sit at multiples of utmpRecordSize from the file
// start, so the skip is derived from the read's starting offset — not
// from the buffer length, which would misalign a torn trailing write.
// A trailing partial record is left for the parser to ignore.
func tailRecords(path string, budget int64) ([]byte, error) {
	data, offset, err := readTail(path, budget)
	if err != nil {
		return nil, err
	}
	if skip := int((utmpRecordSize - offset%utmpRecordSize) % utmpRecordSize); skip > 0 {
		if skip >= len(data) {
			return nil, nil
		}
		data = data[skip:]
	}
	return data, nil
}

// attemptsFromAuthLog parses traditional syslog auth-log bytes into
// attempts, newest first, capped at sshAttemptsLimit. now anchors the
// year, which the stamp omits. Pure, so the grammar and the ordering
// are table-tested without touching a real log.
func attemptsFromAuthLog(data []byte, now time.Time) []core.SSHAttempt {
	var attempts []core.SSHAttempt
	for _, line := range bytes.Split(data, []byte("\n")) {
		attempt, ok := parseAuthLogLine(string(line), now)
		if !ok {
			continue
		}
		attempts = append(attempts, attempt)
	}
	return newestAttempts(attempts)
}

// parseAuthLogLine decodes one traditional syslog line:
//
//	Jan  2 15:04:05 host sshd[1234]: Accepted publickey for alice from ...
//
// The first three fields are the stamp, the fourth the host, and the
// fifth the program tag; everything after the tag is the message,
// which reuses ParseSSHAttemptLine's grammar. Only the OpenSSH family
// is accepted — sshd, sshd-session, sshd-auth — and the pid brackets
// are optional. Every other line, including non-sshd programs and
// malformed syslog, is not an attempt.
func parseAuthLogLine(line string, now time.Time) (core.SSHAttempt, bool) {
	fields := strings.Fields(line)
	if len(fields) < 6 || !sshdProgramTag(fields[4]) {
		return core.SSHAttempt{}, false
	}
	stamp, ok := parseAuthLogTime(fields[0]+" "+fields[1]+" "+fields[2], now)
	if !ok {
		return core.SSHAttempt{}, false
	}
	attempt, ok := ParseSSHAttemptLine(strings.Join(fields[5:], " "))
	if !ok {
		return core.SSHAttempt{}, false
	}
	attempt.Time = stamp
	return attempt, true
}

// sshdProgramTag reports whether a syslog program tag names an OpenSSH
// authentication process. The tag is `prog:` or `prog[pid]:`; pid
// brackets are optional because log relays sometimes drop them.
func sshdProgramTag(tag string) bool {
	tag = strings.TrimSuffix(tag, ":")
	if i := strings.IndexByte(tag, '['); i >= 0 {
		if !strings.HasSuffix(tag, "]") {
			return false
		}
		tag = tag[:i]
	}
	switch tag {
	case "sshd", "sshd-session", "sshd-auth":
		return true
	}
	return false
}

// authLogTimeLayout is the traditional syslog stamp, which carries no
// year.
const authLogTimeLayout = "Jan _2 15:04:05"

// authLogFutureSlack is how far a year-less stamp may sit in the
// future before it is read as last year's: syslog writes local wall
// time, so a December line read just after New Year is last year's,
// not eleven months ahead.
const authLogFutureSlack = 24 * time.Hour

// parseAuthLogTime turns a year-less syslog stamp into an instant
// anchored to now's year and location, stepping back one year when
// that lands more than a day in the future (the Dec/Jan rollover).
// Returns false for anything that is not a syslog stamp.
func parseAuthLogTime(stamp string, now time.Time) (time.Time, bool) {
	t, err := time.ParseInLocation(authLogTimeLayout, stamp, now.Location())
	if err != nil {
		return time.Time{}, false
	}
	t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())
	if t.After(now.Add(authLogFutureSlack)) {
		t = t.AddDate(-1, 0, 0)
	}
	return t, true
}

// attemptsFromWtmp decodes wtmp bytes into best-effort SSH attempts:
// USER_PROCESS records with a non-empty host field, newest first,
// capped. wtmp records successful logins only — a failed attempt
// never reaches the file — so every row is Success. The host field
// cannot distinguish sshd from another remote login service, hence
// "best-effort": the row is a remote login wtmp cannot attribute more
// precisely. Pure, so the binary format is table-tested against
// crafted records.
func attemptsFromWtmp(data []byte) []core.SSHAttempt {
	var attempts []core.SSHAttempt
	for off := 0; off+utmpRecordSize <= len(data); off += utmpRecordSize {
		rec := data[off : off+utmpRecordSize]
		if binary.NativeEndian.Uint16(rec[utmpOffType:utmpOffType+2]) != utmpTypeUserProcess {
			continue
		}
		from := utmpString(rec[utmpOffHost : utmpOffHost+256])
		if from == "" {
			continue // local session: no remote source
		}
		sec := int32(binary.NativeEndian.Uint32(rec[utmpOffSec : utmpOffSec+4]))
		usec := int32(binary.NativeEndian.Uint32(rec[utmpOffUsec : utmpOffUsec+4]))
		attempts = append(attempts, core.SSHAttempt{
			Time:     time.Unix(int64(sec), int64(usec)*1000),
			User:     utmpString(rec[utmpOffUser : utmpOffUser+32]),
			SourceIP: from,
			Success:  true,
		})
	}
	return newestAttempts(attempts)
}

// newestAttempts reverses a file-order (oldest first) listing into the
// port's newest-first order and caps it. The cap drops the OLDEST
// attempts, matching attemptsFromEntries.
func newestAttempts(attempts []core.SSHAttempt) []core.SSHAttempt {
	out := make([]core.SSHAttempt, 0, min(len(attempts), sshAttemptsLimit))
	for i := len(attempts) - 1; i >= 0 && len(out) < sshAttemptsLimit; i-- {
		out = append(out, attempts[i])
	}
	return out
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
