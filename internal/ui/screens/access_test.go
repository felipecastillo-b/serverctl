package screens

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// fakeSessionLister serves a static session listing (or a per-call
// error) instead of parsing utmp.
type fakeSessionLister struct {
	sessions []core.UserSession
	err      error
	calls    int
}

func (f *fakeSessionLister) Sessions() ([]core.UserSession, error) {
	f.calls++
	return f.sessions, f.err
}

// fakeAttemptLister serves a static attempt listing (or a per-call
// error) instead of reading the sshd journal.
type fakeAttemptLister struct {
	attempts []core.SSHAttempt
	err      error
	calls    int
}

func (f *fakeAttemptLister) Attempts() ([]core.SSHAttempt, error) {
	f.calls++
	return f.attempts, f.err
}

// Compile-time proof that the fakes satisfy the m6b ports, exactly
// like collectors.System does.
var (
	_ core.SessionLister    = (*fakeSessionLister)(nil)
	_ core.SSHAttemptLister = (*fakeAttemptLister)(nil)
)

// accessFixtureTime builds a fixed instant in UTC so the rendered
// LOGIN AT / TIME cells are deterministic on every runner timezone
// (the collector's own rounds read the clock; the screen renders
// whatever instant it is handed).
func accessFixtureTime(hour, minute, sec int) time.Time {
	return time.Date(2026, 10, 9, hour, minute, sec, 0, time.UTC)
}

// sessionsFixture mirrors the collector's golden utmp fixture: the
// seat login with a display name in From, and two remote logins —
// one by IP, one by hostname.
var sessionsFixture = []core.UserSession{
	{User: "castillodk", TTY: "tty7", From: ":0", LoginAt: accessFixtureTime(12, 49, 34)},
	{User: "alice", TTY: "pts/0", From: "192.168.1.50", LoginAt: accessFixtureTime(12, 50, 4)},
	{User: "bob", TTY: "pts/1", From: "remote.example.com", LoginAt: accessFixtureTime(12, 51, 4)},
}

// attemptsFixture spans the verdicts: one accepted login and two
// failed attempts — one against an invalid user — so the RESULT
// column and the failed count both have real rows.
var attemptsFixture = []core.SSHAttempt{
	{Time: accessFixtureTime(12, 50, 4), User: "alice", SourceIP: "192.168.1.50", Success: true},
	{Time: accessFixtureTime(13, 49, 30), User: "root", SourceIP: "203.0.113.7", Success: false},
	{Time: accessFixtureTime(13, 49, 39), User: "admin", SourceIP: "203.0.113.7", Success: false},
}

