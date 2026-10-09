package collectors_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
)

// TestParseMountsUnescapesOctalEscapes pins the kernel's mount-field
// mangling: space, tab, newline and backslash arrive as \040, \011,
// \012 and \134 (fs/proc_namespace.c) and must decode in both the
// device and the mount point.
func TestParseMountsUnescapesOctalEscapes(t *testing.T) {
	content := "/dev/sda2 / ext4 rw,relatime 0 0\n" +
		"/dev/sdb1 /mnt/data\\040disk ext4 rw,relatime 0 0\n" +
		"//host\\134share /mnt/tab\\011here cifs rw,relatime 0 0\n" +
		"/dev/sdc1 /mnt/new\\012line ext4 rw 0 0\n"
	parts, err := collectors.ParseMounts(content)
	if err != nil {
		t.Fatalf("ParseMounts: %v", err)
	}
	want := []core.Partition{
		{Device: "/dev/sda2", Mount: "/", FSType: "ext4"},
		{Device: "/dev/sdb1", Mount: "/mnt/data disk", FSType: "ext4"},
		{Device: "//host\\share", Mount: "/mnt/tab\there", FSType: "cifs"},
		{Device: "/dev/sdc1", Mount: "/mnt/new\nline", FSType: "ext4"},
	}
	if len(parts) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(parts), len(want), parts)
	}
	for i := range want {
		if parts[i].Device != want[i].Device || parts[i].Mount != want[i].Mount ||
			parts[i].FSType != want[i].FSType {
			t.Errorf("row %d = %+v, want %+v", i, parts[i], want[i])
		}
	}
}

// TestParseMountsFilterSet pins df's default hiding set. Every hidden
// type below is either in coreutils' ME_DUMMY_0 list (gnulib
// mountlist.c: autofs, proc, sysfs, ...) or a zero-size kernel
// pseudo-filesystem df elides (cgroup2, securityfs, ...). devtmpfs
// STAYS: df (GNU coreutils) 9.12 lists it because it reports a real
// size — verified against `df -T` on a live host. autofs DROPS even
// though it carries a mount entry: ME_DUMMY_0 names it.
func TestParseMountsFilterSet(t *testing.T) {
	hidden := []string{
		"autofs", "proc", "subfs", "debugfs", "devpts", "devfs", "kernfs",
		"fusectl", "fuse.portal", "mqueue", "rpc_pipefs", "sysfs",
		"bpf", "cgroup", "cgroup2", "configfs", "hugetlbfs", "pstore",
		"securityfs", "tracefs", "binfmt_misc",
	}
	kept := []string{
		"devtmpfs", "tmpfs", "overlay", "zfs", "btrfs", "ext4", "xfs",
		"f2fs", "vfat", "efivarfs", "nfs", "iso9660",
	}

	var b strings.Builder
	for _, fstype := range append(append([]string{}, hidden...), kept...) {
		b.WriteString("dev /mnt/" + fstype + " " + fstype + " rw 0 0\n")
	}
	parts, err := collectors.ParseMounts(b.String())
	if err != nil {
		t.Fatalf("ParseMounts: %v", err)
	}

	got := make(map[string]bool, len(parts))
	for _, p := range parts {
		got[p.FSType] = true
	}
	for _, fstype := range hidden {
		if got[fstype] {
			t.Errorf("fstype %q must be filtered like plain df hides it", fstype)
		}
	}
	for _, fstype := range kept {
		if !got[fstype] {
			t.Errorf("fstype %q must stay listed like plain df shows it", fstype)
		}
	}
}

