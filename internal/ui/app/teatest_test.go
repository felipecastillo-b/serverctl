package app

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/coreos/go-systemd/v22/dbus"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/config"
	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/screens"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// TestShellBootsAndQuits is the interactive smoke test: the full TUI boots
// in a virtual terminal, renders its header, and exits on the quit key.
// It runs headless on Linux and is part of the regular test suite.
func TestShellBootsAndQuits(t *testing.T) {
	tm := teatest.NewTestModel(t, New(config.Default()),
		teatest.WithInitialTermSize(100, 30))

	// Wait for "cores": it renders ONLY after real system data flowed
	// through the router into the dashboard CPU block, so it proves the
	// whole pipeline (boot, init collection, message routing, render).
	// One WaitFor, not two: WaitFor consumes the output stream as it
	// scans, and the renderer skips unchanged lines, so a marker already
	// consumed by an earlier WaitFor would never be seen twice.
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("cores"))
	}, teatest.WithDuration(10*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestProcessesScreenBootsAndRenders smoke-tests M3 end to end in a live
// pty: tab into the processes module, wait until a real /proc round flowed
// through collect → route → tracker → table (the status line only renders
// its "sort cpu" summary after the first data round lands), then quit.
func TestProcessesScreenBootsAndRenders(t *testing.T) {
	tm := teatest.NewTestModel(t, New(config.Default()),
		teatest.WithInitialTermSize(100, 30))

	tm.Send(tea.KeyMsg{Type: tea.KeyTab})

	// One WaitFor only: it consumes the output stream, and the renderer
	// skips unchanged lines (see TestShellBootsAndQuits).
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("sort cpu"))
	}, teatest.WithDuration(10*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestServicesScreenBootsAndRenders smoke-tests M4b end to end in a
// live pty: tab twice into the services module, wait until a real
// D-Bus ListUnits round flowed through collect → route → table (the
// status line only renders its "services · sort" summary after the
// first data round lands), then quit. Any systemd host — a CI runner or
// a dev machine — has at least one loaded .service unit, so the summary
// is a stable marker that names no specific unit. Skipped when no
// system bus exists (CI's ubuntu-latest runners have one).
func TestServicesScreenBootsAndRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: talks to the real system bus")
	}
	conn, err := dbus.NewSystemConnectionContext(context.Background())
	if err != nil {
		t.Skipf("no system bus available: %v", err)
	}
	conn.Close()

	tm := teatest.NewTestModel(t, New(config.Default()),
		teatest.WithInitialTermSize(100, 30))

	// Dashboard is active at boot; two tabs move to Services.
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})

	// One WaitFor only: it consumes the output stream, and the renderer
	// skips unchanged lines (see TestShellBootsAndQuits).
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("services · sort"))
	}, teatest.WithDuration(10*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestLogsScreenBootsAndRenders smoke-tests M4c end to end in a live
// pty: tab three times into the logs module, wait until a real
// sdjournal round flowed through tail → route → viewport, then quit.
// The marker is the status line's "N lines ·" summary, which renders
// after the first round lands regardless of journal CONTENT — an
// unprivileged CI user who legitimately sees zero entries (only their
// own session's, see core.JournalEntry) still gets "0 lines ·", so the
// marker cannot flake on content. Boxes whose journal cannot be opened
// at all skip via the pre-flight probe, mirroring the services test's
// bus guard.
func TestLogsScreenBootsAndRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: reads the real system journal in a pty")
	}
	if _, _, err := collectors.New("/").Tail("", 1); err != nil {
		t.Skipf("journal unreadable: %v", err)
	}

	tm := teatest.NewTestModel(t, New(config.Default()),
		teatest.WithInitialTermSize(100, 30))

	// Dashboard is active at boot; three tabs move to Logs.
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})

	// One WaitFor only: it consumes the output stream, and the renderer
	// skips unchanged lines (see TestShellBootsAndQuits).
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("lines · "))
	}, teatest.WithDuration(10*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestStorageScreenBootsAndRenders smoke-tests m5a end to end in a
// live pty: tab four times into the storage module, wait until a real
// /proc/mounts + statfs round flowed through collect → route → table
// (the status line only renders its "filesystems ·" summary after the
// first round lands), then quit. Every Linux host mounts a root
// filesystem, so the summary is a stable marker that names no specific
// mount. Skipped in -short mode alongside the other pty smoke tests.
func TestStorageScreenBootsAndRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: reads the real /proc/mounts and statfs in a pty")
	}

	tm := teatest.NewTestModel(t, New(config.Default()),
		teatest.WithInitialTermSize(100, 30))

	// Dashboard is active at boot; four tabs move to Storage.
	for range 4 {
		tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	}

	// One WaitFor only: it consumes the output stream, and the renderer
	// skips unchanged lines (see TestShellBootsAndQuits).
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("filesystems ·"))
	}, teatest.WithDuration(10*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestNetworkScreenBootsAndRenders smoke-tests m5b end to end in a
// live pty: tab five times into the network module, wait until a real
// /proc/net/dev + netlink round flowed through collect → track →
// route → table (the status line only renders its "interfaces ·"
// summary after the first interfaces round lands), then quit. Every
// Linux host has a loopback interface, so the summary is a stable
// marker that names no specific interface. Skipped in -short mode
// alongside the other pty smoke tests.
func TestNetworkScreenBootsAndRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: reads the real /proc/net/dev and netlink in a pty")
	}

	tm := teatest.NewTestModel(t, New(config.Default()),
		teatest.WithInitialTermSize(100, 30))

	// Dashboard is active at boot; five tabs move to Network.
	for range 5 {
		tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	}

	// One WaitFor only: it consumes the output stream, and the renderer
	// skips unchanged lines (see TestShellBootsAndQuits).
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("interfaces ·"))
	}, teatest.WithDuration(10*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// fakePackageCounter serves one fixed round for the m6a smoke; the
// smoke must inject it through the shell's screen list because the
// app builds every screen from the live System.
type fakePackageCounter struct{}

func (fakePackageCounter) Count() (core.PackageCount, error) {
	return core.PackageCount{Distro: "arch", Manager: "pacman", Installed: 588}, nil
}

// TestPackagesScreenBootsAndRenders smoke-tests m6a end to end in a
// live pty: tab six times into the packages module, wait until the
// fake port's round flowed through collect → route → view (the status
// line only renders its "packages ·" summary after the first round
// lands), then quit. The M5 smokes could read the live system for
// their markers because every Linux host has mounts and a loopback
// interface; the pacman local database exists only on Arch hosts — CI
// runs Ubuntu, where both the database and the `pacman -Qq` fallback
// are absent — so this smoke swaps the packages screen for a fake
// port (ARCHITECTURE.md §10: tests run against fakes) and its marker
// stays deterministic on every host. Skipped in -short mode alongside
// the other pty smokes.
func TestPackagesScreenBootsAndRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: boots the full TUI in a pty")
	}

	cfg := config.Default()
	m := New(cfg)
	m.screens[screens.Packages] = screens.NewPackages(fakePackageCounter{}, theme.FromName(cfg.Theme))

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))

	// Dashboard is active at boot; six tabs move to Packages.
	for range 6 {
		tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	}

	// One WaitFor only: it consumes the output stream, and the renderer
	// skips unchanged lines (see TestShellBootsAndQuits).
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("packages ·"))
	}, teatest.WithDuration(10*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// fakeSessionLister and fakeSSHAttemptLister serve the m6b smokes'
// fixed rounds; the smokes must inject them through the shell's
// screen list because the app builds every screen from the live
// System, and a CI runner's utmp and sshd journal promise no
// deterministic content.
type fakeSessionLister struct{}

func (fakeSessionLister) Sessions() ([]core.UserSession, error) {
	return []core.UserSession{
		{User: "alice", TTY: "pts/0", From: "192.168.1.50", LoginAt: time.Unix(1791561004, 0)},
		{User: "bob", TTY: "tty7", From: ":0", LoginAt: time.Unix(1791560974, 0)},
	}, nil
}

type fakeSSHAttemptLister struct{}

func (fakeSSHAttemptLister) Attempts() ([]core.SSHAttempt, error) {
	return []core.SSHAttempt{
		{Time: time.Unix(1791564839, 0), User: "root", SourceIP: "203.0.113.7", Success: false},
		{Time: time.Unix(1791561004, 0), User: "alice", SourceIP: "192.168.1.50", Success: true},
	}, nil
}

// newUsersTestModel builds the shell with the Users screen swapped
// for the two fake ports — the m6a injection pattern, once per view.
func newUsersTestModel(t *testing.T) *teatest.TestModel {
	t.Helper()
	cfg := config.Default()
	m := New(cfg)
	m.screens[screens.Users] = screens.NewAccess(fakeSessionLister{}, fakeSSHAttemptLister{},
		theme.FromName(cfg.Theme))
	return teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))
}

