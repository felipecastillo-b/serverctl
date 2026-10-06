package collectors

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// LoadAvg reads /proc/loadavg: three load averages (1, 5, 15 minutes).
func (s System) LoadAvg() (core.LoadAvg, error) {
	data, err := os.ReadFile(s.proc("loadavg"))
	if err != nil {
		return core.LoadAvg{}, fmt.Errorf("read loadavg: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return core.LoadAvg{}, fmt.Errorf("malformed loadavg %q: expected at least 3 fields", strings.TrimSpace(string(data)))
	}

	loads := make([]float64, 3)
	for i := range loads {
		loads[i], err = strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return core.LoadAvg{}, fmt.Errorf("malformed loadavg field %q: %w", fields[i], err)
		}
	}
	return core.LoadAvg{Load1: loads[0], Load5: loads[1], Load15: loads[2]}, nil
}

// Uptime reads /proc/uptime: the first field is seconds since boot.
func (s System) Uptime() (time.Duration, error) {
	data, err := os.ReadFile(s.proc("uptime"))
	if err != nil {
		return 0, fmt.Errorf("read uptime: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return core0UptimeErr(strings.TrimSpace(string(data)))
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("malformed uptime field %q: %w", fields[0], err)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// core0UptimeErr builds the malformed-uptime error (kept separate for clarity).
func core0UptimeErr(raw string) (time.Duration, error) {
	return 0, fmt.Errorf("malformed uptime %q: expected at least 1 field", raw)
}

// Kernel reads kernel identity from procfs. It deliberately avoids
// syscall.Uname so tests rooted at fixtures observe fixture data, not the
// host running the tests.
func (s System) Kernel() (core.KernelInfo, error) {
	release, err := readTrimmed(s.proc("sys", "kernel", "osrelease"))
	if err != nil {
		return core.KernelInfo{}, err
	}
	version, err := readTrimmed(s.proc("version"))
	if err != nil {
		return core.KernelInfo{}, err
	}

	// Hardware model is best-effort: DMI data may not exist (VMs, ARM boards).
	hw, hwErr := s.HardwareData()
	model := "Unknown"
	if hwErr == nil && hw.Model != "" {
		model = hw.Model
	}

	return core.KernelInfo{
		Release:       release,
		Version:       version,
		Arch:          runtime.GOARCH,
		HardwareModel: model,
	}, nil
}

// Hostname reads the kernel hostname from procfs. A live system falls back
// to os.Hostname when the procfs node is somehow absent.
func (s System) Hostname() (string, error) {
	name, err := readTrimmed(s.proc("sys", "kernel", "hostname"))
	if err != nil {
		if s.IsLive() {
			fallback, fbErr := os.Hostname()
			if fbErr == nil {
				return fallback, nil
			}
		}
		return "", err
	}
	return name, nil
}

// readTrimmed reads a file and trims surrounding whitespace (procfs files
// conventionally end with a newline).
func readTrimmed(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}
