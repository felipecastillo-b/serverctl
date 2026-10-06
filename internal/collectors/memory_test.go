package collectors_test

import (
	"os"
	"strings"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
)

func TestParseMeminfoFixture(t *testing.T) {
	f, err := os.Open("testdata/proc/meminfo")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	mem, err := collectors.ParseMeminfo(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	const kib = 1024
	checks := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"Total", mem.Total, 16384000 * kib},
		{"Available", mem.Available, 8192000 * kib},
		// Used = Total - Available, the kernel-recommended definition.
		{"Used", mem.Used, (16384000 - 8192000) * kib},
		{"SwapTotal", mem.SwapTotal, 4194304 * kib},
		{"SwapUsed", mem.SwapUsed, (4194304 - 4194300) * kib},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestParseMeminfoErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"missing required key", "MemTotal:       16384000 kB\n"},
		{"non-numeric value", "MemTotal:       lots kB\nMemAvailable:   8192000 kB\nSwapTotal:      4194304 kB\nSwapFree:       4194300 kB\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := collectors.ParseMeminfo(strings.NewReader(tt.input)); err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}
