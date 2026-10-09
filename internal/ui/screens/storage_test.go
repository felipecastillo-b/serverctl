package screens

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// fakeStorageLister serves static listings (or per-list errors) instead
// of reading /proc, /sys and statfs.
type fakeStorageLister struct {
	filesystems []core.Partition
	disks       []core.Disk
	fsErr       error
	diskErr     error
	fsCalls     int
	diskCalls   int
}

func (f *fakeStorageLister) Filesystems() ([]core.Partition, error) {
	f.fsCalls++
	return f.filesystems, f.fsErr
}

func (f *fakeStorageLister) Disks() ([]core.Disk, error) {
	f.diskCalls++
	return f.disks, f.diskErr
}

// Compile-time proof that the fake satisfies the m5a port, exactly
// like collectors.System does.
var _ core.StorageLister = (*fakeStorageLister)(nil)

// fsFixture mixes a danger-threshold boot partition, a
// secondary-threshold /home, a plain root and a zero-usage tmpfs so
// the use% sort ladder and the color thresholds have real rows.
var fsFixture = []core.Partition{
	{Device: "/dev/sda2", Mount: "/", FSType: "ext4", Total: 100 << 30, Used: 20 << 30, Avail: 80 << 30, UsedPct: 20.0},
	{Device: "/dev/sda3", Mount: "/home", FSType: "btrfs", Total: 500 << 30, Used: 400 << 30, Avail: 100 << 30, UsedPct: 80.0},
	{Device: "/dev/sda1", Mount: "/boot", FSType: "vfat", Total: 1 << 30, Used: 990 << 20, Avail: 10 << 20, UsedPct: 99.0},
	{Device: "tmpfs", Mount: "/run", FSType: "tmpfs", Total: 4 << 30, Used: 0, Avail: 4 << 30, UsedPct: 0},
}

// diskFixture mirrors the collector fixture shapes: a SCSI disk with
// partitions and a model, a digit-named NVMe with its p-partition,
// and a model-less virtual device.
var diskFixture = []core.Disk{
	{Name: "sda", Model: "WDC WDS240G2G0A-", SizeBytes: 240 << 30,
		Partitions: []core.Partition{{Device: "sda1"}, {Device: "sda2"}},
		Reads:      61736, Writes: 88433},
	{Name: "zram0", SizeBytes: 4 << 30, Reads: 75, Writes: 1},
	{Name: "nvme0n1", Model: "Samsung SSD 970 EVO Plus 500GB", SizeBytes: 500 << 30,
		Partitions: []core.Partition{{Device: "nvme0n1p1"}},
		Reads:      20485760, Writes: 15693248},
}

// newStorageScreen builds the storage screen over the given fake and
// drives the first collection round synchronously. Init batches
// [collect, tick] and that batch is lazy — executing it only yields
// the legs — so the fixture runs the collect leg; the 30 s timer leg
// stays runtime territory, driving it here would stall the test.
func newStorageScreen(t *testing.T, lister *fakeStorageLister) *storage {
	t.Helper()
	s := newStorage(lister, theme.Dark())
	cmd := s.Init()
	if cmd == nil {
		t.Fatal("Init must collect and start the cadence")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("Init must batch collect and the cadence tick, got %T len %d", batch, len(batch))
	}
	updated, _ := s.Update(batch[0]())
	s, ok = updated.(*storage)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if !s.fsLoaded || !s.disksLoaded || s.fsErr != nil || s.diskErr != nil {
		t.Fatalf("fixture collection: fsLoaded=%v fsErr=%v disksLoaded=%v diskErr=%v",
			s.fsLoaded, s.fsErr, s.disksLoaded, s.diskErr)
	}
	return s
}

// fsMounts lists the mount points of the filesystems view in table
// order.
func fsMounts(parts []core.Partition) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.Mount
	}
	return out
}

// diskNames lists the disk names of the disks view in table order.
func diskNames(disks []core.Disk) []string {
	out := make([]string, len(disks))
	for i, d := range disks {
		out[i] = d.Name
	}
	return out
}

