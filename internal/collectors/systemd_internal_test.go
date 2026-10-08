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

// TestMergeUnits is the pure-merge table: hand-built D-Bus listings,
// no bus (ARCHITECTURE.md §10 parser purity). It pins the two halves
// of the merge — loaded units gain FileState, on-disk-only units
// surface at all — plus the .service filter and the duplicate-path
// dedup rule of moreInformativeState.
func TestMergeUnits(t *testing.T) {
	tests := []struct {
		name  string
		units []dbus.UnitStatus
		files []dbus.UnitFile
		want  []core.Service
	}{
		{
			name: "empty listings",
			want: []core.Service{},
		},
		{
			name: "loaded unit gains its file state",
			units: []dbus.UnitStatus{
				{Name: "sshd.service", Description: "OpenSSH Daemon", LoadState: "loaded", ActiveState: "active", SubState: "running"},
			},
			files: []dbus.UnitFile{
				{Path: "/usr/lib/systemd/system/sshd.service", Type: "enabled"},
			},
			want: []core.Service{
				{Name: "sshd.service", Description: "OpenSSH Daemon", Load: "loaded", Active: "active", Sub: "running", FileState: "enabled"},
			},
		},
		{
			name: "loaded unit without a file keeps empty file state",
			units: []dbus.UnitStatus{
				{Name: "runtime-gen.service", Description: "generated", LoadState: "loaded", ActiveState: "active", SubState: "running"},
			},
			want: []core.Service{
				{Name: "runtime-gen.service", Description: "generated", Load: "loaded", Active: "active", Sub: "running"},
			},
		},
		{
			name: "on-disk-only service surfaces as unloaded inactive dead",
			units: []dbus.UnitStatus{
				{Name: "sshd.service", Description: "OpenSSH Daemon", LoadState: "loaded", ActiveState: "active", SubState: "running"},
			},
			files: []dbus.UnitFile{
				{Path: "/usr/lib/systemd/system/sshd.service", Type: "enabled"},
				{Path: "/usr/lib/systemd/system/docker.service", Type: "disabled"},
			},
			want: []core.Service{
				{Name: "sshd.service", Description: "OpenSSH Daemon", Load: "loaded", Active: "active", Sub: "running", FileState: "enabled"},
				{Name: "docker.service", Load: "unloaded", Active: "inactive", Sub: "dead", FileState: "disabled"},
			},
		},
		{
			name: "non-service units and files are dropped",
			units: []dbus.UnitStatus{
				{Name: "logrotate.timer", Description: "daily rotation", LoadState: "loaded", ActiveState: "active", SubState: "waiting"},
				{Name: "sshd.service", LoadState: "loaded", ActiveState: "active", SubState: "running"},
			},
			files: []dbus.UnitFile{
				{Path: "/usr/lib/systemd/system/dbus.socket", Type: "static"},
				{Path: "/usr/lib/systemd/system/multi-user.target", Type: "static"},
				{Path: "/usr/lib/systemd/system/fstrim.timer", Type: "disabled"},
				{Path: "/usr/lib/systemd/system/docker.service", Type: "disabled"},
			},
			want: []core.Service{
				{Name: "sshd.service", Load: "loaded", Active: "active", Sub: "running"},
				{Name: "docker.service", Load: "unloaded", Active: "inactive", Sub: "dead", FileState: "disabled"},
			},
		},
		{
			name: "duplicate paths collapse to one row, first state kept",
			files: []dbus.UnitFile{
				{Path: "/etc/systemd/system/multi-user.target.wants/app.service", Type: "enabled"},
				{Path: "/usr/lib/systemd/system/app.service", Type: "enabled"},
			},
			want: []core.Service{
				{Name: "app.service", Load: "unloaded", Active: "inactive", Sub: "dead", FileState: "enabled"},
			},
		},
		{
			name: "a bad first listing loses to a readable duplicate",
			files: []dbus.UnitFile{
				{Path: "/usr/lib/systemd/system/broken.service", Type: "bad"},
				{Path: "/etc/systemd/system/broken.service", Type: "disabled"},
			},
			want: []core.Service{
				{Name: "broken.service", Load: "unloaded", Active: "inactive", Sub: "dead", FileState: "disabled"},
			},
		},
		{
			name: "a bad duplicate does not displace a readable first listing",
			files: []dbus.UnitFile{
				{Path: "/etc/systemd/system/kept.service", Type: "disabled"},
				{Path: "/usr/lib/systemd/system/kept.service", Type: "bad"},
			},
			want: []core.Service{
				{Name: "kept.service", Load: "unloaded", Active: "inactive", Sub: "dead", FileState: "disabled"},
			},
		},
		{
			name: "loaded units come first in listing order, files after in path order",
			units: []dbus.UnitStatus{
				{Name: "b.service", LoadState: "loaded", ActiveState: "active", SubState: "running"},
				{Name: "a.service", LoadState: "loaded", ActiveState: "active", SubState: "running"},
			},
			files: []dbus.UnitFile{
				{Path: "/usr/lib/systemd/system/z.service", Type: "disabled"},
				{Path: "/usr/lib/systemd/system/c.service", Type: "static"},
			},
			want: []core.Service{
				{Name: "b.service", Load: "loaded", Active: "active", Sub: "running"},
				{Name: "a.service", Load: "loaded", Active: "active", Sub: "running"},
				{Name: "z.service", Load: "unloaded", Active: "inactive", Sub: "dead", FileState: "disabled"},
				{Name: "c.service", Load: "unloaded", Active: "inactive", Sub: "dead", FileState: "static"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeUnits(tt.units, tt.files)
			if len(got) != len(tt.want) {
				t.Fatalf("mergeUnits returned %d services, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("service %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestMoreInformativeState pins the dedup rule in isolation: first
// occurrence wins unless it says nothing usable.
func TestMoreInformativeState(t *testing.T) {
	tests := []struct {
		name       string
		kept, next string
		want       string
	}{
		{"first state wins ties", "enabled", "disabled", "enabled"},
		{"empty loses to anything", "", "static", "static"},
		{"bad loses to anything", "bad", "disabled", "disabled"},
		{"readable state survives bad duplicate", "masked", "bad", "masked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := moreInformativeState(tt.kept, tt.next); got != tt.want {
				t.Errorf("moreInformativeState(%q, %q) = %q, want %q", tt.kept, tt.next, got, tt.want)
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
