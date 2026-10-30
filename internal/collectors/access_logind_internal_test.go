package collectors

import (
	"strings"
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// TestMapLogindSessions pins the pure logind mapping: every resolved
// field lands on its domain row, the listing is reordered into login
// order (oldest first) regardless of the bus order, sessions whose
// properties could not be read sort last, and equal timestamps keep
// their input order. No bus, no clock.
func TestMapLogindSessions(t *testing.T) {
	tests := []struct {
		name  string
		infos []logindSession
		want  []core.UserSession
	}{
		{
			name: "one session maps every field",
			infos: []logindSession{{
				User:       "alice",
				TTY:        "pts/0",
				RemoteHost: "192.168.1.50",
				Timestamp:  1791561004210000,
			}},
			want: []core.UserSession{{
				User:    "alice",
				TTY:     "pts/0",
				From:    "192.168.1.50",
				LoginAt: time.UnixMicro(1791561004210000),
			}},
		},
		{
			name: "bus order is reordered into login order",
			infos: []logindSession{
				{User: "bob", TTY: "pts/1", RemoteHost: "remote.example.com", Timestamp: 1791561064300000},
				{User: "castillodk", TTY: "tty7", RemoteHost: ":0", Timestamp: 1791560974105900},
				{User: "alice", TTY: "pts/0", RemoteHost: "192.168.1.50", Timestamp: 1791561004210000},
			},
			want: []core.UserSession{
				{User: "castillodk", TTY: "tty7", From: ":0", LoginAt: time.UnixMicro(1791560974105900)},
				{User: "alice", TTY: "pts/0", From: "192.168.1.50", LoginAt: time.UnixMicro(1791561004210000)},
				{User: "bob", TTY: "pts/1", From: "remote.example.com", LoginAt: time.UnixMicro(1791561064300000)},
			},
		},
		{
			name: "unknown login time sorts last",
			infos: []logindSession{
				{User: "unreadable", TTY: "pts/9"},
				{User: "bob", TTY: "pts/1", Timestamp: 1791561064300000},
				{User: "alice", TTY: "pts/0", Timestamp: 1791561004210000},
			},
			want: []core.UserSession{
				{User: "alice", TTY: "pts/0", LoginAt: time.UnixMicro(1791561004210000)},
				{User: "bob", TTY: "pts/1", LoginAt: time.UnixMicro(1791561064300000)},
				{User: "unreadable", TTY: "pts/9"},
			},
		},
		{
			name: "equal timestamps keep input order",
			infos: []logindSession{
				{User: "first", TTY: "pts/1", Timestamp: 1791561004210000},
				{User: "second", TTY: "pts/2", Timestamp: 1791561004210000},
				{User: "earlier", TTY: "pts/0", Timestamp: 1791560974105900},
			},
			want: []core.UserSession{
				{User: "earlier", TTY: "pts/0", LoginAt: time.UnixMicro(1791560974105900)},
				{User: "first", TTY: "pts/1", LoginAt: time.UnixMicro(1791561004210000)},
				{User: "second", TTY: "pts/2", LoginAt: time.UnixMicro(1791561004210000)},
			},
		},
		{
			name: "unknown login times keep their input order",
			infos: []logindSession{
				{User: "z-first", TTY: "pts/8"},
				{User: "z-second", TTY: "pts/9"},
			},
			want: []core.UserSession{
				{User: "z-first", TTY: "pts/8"},
				{User: "z-second", TTY: "pts/9"},
			},
		},
		{
			name: "empty listing maps to no sessions",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapLogindSessions(tt.infos)
			if len(got) != len(tt.want) {
				t.Fatalf("mapLogindSessions = %d sessions, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i].User != tt.want[i].User || got[i].TTY != tt.want[i].TTY || got[i].From != tt.want[i].From {
					t.Errorf("session %d = %+v, want %+v", i, got[i], tt.want[i])
				}
				if !got[i].LoginAt.Equal(tt.want[i].LoginAt) {
					t.Errorf("session %d LoginAt = %v, want %v", i, got[i].LoginAt, tt.want[i].LoginAt)
				}
			}
		})
	}
}

// TestLogindSessionsRefusesOnFixtureSystem pins the live-only guard:
// a System rooted at fixtures must refuse logind reads before any bus
// dial — D-Bus has no root-prefix fixtures, and fixture roots belong
// to the utmp fallback alone.
func TestLogindSessionsRefusesOnFixtureSystem(t *testing.T) {
	_, err := New("testdata").logindSessions()
	if err == nil {
		t.Fatal("a fixture System must refuse logind reads, not touch the host bus")
	}
	if !strings.Contains(err.Error(), "require the live system bus") {
		t.Errorf("error = %q, want the live-guard message", err)
	}
}
