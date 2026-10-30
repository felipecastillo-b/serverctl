package collectors

import (
	"bufio"
	"context"
	"fmt"
	"io"
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

// dpkgStatusPath is dpkg's package database under the root prefix:
// one blank-line-separated stanza per package dpkg knows about,
// world-readable, with the install state in each stanza's Status
// field. There is nothing to exec: the file is the database.
const dpkgStatusPath = "var/lib/dpkg/status"

// rpm database markers under the root prefix. rpm has no text
// database: modern hosts carry a SQLite database, older ones a
// Packages file. Either marker means the host is rpm-based, but the
// count itself cannot be read from them without rpm's own tooling.
const (
	rpmSQLiteDBPath   = "var/lib/rpm/rpmdb.sqlite"
	rpmPackagesDBPath = "var/lib/rpm/Packages"
)

// Manager labels this adapter stamps on its counts. The port
// (core.PackageCounter) never names a manager; the adapter does, so
// each distro family reads back under its own name.
const (
	pacmanManager = "pacman"
	dpkgManager   = "dpkg"
	rpmManager    = "rpm"
)

// queryTimeout bounds the fixed-argv commands of ARCHITECTURE.md §8
// (`pacman -Qq`, `rpm -qa`): one query each that finishes in
// milliseconds live, generous enough never to cut a slow disk short.
const queryTimeout = 10 * time.Second

// Count implements core.PackageCounter for the package managers
// serverctl supports (ARCHITECTURE.md §8): it detects the host's
// manager under the root prefix and dispatches to that manager's
// adapter. Detection order is pacman, dpkg, rpm — the pacman local
// database directory, the dpkg status file, then either rpm database
// marker — so a root carrying more than one marker resolves the same
// way every round.
//
// pacman walks <root>/var/lib/pacman/local, with the fixed-argv
// `pacman -Qq` fallback on a live System; dpkg streams
// <root>/var/lib/dpkg/status with no exec and no sudo; rpm has no
// text database and runs `rpm -qa` with fixed argv and a timeout,
// behind the live guard. Distro is the ID field of
// <root>/etc/os-release (ID_LIKE's first token as the family
// fallback), read through the same root prefix /etc/passwd uses; it
// is best-effort — an unreadable os-release degrades Distro to ""
// rather than failing the count, which is the value the screen
// exists to show (core.PackageCount documents the trade).
//
// When detection or the manager's read fails, Count errors and the
// Packages screen shows its error state (§5).
func (s System) Count() (core.PackageCount, error) {
	manager, err := s.packageManager()
	if err != nil {
		return core.PackageCount{}, err
	}
	var installed int
	switch manager {
	case pacmanManager:
		installed, err = s.pacmanInstalledCount()
	case dpkgManager:
		installed, err = s.dpkgInstalledCount()
	case rpmManager:
		installed, err = s.rpmInstalledCount()
	}
	if err != nil {
		return core.PackageCount{}, err
	}
	return core.PackageCount{
		Distro:    s.distroID(),
		Manager:   manager,
		Installed: installed,
	}, nil
}

// packageManager probes the injected root for a supported package
// manager in the documented order: pacman, dpkg, rpm. The probes are
// os.Stat only — no exec, no privileges — so fixture roots exercise
// detection exactly the way a live host runs it. An unknown root
// errors instead of guessing a manager: a count under the wrong label
// is worse than an explicit unsupported-host message.
func (s System) packageManager() (string, error) {
	switch {
	case dirExists(s.rootPath(pacmanLocalDBPath)):
		return pacmanManager, nil
	case fileExists(s.rootPath(dpkgStatusPath)):
		return dpkgManager, nil
	case pathExists(s.rootPath(rpmSQLiteDBPath)) || pathExists(s.rootPath(rpmPackagesDBPath)):
		return rpmManager, nil
	}
	return "", fmt.Errorf("no supported package manager found (pacman, dpkg, rpm)")
}

// pacmanInstalledCount counts installed packages: the local database
// walk first, the `pacman -Qq` fallback only when that directory is
// absent or unreadable (ARCHITECTURE.md §8). A live database
// directory that reads empty is a legitimate zero — a container
// without packages — and never reaches the fallback.
func (s System) pacmanInstalledCount() (int, error) {
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
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, pacmanManager, "-Qq").Output()
	if err != nil {
		return 0, fmt.Errorf("run pacman -Qq: %w", err)
	}
	return countQueryOutput(string(out)), nil
}

// dpkgInstalledCount counts installed packages by streaming
// <root>/var/lib/dpkg/status — dpkg's world-readable database —
// through the root prefix. No exec and no sudo: the file already
// carries every package's install state, and reading it is a file
// read like the pacman database walk.
func (s System) dpkgInstalledCount() (int, error) {
	f, err := os.Open(s.rootPath(dpkgStatusPath))
	if err != nil {
		return 0, fmt.Errorf("dpkg status: %w", err)
	}
	// Read-only handle: a close error cannot lose data.
	defer func() { _ = f.Close() }()
	return countDpkgInstalled(f)
}

// countDpkgInstalled counts the installed packages of a dpkg status
// database: blank-line-separated stanzas, one per package dpkg knows
// about, each carrying a `Status:` field of three words — want flag,
// error flag, current state. Only the exact value
// "install ok installed" means installed; `deinstall ok config-files`,
// `install ok half-installed`, `install ok unpacked` and the rest of
// dpkg's state vocabulary do not. Malformed lines and CRLF endings
// are tolerated, and the final stanza counts without a trailing blank
// line. Pure over an io.Reader, so the parse is table-testable
// without a real /var/lib/dpkg.
func countDpkgInstalled(r io.Reader) (int, error) {
	scanner := bufio.NewScanner(r)
	// Status lines are short, but Description can carry long
	// translated fields; a 1 MiB ceiling keeps a pathological line
	// from tripping the 64 KiB default.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	installed := 0
	status := ""
	flush := func() {
		if status == "install ok installed" {
			installed++
		}
		status = ""
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if strings.TrimSpace(key) == "Status" {
			status = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("dpkg status: %w", err)
	}
	flush()
	return installed, nil
}

// rpmInstalledCount counts installed packages with `rpm -qa`, the
// only practical source: rpm keeps a binary database (rpmdb.sqlite on
// modern hosts, Packages on older ones), so there is no text file to
// walk. The command is the §8-sanctioned shape — fixed argv, a
// timeout, parsed output, never a shell (§7) — and it runs only on a
// live System: exec has no root prefix, so a fixture System refuses
// instead of querying the runner's own rpm database, the same live
// guard statfs, D-Bus and sdjournal use.
func (s System) rpmInstalledCount() (int, error) {
	if !s.IsLive() {
		return 0, fmt.Errorf("collectors: rpm package count requires the live rpm database: System is rooted at %q", s.root)
	}
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, rpmManager, "-qa").Output()
	if err != nil {
		return 0, fmt.Errorf("rpm -qa: %w", err)
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

// countQueryOutput counts the package names of `pacman -Qq` and
// `rpm -qa` output: one per line. Blank lines — the output's trailing
// newline — are tolerated, not counted. Pure, so both commands'
// parsing is table-testable without running either.
func countQueryOutput(output string) int {
	n := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// dirExists reports whether path exists and is a directory. A stat
// error (missing path, unreadable parent) reads as false: detection
// moves on to the next probe instead of failing the count.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// fileExists reports whether path exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// pathExists reports whether path exists, whatever its type.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
