package collectors

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// meminfoKeys maps the /proc/meminfo rows serverctl needs.
// meminfo values are always KiB regardless of the "kB" suffix on the line.
var meminfoKeys = []string{"MemTotal", "MemAvailable", "SwapTotal", "SwapFree"}

// ParseMeminfo parses /proc/meminfo into a MemoryInfo. Used memory is
// Total - Available (the kernel's own estimate of what is reclaimable),
// which matches what `free` reports.
func ParseMeminfo(r io.Reader) (core.MemoryInfo, error) {
	values := make(map[string]uint64, len(meminfoKeys))

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if !meminfoKeyWanted(key) {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return core.MemoryInfo{}, fmt.Errorf("malformed meminfo line %q: %w", scanner.Text(), err)
		}
		values[key] = v * 1024 // KiB to bytes.
	}
	if err := scanner.Err(); err != nil {
		return core.MemoryInfo{}, fmt.Errorf("scan meminfo: %w", err)
	}

	for _, key := range meminfoKeys {
		if _, ok := values[key]; !ok {
			return core.MemoryInfo{}, fmt.Errorf("meminfo is missing required key %q", key)
		}
	}

	return core.MemoryInfo{
		Total:     values["MemTotal"],
		Used:      values["MemTotal"] - values["MemAvailable"],
		Available: values["MemAvailable"],
		SwapTotal: values["SwapTotal"],
		SwapUsed:  values["SwapTotal"] - values["SwapFree"],
	}, nil
}

// meminfoKeyWanted reports whether key is one of the required meminfo rows.
func meminfoKeyWanted(key string) bool {
	for _, k := range meminfoKeys {
		if k == key {
			return true
		}
	}
	return false
}

// Memory reads this system's /proc/meminfo.
func (s System) Memory() (core.MemoryInfo, error) {
	f, err := os.Open(s.proc("meminfo"))
	if err != nil {
		return core.MemoryInfo{}, fmt.Errorf("open meminfo: %w", err)
	}
	defer func() { _ = f.Close() }()

	return ParseMeminfo(f)
}
