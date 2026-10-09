package collectors

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-systemd/v22/sdjournal"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// TestMapJournalEntry is the pure-mapper table: hand-built
// sdjournal.JournalEntry values, no journal, no fixtures
// (ARCHITECTURE.md §10 parser purity).
func TestMapJournalEntry(t *testing.T) {
	// 2026-10-08 12:34:56.789012 UTC, held as microseconds since the
	// epoch the way sdjournal.JournalEntry.RealtimeTimestamp does.
	wantTime := time.Date(2026, 10, 8, 12, 34, 56, 789012000, time.UTC)
	epochUsec := wantTime.UnixMicro()

	tests := []struct {
		name string
		raw  *sdjournal.JournalEntry
		want core.JournalEntry
	}{
		{
			name: "full entry maps time, unit, priority and message",
			raw: &sdjournal.JournalEntry{
				RealtimeTimestamp: uint64(epochUsec),
				Fields: map[string]string{
					"_SYSTEMD_UNIT": "sshd.service",
					"PRIORITY":      "3",
					"MESSAGE":       "Accepted publickey for root",
				},
			},
			want: core.JournalEntry{
				Time:     wantTime,
				Unit:     "sshd.service",
				Priority: 3,
				Message:  "Accepted publickey for root",
			},
		},
		{
			name: "missing PRIORITY reads as info",
			raw: &sdjournal.JournalEntry{
				RealtimeTimestamp: uint64(epochUsec),
				Fields: map[string]string{
					"_SYSTEMD_UNIT": "cron.service",
					"MESSAGE":       "tick",
				},
			},
			want: core.JournalEntry{Time: wantTime, Unit: "cron.service", Priority: 6, Message: "tick"},
		},
		{
			name: "non-numeric PRIORITY reads as info",
			raw: &sdjournal.JournalEntry{
				RealtimeTimestamp: uint64(epochUsec),
				Fields: map[string]string{
					"_SYSTEMD_UNIT": "weird.service",
					"PRIORITY":      "high",
					"MESSAGE":       "m",
				},
			},
			want: core.JournalEntry{Time: wantTime, Unit: "weird.service", Priority: 6, Message: "m"},
		},
		{
			name: "kernel entry without a unit falls back to the transport",
			raw: &sdjournal.JournalEntry{
				RealtimeTimestamp: uint64(epochUsec),
				Fields: map[string]string{
					"_TRANSPORT": "kernel",
					"PRIORITY":   "4",
					"MESSAGE":    "usb 1-2: new device",
				},
			},
			want: core.JournalEntry{Time: wantTime, Unit: "kernel", Priority: 4, Message: "usb 1-2: new device"},
		},
		{
			name: "neither unit nor transport leaves the unit empty",
			raw: &sdjournal.JournalEntry{
				RealtimeTimestamp: uint64(epochUsec),
				Fields: map[string]string{
					"MESSAGE": "entry from nowhere",
				},
			},
			want: core.JournalEntry{Time: wantTime, Unit: "", Priority: 6, Message: "entry from nowhere"},
		},
		{
			name: "missing MESSAGE and zero timestamp still map",
			raw: &sdjournal.JournalEntry{
				RealtimeTimestamp: 0,
				Fields: map[string]string{
					"_SYSTEMD_UNIT": "quiet.service",
				},
			},
			want: core.JournalEntry{Time: time.UnixMicro(0), Unit: "quiet.service", Priority: 6, Message: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapJournalEntry(tt.raw)
			// time.Time equality is location-sensitive, so the
			// instant is compared with Equal rather than ==.
			if !got.Time.Equal(tt.want.Time) {
				t.Errorf("Time = %v, want %v", got.Time, tt.want.Time)
			}
			if got.Unit != tt.want.Unit {
				t.Errorf("Unit = %q, want %q", got.Unit, tt.want.Unit)
			}
			if got.Priority != tt.want.Priority {
				t.Errorf("Priority = %d, want %d", got.Priority, tt.want.Priority)
			}
			if got.Message != tt.want.Message {
				t.Errorf("Message = %q, want %q", got.Message, tt.want.Message)
			}
		})
	}
}

// TestJournalRequiresLiveSystem pins the fixture guard: a System rooted
// at testdata must refuse rather than touch any journal — like D-Bus,
// the journal has no root-prefix fixtures to read from.
func TestJournalRequiresLiveSystem(t *testing.T) {
	sys := New("testdata")

	entries, cursor, err := sys.Tail("", 5)
	if err == nil {
		t.Fatalf("Tail: expected live-only error, got %d entries cursor %q", len(entries), cursor)
	}
	if !strings.Contains(err.Error(), "journal reads require the live system journal") {
		t.Errorf("Tail error %q should mention the live journal requirement", err)
	}

	entries, cursor, err = sys.After("", "s=abc", 5)
	if err == nil {
		t.Fatalf("After: expected live-only error, got %d entries cursor %q", len(entries), cursor)
	}
	if !strings.Contains(err.Error(), "journal reads require the live system journal") {
		t.Errorf("After error %q should mention the live journal requirement", err)
	}
}

// TestJournalGuardOrder pins that the live guard fires before the
// limit guard: a fixture-rooted System refuses even a zero-limit read
// rather than "succeeding" at nothing.
func TestJournalGuardOrder(t *testing.T) {
	if _, _, err := New("testdata").Tail("", 0); err == nil {
		t.Fatal("zero limit must not bypass the live-system refusal")
	}
}

// journalDirExists reports whether this box has any journal directory
// journald would have written — without one there is nothing to tail,
// live guard or not.
func journalDirExists() bool {
	for _, dir := range []string{"/var/log/journal", "/run/log/journal"} {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// TestJournalTailLiveProbe is the live sdjournal probe: on a box with
// journal directories, Tail must succeed. It deliberately does NOT
// assert a non-empty result — a reader outside the systemd-journal
// group legitimately sees only their own session's entries, which can
// be zero (core.JournalEntry documents the access model).
func TestJournalTailLiveProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: reads the real system journal")
	}
	if !journalDirExists() {
		t.Skip("no journal directories on this box: nothing to probe")
	}

	sys := New("/")

	entries, cursor, err := sys.Tail("", 5)
	if err != nil {
		t.Fatalf("live Tail failed: %v", err)
	}
	if len(entries) > 5 {
		t.Errorf("Tail returned %d entries, want at most 5", len(entries))
	}
	if len(entries) > 0 && cursor == "" {
		t.Error("Tail returned entries but no head cursor")
	}
	if len(entries) == 0 && cursor != "" {
		t.Error("Tail returned no entries but a cursor: the head cursor must be \"\"")
	}

	// The zero-limit guard returns before any journal is opened.
	if entries, cursor, err := sys.Tail("", 0); err != nil || len(entries) != 0 || cursor != "" {
		t.Errorf("zero-limit Tail = (%d, %q, %v), want nothing without error", len(entries), cursor, err)
	}

	// A malformed cursor is as good as a stale one: the seek refuses it
	// and the reader reports the port's re-Tail signal.
	_, _, err = sys.After("", "not-a-cursor", 5)
	if !errors.Is(err, core.ErrStaleCursor) {
		t.Errorf("malformed cursor must surface as core.ErrStaleCursor, got %v", err)
	}
}

// TestErrStaleCursorIsSentinel keeps the exported sentinel honest: the
// screen's stale-recovery path depends on errors.Is finding it inside
// the collector's wrapped errors.
func TestErrStaleCursorIsSentinel(t *testing.T) {
	if !errors.Is(core.ErrStaleCursor, core.ErrStaleCursor) {
		t.Fatal("sentinel must match itself")
	}
}
