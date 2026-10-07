package collectors

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// userHZ is the kernel tick rate used for stat time fields. 100 is the
// value on Linux (verified with getconf CLK_TCK).
const userHZ = 100

// ProcStat holds the /proc/<pid>/stat fields serverctl needs.
type ProcStat struct {
	Name      string
	State     byte
	PPID      int
	Utime     uint64
	Stime     uint64
	Starttime uint64
}

// Jiffies returns the cumulative user+system tick counter of the process.
func (p ProcStat) Jiffies() uint64 {
	return p.Utime + p.Stime
}

// ParseStat parses one /proc/<pid>/stat line. The comm field is enclosed
// in parentheses and may itself contain spaces and parentheses, so the
// split happens at the LAST ')' of the line. Field numbering then counts
// from the character after that ')': state, ppid, utime, stime and
// starttime sit at remainder indices 0, 1, 11, 12 and 19 (absolute fields
// 3, 4, 14, 15 and 22 per proc(5)).
func ParseStat(raw string) (ProcStat, error) {
	open := strings.Index(raw, "(")
	close := strings.LastIndex(raw, ")")
	if open < 0 || close < 0 || close < open {
		return ProcStat{}, fmt.Errorf("malformed stat line: comm parentheses not found")
	}
	rest := strings.Fields(raw[close+1:])
	if len(rest) < 20 {
		return ProcStat{}, fmt.Errorf("malformed stat line: %d fields after comm, want at least 20", len(rest))
	}
	ppid, err := strconv.Atoi(rest[1])
	if err != nil {
		return ProcStat{}, fmt.Errorf("stat ppid %q: %w", rest[1], err)
	}
	utime, err := strconv.ParseUint(rest[11], 10, 64)
	if err != nil {
		return ProcStat{}, fmt.Errorf("stat utime %q: %w", rest[11], err)
	}
	stime, err := strconv.ParseUint(rest[12], 10, 64)
	if err != nil {
		return ProcStat{}, fmt.Errorf("stat stime %q: %w", rest[12], err)
	}
	starttime, err := strconv.ParseUint(rest[19], 10, 64)
	if err != nil {
		return ProcStat{}, fmt.Errorf("stat starttime %q: %w", rest[19], err)
	}
	return ProcStat{
		Name:      raw[open+1 : close],
		State:     rest[0][0],
		PPID:      ppid,
		Utime:     utime,
		Stime:     stime,
		Starttime: starttime,
	}, nil
}

// Processes walks <root>/proc and parses every numeric <pid> directory it
// can read. Processes that vanish mid-walk (or whose stat is unreadable)
// are skipped without failing the whole listing. The result is sorted by
// PID for deterministic output.
func (s System) Processes() ([]core.Process, error) {
	boot, err := s.bootTime()
	if err != nil {
		return nil, err
	}
	users := s.userMap()

	entries, err := os.ReadDir(s.proc())
	if err != nil {
		return nil, fmt.Errorf("read proc: %w", err)
	}

	procs := make([]core.Process, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		proc, err := s.readProcess(pid, boot, users)
		if err != nil {
			continue // died mid-walk or unreadable: skip, never fail the walk
		}
		procs = append(procs, proc)
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].PID < procs[j].PID })
	return procs, nil
}

// readProcess assembles one Process from its stat, statm, cmdline and the
// owner of its proc directory. Missing statm/cmdline degrade to zero
// values instead of dropping the process.
func (s System) readProcess(pid int, boot int64, users map[uint32]string) (core.Process, error) {
	dir := s.proc(strconv.Itoa(pid))

	statRaw, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return core.Process{}, fmt.Errorf("read stat: %w", err)
	}
	stat, err := ParseStat(string(statRaw))
	if err != nil {
		return core.Process{}, err
	}

	info, err := os.Stat(dir)
	if err != nil {
		return core.Process{}, fmt.Errorf("stat proc dir: %w", err)
	}
	uid := uint32(0)
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		uid = st.Uid
	}

	user, ok := users[uid]
	if !ok {
		user = strconv.FormatUint(uint64(uid), 10)
	}

	return core.Process{
		PID:       pid,
		PPID:      stat.PPID,
		Name:      stat.Name,
		State:     stat.State,
		User:      user,
		Cmdline:   readCmdline(dir + "/cmdline"),
		RSS:       readRSS(dir + "/statm"),
		StartTime: time.Unix(boot+int64(stat.Starttime/userHZ), 0),
		Jiffies:   stat.Jiffies(),
	}, nil
}

