// access_logind.go reads login sessions from logind over the system
// D-Bus — ARCHITECTURE.md §8's preferred source for the "Users /
// sessions" row. The collector asks login1's manager for its session
// listing, then reads each session's TTY, remote host and login
// timestamp from that session object's properties. D-Bus has no
// root-prefix fixtures, so these reads are live-only: System.Sessions
// keeps fixture roots on the utmp fallback and never reaches this
// file.
package collectors

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// The logind D-Bus surface this collector reads. login1's names are
// the stable API (org.freedesktop.login1), not an implementation
// detail of the running systemd version.
const (
	logindBusName      = "org.freedesktop.login1"
	logindManagerPath  = "/org/freedesktop/login1"
	logindManagerIface = "org.freedesktop.login1.Manager"
	logindSessionIface = "org.freedesktop.login1.Session"

	// dbusPropertiesIface is the standard D-Bus properties interface:
	// GetAll answers with every property of one interface on one
	// object, keyed by property name.
	dbusPropertiesIface = "org.freedesktop.DBus.Properties"

	// logindTimeout bounds one sessions read — connect, listing and
	// every per-session property fetch share it. Three seconds is
	// generous for a handful of local bus round trips and small
	// enough that a wedged bus cannot stall the 10 s sessions chain
	// for more than a round.
	logindTimeout = 3 * time.Second
)

// logindListedSession is one element of Manager.ListSessions' reply,
// whose D-Bus signature is a(susso): ID, UserID, UserName, SeatID and
// the session's object path, in that positional order. The field
// order and types matter — godbus stores the reply by position — so
// the struct mirrors the signature field for field.
type logindListedSession struct {
	ID         string
	UserID     uint32
	UserName   string
	SeatID     string
	ObjectPath dbus.ObjectPath
}

// logindSession is one session as the collector resolved it: the
// listing's user name plus the session object's properties. A failed
// property read leaves TTY, RemoteHost and Timestamp zero — the
// session still counts, because the listing proved it exists.
type logindSession struct {
	User       string
	TTY        string
	RemoteHost string
	Timestamp  uint64 // microseconds since the Unix epoch
}

// logindSessions reads the login sessions logind knows through the
// system bus (ARCHITECTURE.md §8, row "Users / sessions"). One
// connection rides the whole read — the manager listing, then one
// GetAll per session — and is closed on return, the same per-call
// dial trade Units() makes: no lifecycle on System.
//
// The live guard mirrors the journal's: D-Bus has no root-prefix
// fixtures, so a non-live System refuses rather than guessing which
// bus a fixture root would mean. System.Sessions already sends
// fixture roots to utmp; this guard keeps the method honest when
// called directly.
//
// ListSessions' reply order is not documented as chronological; the
// pure mapper imposes the port's login order. Property failures are
// best-effort per session — a session that ended between the listing
// and its read, or a property the running logind does not expose,
// keeps its place with zeroed fields — while a connect or listing
// failure aborts: without the listing there are no sessions at all.
func (s System) logindSessions() ([]core.UserSession, error) {
	if !s.IsLive() {
		return nil, fmt.Errorf("collectors: logind sessions require the live system bus: System is rooted at %q", s.root)
	}

	ctx, cancel := context.WithTimeout(context.Background(), logindTimeout)
	defer cancel()

	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("collectors: logind sessions: connect to system bus: %w", err)
	}
	defer func() { _ = conn.Close() }()

	var listed []logindListedSession
	manager := conn.Object(logindBusName, logindManagerPath)
	if err := manager.CallWithContext(ctx, logindManagerIface+".ListSessions", 0).Store(&listed); err != nil {
		return nil, fmt.Errorf("collectors: logind sessions: list sessions: %w", err)
	}

	infos := make([]logindSession, 0, len(listed))
	for _, session := range listed {
		info := logindSession{User: session.UserName}
		var props map[string]dbus.Variant
		err := conn.Object(logindBusName, session.ObjectPath).
			CallWithContext(ctx, dbusPropertiesIface+".GetAll", 0, logindSessionIface).
			Store(&props)
		if err == nil {
			info.TTY = logindPropertyString(props, "TTY")
			info.RemoteHost = logindPropertyString(props, "RemoteHost")
			info.Timestamp = logindPropertyUint64(props, "Timestamp")
		}
		infos = append(infos, info)
	}
	return mapLogindSessions(infos), nil
}

// logindPropertyString reads one string-valued session property. A
// missing property or an unexpected D-Bus type reads as "" — the
// restart- and version-skew case the best-effort policy accepts.
func logindPropertyString(props map[string]dbus.Variant, name string) string {
	variant, ok := props[name]
	if !ok {
		return ""
	}
	value, _ := variant.Value().(string)
	return value
}

// logindPropertyUint64 reads one unsigned 64-bit session property. A
// missing property or an unexpected type reads as 0, which
// mapLogindSessions treats as "unknown login time".
func logindPropertyUint64(props map[string]dbus.Variant, name string) uint64 {
	variant, ok := props[name]
	if !ok {
		return 0
	}
	value, _ := variant.Value().(uint64)
	return value
}

// mapLogindSessions converts resolved logind sessions into the port's
// domain rows, in login order — oldest login first, the order
// core.SessionLister promises. logind's listing order is not
// documented as chronological, so the mapper sorts explicitly:
// LoginAt ascending, sessions whose properties could not be read
// (zero LoginAt) last, equal timestamps keeping their input order via
// sort.SliceStable. Timestamp is microseconds since the Unix epoch,
// logind's documented unit for Session.Timestamp; a zero timestamp
// maps to the zero time rather than to 1970 so the unreadable
// case stays distinguishable. Pure — no bus, no clock — so the
// mapping and the order are table-tested without a connection.
func mapLogindSessions(infos []logindSession) []core.UserSession {
	sessions := make([]core.UserSession, 0, len(infos))
	for _, info := range infos {
		var loginAt time.Time
		if info.Timestamp != 0 {
			loginAt = time.UnixMicro(int64(info.Timestamp))
		}
		sessions = append(sessions, core.UserSession{
			User:    info.User,
			TTY:     info.TTY,
			From:    info.RemoteHost,
			LoginAt: loginAt,
		})
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].LoginAt.IsZero() != sessions[j].LoginAt.IsZero() {
			return !sessions[i].LoginAt.IsZero()
		}
		return sessions[i].LoginAt.Before(sessions[j].LoginAt)
	})
	return sessions
}