// newAccessScreen builds the access screen over the given fakes and
// drives both first rounds synchronously. Init batches
// [collectSessions, collectAttempts, tickSessions, tickAttempts] and
// that batch is lazy — executing it only yields the legs — so the
// fixture runs the two collect legs; the 10 s and 30 s timer legs
// stay runtime territory, driving them here would stall the test.
func newAccessScreen(t *testing.T, sessions *fakeSessionLister, attempts *fakeAttemptLister) *access {
	t.Helper()
	s := newAccess(sessions, attempts, theme.Dark())
	cmd := s.Init()
	if cmd == nil {
		t.Fatal("Init must start both chains and collect both listings")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 4 {
		t.Fatalf("Init must batch both collects and both tick legs, got %T len %d", batch, len(batch))
	}
	sessionsMsg := batch[0]()
	if _, ok := sessionsMsg.(sessionsDataMsg); !ok {
		t.Fatalf("the first leg must collect sessions, got %T", sessionsMsg)
	}
	updated, _ := s.Update(sessionsMsg)
	s, ok = updated.(*access)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	attemptsMsg := batch[1]()
	if _, ok := attemptsMsg.(attemptsDataMsg); !ok {
		t.Fatalf("the second leg must collect attempts, got %T", attemptsMsg)
	}
	updated, _ = s.Update(attemptsMsg)
	s, ok = updated.(*access)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if !s.sessionsLoaded || !s.attemptsLoaded {
		t.Fatalf("fixture collection: sessions=%v attempts=%v",
			s.sessionsLoaded, s.attemptsLoaded)
	}
	if s.sessionsErr != nil || s.attemptsErr != nil {
		t.Fatalf("fixture collection errors: %v %v", s.sessionsErr, s.attemptsErr)
	}
	return s
}

// TestAccessRendersSessionsView pins the sessions listing: every
// column renders, rows show the fixture sessions with their From
// shapes, and the status line carries the count with the utmp source
// label. The attempts half of the rounds stays out of this view's
// table.
func TestAccessRendersSessionsView(t *testing.T) {
	s := newAccessScreen(t,
		&fakeSessionLister{sessions: sessionsFixture},
		&fakeAttemptLister{attempts: attemptsFixture})
	view := s.View(100, 24)
	for _, want := range []string{
		"USER", "TTY", "FROM", "LOGIN AT",
		"castillodk", "tty7", ":0",
		"alice", "pts/0", "192.168.1.50",
		"bob", "remote.example.com",
		"Oct 09 12:50:04", // alice's login, rendered from her timezone-fixed instant
		"3 sessions · utmp",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	for _, banned := range []string{"SOURCE IP", "RESULT", "203.0.113.7", "failed"} {
		if strings.Contains(view, banned) {
			t.Errorf("sessions view must not show attempt data %q:\n%s", banned, view)
		}
	}
}

// TestAccessViewCyclesWithV pins the `v` cycle: sessions → attempts →
// sessions, each swap re-rendering the other listing of the same
// rounds without re-collecting, and every key outside the screen
// keymap falling through to the global map.
func TestAccessViewCyclesWithV(t *testing.T) {
	sessions := &fakeSessionLister{sessions: sessionsFixture}
	attempts := &fakeAttemptLister{attempts: attemptsFixture}
	s := newAccessScreen(t, sessions, attempts)
	if sessions.calls != 1 || attempts.calls != 1 {
		t.Fatalf("one round must collect each listing once, got sessions=%d attempts=%d",
			sessions.calls, attempts.calls)
	}

	// v: sessions → attempts.
	updated, _, handled := s.UpdateKey(keyMsg("v"))
	s = updated.(*access)
	if !handled || s.view != accessAttempts {
		t.Fatal("'v' must cycle to the attempts view")
	}
	view := s.View(100, 24)
	for _, want := range []string{
		"TIME", "USER", "SOURCE IP", "RESULT",
		"alice", "192.168.1.50", "ok",
		"root", "203.0.113.7", "failed",
		"3 attempts · 2 failed",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("attempts view missing %q:\n%s", want, view)
		}
	}

	// v: attempts → sessions, and the full cycle re-collects nothing —
	// both views ride the same two rounds.
	updated, _, handled = s.UpdateKey(keyMsg("v"))
	s = updated.(*access)
	if !handled || s.view != accessSessions {
		t.Fatal("'v' must cycle back to the sessions view")
	}
	if !strings.Contains(s.View(100, 24), "3 sessions · utmp") {
		t.Errorf("cycling back must re-render the sessions:\n%s", s.View(100, 24))
	}
	if sessions.calls != 1 || attempts.calls != 1 {
		t.Fatalf("cycling views must not re-collect, got sessions=%d attempts=%d",
			sessions.calls, attempts.calls)
	}

	// Keys outside the screen keymap fall through to the global map
	// (q quits, ? helps, tab cycles).
	for _, k := range []string{"q", "?", "tab"} {
		if _, _, handled := s.UpdateKey(keyMsg(k)); handled {
			t.Errorf("key %q must fall through to the global keymap", k)
		}
	}
}

// TestAccessTwoChainsReArmIndependently pins the module's two
// cadences (§3: sessions 10 s, attempts 30 s): each tick re-collects
// and re-arms its OWN half only, and the shell's global RefreshMsg
// drives neither.
func TestAccessTwoChainsReArmIndependently(t *testing.T) {
	sessions := &fakeSessionLister{sessions: sessionsFixture}
	attempts := &fakeAttemptLister{attempts: attemptsFixture}
	s := newAccessScreen(t, sessions, attempts)

	// The shell's 2 s RefreshMsg drives the dashboard and processes
	// screens; the Users module runs its own two chains and must
	// ignore it.
	updated, cmd := s.Update(RefreshMsg{})
	s = updated.(*access)
	if cmd != nil {
		t.Error("RefreshMsg must not trigger an access collection")
	}
	if sessions.calls != 1 || attempts.calls != 1 {
		t.Errorf("RefreshMsg must not re-collect: sessions=%d attempts=%d",
			sessions.calls, attempts.calls)
	}

	// The 10 s chain: one collect leg plus its own re-arm, nothing of
	// the attempts half.
	updated, cmd = s.Update(sessionsTickMsg{})
	s = updated.(*access)
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the sessions tick must re-arm its own collect and tick, got %T len %d", batch, len(batch))
	}
	updated, _ = s.Update(batch[0]())
	s = updated.(*access)
	if sessions.calls != 2 || attempts.calls != 1 {
		t.Fatalf("the sessions tick must re-collect sessions only: sessions=%d attempts=%d",
			sessions.calls, attempts.calls)
	}

	// The 30 s chain: the attempts half, no sessions round.
	updated, cmd = s.Update(attemptsTickMsg{})
	s = updated.(*access)
	batch, ok = cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the attempts tick must re-arm its own collect and tick, got %T len %d", batch, len(batch))
	}
	attemptsMsg := batch[0]()
	if _, ok := attemptsMsg.(attemptsDataMsg); !ok {
		t.Fatalf("the attempts tick's collect leg must carry an attemptsDataMsg, got %T", attemptsMsg)
	}
	s.Update(attemptsMsg)
	if sessions.calls != 2 || attempts.calls != 2 {
		t.Fatalf("the attempts tick must re-collect attempts only: sessions=%d attempts=%d",
			sessions.calls, attempts.calls)
	}
}

