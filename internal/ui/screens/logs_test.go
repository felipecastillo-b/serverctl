package screens

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// afterRound is one scripted strictly-cursor After result.
type afterRound struct {
	entries []core.JournalEntry
	cursor  string
	err     error
}

// fakeJournal scripts JournalReader rounds and records how the screen
// drives it — every cursor and limit passed to After, and the Tail and
// After call counts.
type fakeJournal struct {
	tail       []core.JournalEntry
	tailCursor string
	tailErr    error

	// afterQ scripts the strictly-cursor rounds, front first; an empty
	// queue means "nothing new": After echoes the caller's cursor.
	afterQ []afterRound

	tailCalls    int
	afterCalls   int
	tailLimits   []int
	afterLimits  []int
	afterCursors []string
}

func (f *fakeJournal) Tail(unit string, limit int) ([]core.JournalEntry, string, error) {
	f.tailCalls++
	f.tailLimits = append(f.tailLimits, limit)
	return f.tail, f.tailCursor, f.tailErr
}

func (f *fakeJournal) After(unit string, cursor string, limit int) ([]core.JournalEntry, string, error) {
	f.afterCalls++
	f.afterLimits = append(f.afterLimits, limit)
	f.afterCursors = append(f.afterCursors, cursor)
	if cursor == "" {
		// The port contract: "" behaves like a bounded tail. Serve the
		// tail script without counting it as a Tail call.
		return f.tail, f.tailCursor, f.tailErr
	}
	if len(f.afterQ) == 0 {
		return nil, cursor, nil // nothing new: the head is where we are
	}
	r := f.afterQ[0]
	f.afterQ = f.afterQ[1:]
	return r.entries, r.cursor, r.err
}

// Compile-time proof that the fake satisfies the m4c port, exactly
// like collectors.System does.
var _ core.JournalReader = (*fakeJournal)(nil)

// logFixture is a small journal tail with varied units, priorities and
// messages.
var logFixture = []core.JournalEntry{
	{Time: time.Date(2026, 10, 8, 9, 0, 1, 0, time.UTC), Unit: "sshd.service", Priority: 4, Message: "Accepted publickey for alice"},
	{Time: time.Date(2026, 10, 8, 9, 0, 2, 0, time.UTC), Unit: "nginx.service", Priority: 3, Message: "worker process exited"},
	{Time: time.Date(2026, 10, 8, 9, 0, 3, 0, time.UTC), Unit: "kernel", Priority: 6, Message: "usb 1-2: new device"},
}

// genEntries builds n monotonic entries with messages prefix+index,
// one millisecond apart — bulk entries for the buffer tests.
func genEntries(n int, prefix string, startIndex int) []core.JournalEntry {
	base := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	out := make([]core.JournalEntry, n)
	for i := range out {
		out[i] = core.JournalEntry{
			Time:     base.Add(time.Duration(startIndex+i) * time.Millisecond),
			Unit:     "gen.service",
			Priority: 6,
			Message:  fmt.Sprintf("%s%d", prefix, startIndex+i),
		}
	}
	return out
}

// newLogsScreen builds the logs screen over the given fake and drives
// the first tail round synchronously. Init batches [tail, tick] and
// that batch is lazy — executing it only yields the legs — so the
// fixture runs the tail leg; the 1 s timer leg stays runtime
// territory, executing it here would stall the test (m4b's
// tea.Batch-laziness discovery, at the Logs cadence).
func newLogsScreen(t *testing.T, reader core.JournalReader) *logs {
	t.Helper()
	s := newLogs(reader, theme.Dark())
	cmd := s.Init()
	if cmd == nil {
		t.Fatal("Init must tail and start the cadence")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("Init must batch the tail and the cadence tick, got %T len %d", batch, len(batch))
	}
	updated, _ := s.Update(batch[0]())
	s, ok = updated.(*logs)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if s.collectErr != nil {
		t.Fatalf("fixture tail: err=%v", s.collectErr)
	}
	return s
}

// driveTick runs one follow round synchronously: the tick re-arms
// [followUp, tick], and only the followUp leg is executed — the timer
// leg would block the full interval.
func driveTick(s *logs) tea.Cmd {
	_, cmd := s.Update(logsTickMsg{})
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		panic(fmt.Sprintf("tick must re-arm followUp and the next tick, got %T len %d", batch, len(batch)))
	}
	_, cmd = s.Update(batch[0]())
	return cmd
}

