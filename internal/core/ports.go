// Ports of serverctl (ARCHITECTURE.md §2): the read and mutate contracts
// the UI consumes and the collectors/actions layers implement —
// dependencies point downward and the UI never touches a kernel
// interface directly.

package core

import "errors"

// UnitLister is the Services module's read port: one snapshot of the
// services the Services screen lists. collectors.System implements it
// over the system D-Bus and its doc comment owns the exact listing
// strategy — which units make the snapshot — since that is collector
// territory and shifts with systemd's two listings (loaded units,
// unit files); the screen consumes the port on its refresh cadence
// (ARCHITECTURE.md §3).
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

// JournalReader reads entries from the local systemd journal (§8:
// sdjournal, no journctl shells). Cursor strings are opaque journal
// cursors as returned by the reader; "" means "start at the tail".
type JournalReader interface {
	// Tail returns up to limit entries ending at the journal head
	// for unit ("" = all units), oldest first, plus the cursor of
	// the newest returned entry.
	Tail(unit string, limit int) ([]JournalEntry, string, error)
	// After returns entries strictly after cursor for unit
	// ("" = all units), oldest first, up to limit, plus the new head
	// cursor. A stale/rotated cursor is the caller's signal to
	// re-Tail.
	After(unit string, cursor string, limit int) ([]JournalEntry, string, error)
}

// ErrStaleCursor is the After-side of the stale-cursor contract above:
// readers return it (wrapped) when a cursor names an entry the journal
// no longer holds — rotated or vacuumed away — and the caller's move
// is to re-Tail from the head.
var ErrStaleCursor = errors.New("journal cursor is stale: entry rotated out of the journal")

// StorageLister is the Storage module's read port: the block devices
// and mounted filesystems the Storage screen lists. collectors.System
// implements it over /proc/mounts, statfs and /proc/diskstats
// (ARCHITECTURE.md §3, §8); the screen consumes it on the module's own
// 30 s cadence. The exact row policy — which filesystems plain df
// would show, which disks appear — is collector territory and lives
// in the implementer's doc comment, as with UnitLister.
type StorageLister interface {
	// Disks lists the block devices of /sys/block with their sizes,
	// models, partitions and completed-I/O-op counters.
	Disks() ([]Disk, error)
	// Filesystems lists the mounted filesystems of /proc/mounts with
	// their statfs usage — the rows plain df shows by default.
	Filesystems() ([]Partition, error)
}
