package collectors

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// dfHiddenFSTypes is the filesystem-type hiding set that makes the
// Filesystems listing match what plain `df` prints. Two df mechanisms
// collapse into this one set: coreutils' "dummy" types (gnulib
// mountlist.c ME_DUMMY_0: autofs, proc, subfs, debugfs, devpts,
// fusectl, fuse.portal, mqueue, rpc_pipefs, sysfs, devfs, kernfs) and
// the kernel pseudo-filesystems df drops because statfs reports zero
// size for them (cgroup, cgroup2, securityfs, pstore, bpf, tracefs,
// configfs, hugetlbfs, binfmt_misc). Verified against df (GNU
// coreutils) 9.12 on a live host: devtmpfs is NOT hidden — it reports
// a real size and df lists it — and autofs IS hidden (ME_DUMMY_0),
// even though it carries a mount entry. Everything a server
// administrator sizes disks with stays listed: tmpfs, overlay, zfs,
// btrfs, ext4, xfs, f2fs, vfat, efivarfs, and friends.
var dfHiddenFSTypes = map[string]bool{
	// gnulib ME_DUMMY_0.
	"autofs":      true,
	"proc":        true,
	"subfs":       true,
	"debugfs":     true,
	"devpts":      true,
	"devfs":       true,
	"kernfs":      true,
	"fusectl":     true,
	"fuse.portal": true,
	"mqueue":      true,
	"rpc_pipefs":  true,
	"sysfs":       true,
	// Zero-size kernel pseudo-filesystems df elides.
	"bpf":         true,
	"cgroup":      true,
	"cgroup2":     true,
	"configfs":    true,
	"hugetlbfs":   true,
	"pstore":      true,
	"securityfs":  true,
	"tracefs":     true,
	"binfmt_misc": true,
}

// ParseMounts parses /proc/mounts content into the filesystem rows
// plain df shows: device, mount point and type, with df's hiding set
// (dfHiddenFSTypes) already filtered out. Mount table fields carry the
// kernel's octal escapes for space, tab, newline and backslash
// (fs/proc_namespace.c: \040, \011, \012, \134); both the device and
// the mount point are unescaped. The file is kernel-generated, so a
// line too short to carry the six mount fields is corruption, not
// churn: it errors the whole parse the way ParseStat errors a
// malformed stat line. Blank lines are tolerated (trailing newline).
func ParseMounts(content string) ([]core.Partition, error) {
	var out []core.Partition
	for lineno, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			return nil, fmt.Errorf("malformed mounts line %d (%q): %d fields, want at least 4", lineno+1, line, len(fields))
		}
		if dfHiddenFSTypes[fields[2]] {
			continue
		}
		out = append(out, core.Partition{
			Device: unescapeMountField(fields[0]),
			Mount:  unescapeMountField(fields[1]),
			FSType: fields[2],
		})
	}
	return out, nil
}

