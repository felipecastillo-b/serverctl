// Ports of serverctl (ARCHITECTURE.md §2): the read and mutate contracts
// the UI consumes and the collectors/actions layers implement —
// dependencies point downward and the UI never touches a kernel
// interface directly.

package core

// UnitLister is the Services module's read port: one snapshot of every
// systemd unit the manager currently has loaded. collectors.System
// implements it over the system D-Bus (ListUnits); the Services screen
// consumes it on its refresh cadence (ARCHITECTURE.md §3).
type UnitLister interface {
	Units() ([]Service, error)
}

// UnitManager is the Services module's mutating port: the three
// whitelisted lifecycle verbs — the only unit mutations serverctl
// offers, there is no generic method passthrough. actions.Actor
// implements it over the system D-Bus, validating the unit name before
// any bus call and auditing every executed or failed attempt; the
// Services screen reaches it only behind a confirmation modal
// (ARCHITECTURE.md §7).
type UnitManager interface {
	Start(name string) error
	Stop(name string) error
	Restart(name string) error
}
