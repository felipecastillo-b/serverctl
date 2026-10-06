package collectors_test

import (
	"os"
	"strings"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
)

func TestParseCPUStatFixture(t *testing.T) {
	f, err := os.Open("testdata/proc/stat")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	agg, perCPU, err := collectors.ParseCPUStat(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if agg.User != 1000 || agg.Idle != 8000 || agg.IOWait != 200 || agg.SoftIRQ != 100 {
		t.Errorf("aggregate mismatch: %+v", agg)
	}
	if got, want := agg.Total(), uint64(9800); got != want {
		t.Errorf("aggregate total = %d, want %d", got, want)
	}
	if got, want := agg.IdleTime(), uint64(8200); got != want {
		t.Errorf("aggregate idle = %d, want %d", got, want)
	}
	if len(perCPU) != 4 {
		t.Fatalf("perCPU = %d cores, want 4", len(perCPU))
	}
	if perCPU[0].User != 250 || perCPU[0].Idle != 2000 {
		t.Errorf("cpu0 mismatch: %+v", perCPU[0])
	}
}

func TestParseCPUStatErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"non-numeric field", "cpu  abc 0 500 8000 200 0 100 0 0 0\n"},
		{"short line", "cpu  1000 0\n"},
		{"no cpu rows", "intr 123\nctxt 456\n"},
		{"aggregate only is valid", "cpu  1000 0 500 8000 200 0 100 0 0 0\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg, _, err := collectors.ParseCPUStat(strings.NewReader(tt.input))
			if tt.name == "aggregate only is valid" {
				if err != nil || agg.User != 1000 {
					t.Fatalf("valid input rejected: agg=%+v err=%v", agg, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestUsagePercent(t *testing.T) {
	prev := collectors.CPUSample{User: 1000, System: 500, Idle: 8000, IOWait: 200, SoftIRQ: 100}
	curr := collectors.CPUSample{User: 1100, System: 500, Idle: 8100, IOWait: 200, SoftIRQ: 100}

	// Delta: total +200 jiffies (100 user, 100 idle), idle +100 -> half busy.
	if got := collectors.UsagePercent(prev, curr); got != 50 {
		t.Errorf("UsagePercent = %v, want 50", got)
	}
}

func TestUsagePercentZeroDeltaIsZero(t *testing.T) {
	sample := collectors.CPUSample{User: 1000, Idle: 8000}
	if got := collectors.UsagePercent(sample, sample); got != 0 {
		t.Errorf("UsagePercent with no elapsed jiffies must be 0, got %v", got)
	}
}

func TestCPUTrackerBaselineThenPercent(t *testing.T) {
	var tracker collectors.CPUTracker
	first := collectors.CPUSample{User: 1000, System: 500, Idle: 8000, IOWait: 200, SoftIRQ: 100}
	second := collectors.CPUSample{User: 1100, System: 500, Idle: 8100, IOWait: 200, SoftIRQ: 100}

	// First refresh establishes the baseline: no meaningful usage exists yet.
	usage, per := tracker.Refresh(first, []collectors.CPUSample{first})
	if usage != 0 || len(per) != 1 || per[0] != 0 {
		t.Fatalf("baseline refresh = %v %v, want zeros", usage, per)
	}

	usage, _ = tracker.Refresh(second, []collectors.CPUSample{second})
	if usage != 50 {
		t.Errorf("second refresh = %v, want 50", usage)
	}
}

func TestCPUModel(t *testing.T) {
	sys := collectors.New("testdata")
	model, err := sys.CPUModel()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "Intel(R) Core(TM) i5-8400 CPU @ 2.80GHz"; model != want {
		t.Errorf("model = %q, want %q", model, want)
	}
}
