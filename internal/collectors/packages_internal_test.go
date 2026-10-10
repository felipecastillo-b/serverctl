package collectors

import (
	"errors"
	"os"
	"testing"
)

// fakeDirEntry is a scripted os.DirEntry for the countPackageDirs
// table: a pacman local database listing without touching a real
// directory. Info is never called — IsDir answers from the scripted
// flag, the way ReadDir's dirent types do.
type fakeDirEntry struct {
	name  string
	isDir bool
}

func (e fakeDirEntry) Name() string { return e.name }
func (e fakeDirEntry) IsDir() bool  { return e.isDir }
func (e fakeDirEntry) Type() os.FileMode {
	if e.isDir {
		return os.ModeDir
	}
	return 0
}
func (e fakeDirEntry) Info() (os.FileInfo, error) {
	return nil, errors.New("fake DirEntry carries no Info")
}

// TestCountPackageDirs pins the local-database counting rule: one
// subdirectory per package, regular files never counted. The
// non-package rows are the live-host shapes — ALPM_DB_VERSION, which
// every pacman 5.x database directory carries, and db.lck, the
// transient transaction lock — both observed as plain files on real
// hosts (a full listing reads 589 entries against 588 packages).
func TestCountPackageDirs(t *testing.T) {
	tests := []struct {
		name    string
		entries []os.DirEntry
		want    int
	}{
		{
			name: "package directories count",
			entries: []os.DirEntry{
				fakeDirEntry{name: "filesystem-2024.04.22-1", isDir: true},
				fakeDirEntry{name: "linux-6.6.8-1", isDir: true},
				fakeDirEntry{name: "pacman-7.0.0-2", isDir: true},
			},
			want: 3,
		},
		{
			name: "ALPM_DB_VERSION is a file, not a package",
			entries: []os.DirEntry{
				fakeDirEntry{name: "ALPM_DB_VERSION"},
				fakeDirEntry{name: "bash-5.2.26-1", isDir: true},
			},
			want: 1,
		},
		{
			name: "db.lck is a file, not a package",
			entries: []os.DirEntry{
				fakeDirEntry{name: "db.lck"},
				fakeDirEntry{name: "vim-9.1.0700-1", isDir: true},
			},
			want: 1,
		},
		{
			name:    "empty database reads zero",
			entries: nil,
			want:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countPackageDirs(tt.entries); got != tt.want {
				t.Errorf("countPackageDirs = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCountQueryOutput pins the `pacman -Qq` line counting: one
// package per line, the trailing newline tolerated, blank lines never
// counted.
func TestCountQueryOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   int
	}{
		{"empty output reads zero", "", 0},
		{"bare newline reads zero", "\n", 0},
		{"one package", "bash\n", 1},
		{"trailing newline tolerated", "bash\npacman\n", 2},
		{"no trailing newline still counts", "bash\npacman", 2},
		{"blank line never counts", "bash\n\npacman\n", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countQueryOutput(tt.output); got != tt.want {
				t.Errorf("countQueryOutput = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCountQueryOutputOnSample pins the count against the sample
// `pacman -Qq` output kept under testdata, the shape the live
// fallback parses: one package name per line with a trailing newline.
func TestCountQueryOutputOnSample(t *testing.T) {
	raw, err := os.ReadFile("testdata/pacman-Qq.txt")
	if err != nil {
		t.Fatalf("read sample output: %v", err)
	}
	if got := countQueryOutput(string(raw)); got != 6 {
		t.Errorf("countQueryOutput(sample) = %d, want 6", got)
	}
}