// unescapeMountField reverses the kernel's octal mangling of mount
// table fields: space, tab, newline and backslash appear as \040,
// \011, \012 and \134. Only four-character backslash escapes are
// decoded, mirroring gnulib's unescape_tab; any other backslash stays
// literal.
func unescapeMountField(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) &&
			s[i+1] >= '0' && s[i+1] <= '3' &&
			s[i+2] >= '0' && s[i+2] <= '7' &&
			s[i+3] >= '0' && s[i+3] <= '7' {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Filesystems lists the mounted filesystems plain df shows: the rows
// ParseMounts reads from <root>/proc/mounts, enriched with statfs
// usage byte counts when the System is live (ARCHITECTURE.md §8).
//
// statfs is a syscall, not a file read — it has no root prefix, so a
// fixture System would measure the test runner's own filesystems
// instead of the fixture. The live guard skips enrichment entirely
// there and the parser-only rows keep zero usage (the shape
// core.Partition documents), which keeps this collector hermetic for
// fixture roots. Live behavior is exercised through enrichUsage's
// injectable statfs hook and usageFromStatfs's pure math instead of a
// real syscall. On a live System, a statfs failure or a non-absolute
// mount point (possible in network namespaces, and something df skips
// too) degrades that ONE row to zeros without dropping it from the
// listing.
func (s System) Filesystems() ([]core.Partition, error) {
	raw, err := os.ReadFile(s.proc("mounts"))
	if err != nil {
		return nil, fmt.Errorf("read mounts: %w", err)
	}
	parts, err := ParseMounts(string(raw))
	if err != nil {
		return nil, err
	}
	if !s.IsLive() {
		return parts, nil
	}
	return enrichUsage(parts, func(mount string) (syscall.Statfs_t, error) {
		var st syscall.Statfs_t
		if err := syscall.Statfs(mount, &st); err != nil {
			return st, err
		}
		return st, nil
	}), nil
}

// enrichUsage fills the usage columns of every mount row through
// statfsFn, the injectable hook Filesystems binds to syscall.Statfs
// on a live System. statfsFn lets tests drive the live path with fake
// values instead of a real syscall (ARCHITECTURE.md §10). A mount
// whose statfs fails degrades that row to zeros and stays listed —
// mounts can vanish mid-collection (container churn) and hiding the
// row would hide a filesystem that was mounted a tick earlier; a
// relative mount point is never stated (statfs would resolve it
// against the process working directory, inventing numbers df never
// shows — plain df skips relative rows too).
func enrichUsage(parts []core.Partition, statfsFn func(string) (syscall.Statfs_t, error)) []core.Partition {
	out := make([]core.Partition, len(parts))
	copy(out, parts)
	for i := range out {
		if !strings.HasPrefix(out[i].Mount, "/") {
			continue // relative mount point: no statfs, zeros stay
		}
		st, err := statfsFn(out[i].Mount)
		if err != nil {
			continue // degraded row: zeros stay, listing survives
		}
		out[i].Total, out[i].Used, out[i].Avail, out[i].UsedPct = usageFromStatfs(&st)
	}
	return out
}

// usageFromStatfs converts one statfs result into df's byte counts
// and Use%: Total = f_blocks×f_bsize, Used = (f_blocks−f_bfree)×f_bsize
// (root-reserved blocks count as used), Avail = f_bavail×f_bsize, and
// UsedPct = used/(used+avail)×100 — df's non-root denominator, so a
// filesystem with no user-claimable space reads 100% while root
// reserve remains (core.Partition documents the formula; df rounds
// the percentage up for display). A zero-size result (f_blocks 0, the
// shape some pseudo-filesystems report) reads all zeros with 0%,
// never a divide-by-zero. Pure: hand-built Statfs_t values test it
// without touching a real filesystem.
func usageFromStatfs(st *syscall.Statfs_t) (total, used, avail uint64, pct float64) {
	bsize := uint64(st.Bsize)
	total = uint64(st.Blocks) * bsize
	used = (uint64(st.Blocks) - uint64(st.Bfree)) * bsize
	avail = uint64(st.Bavail) * bsize
	denom := float64(used) + float64(avail)
	if denom > 0 {
		pct = float64(used) / denom * 100
	}
	return total, used, avail, pct
}

// diskSectorSize is the block size sysfs size files always report
// sectors in, whatever the device's logical block size is.
const diskSectorSize = 512

// DiskIO holds the /proc/diskstats counters a Disk row joins:
// completed I/O operations, cumulative since boot.
type DiskIO struct {
	Reads  uint64 // field 4: reads completed
	Writes uint64 // field 8: writes completed
}

// ParseDiskstats parses /proc/diskstats content into completed-I/O-op
// counters per device name. The first three fields are major, minor
// and name; reads completed is field 4 and writes completed field 8
// per kernel Documentation/admin-guide/iostats.rst — later fields
// (merges, sectors, ticks, flushes, discards on newer kernels) are
// ignored, and partition rows (sda1, sda2, ...) are simply map entries
// the disks join never looks up. The file is kernel-generated, so a
// short or non-numeric line errors the whole parse (the ParseStat
// policy), not a silent counter loss.
func ParseDiskstats(content string) (map[string]DiskIO, error) {
	out := make(map[string]DiskIO)
	for lineno, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			return nil, fmt.Errorf("malformed diskstats line %d (%q): %d fields, want at least 8", lineno+1, line, len(fields))
		}
		reads, err := strconv.ParseUint(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("diskstats reads for %s: %w", fields[2], err)
		}
		writes, err := strconv.ParseUint(fields[7], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("diskstats writes for %s: %w", fields[2], err)
		}
		out[fields[2]] = DiskIO{Reads: reads, Writes: writes}
	}
	return out, nil
}

