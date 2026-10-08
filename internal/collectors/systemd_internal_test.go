package collectors

import (
	"strings"
	"testing"

	"github.com/coreos/go-systemd/v22/dbus"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// TestMapUnits is the pure-mapper table: hand-built dbus.UnitStatus
// values, no bus, no fixtures (ARCHITECTURE.md §10 parser purity).
func TestMapUnits(t *testing.T) {
	tests := []struct {
		name  string
		units []dbus.UnitStatus
		want  []core.Service
	}{
		{
			name:  "empty listing",
			units: nil,
			want:  []core.Service{},
		},
		{
			name: "loaded running service",
			units: []dbus.UnitStatus{
				{
					Name:        "sshd.service",
					Description: "OpenSSH Daemon",
					LoadState:   "loaded",
					ActiveState: "active",
					SubState:    "running",
				},
			},
			want: []core.Service{
				{Name: "sshd.service", Description: "OpenSSH Daemon", Load: "loaded", Active: "active", Sub: "running"},
			},
		},
		{
			name: "failed and masked units keep their state triple",
			units: []dbus.UnitStatus{
				{
					Name:        "nginx.service",
					Description: "A high performance web server",
					LoadState:   "error",
					ActiveState: "failed",
					SubState:    "failed",
				},
				{
					Name:        "cups.service",
					Description: "CUPS Scheduler",
					LoadState:   "masked",
					ActiveState: "inactive",
					SubState:    "dead",
				},
			},
			want: []core.Service{
				{Name: "nginx.service", Description: "A high performance web server", Load: "error", Active: "failed", Sub: "failed"},
				{Name: "cups.service", Description: "CUPS Scheduler", Load: "masked", Active: "inactive", Sub: "dead"},
			},
		},
		{
			name: "instance and job fields are dropped, order preserved",
			units: []dbus.UnitStatus{
				{
					Name:        "user@1000.service",
					Description: "User Manager for UID 1000",
					LoadState:   "loaded",
					ActiveState: "active",
					SubState:    "running",
					JobId:       42,
					JobType:     "start",
				},
				{
					Name:      "systemd-journald.service",
					LoadState: "loaded", ActiveState: "active", SubState: "running",
				},
			},
			want: []core.Service{
				{Name: "user@1000.service", Description: "User Manager for UID 1000", Load: "loaded", Active: "active", Sub: "running"},
				{Name: "systemd-journald.service", Description: "", Load: "loaded", Active: "active", Sub: "running"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapUnits(tt.units)
			if len(got) != len(tt.want) {
				t.Fatalf("mapUnits returned %d services, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("service %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestUnitsRequiresLiveSystem pins the fixture guard: a System rooted at
// testdata must refuse rather than touch any bus, because D-Bus has no
// root-prefix fixtures to read from.
func TestUnitsRequiresLiveSystem(t *testing.T) {
	sys := New("testdata")
	services, err := sys.Units()
	if err == nil {
		t.Fatalf("expected live-only error, got services: %+v", services)
	}
	if !strings.Contains(err.Error(), "systemd units require the live system bus") {
		t.Errorf("error %q should mention the live system bus requirement", err)
	}
}
