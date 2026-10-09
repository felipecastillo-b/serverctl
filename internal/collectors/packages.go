package collectors

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// pacmanLocalDBPath is the pacman local database directory under the
// root prefix: one subdirectory per installed package, named
// <name>-<version> after the package it describes. The directory also
// carries the plain ALPM_DB_VERSION file — not a package — so the
// count is subdirectories only. Verified against `pacman -Q` on a
// live Arch host: 588 subdirectories, 588 packages, plus that one
// file (589 ReadDir entries in total).
const pacmanLocalDBPath = "var/lib/pacman/local"

// pacmanManager is the manager label this Arch adapter stamps on its
// counts. The port (core.PackageCounter) never names a manager; the
// adapter does, so post-MVP distros bring their own label.
const pacmanManager = "pacman"

// pacmanQueryTimeout bounds the `pacman -Qq` fallback of
// ARCHITECTURE.md §8: one fixed-argv query that finishes in
// milliseconds live, generous enough never to cut a slow disk short.
const pacmanQueryTimeout = 10 * time.Second

// Count implements core.PackageCounter for an Arch Linux host
// (ARCHITECTURE.md §8, row "Installed package count").
//
// The primary source is the pacman local database directory under the
// root prefix (<root>/var/lib/pacman/local): every installed package
// is one <name>-<version> subdirectory, and counting subdirectories
// is the whole job — a directory read, so fixture roots keep the walk
// hermetic the way Disks' sysfs tree does. Distro is the ID field of
// <root>/etc/os-release (ID_LIKE's first token as the family
// fallback), read through the same root prefix /etc/passwd uses; it
// is best-effort — an unreadable os-release degrades Distro to ""
// rather than failing the count, which is the value the screen
// exists to show (core.PackageCount documents the trade).
//
// The §8 fallback — `pacman -Qq` through exec with fixed argv and a
// timeout (§7: never a shell, never user input) — runs only when the
// database directory is absent or unreadable AND the System is live:
// exec has no root prefix, so a fixture System must refuse instead of
// querying the test runner's own host, the same live guard statfs,
// D-Bus and sdjournal use. When both sources fail, Count errors and
// the Packages screen shows its error state (§5).
func (s System) Count() (core.PackageCount, error) {
	installed, err := s.installedCount()
	if err != nil {
		return core.PackageCount{}, err
	}
	return core.PackageCount{
		Distro:    s.distroID(),
		Manager:   pacmanManager,
		Installed: installed,
	}, nil
}

// installedCount counts installed packages: the local database walk
// first, the `pacman -Qq` fallback only when that directory is absent
// or unreadable (ARCHITECTURE.md §8). A live database directory that
// reads empty is a legitimate zero — a container without packages —
// and never reaches the fallback.
func (s System) installedCount() (int, error) {
	entries, err := os.ReadDir(s.rootPath(pacmanLocalDBPath))
	if err == nil {
		return countPackageDirs(entries), nil
	}
	if !s.IsLive() {
		// exec has no root prefix: a fixture System must never query
		// the runner's host pacman, whatever made the directory
		// unreadable.
		return 0, fmt.Errorf("read pacman local database: %w", err)
	}
	return s.pacmanQueryCount()
}

// pacmanQueryCount is the live-only §8 fallback: `pacman -Qq` through
// exec with fixed argv and a timeout. §7 compliance is structural —
// the argv is a constant pair, nothing user-typed ever reaches the
// process, and there is no shell to interpolate through. `pacman -Qq`
// prints one package name per line, so the count is the output's
// non-blank line count.
func (s System) pacmanQueryCount() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pacmanQueryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, pacmanManager, "-Qq").Output()
	if err != nil {
		return 0, fmt.Errorf("run pacman -Qq: %w", err)
	}
	return countQueryOutput(string(out)), nil
}

// countPackageDirs counts the installed packages of a pacman local
// database listing: one subdirectory per package, everything else
// left out. The exclusion is load-bearing: a live database directory
// carries the plain ALPM_DB_VERSION file — and, mid-transaction, the
// db.lck lock file — regular files that are never packages, and
// counting entries instead of subdirectories would read one package
// too many (observed on a live host: 589 entries, 588 packages).
func countPackageDirs(entries []os.DirEntry) int {
	n := 0
	for _, entry := range entries {
		if entry.IsDir() {
			n++
		}
	}
	return n
}

// countQueryOutput counts the package names of `pacman -Qq` output:
// one per line. Blank lines — the output's trailing newline — are
// tolerated, not counted. Pure, so the fallback's parsing is
// table-testable without running pacman.
func countQueryOutput(output string) int {
	n := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// ParseOSReleaseID reads the distribution ID out of an os-release(5)
// file's content: the ID field when it carries one, else the first
// ID_LIKE token — the distro family os-release(5) offers as the
// fallback — else "". Quoted values ("arch") are unquoted; every
// other field and comment lines are ignored. Pure, so the labeling is
// table-testable against any distro's os-release shape.
func ParseOSReleaseID(content string) string {
	id, idLike := "", ""
	for _, line := range strings.Split(content, "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.TrimSpace(key) {
		case "ID":
			id = value
		case "ID_LIKE":
			idLike = value
		}
	}
	if id != "" {
		return id
	}
	if first, _, _ := strings.Cut(idLike, " "); first != "" {
		return first
	}
	return ""
}

// distroID reads the distro ID from <root>/etc/os-release through the
// root prefix, the fixture-covered /etc path userMap reads
// /etc/passwd through. Best-effort: a missing or unreadable os-release
// reads as "" — the count stays, only the label drops
// (core.PackageCount documents the trade).
func (s System) distroID() string {
	raw, err := os.ReadFile(s.rootPath("etc", "os-release"))
	if err != nil {
		return ""
	}
	return ParseOSReleaseID(string(raw))
}

// Compile-time proof that System satisfies the Packages read port.
var _ core.PackageCounter = System{}
