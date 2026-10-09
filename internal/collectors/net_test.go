package collectors_test

import (
	"os"
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
)

// TestParseNetDevCounters drives the parser against the golden
// fixture: the two header lines are skipped and the byte counters
// are fields 1 and 9 after the colon (receive bytes first, transmit
// bytes ninth), whatever the whitespace padding.
func TestParseNetDevCounters(t *testing.T) {
	raw, err := os.ReadFile("testdata/proc/net/dev")
	if err != nil {
		t.Fatal(err)
	}
	counters, err := collectors.ParseNetDev(string(raw))
	if err != nil {
		t.Fatalf("ParseNetDev: %v", err)
	}
	want := map[string]collectors.NetDevCounters{
		"lo":    {Rx: 5123456789, Tx: 5123456789},
		"eth0":  {Rx: 987654321, Tx: 123456789},
		"wlan0": {Rx: 45678901, Tx: 12345678},
	}
	if len(counters) != len(want) {
		t.Fatalf("got %d interfaces, want %d: %+v", len(counters), len(want), counters)
	}
	for name, c := range want {
		if got := counters[name]; got != c {
			t.Errorf("counters[%q] = %+v, want %+v", name, got, c)
		}
	}
}

// TestParseNetDevMalformedRows errors on the shapes the kernel never
// writes: a row too short to carry both byte counters, a non-numeric
// counter, and an empty interface name before the colon. Header lines
// (no colon) are not data rows, so they never error.
func TestParseNetDevMalformedRows(t *testing.T) {
	bad := []string{
		"  eth0: 1 2 3 4 5 6 7 8\n",                        // 8 fields: cannot reach tx bytes
		"  eth0: x 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16\n", // rx bytes not a number
		"    : 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16\n",   // no interface name
	}
	for _, content := range bad {
		if _, err := collectors.ParseNetDev(content); err == nil {
			t.Errorf("ParseNetDev(%q) must error", content)
		}
	}
}

// TestNetTrackerRates pins the tracker's delta math: the first
// refresh establishes the baseline and reports zero rates; later
// refreshes divide the byte deltas by the elapsed seconds; a zero
// elapsed window reports zero rates and still takes the baseline.
func TestNetTrackerRates(t *testing.T) {
	listing := func(rx, tx uint64) []core.NetworkInterface {
		return []core.NetworkInterface{
			{Name: "eth0", Rx: rx, Tx: tx},
			{Name: "lo", Rx: 1 << 20, Tx: 1 << 20},
		}
	}

	var tr collectors.NetTracker

	// First refresh: baseline only, zero rates even with a clock.
	out := tr.Refresh(listing(1000, 2000), 2*time.Second)
	if out[0].RxRate != 0 || out[0].TxRate != 0 {
		t.Fatalf("first refresh must report zero rates, got rx=%v tx=%v", out[0].RxRate, out[0].TxRate)
	}

	// Second refresh: deltas over elapsed seconds.
	out = tr.Refresh(listing(3000, 2500), 2*time.Second)
	if out[0].RxRate != 1000 || out[0].TxRate != 250 {
		t.Fatalf("rates = rx %v tx %v, want rx 1000 tx 250", out[0].RxRate, out[0].TxRate)
	}

	// A zero elapsed window reports zero rates and rebaselines.
	out = tr.Refresh(listing(3000, 2500), 0)
	if out[0].RxRate != 0 || out[0].TxRate != 0 {
		t.Fatalf("zero elapsed must report zero rates, got rx=%v tx=%v", out[0].RxRate, out[0].TxRate)
	}
	// ...and the baseline moved, so the next window measures from the
	// rebaselined counters.
	out = tr.Refresh(listing(4000, 2500), 2*time.Second)
	if out[0].RxRate != 500 || out[0].TxRate != 0 {
		t.Fatalf("post-rebaseline rates = rx %v tx %v, want rx 500 tx 0", out[0].RxRate, out[0].TxRate)
	}
}

// TestNetTrackerCounterResetAndNewInterfaces pins the guard rails: a
// counter that went backwards (interface down/up resets /proc/net/dev)
// reports zero instead of a negative rate, and an interface unseen in
// the previous listing starts its own baseline.
func TestNetTrackerCounterResetAndNewInterfaces(t *testing.T) {
	var tr collectors.NetTracker
	if out := tr.Refresh([]core.NetworkInterface{{Name: "wlan0", Rx: 5000, Tx: 100}}, time.Second); out[0].RxRate != 0 {
		t.Fatal("first refresh must baseline")
	}

	// Counter went backwards: the window's rate pair is incoherent
	// (rx reset, tx maybe not), so both rates read zero, not negative.
	out := tr.Refresh([]core.NetworkInterface{{Name: "wlan0", Rx: 100, Tx: 150}}, time.Second)
	if out[0].RxRate != 0 || out[0].TxRate != 0 {
		t.Fatalf("reset window rates = rx %v tx %v, want both 0", out[0].RxRate, out[0].TxRate)
	}

	// An interface missing from one listing starts its own baseline on
	// return (it also left the tracker state with the listing that
	// dropped it — the snapshot is always the last listing's rows).
	out = tr.Refresh([]core.NetworkInterface{
		{Name: "wlan0", Rx: 200, Tx: 200},
		{Name: "tun0", Rx: 10, Tx: 10},
	}, time.Second)
	if out[0].RxRate != 100 || out[0].TxRate != 50 {
		t.Fatalf("wlan0 rates = rx %v tx %v, want rx 100 tx 50", out[0].RxRate, out[0].TxRate)
	}
	if out[1].RxRate != 0 || out[1].TxRate != 0 {
		t.Fatalf("a new interface must start at zero rates, got rx=%v tx=%v", out[1].RxRate, out[1].TxRate)
	}
}

// TestInterfacesRequiresLiveSystem pins the live guard: netlink has
// no root prefix, so a fixture System refuses interface enumeration
// instead of measuring the test runner's own interfaces (the
// Units()/Tail() policy).
func TestInterfacesRequiresLiveSystem(t *testing.T) {
	if _, err := collectors.New("testdata").Interfaces(); err == nil {
		t.Fatal("a fixture System must refuse interface enumeration")
	}
}
