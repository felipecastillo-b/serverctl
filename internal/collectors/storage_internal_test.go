package collectors

import (
	"errors"
	"math"
	"syscall"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// syscallStatfsT aliases the platform statfs struct; the builder
// below keeps the table rows as plain numbers while the typed-field
// conversion lives in exactly one place.
type syscallStatfsT = syscall.Statfs_t

// statfsOf builds one statfs row: block size, total blocks, free
// blocks (root-included) and available blocks (user-claimable).
func statfsOf(bsize, blocks, bfree, bavail int64) syscallStatfsT {
	return syscallStatfsT{
		Bsize:  bsize,
		Blocks: uint64(blocks),
		Bfree:  uint64(bfree),
		Bavail: uint64(bavail),
	}
}

// TestUsageFromStatfs pins df's math (core.Partition documents the
// formulas). The first row is the observed statfs of a live root
// filesystem and matches its `df -B1 --output=size,used,avail,pcent`
// line byte for byte (sizes) and after df's display rounding (Use%:
// 44.85% shows as 45%). The reserved-space rows pin the non-root
// denominator: root-reserved blocks count as Used, so a filesystem
// with no user-claimable space reads 100% while free-for-root blocks
// remain, and the denominator is used+avail, never the total.
func TestUsageFromStatfs(t *testing.T) {
	tests := []struct {
		name    string
		statfs  syscallStatfsT
		wantTot uint64
		wantUse uint64
		wantAv  uint64
		wantPct float64
		pctCeil int // what plain df prints for Use%
	}{
		{
			name:    "live root ext4, df -B1 verified",
			statfs:  statfsOf(4096, 8179140, 4700452, 4276926),
			wantTot: 33501757440,
			wantUse: 14248706048,
			wantAv:  17518288896,
			wantPct: 44.854,
			pctCeil: 45,
		},
		{
			name:    "reserved blocks count as used",
			statfs:  statfsOf(1024, 1000, 950, 900),
			wantTot: 1024000,
			wantUse: 51200,
			wantAv:  921600,
			wantPct: 5.263,
			pctCeil: 6,
		},
		{
			name:    "user-exhausted reads 100 percent while root reserve remains",
			statfs:  statfsOf(1024, 1000, 950, 0),
			wantTot: 1024000,
			wantUse: 51200,
			wantAv:  0,
			wantPct: 100,
			pctCeil: 100,
		},
		{
			name:    "zero-size filesystem guards the divide",
			statfs:  statfsOf(4096, 0, 0, 0),
			wantTot: 0,
			wantUse: 0,
			wantAv:  0,
			wantPct: 0,
			pctCeil: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := tt.statfs
			total, used, avail, pct := usageFromStatfs(&st)
			if total != tt.wantTot || used != tt.wantUse || avail != tt.wantAv {
				t.Errorf("bytes = (%d, %d, %d), want (%d, %d, %d)",
					total, used, avail, tt.wantTot, tt.wantUse, tt.wantAv)
			}
			if math.Abs(pct-tt.wantPct) > 0.001 {
				t.Errorf("pct = %f, want ~%f", pct, tt.wantPct)
			}
			// df prints Use% rounded UP to the next integer
			// (coreutils df.c's integer ceil), 0 staying 0.
			if got := int(math.Ceil(pct)); got != tt.pctCeil {
				t.Errorf("df display ceil(pct) = %d, want %d", got, tt.pctCeil)
			}
		})
	}
}

// TestEnrichUsage drives the live statfs path through its injectable
// hook: filled rows get df's byte counts, a failing mount degrades to
// zeros while staying listed, and a relative mount point is never
// handed to statfs (statfs would resolve it against the working
// directory and invent numbers df never shows).
func TestEnrichUsage(t *testing.T) {
	parts := []core.Partition{
		{Device: "/dev/sda2", Mount: "/", FSType: "ext4"},
		{Device: "gone", Mount: "/vanished", FSType: "tmpfs"},
		{Device: "ns", Mount: "relative", FSType: "ext4"},
	}
	stated := make(map[string]bool)
	got := enrichUsage(parts, func(mount string) (syscallStatfsT, error) {
		stated[mount] = true
		if mount == "/vanished" {
			return syscallStatfsT{}, errors.New("vanished mid-collection")
		}
		return statfsOf(4096, 8179140, 4700452, 4276926), nil
	})

	if stated["relative"] {
		t.Error("a relative mount point must never reach statfs")
	}
	if !stated["/"] || !stated["/vanished"] {
		t.Error("absolute mounts must be stated, even when they fail")
	}
	if got[0].Total != 33501757440 || got[0].Used != 14248706048 ||
		got[0].Avail != 17518288896 {
		t.Errorf("filled row = %+v, want the df byte counts", got[0])
	}
	if got[0].UsedPct < 44 || got[0].UsedPct > 45 {
		t.Errorf("filled row UsedPct = %f, want ~44.85", got[0].UsedPct)
	}
	for _, i := range []int{1, 2} {
		if got[i].Total != 0 || got[i].Used != 0 || got[i].Avail != 0 || got[i].UsedPct != 0 {
			t.Errorf("degraded row %d must carry zeros, got %+v", i, got[i])
		}
	}
	if got[1].Mount != "/vanished" || got[1].FSType != "tmpfs" {
		t.Errorf("the failed row must stay listed with its parse data: %+v", got[1])
	}
	// The input slice must not be mutated: enrichUsage copies.
	if parts[0].Total != 0 {
		t.Error("enrichUsage must not mutate its input rows")
	}
}

// TestPartitionPattern pins the kernel's partition-naming rule
// (block/partitions/core.c): the partition number is appended directly
// unless the disk name ends in a digit, where a "p" separates —
// nvme0n1 → nvme0n1p1, sda → sda1. The pattern must not match another
// disk's subdirectories (sda's pattern never matches sdb1) nor
// non-partition files.
func TestPartitionPattern(t *testing.T) {
	tests := []struct {
		disk  string
		match map[string]bool
	}{
		{"sda", map[string]bool{
			"sda1": true, "sda2": true, "sda10": true,
			"sdb1": false, "sda": false, "size": false,
		}},
		{"nvme0n1", map[string]bool{
			"nvme0n1p1": true, "nvme0n1p12": true,
			"nvme0n11": false, "nvme0n1": false, "device": false,
		}},
		{"zram0", map[string]bool{
			"zram0p1": true,
			"zram01":  false,
		}},
		{"dm-0", map[string]bool{
			"dm-0p1": true,
			"dm-01":  false,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.disk, func(t *testing.T) {
			pattern := partitionPattern(tt.disk)
			for name, want := range tt.match {
				if got := pattern.MatchString(name); got != want {
					t.Errorf("partitionPattern(%q).Match(%q) = %v, want %v",
						tt.disk, name, got, want)
				}
			}
		})
	}
}
