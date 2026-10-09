// Package core defines the domain models of serverctl: the shapes of system
// information that collectors produce and the UI consumes (ARCHITECTURE.md §4).
package core

import "time"

// ServerSnapshot is one coherent read of the server's general state.
type ServerSnapshot struct {
	Hostname string
	Kernel   KernelInfo
	CPU      CPUInfo
	Memory   MemoryInfo
	Load     LoadAvg
	Uptime   time.Duration
	At       time.Time
}

// KernelInfo describes the running kernel and the machine model.
type KernelInfo struct {
	Release       string
	Version       string
	Arch          string
	HardwareModel string
}

// LoadAvg holds the classic 1-, 5- and 15-minute load averages.
type LoadAvg struct {
	Load1  float64
	Load5  float64
	Load15 float64
}

// CPUInfo describes the processor and its current utilization.
// Usage and PerCPU are percentages in the range 0–100.
type CPUInfo struct {
	Model  string
	Cores  int
	Usage  float64
	PerCPU []float64
}

// MemoryInfo reports RAM and swap occupation in bytes.
type MemoryInfo struct {
	Total     uint64
	Used      uint64
	Available uint64
	SwapTotal uint64
	SwapUsed  uint64
}

// SensorReading is one hardware temperature sensor.
type SensorReading struct {
	Name    string
	Label   string
	Celsius float64
}

// HardwareInfo identifies the physical (or virtual) machine.
type HardwareInfo struct {
	Vendor string
	Model  string
}

// Service is one systemd unit as reported by systemd's D-Bus API: the
// unit name and human-readable description plus the load/active/sub
// state triple systemd exposes (ARCHITECTURE.md §4).
//
// systemd offers two listings, and this one shape carries both.
// ListUnits reports the units the manager has LOADED, with their live
// state triple; ListUnitFiles reports every unit FILE on disk, with
// its enablement state. FileState is that enablement state ("enabled",
// "disabled", "static", "masked", ...; empty when systemd reports
// none). A unit that exists only as a file — disabled and stopped, so
// the manager never loaded it — is listed with Load "unloaded",
// serverctl's own label for known-on-disk-but-not-loaded: systemd's
// load states describe the fate of a loaded unit and never say
// "unloaded".
type Service struct {
	Name        string
	Description string
	Load        string
	Active      string
	Sub         string
	FileState   string
}

// JournalEntry is one systemd journal line as the Logs screen shows it
// (ARCHITECTURE.md §4): when it happened, which unit wrote it, the
// syslog priority (0 emerg … 7 debug; journald allows entries without
// a priority, which read as info/6 here) and the message text.
//
// Unprivileged journal visibility: a user outside the systemd-journal
// (or adm) group sees only the entries of their own session, so a dev
// box or CI runner can legitimately show an empty or sparse journal —
// that is journald's access model, not a collector failure.
type JournalEntry struct {
	Time     time.Time
	Unit     string
	Priority int
	Message  string
}

// Process is one running process read from /proc/<pid>. Jiffies is the
// cumulative utime+stime tick counter; CpuPct is derived between two
// samples by collectors.ProcessTracker and is 0 on the first sample.
type Process struct {
	PID       int
	PPID      int
	Name      string
	State     byte
	User      string
	Cmdline   string
	RSS       uint64
	StartTime time.Time
	CpuPct    float64
	Jiffies   uint64
}

// Partition is one mounted filesystem as the Storage screen lists it
// (ARCHITECTURE.md §4 sketch, grown by the Storage milestone the way
// Process grew CpuPct): the device, its mount point and type, plus
// usage in bytes. Total/Used/Avail are statfs byte counts — Total =
// f_blocks×f_bsize, Used = (f_blocks−f_bfree)×f_bsize, Avail =
// f_bavail×f_bsize — the exact values `df -B1` prints. Avail is what
// an unprivileged user may still claim: root-reserved blocks count as
// Used, matching df's Available column. UsedPct is df's Use% formula,
// used/(used+avail)×100 — the denominator is the non-root total, NOT
// the filesystem size, so a filesystem whose user-claimable space is
// exhausted reads as 100% while reserved blocks remain; df rounds the
// value up to the next integer for display (44.85 shows as 45%).
// A row without usage data — the parser-only rows of a fixture
// system, or a live mount whose statfs failed — carries zeros and 0%
// instead of vanishing from the listing.
type Partition struct {
	Device  string
	Mount   string
	FSType  string
	Total   uint64
	Used    uint64
	Avail   uint64
	UsedPct float64
}

// Disk is one block device of /sys/block as the Storage screen lists
// it (ARCHITECTURE.md §4 sketch plus the I/O counters): the device
// name (directory base, e.g. "sda"), the vendor model string when
// sysfs carries one (virtual devices like loop or zram have no
// device/model file and read as ""), and SizeBytes from sysfs's
// 512-byte sector count. Partitions are the device's partition
// subdirectories (sda1, sda2, ...): each carries Device and Total
// (its own size) with an empty Mount and FSType — a partition row is
// geometry, not a mounted filesystem. Reads and Writes are the
// completed-I/O-op counters of /proc/diskstats fields 4 and 8,
// cumulative since boot; a collector that cannot join diskstats
// leaves them at zero rather than dropping the disk.
type Disk struct {
	Name       string
	Model      string
	SizeBytes  uint64
	Partitions []Partition
	Reads      uint64
	Writes     uint64
}
