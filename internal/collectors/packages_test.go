package collectors_test

import (
	"os"
	"path/filepath"
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
// guard: a root without the pacman local database must ERROR on a
// fixture System, never fall through to `pacman -Qq` — exec has no
// root prefix, so the fallback would silently query the test runner's
// own host. On a machine WITH pacman (the dev box) the test doubles as
// proof the guard actually guards: an unguarded fallback would return
// the host's real count instead of an error.
func TestCountWithoutLocalDBRefusesOnFixtureSystem(t *testing.T) {
	if _, err := collectors.New(t.TempDir()).Count(); err == nil {
		t.Fatal("a fixture System without the local database must error, not exec the host pacman")
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
