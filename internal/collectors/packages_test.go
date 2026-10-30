package collectors_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
)

// TestParseOSReleaseID pins the os-release(5) labeling contract: ID
// wins, ID_LIKE's first token is the family fallback, quotes come
// off, and a file carrying neither label reads as "" — the
// best-effort shape core.PackageCount documents. The first row is the
// observed /etc/os-release of a live Arch host (no ID_LIKE there).
func TestParseOSReleaseID(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "live arch file has ID only",
			content: "NAME=\"Arch Linux\"\n" +
				"PRETTY_NAME=\"Arch Linux\"\n" +
				"ID=arch\n" +
				"BUILD_ID=rolling\n",
			want: "arch",
		},
		{
			name:    "quoted ID is unquoted",
			content: "NAME=\"Some Distro\"\nID=\"debian\"\n",
			want:    "debian",
		},
		{
			name:    "ID beats ID_LIKE",
			content: "ID=ubuntu\nID_LIKE=debian\n",
			want:    "ubuntu",
		},
		{
			name:    "ID_LIKE fallback takes the first family token",
			content: "ID_LIKE=suse fedora\n",
			want:    "suse",
		},
		{
			name:    "comment lines and blanks are ignored",
			content: "# a comment\n\nID=arch\n",
			want:    "arch",
		},
		{
			name:    "neither field reads as empty",
			content: "NAME=\"No ID Here\"\nVERSION=\"1.0\"\n",
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collectors.ParseOSReleaseID(tt.content); got != tt.want {
				t.Errorf("ParseOSReleaseID = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCountOnFixture drives Count against the golden fixture root end
// to end: the pacman local database walk counts the three <name>-
// <version> directories, the ALPM_DB_VERSION file stays out of the
// count (the live-host gotcha: 589 entries, 588 packages), the distro
// comes from the fixture os-release, and the manager label is the
// adapter's. Hermetic: nothing here reads /var/lib/pacman or /etc of
// the test runner.
func TestCountOnFixture(t *testing.T) {
	count, err := collectors.New("testdata").Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	want := core.PackageCount{Distro: "arch", Manager: "pacman", Installed: 3}
	if count != want {
		t.Fatalf("count = %+v, want %+v", count, want)
	}
}

// TestCountWithoutLocalDBRefusesOnFixtureSystem pins the hermeticity
// guard: a root carrying no package-manager marker must ERROR on a
// fixture System, never fall through to a manager command — exec has
// no root prefix, so a fallback would silently query the test
// runner's own host. On a machine WITH pacman (the dev box) the test
// doubles as proof the guard actually guards: an unguarded fallback
// would return the host's real count instead of an error.
func TestCountWithoutLocalDBRefusesOnFixtureSystem(t *testing.T) {
	if _, err := collectors.New(t.TempDir()).Count(); err == nil {
		t.Fatal("a fixture System without a package-manager marker must error, not exec the host")
	}
}

// TestCountDistroBestEffort pins the degradation policy: a readable
// local database whose os-release is missing still counts — the count
// is the value the screen exists to show, the labels are best-effort
// (core.PackageCount documents the trade).
func TestCountDistroBestEffort(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "var", "lib", "pacman", "local", "filesystem-1.0-1")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}

	count, err := collectors.New(dir).Count()
	if err != nil {
		t.Fatalf("missing os-release must not fail Count: %v", err)
	}
	want := core.PackageCount{Distro: "", Manager: "pacman", Installed: 1}
	if count != want {
		t.Fatalf("count = %+v, want %+v", count, want)
	}
}

// dpkgStatusFixture is a dpkg status database shape: two installed
// stanzas, one removed-but-config-files stanza, and a multiline
// Description continuation that must not confuse the parser.
const dpkgStatusFixture = `Package: bash
Status: install ok installed
Version: 5.2.26-1

Package: vim
Status: deinstall ok config-files
Version: 9.1.0700-1
Description: Vi IMproved
 continuation lines start with a space and carry no field colon

Package: curl
Status: install ok installed
Version: 8.5.0-2
`

// TestCountOnDpkgRoot drives Count end to end against a dpkg root:
// detection picks dpkg from var/lib/dpkg/status, the stream parse
// counts the two installed stanzas, and the distro label follows the
// os-release ID (ID_LIKE's first token as the fallback, "" when the
// file is missing). Hermetic: the fixture root stands in for
// /var/lib/dpkg and /etc of the test runner.
func TestCountOnDpkgRoot(t *testing.T) {
	tests := []struct {
		name       string
		osRelease  string
		wantDistro string
	}{
		{
			name:       "ID labels the count",
			osRelease:  "NAME=\"Ubuntu\"\nID=ubuntu\nID_LIKE=debian\n",
			wantDistro: "ubuntu",
		},
		{
			name:       "ID_LIKE fallback labels the count",
			osRelease:  "NAME=\"Some Distro\"\nID_LIKE=ubuntu debian\n",
			wantDistro: "ubuntu",
		},
		{
			name:       "missing os-release leaves the label empty",
			osRelease:  "",
			wantDistro: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixtureFile(t, dir, "var/lib/dpkg/status", dpkgStatusFixture)
			if tt.osRelease != "" {
				writeFixtureFile(t, dir, "etc/os-release", tt.osRelease)
			}

			count, err := collectors.New(dir).Count()
			if err != nil {
				t.Fatalf("Count: %v", err)
			}
			want := core.PackageCount{Distro: tt.wantDistro, Manager: "dpkg", Installed: 2}
			if count != want {
				t.Fatalf("count = %+v, want %+v", count, want)
			}
		})
	}
}

// TestCountOnRPMRootRefusesOnFixtureSystem pins the rpm live guard:
// rpm has no text database, so a fixture root can only be detected,
// never counted. A shim `rpm` on PATH would leave a marker if Count
// ever exec'd it — the test proves the refusal happens before any
// process starts.
func TestCountOnRPMRootRefusesOnFixtureSystem(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "var/lib/rpm/rpmdb.sqlite", "")

	shimDir := t.TempDir()
	marker := filepath.Join(shimDir, "executed")
	script := "#!/bin/sh\n: > " + strconv.Quote(marker) + "\n"
	if err := os.WriteFile(filepath.Join(shimDir, "rpm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir)

	_, err := collectors.New(dir).Count()
	if err == nil {
		t.Fatal("a fixture System with an rpm root must error, not exec the host rpm")
	}
	if !strings.Contains(err.Error(), "requires the live rpm database") {
		t.Fatalf("Count error = %v, want the live-rpm-database requirement", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("Count executed rpm on a fixture System")
	}
}

// writeFixtureFile creates one root-prefixed fixture file, parents
// included.
func writeFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