// TestAccessErrorPersistsPerView pins the §5 ladder with the
// two-chain twist: the collecting state until a view's first round
// lands, that view's error while its chain fails, and — the
// persistence the network screen established — a failed round in
// one view never disturbing the other view's good listing or state.
func TestAccessErrorPersistsPerView(t *testing.T) {
	// Before any round: the active view collects.
	fresh := newAccess(&fakeSessionLister{}, &fakeAttemptLister{}, theme.Dark())
	if out := fresh.View(100, 24); !strings.Contains(out, "collecting...") {
		t.Errorf("fresh screen must show collecting..., got:\n%s", out)
	}

	// A failed sessions round leaves the attempts view untouched.
	s := newAccess(
		&fakeSessionLister{err: errors.New("no utmp")},
		&fakeAttemptLister{attempts: attemptsFixture},
		theme.Dark())
	updated, _ := s.Update(s.collectSessions()())
	s = updated.(*access)
	if s.sessionsLoaded {
		t.Fatal("a failed sessions round must not mark the sessions view loaded")
	}
	out := s.View(100, 24) // sessions view is active
	if !strings.Contains(out, "n/a (no utmp)") {
		t.Errorf("sessions view must show its collection error:\n%s", out)
	}

	// The attempts half of the same screen still renders its good
	// round — the error stays scoped to the failing chain.
	updated, _ = s.Update(s.collectAttempts()())
	s = updated.(*access)
	updated, _, _ = s.UpdateKey(keyMsg("v"))
	s = updated.(*access)
	out = s.View(100, 24)
	if !strings.Contains(out, "3 attempts · 2 failed") {
		t.Errorf("attempts view must keep its good listing under a sessions failure:\n%s", out)
	}
	if strings.Contains(out, "no utmp") {
		t.Errorf("the sessions error must not bleed into the attempts view:\n%s", out)
	}

	// And the mirror case: a failed attempts round, a good sessions
	// listing under it.
	s = newAccess(
		&fakeSessionLister{sessions: sessionsFixture},
		&fakeAttemptLister{err: errors.New("journal locked")},
		theme.Dark())
	updated, _ = s.Update(s.collectSessions()())
	s = updated.(*access)
	updated, _ = s.Update(s.collectAttempts()())
	s = updated.(*access)
	out = s.View(100, 24) // sessions view active
	if !strings.Contains(out, "3 sessions · utmp") {
		t.Errorf("sessions view must keep its good listing under an attempts failure:\n%s", out)
	}
	updated, _, _ = s.UpdateKey(keyMsg("v"))
	s = updated.(*access)
	out = s.View(100, 24)
	if !strings.Contains(out, "n/a (journal locked)") {
		t.Errorf("attempts view must show its own error:\n%s", out)
	}
}

