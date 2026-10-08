package app

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/coreos/go-systemd/v22/dbus"

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