// bootTime reads the btime line of <root>/proc/stat: seconds since epoch
// at which the system booted.
func (s System) bootTime() (int64, error) {
	f, err := os.Open(s.proc("stat"))
	if err != nil {
		return 0, fmt.Errorf("open stat: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "btime" {
			boot, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("malformed btime %q: %w", fields[1], err)
			}
			return boot, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan stat: %w", err)
	}
	return 0, fmt.Errorf("no btime line in proc stat")
}

// userMap parses <root>/etc/passwd into a uid-to-name map. An unreadable
// passwd degrades to an empty map (users fall back to decimal uids); the
// root prefix keeps golden fixtures hermetic, which os/user.LookupId
// cannot honor.
func (s System) userMap() map[uint32]string {
	users := make(map[uint32]string)
	f, err := os.Open(s.rootPath("etc", "passwd"))
	if err != nil {
		return users
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) < 3 {
			continue
		}
		uid, err := strconv.ParseUint(fields[2], 10, 32)
		if err != nil {
			continue
		}
		users[uint32(uid)] = fields[0]
	}
	return users
}

// readCmdline joins the NUL-separated argv fields of /proc/<pid>/cmdline
// with spaces. Kernel threads have an empty cmdline.
func readCmdline(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	parts := strings.FieldsFunc(string(raw), func(r rune) bool { return r == 0 })
	return strings.Join(parts, " ")
}

// readRSS converts the resident-pages field of /proc/<pid>/statm to bytes.
func readRSS(path string) uint64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}

// ProcessTracker turns cumulative per-PID jiffies into live CPU
// percentages between two process listings, mirroring CPUTracker. The
// zero value is ready to use.
//
// CpuPct is pidDelta/totalDelta*100 with NO core-count multiplier
// (Solaris mode): 100% means the process consumed every jiffy the whole
// machine had; one busy thread on N cores reads about 100/N.
type ProcessTracker struct {
	prevJiffies map[int]uint64
	prevTotal   uint64
	started     bool
}

// Refresh consumes one process listing plus the machine's current total
// jiffies (the aggregate CPUSample total) and returns a copy of it with
// CpuPct filled. The first call and any zero or backwards total delta can
// only establish the baseline, so they report 0% by design. PIDs unseen
// in the previous sample report 0, and vanished PIDs are dropped from the
// tracker state so the map cannot leak.
func (t *ProcessTracker) Refresh(current []core.Process, totalJiffies uint64) []core.Process {
	out := make([]core.Process, len(current))
	copy(out, current)

	if !t.started || totalJiffies <= t.prevTotal {
		t.prevJiffies = snapshotJiffies(out)
		t.prevTotal = totalJiffies
		t.started = true
		return out
	}

	totalDelta := totalJiffies - t.prevTotal
	for i := range out {
		prev, ok := t.prevJiffies[out[i].PID]
		if !ok || out[i].Jiffies < prev {
			continue // new pid, or counter reset after pid reuse: baseline only
		}
		out[i].CpuPct = float64(out[i].Jiffies-prev) / float64(totalDelta) * 100
	}
	t.prevJiffies = snapshotJiffies(out)
	t.prevTotal = totalJiffies
	return out
}

// snapshotJiffies captures the per-PID jiffy counters of one listing.
func snapshotJiffies(procs []core.Process) map[int]uint64 {
	snap := make(map[int]uint64, len(procs))
	for _, p := range procs {
		snap[p.PID] = p.Jiffies
	}
	return snap
}
