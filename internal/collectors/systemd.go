package collectors

import (
	"context"
	"fmt"

	"github.com/coreos/go-systemd/v22/dbus"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// Units lists the systemd units the manager currently has loaded, read
// through the system D-Bus (ARCHITECTURE.md §3, Services module). The
// connection is dialed per call: the 5 s refresh cadence makes an
// always-on connection not worth the state, and a per-call dial keeps
// System a value with no lifecycle to manage.
//
// D-Bus has no root-prefix fixtures — the collectors.New("testdata")
// trick cannot work for a bus — so a System that is not live refuses
// instead of guessing where units would come from.
func (s System) Units() ([]core.Service, error) {
	if !s.IsLive() {
		return nil, fmt.Errorf("collectors: systemd units require the live system bus: System is rooted at %q", s.root)
	}

	conn, err := dbus.NewSystemConnectionContext(context.Background())
	if err != nil {
		return nil, fmt.Errorf("collectors: connect to system bus: %w", err)
	}
	defer conn.Close()

	units, err := conn.ListUnitsContext(context.Background())
	if err != nil {
		return nil, fmt.Errorf("collectors: list units: %w", err)
	}
	return mapUnits(units), nil
}

// mapUnits converts raw D-Bus unit statuses into core.Service values. It
// is pure — no bus, no clock, no filesystem — so tests exercise it with
// hand-built dbus.UnitStatus values and no connection at all.
func mapUnits(units []dbus.UnitStatus) []core.Service {
	out := make([]core.Service, 0, len(units))
	for _, u := range units {
		out = append(out, core.Service{
			Name:        u.Name,
			Description: u.Description,
			Load:        u.LoadState,
			Active:      u.ActiveState,
			Sub:         u.SubState,
		})
	}
	return out
}

// Compile-time proof that System satisfies the Services read port.
var _ core.UnitLister = System{}
