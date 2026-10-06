// Package collectors reads Linux kernel interfaces (/proc, /sys) and turns
// them into core domain models. Every collector is read-only, spawns no
// processes, and takes its data through a root prefix so tests can point it
// at golden fixtures instead of a live system.
package collectors

import "path/filepath"

// System is a read-only view of a Linux system rooted at root.
// Production code uses New("/"); tests use New("testdata").
type System struct {
	root string
}

// New returns a System reading kernel interfaces under root.
func New(root string) System {
	return System{root: root}
}

// rootPath joins elements onto the system root.
func (s System) rootPath(elem ...string) string {
	parts := append([]string{s.root}, elem...)
	return filepath.Join(parts...)
}

// proc joins elements under the proc filesystem (root/proc).
func (s System) proc(elem ...string) string {
	return s.rootPath(append([]string{"proc"}, elem...)...)
}

// sys joins elements under the sysfs filesystem (root/sys).
func (s System) sys(elem ...string) string {
	return s.rootPath(append([]string{"sys"}, elem...)...)
}

// IsLive reports whether this System points at the real kernel interfaces.
func (s System) IsLive() bool {
	return s.root == "/"
}