func TestLogsScreenRendersTail(t *testing.T) {
	fake := &fakeJournal{tail: logFixture, tailCursor: "c3"}
	s := newLogsScreen(t, fake)

	if s.cursor != "c3" {
		t.Errorf("cursor = %q, want the tail's head cursor c3", s.cursor)
	}
	if !slices.Equal(fake.tailLimits, []int{logsTailCount}) {
		t.Errorf("tail limit = %v, want %d", fake.tailLimits, logsTailCount)
	}

	view := s.View(100, 24)
	for _, want := range []string{
		"sshd.service", "Accepted publickey for alice",
		"nginx.service", "worker process exited",
		"kernel", "usb 1-2: new device",
		"Oct 08", // the per-line `Jan 02 15:04:05` layout, rendered
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "collecting...") {
		t.Error("a loaded screen must not still say collecting...")
	}
	if !strings.Contains(view, "3 lines · following") {
		t.Errorf("status must summarize the tail:\n%s", view)
	}
}

func TestLogsTickAppendsAndTrims(t *testing.T) {
	fake := &fakeJournal{
		tail:       genEntries(600, "m", 0),
		tailCursor: "c600",
		afterQ: []afterRound{
			{entries: genEntries(500, "m", 600), cursor: "c1100"},
		},
	}
	s := newLogsScreen(t, fake)
	if len(s.entries) != 600 {
		t.Fatalf("fixture tail must hold 600 entries, got %d", len(s.entries))
	}

	driveTick(s)

	if len(s.entries) != logsBufferCap {
		t.Fatalf("buffer must trim to the %d cap, got %d", logsBufferCap, len(s.entries))
	}
	// The oldest 100 entries fell off: m0..m99 gone, m100 survives.
	if got := s.entries[0].Message; got != "m100" {
		t.Errorf("oldest survivor = %q, want m100", got)
	}
	if got := s.entries[len(s.entries)-1].Message; got != "m1099" {
		t.Errorf("newest = %q, want m1099", got)
	}
	if s.cursor != "c1100" {
		t.Errorf("cursor = %q, want c1100", s.cursor)
	}
	if !slices.Equal(fake.afterCursors, []string{"c600"}) {
		t.Errorf("After cursors = %v, want [c600]", fake.afterCursors)
	}
	if !slices.Equal(fake.afterLimits, []int{logsFollowCount}) {
		t.Errorf("After limits = %v, want %d", fake.afterLimits, logsFollowCount)
	}
	if len(s.view) != logsBufferCap {
		t.Errorf("view must show the trimmed buffer, got %d lines", len(s.view))
	}
}

func TestLogsFollowPauseAndResume(t *testing.T) {
	fake := &fakeJournal{tail: genEntries(50, "l", 0), tailCursor: "c50"}
	s := newLogsScreen(t, fake)

	// A fresh tail starts at the bottom and follows.
	s.View(80, 20)
	if !s.follow || !s.vp.AtBottom() {
		t.Fatalf("fresh tail must follow at the bottom: follow=%v atBottom=%v", s.follow, s.vp.AtBottom())
	}

	// Scrolling up pauses following.
	updated, _, handled := s.UpdateKey(keyMsg("up"))
	s = updated.(*logs)
	if !handled {
		t.Fatal("up must be claimed by the viewport")
	}
	if s.follow {
		t.Error("scrolling up must pause following")
	}
	if s.vp.AtBottom() {
		t.Error("the viewport must have moved off the bottom")
	}
	if out := s.View(80, 20); !strings.Contains(out, "paused") {
		t.Errorf("status must say paused:\n%s", out)
	}

	// New entries arrive while paused: the viewport keeps its offset.
	fake.afterQ = []afterRound{{entries: genEntries(3, "l", 50), cursor: "c53"}}
	driveTick(s)
	s.View(80, 20)
	if s.follow || s.vp.AtBottom() {
		t.Errorf("a paused tail must stay put on new entries: follow=%v atBottom=%v", s.follow, s.vp.AtBottom())
	}

	// f resumes: the next view snaps back to the newest line.
	updated, _, handled = s.UpdateKey(keyMsg("f"))
	s = updated.(*logs)
	if !handled || !s.follow {
		t.Fatal("f must resume following")
	}
	out := s.View(80, 20)
	if !s.vp.AtBottom() {
		t.Error("resuming must scroll back to the bottom")
	}
	if !strings.Contains(out, "following") {
		t.Errorf("status must say following:\n%s", out)
	}
}

