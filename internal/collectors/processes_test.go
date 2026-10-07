package collectors_test

import (
	"os"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
)

func TestParseStat(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		wantName      string
		wantState     byte
		wantPPID      int
		wantUtime     uint64
		wantStime     uint64
		wantStarttime uint64
	}{
		{
			name:     "plain comm",
			raw:      "1234 (vim) S 1000 1234 1234 34816 2190 4194304 5000 0 10 2 3000 1500 0 0 20 0 1 0 5000 50000000 2000",
			wantName: "vim", wantState: 'S', wantPPID: 1000,
			wantUtime: 3000, wantStime: 1500, wantStarttime: 5000,
		},
		{
			name:     "comm with spaces and nested parens",
			raw:      "4321 (weird (name) here) R 1000 4321 4321 0 -1 4194304 100 0 0 0 50 25 0 0 20 0 1 0 100 10000000 500",
			wantName: "weird (name) here", wantState: 'R', wantPPID: 1000,
			wantUtime: 50, wantStime: 25, wantStarttime: 100,
		},
		{
			// kthreadd has PPID 0: a valid process, never assert ppid > 0.
			name:     "kernel thread with ppid 0",
			raw:      "2 (kthreadd) S 0 0 0 0 -1 2129984 0 0 0 0 0 0 0 0 20 0 1 0 3 0 0",
			wantName: "kthreadd", wantState: 'S', wantPPID: 0,
			wantUtime: 0, wantStime: 0, wantStarttime: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stat, err := collectors.ParseStat(tt.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stat.Name != tt.wantName || stat.State != tt.wantState || stat.PPID != tt.wantPPID {
				t.Errorf("identity mismatch: %+v", stat)
			}
			if stat.Utime != tt.wantUtime || stat.Stime != tt.wantStime || stat.Starttime != tt.wantStarttime {
				t.Errorf("time fields mismatch: %+v", stat)
			}
			if got, want := stat.Jiffies(), tt.wantUtime+tt.wantStime; got != want {
				t.Errorf("Jiffies = %d, want %d", got, want)
			}
		})
	}
}

func TestParseStatErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"no comm parens", "1234 vim S 1000 1234 1234 34816 2190 4194304 5000 0 10 2 3000 1500 0 0 20 0 1 0 5000 50000000 2000"},
		{"short remainder", "1234 (vim) S 1000 1234"},
		{"non-numeric ppid", "1234 (vim) S x 1234 1234 34816 2190 4194304 5000 0 10 2 3000 1500 0 0 20 0 1 0 5000 50000000 2000"},
		{"non-numeric utime", "1234 (vim) S 1000 1234 1234 34816 2190 4194304 5000 0 10 2 x 1500 0 0 20 0 1 0 5000 50000000 2000"},
		{"non-numeric starttime", "1234 (vim) S 1000 1234 1234 34816 2190 4194304 5000 0 10 2 3000 1500 0 0 20 0 1 0 x 50000000 2000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := collectors.ParseStat(tt.raw); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestProcessesFixtures(t *testing.T) {
	sys := collectors.New("testdata")
	procs, err := sys.Processes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(procs) != 3 {
		t.Fatalf("got %d processes, want 3: %+v", len(procs), procs)
	}
	// Deterministic PID ordering: 2, 1234, 4321.
	if procs[0].PID != 2 || procs[1].PID != 1234 || procs[2].PID != 4321 {
		t.Fatalf("not sorted by PID: %d, %d, %d", procs[0].PID, procs[1].PID, procs[2].PID)
	}

	page := uint64(os.Getpagesize())

	kthreadd := procs[0]
	if kthreadd.Name != "kthreadd" || kthreadd.PPID != 0 || kthreadd.State != 'S' {
		t.Errorf("kthreadd identity mismatch: %+v", kthreadd)
	}
	if kthreadd.Cmdline != "" {
		t.Errorf("kernel thread cmdline = %q, want empty", kthreadd.Cmdline)
	}
	if kthreadd.Jiffies != 0 || kthreadd.RSS != 0 {
		t.Errorf("kthreadd counters mismatch: %+v", kthreadd)
	}
	if got, want := kthreadd.StartTime.Unix(), int64(1759500000); got != want {
		t.Errorf("kthreadd start = %d, want %d", got, want)
	}

	vim := procs[1]
	if vim.Name != "vim" || vim.State != 'S' || vim.PPID != 1000 {
		t.Errorf("vim identity mismatch: %+v", vim)
	}
	if vim.Cmdline != "vim /tmp/file.txt" {
		t.Errorf("vim cmdline = %q", vim.Cmdline)
	}
	if got, want := vim.RSS, 2000*page; got != want {
		t.Errorf("vim RSS = %d, want %d", got, want)
	}
	if got, want := vim.StartTime.Unix(), int64(1759500050); got != want {
		t.Errorf("vim start = %d, want %d (btime + starttime/USER_HZ)", got, want)
	}
	if got, want := vim.Jiffies, uint64(4500); got != want {
		t.Errorf("vim jiffies = %d, want %d", got, want)
	}

	weird := procs[2]
	if weird.Name != "weird (name) here" || weird.State != 'R' {
		t.Errorf("parens-aware comm mismatch: %+v", weird)
	}
	if weird.Cmdline != "weird (name) here --verbose" {
		t.Errorf("weird cmdline = %q", weird.Cmdline)
	}
	if got, want := weird.RSS, 500*page; got != want {
		t.Errorf("weird RSS = %d, want %d", got, want)
	}
	if got, want := weird.StartTime.Unix(), int64(1759500001); got != want {
		t.Errorf("weird start = %d, want %d", got, want)
	}

	// Fixture dirs are owned by whoever runs the tests; the resolver must
	// always produce something (passwd name or decimal uid fallback).
	for _, p := range procs {
		if p.User == "" {
			t.Errorf("pid %d has empty user", p.PID)
		}
		if p.CpuPct != 0 {
			t.Errorf("pid %d CpuPct = %v: the collector never sets it, the tracker does", p.PID, p.CpuPct)
		}
	}
}

