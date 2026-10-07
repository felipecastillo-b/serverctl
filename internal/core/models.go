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