// TestUsersScreenSessionsBootsAndRenders smoke-tests m6b's sessions
// view end to end in a live pty: tab seven times into the users
// module, wait until the fake port's round flowed through collect →
// route → table (the status line only renders its "sessions ·"
// summary after the first sessions round lands), then quit. The ports
// are fakes because CI's Ubuntu runners promise no deterministic
// utmp content — an empty or absent /run/utmp is normal there — so
// the marker stays deterministic on every host (the m6a lesson,
// ARCHITECTURE.md §10: tests run against fakes). Skipped in -short
// mode alongside the other pty smokes.
func TestUsersScreenSessionsBootsAndRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: boots the full TUI in a pty")
	}

	tm := newUsersTestModel(t)

	// Dashboard is active at boot; seven tabs move to Users.
	for range 7 {
		tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	}

	// One WaitFor only: it consumes the output stream, and the renderer
	// skips unchanged lines (see TestShellBootsAndQuits).
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("sessions ·"))
	}, teatest.WithDuration(10*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestUsersScreenAttemptsBootsAndRenders smoke-tests m6b's attempts
// view end to end in the same pty: tab seven times into the users
// module, press v to cycle to the attempts view, wait for the
// "attempts ·" status marker (the attempts chain runs from the same
// Init, so its round has usually landed before the swap; when it has
// not, the status line re-renders when it does), then quit. The
// journal port is a fake because CI runners have no readable sshd
// journal entries — an unprivileged reader outside the
// systemd-journal group sees none even on hosts that have them
// (probed live; core.SSHAttempt documents the access model). Skipped
// in -short mode alongside the other pty smokes.
func TestUsersScreenAttemptsBootsAndRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: boots the full TUI in a pty")
	}

	tm := newUsersTestModel(t)

	// Dashboard is active at boot; seven tabs move to Users.
	for range 7 {
		tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	}
	// v cycles the screen to the SSH attempts view.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})

	// One WaitFor only: it consumes the output stream, and the renderer
	// skips unchanged lines (see TestShellBootsAndQuits).
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("attempts ·"))
	}, teatest.WithDuration(10*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}