func TestStorageRendersFilesystemsView(t *testing.T) {
	s := newStorageScreen(t, &fakeStorageLister{filesystems: fsFixture, disks: diskFixture})
	view := s.View(100, 24)
	for _, want := range []string{
		"DEVICE", "MOUNT", "FSTYPE", "SIZE", "USED", "AVAIL", "USE%",
		"/dev/sda2", "/home", "btrfs", "100.0 GiB", "400.0 GiB", "99%",
		"4 filesystems · sort use ↓",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	// The disks half of the round stays out of this view's table.
	for _, banned := range []string{"READS", "nvme0n1", "WDC"} {
		if strings.Contains(view, banned) {
			t.Errorf("filesystems view must not show disk data %q:\n%s", banned, view)
		}
	}
}

func TestStorageDisksViewToggle(t *testing.T) {
	lister := &fakeStorageLister{filesystems: fsFixture, disks: diskFixture}
	s := newStorageScreen(t, lister)
	if lister.fsCalls != 1 || lister.diskCalls != 1 {
		t.Fatalf("one round must collect both lists once, got fs=%d disk=%d",
			lister.fsCalls, lister.diskCalls)
	}

	updated, _, handled := s.UpdateKey(keyMsg("d"))
	s = updated.(*storage)
	if !handled || s.view != storageDisks {
		t.Fatal("'d' must toggle to the disks view")
	}
	// The toggle re-renders the round's other half: no new collection.
	if lister.fsCalls != 1 || lister.diskCalls != 1 {
		t.Fatalf("toggle must not re-collect, got fs=%d disk=%d", lister.fsCalls, lister.diskCalls)
	}
	view := s.View(100, 24)
	for _, want := range []string{
		"NAME", "MODEL", "SIZE", "PARTS", "READS", "WRITES",
		"sda", "WDC WDS240G2G0A-", "zram0", "2", "61736", "88433",
		"3 disks · sort name ↑",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("disks view missing %q:\n%s", want, view)
		}
	}

	// And back: the filesystems reappear, still without re-collecting.
	updated, _, handled = s.UpdateKey(keyMsg("d"))
	s = updated.(*storage)
	if !handled || s.view != storageFilesystems {
		t.Fatal("'d' must toggle back to the filesystems view")
	}
	if !strings.Contains(s.View(100, 24), "4 filesystems · sort use ↓") {
		t.Errorf("toggle back must re-render the filesystems:\n%s", s.View(100, 24))
	}
	if lister.fsCalls != 1 || lister.diskCalls != 1 {
		t.Fatalf("round-tripping the toggle must not re-collect, got fs=%d disk=%d",
			lister.fsCalls, lister.diskCalls)
	}
}

func TestStorageUsePctColorThresholds(t *testing.T) {
	th := theme.Dark()
	tests := []struct {
		name string
		pct  float64
		want lipgloss.Color
	}{
		{"zero is default", 0, th.Foreground},
		{"just below danger stays secondary", 89.9, th.Secondary},
		{"danger at 90", 90, th.Danger},
		{"full is danger", 100, th.Danger},
		{"secondary at 75", 75, th.Secondary},
		{"just below secondary is default", 74.9, th.Foreground},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := usePctColor(tt.pct, th); got != tt.want {
				t.Errorf("usePctColor(%v) = %v, want %v", tt.pct, got, tt.want)
			}
		})
	}
}

// TestStorageUsePctCellRendersDfInteger pins df's display convention:
// the raw percentage rounded UP to the next integer (coreutils df.c),
// 0 staying 0 — 44.85 renders as "45%", 99.9 as "100%".
func TestStorageUsePctCellRendersDfInteger(t *testing.T) {
	th := theme.Dark()
	tests := []struct {
		pct  float64
		want string
	}{
		{0, "0%"},
		{44.854, "45%"},
		{99.9, "100%"},
		{80.0, "80%"},
	}
	for _, tt := range tests {
		if cell := usePctCell(tt.pct, th); !strings.Contains(cell, tt.want) {
			t.Errorf("usePctCell(%v) = %q, want it to display %q", tt.pct, cell, tt.want)
		}
	}
}