func TestLogsFilter(t *testing.T) {
	s := newLogsScreen(t, &fakeJournal{tail: logFixture, tailCursor: "c3"})

	// "sshd" matches a unit, "key" would match a message: the query
	// spans unit and message.
	if _, _, handled := s.UpdateKey(keyMsg("/")); !handled || !s.filtering {
		t.Fatal("filter mode did not open")
	}
	for _, r := range "sshd" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if len(s.view) != 1 || s.view[0].Unit != "sshd.service" {
		t.Fatalf("filter 'sshd': %d view entries", len(s.view))
	}
	if out := s.View(100, 24); !strings.Contains(out, `filter "sshd"`) {
		t.Errorf("status line must show the active filter:\n%s", out)
	}

	// Filter mode is exclusive: q belongs to the input, not the shell.
	updated, _, handled := s.UpdateKey(keyMsg("q"))
	s = updated.(*logs)
	if !handled {
		t.Fatal("filter mode must swallow every key")
	}
	// Undo the probe's keystroke so the query stays "sshd".
	s.UpdateKey(tea.KeyMsg{Type: tea.KeyBackspace})

	// esc closes the input but keeps the applied filter.
	if _, _, handled := s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled || s.filtering {
		t.Fatal("esc must close filter mode")
	}
	if len(s.view) != 1 {
		t.Fatalf("closed filter must stay applied: %d view entries", len(s.view))
	}

	// enter closes too; a no-match query empties the view without the
	// empty-journal hint — the buffer is not empty, the filter is.
	s.UpdateKey(keyMsg("/"))
	s.filter.SetValue("")
	for _, r := range "zzz" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if len(s.view) != 0 {
		t.Fatalf("no-match filter must empty the view, got %d", len(s.view))
	}
	out := s.View(100, 24)
	if !strings.Contains(out, "0 lines") {
		t.Errorf("status must count zero view lines:\n%s", out)
	}
	if strings.Contains(out, "systemd-journal group membership") {
		t.Error("a no-match filter is not an empty journal: no membership hint")
	}
	s.UpdateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if s.filtering {
		t.Fatal("enter must close filter mode")
	}
}

func TestLogsStatusStates(t *testing.T) {
	// Before the first round lands: collecting.
	fresh := newLogs(&fakeJournal{}, theme.Dark())
	if out := fresh.View(100, 24); !strings.Contains(out, "collecting...") {
		t.Errorf("unloaded screen must say collecting...:\n%s", out)
	}

	// A failed round shows the error.
	failing := newLogs(&fakeJournal{tailErr: errors.New("no journal")}, theme.Dark())
	updated, _ := failing.Update(failing.tail()())
	failing = updated.(*logs)
	if out := failing.View(100, 24); !strings.Contains(out, "n/a (no journal)") {
		t.Errorf("failed round must show n/a with the error:\n%s", out)
	}

	// An empty journal is not an error: the honest hint explains
	// journald's unprivileged visibility.
	empty := newLogsScreen(t, &fakeJournal{})
	out := empty.View(100, 24)
	if !strings.Contains(out, "0 lines · following") {
		t.Errorf("empty tail must still summarize:\n%s", out)
	}
	if !strings.Contains(out, "systemd-journal group membership") {
		t.Errorf("empty tail must explain the access model:\n%s", out)
	}
}

func TestLogsStaleCursorRetriesTail(t *testing.T) {
	stale := fmt.Errorf("reader: %w", core.ErrStaleCursor)
	fake := &fakeJournal{tail: logFixture, tailCursor: "c3", afterQ: []afterRound{{err: stale}}}
	s := newLogsScreen(t, fake)

	driveTick(s)

	if s.collectErr == nil {
		t.Fatal("a stale round must surface as a collection error")
	}
	if s.cursor != "" {
		t.Fatalf("stale cursor must be dropped for the re-tail, got %q", s.cursor)
	}
	if out := s.View(100, 24); !strings.Contains(out, "n/a (") {
		t.Errorf("the stale round must show in the status line:\n%s", out)
	}

	// The next round tails per the port's "" semantics and recovers.
	// The rotation that made the cursor stale also vacuumed the old
	// entries away, so the fresh tail serves only what survived.
	fake.tail = []core.JournalEntry{{
		Time: time.Date(2026, 10, 8, 9, 0, 4, 0, time.UTC),
		Unit: "cron.service", Priority: 6, Message: "tick",
	}}
	fake.tailCursor = "c4"
	driveTick(s)

	if s.collectErr != nil {
		t.Fatalf("the re-tail must clear the error, got %v", s.collectErr)
	}
	if s.cursor != "c4" {
		t.Errorf("cursor = %q, want c4 after recovery", s.cursor)
	}
	if !slices.Equal(fake.afterCursors, []string{"c3", ""}) {
		t.Errorf("After cursors = %v, want [c3 ]: the stale drop must re-tail", fake.afterCursors)
	}
	if len(s.entries) != len(logFixture)+1 {
		t.Errorf("recovered buffer = %d entries, want %d", len(s.entries), len(logFixture)+1)
	}
}

