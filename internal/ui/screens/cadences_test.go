package screens

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// TestDefaultCadencesMatchBuiltinConstants pins the §3 values: the
// built-in constants are the defaults the config `intervals` overrides
// replace, so a drifted constant silently changes the fallback too.
func TestDefaultCadencesMatchBuiltinConstants(t *testing.T) {
	cad := DefaultCadences()
	want := Cadences{
		Services:          5 * time.Second,
		Logs:              time.Second,
		Storage:           30 * time.Second,
		NetworkInterfaces: 2 * time.Second,
		NetworkSockets:    5 * time.Second,
		Packages:          30 * time.Second,
		UsersSessions:     10 * time.Second,
		UsersAttempts:     30 * time.Second,
		Containers:        10 * time.Second,
	}
	if cad != want {
		t.Errorf("DefaultCadences() = %+v, want %+v", cad, want)
	}
}

// TestAllInjectsCadences proves the wiring seam end to end at the
// constructor level: each configured cadence lands on the screen that
// owns its chain — Network and Access receive both of theirs — without
// waiting on a real tick (the timer legs stay runtime territory).
func TestAllInjectsCadences(t *testing.T) {
	cad := DefaultCadences()
	cad.Services = 7 * time.Second
	cad.Logs = 8 * time.Second
	cad.Storage = 9 * time.Second
	cad.NetworkInterfaces = 11 * time.Second
	cad.NetworkSockets = 12 * time.Second
	cad.Packages = 13 * time.Second
	cad.UsersSessions = 14 * time.Second
	cad.UsersAttempts = 15 * time.Second
	cad.Containers = 16 * time.Second

	list := All(theme.Dark(), collectors.New("/nonexistent-fixtures"), cad)

	if s := list[Services].(*services); s.interval != 7*time.Second {
		t.Errorf("services interval = %v, want 7s", s.interval)
	}
	if s := list[Logs].(*logs); s.interval != 8*time.Second {
		t.Errorf("logs interval = %v, want 8s", s.interval)
	}
	if s := list[Storage].(*storage); s.interval != 9*time.Second {
		t.Errorf("storage interval = %v, want 9s", s.interval)
	}
	if s := list[Network].(*network); s.ifaceInterval != 11*time.Second || s.sockInterval != 12*time.Second {
		t.Errorf("network intervals = %v/%v, want 11s/12s", s.ifaceInterval, s.sockInterval)
	}
	if s := list[Packages].(*packages); s.interval != 13*time.Second {
		t.Errorf("packages interval = %v, want 13s", s.interval)
	}
	if s := list[Users].(*access); s.sessionsInterval != 14*time.Second || s.attemptsInterval != 15*time.Second {
		t.Errorf("access intervals = %v/%v, want 14s/15s", s.sessionsInterval, s.attemptsInterval)
	}
	if s := list[Containers].(*containers); s.interval != 16*time.Second {
		t.Errorf("containers interval = %v, want 16s", s.interval)
	}
}

// TestAllDefaultsWhenCadencesZero pins the zero-field fallback: All
// with a zero Cadences must boot every chain on the built-in defaults,
// so a partially wired config can never stall a screen on a zero tick.
func TestAllDefaultsWhenCadencesZero(t *testing.T) {
	list := All(theme.Dark(), collectors.New("/nonexistent-fixtures"), Cadences{})
	want := DefaultCadences()

	if s := list[Services].(*services); s.interval != want.Services {
		t.Errorf("services interval = %v, want the default %v", s.interval, want.Services)
	}
	if s := list[Logs].(*logs); s.interval != want.Logs {
		t.Errorf("logs interval = %v, want the default %v", s.interval, want.Logs)
	}
	if s := list[Storage].(*storage); s.interval != want.Storage {
		t.Errorf("storage interval = %v, want the default %v", s.interval, want.Storage)
	}
	if s := list[Network].(*network); s.ifaceInterval != want.NetworkInterfaces || s.sockInterval != want.NetworkSockets {
		t.Errorf("network intervals = %v/%v, want the defaults %v/%v",
			s.ifaceInterval, s.sockInterval, want.NetworkInterfaces, want.NetworkSockets)
	}
	if s := list[Packages].(*packages); s.interval != want.Packages {
		t.Errorf("packages interval = %v, want the default %v", s.interval, want.Packages)
	}
	if s := list[Users].(*access); s.sessionsInterval != want.UsersSessions || s.attemptsInterval != want.UsersAttempts {
		t.Errorf("access intervals = %v/%v, want the defaults %v/%v",
			s.sessionsInterval, s.attemptsInterval, want.UsersSessions, want.UsersAttempts)
	}
	if s := list[Containers].(*containers); s.interval != want.Containers {
		t.Errorf("containers interval = %v, want the default %v", s.interval, want.Containers)
	}
}

// TestErrStatusAgesLastGoodRound pins the shared §5 error state: the
// round error always renders, the muted stale fragment appears only
// when a last good round exists to age, and a whole-minute age reads
// in format.Duration's shape.
func TestErrStatusAgesLastGoodRound(t *testing.T) {
	muted := lipgloss.NewStyle()
	danger := lipgloss.NewStyle()

	if got := errStatus(errors.New("dbus went away"), time.Time{}, muted, danger); got != "n/a (dbus went away)" {
		t.Errorf("first-round failure = %q, want the error with no stale fragment", got)
	}

	got := errStatus(errors.New("dbus went away"), time.Now().Add(-45*time.Second), muted, danger)
	if !strings.Contains(got, "n/a (dbus went away)") {
		t.Errorf("stale state must keep the error, got %q", got)
	}
	if !strings.Contains(got, "stale 45s") {
		t.Errorf("stale state must age the last good round, got %q", got)
	}

	got = errStatus(errors.New("dbus went away"), time.Now().Add(-90*time.Second), muted, danger)
	if !strings.Contains(got, "stale 1m 30s") {
		t.Errorf("minute-scale age must read in format.Duration's shape, got %q", got)
	}
}