func TestStorageFilesystemsSorting(t *testing.T) {
	s := newStorageScreen(t, &fakeStorageLister{filesystems: fsFixture, disks: diskFixture})

	cases := []struct {
		name string
		key  string
		want []string
	}{
		{"use attention order is the default", "",
			[]string{"/boot", "/home", "/", "/run"}},
		{"m sorts by mount asc", "m",
			[]string{"/", "/boot", "/home", "/run"}},
		{"m again flips to desc", "m",
			[]string{"/run", "/home", "/boot", "/"}},
		{"s sorts by size asc", "s",
			[]string{"/boot", "/run", "/", "/home"}},
		{"s again flips to biggest first", "s",
			[]string{"/home", "/", "/run", "/boot"}},
		{"u returns to the use ladder", "u",
			[]string{"/boot", "/home", "/", "/run"}},
	}

	for idx, tc := range cases {
		if tc.key != "" {
			updated, _, handled := s.UpdateKey(keyMsg(tc.key))
			if !handled {
				t.Fatalf("sort key %q not handled", tc.key)
			}
			s = updated.(*storage)
		} else if idx > 0 {
			t.Fatal("table driven misuse: default case must be first")
		}
		if got := fsMounts(s.fsView); !slices.Equal(got, tc.want) {
			t.Errorf("%s: order = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestStorageFilesystemsFilter(t *testing.T) {
	s := newStorageScreen(t, &fakeStorageLister{filesystems: fsFixture, disks: diskFixture})

	if _, _, handled := s.UpdateKey(keyMsg("/")); !handled || !s.filtering {
		t.Fatal("filter mode did not open")
	}
	// "btrfs" matches a fstype, proving the query spans device, mount
	// and type.
	for _, r := range "btrfs" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if got := fsMounts(s.fsView); !slices.Equal(got, []string{"/home"}) {
		t.Fatalf("filter 'btrfs': %v", got)
	}
	if !strings.Contains(s.View(100, 24), `filter "btrfs"`) {
		t.Error("status line must show the active filter")
	}

	// esc closes the input but keeps the applied filter.
	if _, _, handled := s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled || s.filtering {
		t.Fatal("esc must close filter mode")
	}
	if got := fsMounts(s.fsView); !slices.Equal(got, []string{"/home"}) {
		t.Fatalf("closed filter must keep rows applied: %v", got)
	}

	// A fresh query: "boot" matches the mount point.
	s.UpdateKey(keyMsg("/"))
	s.filter.SetValue("")
	for _, r := range "boot" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if got := fsMounts(s.fsView); !slices.Equal(got, []string{"/boot"}) {
		t.Fatalf("filter 'boot': %v", got)
	}

	// A no-match query empties the view.
	s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc})
	s.UpdateKey(keyMsg("/"))
	for _, r := range "zzz" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if len(s.fsView) != 0 {
		t.Fatalf("no-match filter must empty the view: %v", fsMounts(s.fsView))
	}
}

func TestStorageDisksSortAndFilter(t *testing.T) {
	s := newStorageScreen(t, &fakeStorageLister{filesystems: fsFixture, disks: diskFixture})
	s.UpdateKey(keyMsg("d"))

	if got := diskNames(s.diskView); !slices.Equal(got, []string{"nvme0n1", "sda", "zram0"}) {
		t.Fatalf("disks default order = %v, want name asc", got)
	}
	// n flips the single disks sort column.
	if _, _, handled := s.UpdateKey(keyMsg("n")); !handled {
		t.Fatal("'n' must be handled in the disks view")
	}
	if got := diskNames(s.diskView); !slices.Equal(got, []string{"zram0", "sda", "nvme0n1"}) {
		t.Fatalf("after n = %v, want name desc", got)
	}

	// The filter spans name and model: "samsung" matches a model only.
	s.UpdateKey(keyMsg("/"))
	for _, r := range "samsung" {
		s.UpdateKey(keyMsg(string(r)))
	}
	if got := diskNames(s.diskView); !slices.Equal(got, []string{"nvme0n1"}) {
		t.Fatalf("filter 'samsung': %v", got)
	}
	if !strings.Contains(s.View(100, 24), `filter "samsung"`) {
		t.Error("status line must show the active filter in the disks view")
	}
}

func TestStorageTickRearmsOwnCadence(t *testing.T) {
	lister := &fakeStorageLister{filesystems: fsFixture, disks: diskFixture}
	s := newStorageScreen(t, lister)
	if lister.fsCalls != 1 || lister.diskCalls != 1 {
		t.Fatalf("fixture must have collected once, got fs=%d disk=%d", lister.fsCalls, lister.diskCalls)
	}

	// The shell's 2 s RefreshMsg drives the dashboard and processes
	// screens; Storage must ignore it and keep its own 30 s cadence
	// (ARCHITECTURE.md §3).
	updated, cmd := s.Update(RefreshMsg{})
	s = updated.(*storage)
	if cmd != nil {
		t.Error("RefreshMsg must not trigger a storage collection")
	}
	if lister.fsCalls != 1 || lister.diskCalls != 1 {
		t.Errorf("RefreshMsg must not re-collect: fs=%d disk=%d", lister.fsCalls, lister.diskCalls)
	}

	// The screen's own tick re-collects both halves and re-arms.
	updated, cmd = s.Update(storageTickMsg{})
	s = updated.(*storage)
	if cmd == nil {
		t.Fatal("the tick must re-arm the cadence")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the tick must re-arm collect and the next tick, got %T len %d", batch, len(batch))
	}
	s.Update(batch[0]())
	if lister.fsCalls != 2 || lister.diskCalls != 2 {
		t.Errorf("the re-armed collect must fetch fresh listings: fs=%d disk=%d",
			lister.fsCalls, lister.diskCalls)
	}
}

// TestStorageErrorAndCollectingStates pins the three status-line
// states per view: the error of the ACTIVE view's half — one failed
// half never blanks the other — the collecting state until the first
// round lands, and the listing summary.
func TestStorageErrorAndCollectingStates(t *testing.T) {
	// Before the first round: collecting.
	fresh := newStorage(&fakeStorageLister{}, theme.Dark())
	if out := fresh.View(100, 24); !strings.Contains(out, "collecting...") {
		t.Errorf("fresh filesystems view must show collecting..., got:\n%s", out)
	}

	// A round with a failed filesystems half and a healthy disks half.
	lister := &fakeStorageLister{
		disks: diskFixture,
		fsErr: errors.New("no proc mounts"),
	}
	s := newStorage(lister, theme.Dark())
	updated, _ := s.Update(s.collect()())
	s = updated.(*storage)
	if !s.disksLoaded || s.fsLoaded {
		t.Fatalf("round state: fsLoaded=%v (want false on error) disksLoaded=%v (want true)",
			s.fsLoaded, s.disksLoaded)
	}
	if out := s.View(100, 24); !strings.Contains(out, "n/a (no proc mounts)") {
		t.Errorf("filesystems view must show its collection error:\n%s", out)
	}

	// The disks half of the same round is fine and renders normally.
	s.UpdateKey(keyMsg("d"))
	out := s.View(100, 24)
	if !strings.Contains(out, "3 disks · sort name ↑") {
		t.Errorf("the healthy disks half must render despite the fs error:\n%s", out)
	}
	if strings.Contains(out, "n/a") {
		t.Errorf("the disks view must not inherit the filesystems error:\n%s", out)
	}

	// An empty round renders as a zero-row listing, not an error.
	empty := newStorageScreen(t, &fakeStorageLister{})
	if out := empty.View(100, 24); !strings.Contains(out, "0 filesystems") {
		t.Errorf("empty round must show the zero-row summary:\n%s", out)
	}
}

func TestStorageCursorKeptOnRefresh(t *testing.T) {
	s := newStorageScreen(t, &fakeStorageLister{filesystems: fsFixture, disks: diskFixture})
	// Default use ladder: /boot, /home, /, /run. Move to /home.
	if _, _, handled := s.UpdateKey(keyMsg("down")); !handled {
		t.Fatal("down must be handled")
	}
	if got := s.fsView[s.table.Cursor()].Mount; got != "/home" {
		t.Fatalf("cursor on %q, want /home", got)
	}

	// A refreshed round flips the ladder — / jumps to 95% — and the
	// cursor must stay on /home.
	refreshed := slices.Clone(fsFixture)
	refreshed[0].Used = 95 << 30
	refreshed[0].Avail = 5 << 30
	refreshed[0].UsedPct = 95
	updated, _ := s.Update(storageDataMsg{filesystems: refreshed})
	s = updated.(*storage)
	if got := s.fsView[s.table.Cursor()].Mount; got != "/home" {
		t.Fatalf("cursor drifted to %q after refresh, want /home", got)
	}
}

func TestStorageNavKeysClaimedAndOthersFallThrough(t *testing.T) {
	s := newStorageScreen(t, &fakeStorageLister{filesystems: fsFixture, disks: diskFixture})

	if _, _, handled := s.UpdateKey(keyMsg("down")); !handled {
		t.Fatal("navigation keys must be claimed by the screen")
	}
	if s.table.Cursor() != 1 {
		t.Errorf("cursor = %d, want 1 after down", s.table.Cursor())
	}

	// Keys outside the screen keymap fall through to the global map
	// (q quits, ? helps, tab cycles, j is the shell's up).
	for _, k := range []string{"q", "?", "j", "tab"} {
		if _, _, handled := s.UpdateKey(keyMsg(k)); handled {
			t.Errorf("key %q must fall through to the global keymap", k)
		}
	}
}