// diskIO reads <root>/proc/diskstats for the Disks join. A missing
// file degrades to no counters — some containers mask /proc/diskstats
// and a disk listing without I/O counts is still worth showing —
// while a present but corrupt file errors the listing.
func (s System) diskIO() (map[string]DiskIO, error) {
	raw, err := os.ReadFile(s.proc("diskstats"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read diskstats: %w", err)
	}
	return ParseDiskstats(string(raw))
}

// Disks lists the block devices of <root>/sys/block with their sizes,
// models, partitions and completed-I/O-op counters (ARCHITECTURE.md
// §8). Unlike Filesystems' statfs half, everything here is a file read
// under the root prefix, so the walk is hermetic: fixture roots carry
// their own sys/block tree and proc/diskstats. A missing sys/block
// tree is an empty listing, not an error (the Sensors policy: the
// kernel always provides it live, only fixtures can lack it); a
// missing proc/diskstats leaves counters at zero without failing the
// listing. Partition rows carry geometry only — Device and Total,
// with empty Mount and FSType (core.Disk).
func (s System) Disks() ([]core.Disk, error) {
	blockRoot := s.sys("block")
	entries, err := os.ReadDir(blockRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sys/block: %w", err)
	}

	diskStats, err := s.diskIO()
	if err != nil {
		return nil, err
	}

	disks := make([]core.Disk, 0, len(entries))
	for _, entry := range entries {
		// /sys/block entries are symlinks into /sys/devices; the
		// DirEntry itself reports as a link, so resolve before the
		// directory check.
		info, err := os.Stat(filepath.Join(blockRoot, entry.Name()))
		if err != nil || !info.IsDir() {
			continue // vanished mid-walk or not a device: skip
		}
		dir := filepath.Join(blockRoot, entry.Name())
		disk := core.Disk{
			Name:      entry.Name(),
			SizeBytes: readSysSize(filepath.Join(dir, "size")),
			// Virtual devices (loop, zram, ram) have no device/model
			// file; they list with an empty model rather than a guess.
			Model: readSysTrimmed(filepath.Join(dir, "device", "model")),
		}
		disk.Partitions = readPartitions(dir, disk.Name)
		if stats, ok := diskStats[disk.Name]; ok {
			disk.Reads, disk.Writes = stats.Reads, stats.Writes
		}
		disks = append(disks, disk)
	}
	return disks, nil
}

// readSysTrimmed reads a sysfs string attribute, trimmed; missing
// files read as "" so callers can treat absence as the empty value.
func readSysTrimmed(path string) string {
	value, err := readTrimmed(path)
	if err != nil {
		return ""
	}
	return value
}

// readSysSize reads a sysfs size file (512-byte sectors) into bytes.
// A missing or malformed file reads as 0 — a partition without a size
// mid-transition is a sysfs churn state, not a listing-killer.
func readSysSize(path string) uint64 {
	raw, err := readTrimmed(path)
	if err != nil {
		return 0
	}
	sectors, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return sectors * diskSectorSize
}

// readPartitions lists the partition subdirectories of one block
// device: name and size each. Matched by partitionPattern.
func readPartitions(dir, name string) []core.Partition {
	pattern := partitionPattern(name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var parts []core.Partition
	for _, entry := range entries {
		if !pattern.MatchString(entry.Name()) {
			continue
		}
		parts = append(parts, core.Partition{
			Device: entry.Name(),
			Total:  readSysSize(filepath.Join(dir, entry.Name(), "size")),
		})
	}
	return parts
}

// partitionPattern builds the sysfs partition-name pattern for one
// disk. The kernel appends the partition number directly (sda → sda1)
// unless the disk name ends in a digit, where it inserts a "p"
// (nvme0n1 → nvme0n1p1) — block/partitions/core.c's naming rule — so
// the pattern follows the same rule and never matches another disk's
// subdirectories.
func partitionPattern(name string) *regexp.Regexp {
	sep := ""
	if len(name) > 0 && name[len(name)-1] >= '0' && name[len(name)-1] <= '9' {
		sep = "p"
	}
	return regexp.MustCompile("^" + regexp.QuoteMeta(name) + sep + `\d+$`)
}

// Compile-time proof that System satisfies the Storage read port.
var _ core.StorageLister = System{}