// TestParseMountsMalformedLineErrors pins the corruption policy: the
// kernel always writes six fields per mount line, so a short line
// errors the parse (the ParseStat policy), while blank lines — the
// file's trailing newline — are tolerated.
func TestParseMountsMalformedLineErrors(t *testing.T) {
	if _, err := collectors.ParseMounts("dev /mnt ext4\n"); err == nil {
		t.Fatal("a 3-field line must error")
	}
	parts, err := collectors.ParseMounts("dev / ext4 rw 0 0\n\n")
	if err != nil {
		t.Fatalf("trailing blank line must be tolerated: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("got %d rows, want 1", len(parts))
	}
}

// TestFilesystemsOnFixture drives Filesystems against the golden
// fixture root: the parse keeps the rows plain df shows (devtmpfs
// included, the autofs row gone, the \040 mount unescaped) and the
// live guard keeps usage at zeros — statfs has no root prefix, so a
// fixture System must never measure the test runner's filesystems.
func TestFilesystemsOnFixture(t *testing.T) {
	parts, err := collectors.New("testdata").Filesystems()
	if err != nil {
		t.Fatalf("Filesystems: %v", err)
	}
	want := []core.Partition{
		{Device: "udev", Mount: "/dev", FSType: "devtmpfs"},
		{Device: "tmpfs", Mount: "/run", FSType: "tmpfs"},
		{Device: "/dev/sda2", Mount: "/", FSType: "ext4"},
		{Device: "tmpfs", Mount: "/dev/shm", FSType: "tmpfs"},
		{Device: "overlay", Mount: "/var/lib/docker/overlay2/6f3cb1.../merged", FSType: "overlay"},
		{Device: "/dev/sda1", Mount: "/boot", FSType: "vfat"},
		{Device: "/dev/sda3", Mount: "/home", FSType: "btrfs"},
		{Device: "/dev/sdb1", Mount: "/mnt/data disk", FSType: "ext4"},
		{Device: "tmpfs", Mount: "/run/user/1000", FSType: "tmpfs"},
	}
	if len(parts) != len(want) {
		t.Fatalf("got %d rows, want %d:\n%+v", len(parts), len(want), parts)
	}
	for i := range want {
		if parts[i].Device != want[i].Device || parts[i].Mount != want[i].Mount ||
			parts[i].FSType != want[i].FSType {
			t.Errorf("row %d = %+v, want %+v", i, parts[i], want[i])
		}
		if parts[i].Total != 0 || parts[i].Used != 0 || parts[i].Avail != 0 || parts[i].UsedPct != 0 {
			t.Errorf("fixture row %d must carry zero usage (live guard): %+v", i, parts[i])
		}
	}
}

// TestFilesystemsMissingMountsFile pins the error surface: a root
// without proc/mounts fails the listing instead of returning an
// empty filesystem table that would read as "nothing mounted".
func TestFilesystemsMissingMountsFile(t *testing.T) {
	if _, err := collectors.New(t.TempDir()).Filesystems(); err == nil {
		t.Fatal("a missing proc/mounts must error")
	}
}

// TestParseDiskstatsFields pins the field positions per kernel
// Documentation/admin-guide/iostats.rst: field 4 is reads completed
// and field 8 writes completed, with the merged/sector/tick fields
// between them ignored. Partition rows (sda1, sda2) land in the map
// like any other row — the disks join never looks them up by name.
func TestParseDiskstatsFields(t *testing.T) {
	content := "8 0 sda 111 222 333 444 555 666 777 888 999 12 13 14 15\n" +
		"259 0 nvme0n1 20485760 5120 302483712 1038462 15693248 2560 439806464 1276555 0 2327950 2307439 0 0 0 0\n" +
		"7 0 loop0 512 0 4096 12 0 0 0 0 0 12 12 0 0 0 0\n"
	stats, err := collectors.ParseDiskstats(content)
	if err != nil {
		t.Fatalf("ParseDiskstats: %v", err)
	}
	want := map[string]collectors.DiskIO{
		"sda":     {Reads: 111, Writes: 555},
		"nvme0n1": {Reads: 20485760, Writes: 15693248},
		"loop0":   {Reads: 512, Writes: 0},
	}
	if len(stats) != len(want) {
		t.Fatalf("got %d devices, want %d: %+v", len(stats), len(want), stats)
	}
	for name, io := range want {
		if got := stats[name]; got != io {
			t.Errorf("stats[%q] = %+v, want %+v", name, got, io)
		}
	}
}

// TestParseDiskstatsMalformedRows errors on both shapes the kernel
// never writes: a short line (fewer than the 8 fields through writes
// completed) and a non-numeric counter.
func TestParseDiskstatsMalformedRows(t *testing.T) {
	if _, err := collectors.ParseDiskstats("8 0 sda 111 222\n"); err == nil {
		t.Fatal("a short row must error")
	}
	if _, err := collectors.ParseDiskstats("8 0 sda nan 222 333 444 555 666\n"); err == nil {
		t.Fatal("a non-numeric counter must error")
	}
	// Blank lines — the trailing newline — are tolerated.
	if stats, err := collectors.ParseDiskstats("8 0 sda 1 0 0 0 2 0 0 0 0 0 0\n\n"); err != nil || stats["sda"] != (collectors.DiskIO{Reads: 1, Writes: 2}) {
		t.Fatalf("trailing blank line must be tolerated: %v %+v", err, stats)
	}
}

// TestDisksOnFixture walks the golden sysfs tree end to end: names
// and sizes from the block directories, models where device/model
// exists (and empty where it does not — loop0), partition geometry
// through the kernel's digit-ending rule (nvme0n1p1), and the
// diskstats join by name — sda's counters are its own line's, not the
// sum over its partition rows.
func TestDisksOnFixture(t *testing.T) {
	disks, err := collectors.New("testdata").Disks()
	if err != nil {
		t.Fatalf("Disks: %v", err)
	}
	want := []core.Disk{
		{
			Name:      "loop0",
			SizeBytes: 121734144 * 512,
			Reads:     512,
			Writes:    0,
		},
		{
			Name:      "nvme0n1",
			Model:     "Samsung SSD 970 EVO Plus 500GB",
			SizeBytes: 1000215216 * 512,
			Partitions: []core.Partition{
				{Device: "nvme0n1p1", Total: 976771168 * 512},
			},
			Reads:  20485760,
			Writes: 15693248,
		},
		{
			Name:      "sda",
			Model:     "WDC WDS240G2G0A-",
			SizeBytes: 468877312 * 512,
			Partitions: []core.Partition{
				{Device: "sda1", Total: 2048000 * 512},
				{Device: "sda2", Total: 134217728 * 512},
			},
			Reads:  11820704,
			Writes: 11292348,
		},
	}
	if len(disks) != len(want) {
		t.Fatalf("got %d disks, want %d:\n%+v", len(disks), len(want), disks)
	}
	for i := range want {
		got := disks[i]
		if got.Name != want[i].Name || got.Model != want[i].Model ||
			got.SizeBytes != want[i].SizeBytes || got.Reads != want[i].Reads ||
			got.Writes != want[i].Writes {
			t.Errorf("disk %d = %+v, want %+v", i, got, want[i])
		}
		if len(got.Partitions) != len(want[i].Partitions) {
			t.Errorf("disk %s: %d partitions, want %d: %+v",
				got.Name, len(got.Partitions), len(want[i].Partitions), got.Partitions)
			continue
		}
		for j := range want[i].Partitions {
			p, wp := got.Partitions[j], want[i].Partitions[j]
			if p.Device != wp.Device || p.Total != wp.Total {
				t.Errorf("disk %s partition %d = %+v, want %+v", got.Name, j, p, wp)
			}
			// Partition rows are geometry only (core.Disk): no mount,
			// no fstype, no usage story to tell.
			if p.Mount != "" || p.FSType != "" || p.UsedPct != 0 {
				t.Errorf("disk %s partition %d must be geometry-only, got %+v", got.Name, j, p)
			}
		}
	}
}

// TestDisksWithoutDiskstats pins the container case: some environments
// mask /proc/diskstats, and the disks listing survives with zero
// counters instead of failing — geometry is still worth showing.
func TestDisksWithoutDiskstats(t *testing.T) {
	dir := t.TempDir()
	block := filepath.Join(dir, "sys", "block", "vda")
	if err := os.MkdirAll(block, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(block, "size"), []byte("1000215216\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	disks, err := collectors.New(dir).Disks()
	if err != nil {
		t.Fatalf("missing diskstats must not fail Disks: %v", err)
	}
	if len(disks) != 1 {
		t.Fatalf("got %d disks, want 1: %+v", len(disks), disks)
	}
	if disks[0].Name != "vda" || disks[0].Reads != 0 || disks[0].Writes != 0 {
		t.Errorf("disk = %+v, want vda with zero counters", disks[0])
	}
}

// TestDisksMissingSysBlock pins the Sensors policy: a root without a
// sys/block tree is an empty listing, not an error — live systems
// always carry the tree, only fixtures (or a masked sysfs) can lack it.
func TestDisksMissingSysBlock(t *testing.T) {
	disks, err := collectors.New(t.TempDir()).Disks()
	if err != nil {
		t.Fatalf("missing sys/block must be an empty listing: %v", err)
	}
	if len(disks) != 0 {
		t.Fatalf("got %d disks, want none", len(disks))
	}
}