func TestProcessTrackerBaselineThenPercents(t *testing.T) {
	var tracker collectors.ProcessTracker

	first := []core.Process{{PID: 1, Jiffies: 100}, {PID: 2, Jiffies: 200}}
	out := tracker.Refresh(first, 1000)
	for _, p := range out {
		if p.CpuPct != 0 {
			t.Fatalf("baseline must report 0%%, pid %d got %v", p.PID, p.CpuPct)
		}
	}

	// totalDelta = 200: pid1 +50 -> 25%, pid2 +100 -> 50%, pid3 is new -> 0%.
	second := []core.Process{{PID: 1, Jiffies: 150}, {PID: 2, Jiffies: 300}, {PID: 3, Jiffies: 70}}
	out = tracker.Refresh(second, 1200)
	want := map[int]float64{1: 25, 2: 50, 3: 0}
	for _, p := range out {
		if p.CpuPct != want[p.PID] {
			t.Errorf("pid %d CpuPct = %v, want %v", p.PID, p.CpuPct, want[p.PID])
		}
	}

	// Input must not be mutated: the tracker returns a copy.
	if second[0].CpuPct != 0 {
		t.Errorf("input slice was mutated: %+v", second[0])
	}
}

func TestProcessTrackerZeroTotalDeltaIsZero(t *testing.T) {
	var tracker collectors.ProcessTracker
	sample := []core.Process{{PID: 1, Jiffies: 100}}
	_ = tracker.Refresh(sample, 1000)

	out := tracker.Refresh([]core.Process{{PID: 1, Jiffies: 150}}, 1000)
	if out[0].CpuPct != 0 {
		t.Errorf("zero total delta must yield 0%%, got %v", out[0].CpuPct)
	}
}

func TestProcessTrackerVanishedPidRestartsAtZero(t *testing.T) {
	var tracker collectors.ProcessTracker
	_ = tracker.Refresh([]core.Process{{PID: 1, Jiffies: 100}, {PID: 2, Jiffies: 200}}, 1000)
	out := tracker.Refresh([]core.Process{{PID: 1, Jiffies: 150}}, 1200)
	if out[0].CpuPct != 25 {
		t.Fatalf("pid 1 CpuPct = %v, want 25", out[0].CpuPct)
	}

	// PID 2 vanished and its state was dropped; a reappearing PID 2 is a
	// new baseline and reports 0, not a stale delta.
	out = tracker.Refresh([]core.Process{{PID: 1, Jiffies: 170}, {PID: 2, Jiffies: 500}}, 1400)
	for _, p := range out {
		if p.PID == 2 && p.CpuPct != 0 {
			t.Errorf("reappeared pid 2 CpuPct = %v, want 0 (fresh baseline)", p.CpuPct)
		}
		if p.PID == 1 && p.CpuPct != 10 {
			t.Errorf("pid 1 CpuPct = %v, want 10", p.CpuPct)
		}
	}
}
