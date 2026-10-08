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
