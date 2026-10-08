// journal.go reads the systemd journal through the sdjournal C API —
// ARCHITECTURE.md §8's "no journctl shells" rule for the Logs module.
//
// Build note: the go-systemd sdjournal package is cgo, but it resolves
// libsystemd with dlopen at RUN time (github.com/coreos/go-systemd/
// v22/internal/dlopen), so building needs only the systemd headers
// and linking needs no -lsystemd; running needs libsystemd.so on the
// box, which any systemd host has.
package collectors

import (
	"fmt"
	"strconv"
	"time"

	"github.com/coreos/go-systemd/v22/sdjournal"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// Compile-time proof that System satisfies the Logs read port.
var _ core.JournalReader = System{}

// journalPriorityInfo is the priority assumed when an entry carries no
// parseable PRIORITY field: journald explicitly allows entries without
// one, and syslog info (6) is the least surprising reading.
const journalPriorityInfo = 6

// Tail returns up to limit entries ending at the journal head for unit
// ("" = all units), oldest first, plus the cursor of the newest entry
// returned ("" when the journal holds nothing readable — the port's
// "start at the tail" case). The journal is opened per call: the 1 s
// follow cadence (ARCHITECTURE.md §3) makes an always-open handle not
// worth the state, and a per-call open keeps System a value with no
// lifecycle to manage — the same trade Units() makes about its bus
// dial.
//
// Like Units(), there are no root-prefix journal fixtures: a System
// that is not live refuses instead of guessing where entries would
// come from.
func (s System) Tail(unit string, limit int) ([]core.JournalEntry, string, error) {
	if !s.IsLive() {
		return nil, "", fmt.Errorf("collectors: journal reads require the live system journal: System is rooted at %q", s.root)
	}
	if limit <= 0 {
		return nil, "", nil
	}

	j, err := sdjournal.NewJournal()
	if err != nil {
		return nil, "", fmt.Errorf("collectors: open journal: %w", err)
	}
	// Close's error can only be a failed dlopen lookup, impossible
	// once the open above succeeded and cached the handle.
	defer func() { _ = j.Close() }()
	if err := matchUnit(j, unit); err != nil {
		return nil, "", err
	}
	if err := j.SeekTail(); err != nil {
		return nil, "", fmt.Errorf("collectors: seek journal tail: %w", err)
	}

	// SeekTail parks the read pointer past the newest entry, so
	// Previous walks back one entry at a time; the first entry read is
	// the newest, hence the head cursor.
	raw := make([]*sdjournal.JournalEntry, 0, limit)
	head := ""
	for len(raw) < limit {
		n, err := j.Previous()
		if err != nil {
			return nil, "", fmt.Errorf("collectors: step journal backwards: %w", err)
		}
		if n == 0 {
			break // walked past the oldest readable entry
		}
		entry, err := j.GetEntry()
		if err != nil {
			return nil, "", fmt.Errorf("collectors: read journal entry: %w", err)
		}
		if head == "" {
			head = entry.Cursor
		}
		raw = append(raw, entry)
	}

	// Previous yields newest-first; the port promises oldest-first.
	for i, k := 0, len(raw)-1; i < k; i, k = i+1, k-1 {
		raw[i], raw[k] = raw[k], raw[i]
	}
	entries := make([]core.JournalEntry, len(raw))
	for i, entry := range raw {
		entries[i] = mapJournalEntry(entry)
	}
	return entries, head, nil
}

// After returns up to limit entries strictly after cursor for unit
// ("" = all units), oldest first, plus the cursor of the newest entry
// returned — the new head once the caller consumes them. The port's
// "" cursor means "no position yet", so it degrades to a bounded Tail
// (the empty-journal and stale-recovery paths both need that). When
// nothing follows the cursor the input cursor comes back unchanged: no
// new head exists.
//
// Stale cursors — the entry was rotated or vacuumed out between
// rounds — surface as core.ErrStaleCursor (wrapped), the port's signal
// that the caller should re-Tail. The seek/next/test sequence is the
// same one journalctl uses: sd_journal_seek_cursor parks BEFORE the
// entry the cursor names, one Next lands on it, and TestCursor proves
// the landing really is that entry rather than the nearest survivor of
// a rotation.
func (s System) After(unit string, cursor string, limit int) ([]core.JournalEntry, string, error) {
	if !s.IsLive() {
		return nil, "", fmt.Errorf("collectors: journal reads require the live system journal: System is rooted at %q", s.root)
	}
	if cursor == "" {
		return s.Tail(unit, limit)
	}
	if limit <= 0 {
		return nil, cursor, nil
	}

	j, err := sdjournal.NewJournal()
	if err != nil {
		return nil, "", fmt.Errorf("collectors: open journal: %w", err)
	}
	defer func() { _ = j.Close() }()
	if err := matchUnit(j, unit); err != nil {
		return nil, "", err
	}
	if err := j.SeekCursor(cursor); err != nil {
		return nil, "", fmt.Errorf("%w: seek: %v", core.ErrStaleCursor, err)
	}
	// Land on the entry the cursor names; the entry itself is not
	// returned — the port says "strictly after".
	if _, err := j.Next(); err != nil {
		return nil, "", fmt.Errorf("collectors: step journal: %w", err)
	}
	if err := j.TestCursor(cursor); err != nil {
		return nil, "", fmt.Errorf("%w: %v", core.ErrStaleCursor, err)
	}

	head := cursor
	raw := make([]*sdjournal.JournalEntry, 0, limit)
	for len(raw) < limit {
		n, err := j.Next()
		if err != nil {
			return nil, "", fmt.Errorf("collectors: step journal: %w", err)
		}
		if n == 0 {
			break // at the head: nothing newer exists yet
		}
		entry, err := j.GetEntry()
		if err != nil {
			return nil, "", fmt.Errorf("collectors: read journal entry: %w", err)
		}
		head = entry.Cursor
		raw = append(raw, entry)
	}

	entries := make([]core.JournalEntry, len(raw))
	for i, entry := range raw {
		entries[i] = mapJournalEntry(entry)
	}
	return entries, head, nil
}

// matchUnit narrows the journal to one systemd unit's entries. The
// screen-side unit filter stays client-side for now; the parameter
// exists so the port is ready for a per-unit journal view
// (ARCHITECTURE.md §6's logs → filtered unit view).
func matchUnit(j *sdjournal.Journal, unit string) error {
	if unit == "" {
		return nil
	}
	if err := j.AddMatch(sdjournal.SD_JOURNAL_FIELD_SYSTEMD_UNIT + "=" + unit); err != nil {
		return fmt.Errorf("collectors: match unit %q: %w", unit, err)
	}
	return nil
}

// mapJournalEntry converts one raw sdjournal entry into the core
// model. It is pure — no journal, no clock — so tests exercise it with
// hand-built sdjournal.JournalEntry values and no journal at all.
//
// Naming surprise worth knowing: GetEntry lifts __REALTIME_TIMESTAMP
// and __CURSOR out of the field map into typed struct fields
// (RealtimeTimestamp holds microseconds since the epoch, Cursor the
// opaque position string), so the time needs no string parsing — the
// same kind of field-name drift m4a hit with dbus.UnitFile.Type.
func mapJournalEntry(raw *sdjournal.JournalEntry) core.JournalEntry {
	entry := core.JournalEntry{
		Time:    time.UnixMicro(int64(raw.RealtimeTimestamp)),
		Unit:    raw.Fields[sdjournal.SD_JOURNAL_FIELD_SYSTEMD_UNIT],
		Message: raw.Fields[sdjournal.SD_JOURNAL_FIELD_MESSAGE],
	}
	// Kernel-transport entries (and a few others) carry no
	// _SYSTEMD_UNIT; the transport name stands in so a rendered log
	// line never shows an empty middle field.
	if entry.Unit == "" {
		entry.Unit = raw.Fields[sdjournal.SD_JOURNAL_FIELD_TRANSPORT]
	}
	entry.Priority = journalPriority(raw)
	return entry
}

// journalPriority parses the PRIORITY field, defaulting to syslog info
// when it is absent or not a number — journald allows entries without
// a priority.
func journalPriority(raw *sdjournal.JournalEntry) int {
	p, err := strconv.Atoi(raw.Fields[sdjournal.SD_JOURNAL_FIELD_PRIORITY])
	if err != nil {
		return journalPriorityInfo
	}
	return p
}
