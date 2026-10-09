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
