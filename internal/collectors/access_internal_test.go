package collectors

import (
	"fmt"
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// TestAttemptsFromEntries pins the distillation of journal entries
// into attempts: newest first (the audit reading order, opposite of
// Tail's oldest-first), the entry's timestamp stamped on each
// attempt, and the chatter lines sshd writes between its
// authentication lines left out.
func TestAttemptsFromEntries(t *testing.T) {
	base := time.Unix(1791564800, 0)
	entries := []core.JournalEntry{
		// Oldest first, the order Tail returns.
		{Time: base, Unit: sshdUnit, Message: "Server listening on 0.0.0.0 port 22."},
		{Time: base.Add(1 * time.Second), Unit: sshdUnit, Message: "Accepted publickey for alice from 192.168.1.50 port 51422 ssh2"},
		{Time: base.Add(2 * time.Second), Unit: sshdUnit, Message: "Received disconnect from 203.0.113.7 port 41000:11: Bye Bye [preauth]"},
		{Time: base.Add(3 * time.Second), Unit: sshdUnit, Message: "Failed password for invalid user admin from 203.0.113.7 port 41002 ssh2"},
		{Time: base.Add(4 * time.Second), Unit: sshdUnit, Message: "Failed password for root from 203.0.113.7 port 41000 ssh2"},
	}

	got := attemptsFromEntries(entries)
	want := []core.SSHAttempt{
		{Time: base.Add(4 * time.Second), User: "root", SourceIP: "203.0.113.7"},
		{Time: base.Add(3 * time.Second), User: "admin", SourceIP: "203.0.113.7"},
		{Time: base.Add(1 * time.Second), User: "alice", SourceIP: "192.168.1.50", Success: true},
	}
	if len(got) != len(want) {
		t.Fatalf("attemptsFromEntries = %d attempts, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("attempt %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestAttemptsFromEntriesCapsAtLimit pins the bound: a journal window
// holding more attempts than the cap returns the NEWEST cap — the
// walk starts at the head and stops the moment the listing is full,
// so the oldest overflow drops off, never the freshest round.
func TestAttemptsFromEntriesCapsAtLimit(t *testing.T) {
	base := time.Unix(1791564800, 0)
	entries := make([]core.JournalEntry, sshAttemptsLimit+50)
	for i := range entries {
		entries[i] = core.JournalEntry{
			Time:    base.Add(time.Duration(i) * time.Second),
			Unit:    sshdUnit,
			Message: fmt.Sprintf("Failed password for user%d from 203.0.113.7 port %d ssh2", i, 40000+i),
		}
	}

	got := attemptsFromEntries(entries)
	if len(got) != sshAttemptsLimit {
		t.Fatalf("attemptsFromEntries = %d attempts, want the cap %d", len(got), sshAttemptsLimit)
	}
	// The newest entry (index len-1) must lead the listing; the
	// oldest 50 overflow entries must be the ones dropped.
	wantNewest := fmt.Sprintf("user%d", len(entries)-1)
	if got[0].User != wantNewest {
		t.Errorf("newest attempt = %q, want %q — the cap must keep the freshest round", got[0].User, wantNewest)
	}
	if got[len(got)-1].User != "user50" {
		t.Errorf("oldest kept attempt = %q, want user50 — the overflow must drop from the old end", got[len(got)-1].User)
	}
}

// TestAttemptsFromEntriesEmpty pins the quiet-box shape: a window
// with nothing readable (the unprivileged reality of a reader outside
// the systemd-journal group) distills to an empty listing, no error,
// no nil-deref downstream.
func TestAttemptsFromEntriesEmpty(t *testing.T) {
	got := attemptsFromEntries(nil)
	if len(got) != 0 {
		t.Errorf("attemptsFromEntries(nil) = %d attempts, want 0", len(got))
	}
	got = attemptsFromEntries([]core.JournalEntry{{Time: time.Now(), Unit: sshdUnit, Message: "Server listening on 0.0.0.0 port 22."}})
	if len(got) != 0 {
		t.Errorf("chatter-only window = %d attempts, want 0", len(got))
	}
}