// TestAccessEmptyAttemptsExplainsVisibility pins the unprivileged
// reality's status line: a zero-attempts round carries journald's
// access-model hint — an empty sshd window is most often a reader
// outside the systemd-journal group, not a quiet sshd (probed live;
// core.SSHAttempt documents the access model).
func TestAccessEmptyAttemptsExplainsVisibility(t *testing.T) {
	s := newAccessScreen(t, &fakeSessionLister{sessions: sessionsFixture}, &fakeAttemptLister{})
	updated, _, _ := s.UpdateKey(keyMsg("v"))
	s = updated.(*access)
	out := s.View(100, 24)
	if !strings.Contains(out, "0 attempts · 0 failed") {
		t.Errorf("empty attempts must render their count summary:\n%s", out)
	}
	if !strings.Contains(out, "systemd-journal group membership") {
		t.Errorf("empty attempts must explain journald's access model:\n%s", out)
	}
}

// TestAccessMouseFallsThrough pins the §6 parity story for a
// read-only module: no mouse actions exist, so every mouse event
// passes to the shell and the keyboard side is complete by the
// empty set.
func TestAccessMouseFallsThrough(t *testing.T) {
	s := newAccessScreen(t, &fakeSessionLister{sessions: sessionsFixture}, &fakeAttemptLister{})
	if _, _, handled := s.UpdateMouse(tea.MouseMsg{}); handled {
		t.Error("mouse events must fall through to the shell")
	}
}

// TestAccessCursorKeptOnRefresh pins the refresh behavior an audit
// listing needs: the cursor stays on the same row identity across a
// re-collection — the whole session tuple — and falls back to the
// top when that row left the listing.
func TestAccessCursorKeptOnRefresh(t *testing.T) {
	sessions := &fakeSessionLister{sessions: sessionsFixture}
	s := newAccessScreen(t, sessions, &fakeAttemptLister{})
	s.table.SetCursor(1)
	if s.table.Cursor() != 1 {
		t.Fatalf("fixture must park the cursor on row 1, got %d", s.table.Cursor())
	}

	// The same listing again: the cursor stays on its row.
	updated, _ := s.Update(s.collectSessions()())
	s = updated.(*access)
	if s.table.Cursor() != 1 {
		t.Errorf("cursor must stay on its row across a refresh, got %d", s.table.Cursor())
	}

	// The row under the cursor vanishes: the cursor falls to the top.
	sessions.sessions = sessionsFixture[2:] // only bob survives
	updated, _ = s.Update(s.collectSessions()())
	s = updated.(*access)
	if s.table.Cursor() != 0 {
		t.Errorf("cursor must fall to the top when its row vanishes, got %d", s.table.Cursor())
	}
}
