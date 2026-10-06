package collectors

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// CPUSample is one parsed cpu line of /proc/stat: jiffies spent in each
// state since boot. Percentages require TWO samples (the kernel only
// exposes cumulative counters), which is why CPUTracker exists.
type CPUSample struct {
	User    uint64
	Nice    uint64
	System  uint64
	Idle    uint64
	IOWait  uint64
	IRQ     uint64
	SoftIRQ uint64
	Steal   uint64
}

// Total returns all jiffies accounted in the sample.
func (c CPUSample) Total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

// IdleTime returns jiffies not doing useful work (idle plus iowait).
func (c CPUSample) IdleTime() uint64 {
	return c.Idle + c.IOWait
}

// ParseCPUStat parses /proc/stat cpu lines: the aggregate "cpu" row and one
// "cpuN" row per core. Non-cpu lines are ignored.
func ParseCPUStat(r io.Reader) (aggregate CPUSample, perCPU []CPUSample, err error) {
	scanner := bufio.NewScanner(r)
	seen := false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		if len(fields) < 9 {
			return CPUSample{}, nil, fmt.Errorf("malformed cpu line %q: expected at least 9 fields", scanner.Text())
		}
		sample, err := parseCPULine(fields[1:9])
		if err != nil {
			return CPUSample{}, nil, fmt.Errorf("malformed cpu line %q: %w", scanner.Text(), err)
		}
		if fields[0] == "cpu" {
			aggregate = sample
			seen = true
			continue
		}
		perCPU = append(perCPU, sample)
	}
	if err := scanner.Err(); err != nil {
		return CPUSample{}, nil, fmt.Errorf("scan stat: %w", err)
	}
	if !seen {
		return CPUSample{}, nil, fmt.Errorf("no aggregate cpu line found")
	}
	return aggregate, perCPU, nil
}

// parseCPULine converts eight numeric fields (user..steal) to a CPUSample.
func parseCPULine(fields []string) (CPUSample, error) {
	values := make([]uint64, len(fields))
	for i, f := range fields {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return CPUSample{}, fmt.Errorf("field %d: %w", i, err)
		}
		values[i] = v
	}
	return CPUSample{
		User: values[0], Nice: values[1], System: values[2], Idle: values[3],
		IOWait: values[4], IRQ: values[5], SoftIRQ: values[6], Steal: values[7],
	}, nil
}

// UsagePercent computes busy percentage between two consecutive samples.
// A zero total delta (no jiffies elapsed) yields 0 instead of NaN.
func UsagePercent(prev, curr CPUSample) float64 {
	totalDelta := curr.Total() - prev.Total()
	if totalDelta == 0 {
		return 0
	}
	idleDelta := curr.IdleTime() - prev.IdleTime()
	return (1 - float64(idleDelta)/float64(totalDelta)) * 100
}

// CPUStats reads this system's /proc/stat.
func (s System) CPUStats() (CPUSample, []CPUSample, error) {
	f, err := os.Open(s.proc("stat"))
	if err != nil {
		return CPUSample{}, nil, fmt.Errorf("open stat: %w", err)
	}
	// The close error of a read-only kernel interface carries no signal.
	defer func() { _ = f.Close() }()

	return ParseCPUStat(f)
}

// CPUTracker turns cumulative /proc/stat samples into live percentages.
// It is the only stateful piece of the collectors: the kernel exposes
// counters, and utilization only exists between two reads.
type CPUTracker struct {
	prev    CPUSample
	prevPer []CPUSample
	started bool
}

// Refresh consumes one sample pair. The very first call can only establish
// the baseline, so it reports 0% and no error — by design, not by failure.
func (t *CPUTracker) Refresh(aggregate CPUSample, perCPU []CPUSample) (usage float64, per []float64) {
	if !t.started {
		t.prev, t.prevPer, t.started = aggregate, perCPU, true
		return 0, make([]float64, len(perCPU))
	}
	usage = UsagePercent(t.prev, aggregate)
	per = make([]float64, len(perCPU))
	for i := range perCPU {
		if i < len(t.prevPer) {
			per[i] = UsagePercent(t.prevPer[i], perCPU[i])
		}
	}
	t.prev, t.prevPer = aggregate, perCPU
	return usage, per
}

// CPUModel returns the processor model name from /proc/cpuinfo. On
// architectures without a "model name" row (common on ARM) it reports a
// neutral placeholder instead of failing.
func (s System) CPUModel() (string, error) {
	f, err := os.Open(s.proc("cpuinfo"))
	if err != nil {
		return "", fmt.Errorf("open cpuinfo: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan cpuinfo: %w", err)
	}
	return "Unknown CPU", nil
}
