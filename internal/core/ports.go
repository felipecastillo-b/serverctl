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

// NetLister is the Network module's read port: one snapshot of the
// host's interfaces with their traffic counters. collectors.System
// implements it over /proc/net/dev for the counters and the standard
// library's net walker for names, addresses and flags (ARCHITECTURE.md
// §3, §8); the screen consumes it on the module's 2 s cadence. The
// exact row policy — which interfaces appear, what an address listing
// contains — is collector territory and lives in the implementer's
// doc comment, as with UnitLister.
type NetLister interface {
	// Interfaces lists the host's network interfaces with their
	// cumulative receive/transmit byte counters; rates stay zero
	// here and are the consumer's delta math.
	Interfaces() ([]NetworkInterface, error)
}

// SocketLister is the ports-and-connections half of the Network
// module: the listening sockets and the socket pairs in flight, read
// from /proc/net/{tcp,tcp6,udp,udp6} (ARCHITECTURE.md §3 lists ports
// and connections as one module at a 5 s cadence). collectors.System
// implements it over procfs; the row policy — which states count as
// listening, how PIDs resolve — is collector territory documented on
// the implementer, as with NetLister.
type SocketLister interface {
	// Listeners lists the sockets bound and waiting for peers: TCP
	// LISTEN sockets and unconnected UDP sockets, deduplicated per
	// protocol, address and port.
	Listeners() ([]ListenPort, error)
	// Connections lists the socket pairs in flight: TCP sockets in
	// every state but LISTEN, plus connected UDP sockets.
	Connections() ([]Connection, error)
}

// PackageCounter is the Packages module's read port: the installed
// package count of the host's package manager. The port is
// distro/manager-agnostic by construction — the manager and distro
// ride IN the result, never the method set — so post-MVP adapters
// (dpkg, rpm, ...) plug in beside collectors.System's Arch
// implementation without the screen or this contract changing, as
// with UnitLister. collectors.System implements it over the pacman
// local database with `pacman -Qq` as the fixed-argv fallback
// (ARCHITECTURE.md §3, §8); the exact counting policy lives in the
// implementer's doc comment.
type PackageCounter interface {
	// Count returns the installed package count, labeled with the
	// distro and manager that produced it.
	Count() (PackageCount, error)
}