func TestLogsReentryContinuesFromCursor(t *testing.T) {
	fake := &fakeJournal{tail: logFixture, tailCursor: "c3"}
	s := newLogsScreen(t, fake)

	// The router keeps screen instances alive across tab switches;
	// re-entry must continue after the stored cursor, not re-tail the
	// same newest window into the buffer a second time.
	cmd := s.Init()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("re-entry Init must batch followUp and the tick, got %T len %d", batch, len(batch))
	}
	s.Update(batch[0]())

	if fake.tailCalls != 1 {
		t.Errorf("re-entry must not re-tail: %d Tail calls", fake.tailCalls)
	}
	if !slices.Equal(fake.afterCursors, []string{"c3"}) {
		t.Errorf("re-entry must resume after the stored cursor, got %v", fake.afterCursors)
	}
}

func TestLogsTickIgnoresShellRefresh(t *testing.T) {
	fake := &fakeJournal{tail: logFixture, tailCursor: "c3"}
	s := newLogsScreen(t, fake)

	// The shell's 2 s RefreshMsg drives the dashboard and processes
	// screens; the Logs screen must ignore it and keep its own 1 s
	// cadence (ARCHITECTURE.md §3).
	updated, cmd := s.Update(RefreshMsg{})
	s = updated.(*logs)
	if cmd != nil {
		t.Error("RefreshMsg must not trigger a logs round")
	}
	if fake.tailCalls != 1 || fake.afterCalls != 0 {
		t.Errorf("RefreshMsg must not re-collect: %d tails, %d afters", fake.tailCalls, fake.afterCalls)
	}

	// The screen's own tick re-arms the follow round and the next tick.
	driveTick(s)
	if fake.afterCalls != 1 {
		t.Errorf("the re-armed followUp must run, got %d After calls", fake.afterCalls)
	}
	if s.cursor != "c3" {
		t.Errorf("an idle After must echo the cursor, got %q", s.cursor)
	}
}

func TestLogsNavKeysClaimedAndOthersFallThrough(t *testing.T) {
	s := newLogsScreen(t, &fakeJournal{tail: logFixture, tailCursor: "c3"})

	for _, k := range []string{"up", "down", "pgup", "pgdown"} {
		if _, _, handled := s.UpdateKey(keyMsg(k)); !handled {
			t.Errorf("navigation key %q must be claimed", k)
		}
	}

	// Keys outside the screen keymap fall through to the global map —
	// k and j included, which stay the sidebar's, exactly like the
	// services and processes screens.
	for _, k := range []string{"q", "?", "j", "k", "tab"} {
		if _, _, handled := s.UpdateKey(keyMsg(k)); handled {
			t.Errorf("key %q must fall through to the global keymap", k)
		}
	}
}

func TestLogsMouseUnhandled(t *testing.T) {
	s := newLogsScreen(t, &fakeJournal{tail: logFixture, tailCursor: "c3"})
	press := tea.MouseMsg(tea.MouseEvent{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 1, Y: 1})
	if _, _, handled := s.UpdateMouse(press); handled {
		t.Error("v1 logs handles no mouse: the event must fall through")
	}
}

func TestLogsHints(t *testing.T) {
	s := newLogsScreen(t, &fakeJournal{tail: logFixture, tailCursor: "c3"})
	want := []string{"filter", "follow", "scroll", "page"}
	for _, b := range s.Hints() {
		if !slices.Contains(want, b.Help().Desc) {
			t.Errorf("unexpected hint %q", b.Help().Desc)
		}
	}
	if len(s.Hints()) != len(want) {
		t.Errorf("Hints = %d bindings, want %d", len(s.Hints()), len(want))
	}
}
