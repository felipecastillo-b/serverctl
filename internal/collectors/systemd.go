package collectors

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/coreos/go-systemd/v22/dbus"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// Units lists the systemd .service units known to the system, read
// through the system D-Bus (ARCHITECTURE.md §3, Services module). Two
// calls ride one connection: ListUnits returns the units the manager
// has LOADED with their live state, ListUnitFiles returns every unit
// file on disk with its enablement state. The merge matters because a
// unit that is disabled and stopped — docker.service on a fresh box —
// is never loaded, so only the file half of the merge surfaces it. The
// connection is dialed per call: the 5 s refresh cadence makes an
// always-on connection not worth the state, and a per-call dial keeps
// System a value with no lifecycle to manage.
//
// D-Bus has no root-prefix fixtures — the collectors.New("testdata")
// trick cannot work for a bus — so a System that is not live refuses
// instead of guessing where units would come from.
func (s System) Units() ([]core.Service, error) {
	if !s.IsLive() {
		return nil, fmt.Errorf("collectors: systemd units require the live system bus: System is rooted at %q", s.root)
	}

	ctx := context.Background()
	conn, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("collectors: connect to system bus: %w", err)
	}
	defer conn.Close()

	units, err := conn.ListUnitsContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("collectors: list units: %w", err)
	}
	files, err := conn.ListUnitFilesContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("collectors: list unit files: %w", err)
	}
	return mergeUnits(units, files), nil
}

// mergeUnits folds systemd's two listings into one .service listing:
// the loaded units with their live state, plus the on-disk unit files
// the manager has not loaded. It is pure — no bus, no filesystem — so
// tests exercise it with hand-built D-Bus values and no connection at
// all (ARCHITECTURE.md §10 parser purity).
//
// Loaded units keep their listing order and gain the enablement state
// of their unit file. On-disk-only units follow in file-listing order
// as Name + FileState plus the "unloaded"/inactive/dead triple
// (core.Service documents the label). The merge keeps only .service
// names — the Services screen lists services (ARCHITECTURE.md §3), so
// timer, socket and target files stay out of the listing.
func mergeUnits(units []dbus.UnitStatus, files []dbus.UnitFile) []core.Service {
	// Enablement state per unit name. go-systemd names the state field
	// Type: systemd's D-Bus ListUnitFiles returns (path, state) pairs
	// and the struct field names predate that reading. The same name
	// can be listed more than once — an /etc copy shadowing the distro
	// file in /usr/lib, or a wants-directory symlink — so the map
	// applies the dedup rule of moreInformativeState.
	states := make(map[string]string, len(files))
	for _, f := range files {
		name := filepath.Base(f.Path)
		if !strings.HasSuffix(name, ".service") {
			continue
		}
		if prev, ok := states[name]; ok {
			states[name] = moreInformativeState(prev, f.Type)
		} else {
			states[name] = f.Type
		}
	}

	// Loaded units first, in listing order, keeping their live state
	// triple and gaining the file's enablement state.
	out := make([]core.Service, 0, len(units)+len(states))
	seen := make(map[string]bool, len(units))
	for _, svc := range mapUnits(units) {
		if !strings.HasSuffix(svc.Name, ".service") || seen[svc.Name] {
			continue
		}
		seen[svc.Name] = true
		svc.FileState = states[svc.Name]
		out = append(out, svc)
	}

	// On-disk-only units last, in file-listing order so the output is
	// deterministic; FileState reads the deduped map, not the raw
	// entry, so a "bad" first listing does not shadow a readable
	// duplicate.
	for _, f := range files {
		name := filepath.Base(f.Path)
		if !strings.HasSuffix(name, ".service") || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, core.Service{
			Name:      name,
			Load:      "unloaded",
			Active:    "inactive",
			Sub:       "dead",
			FileState: states[name],
		})
	}
	return out
}

// moreInformativeState resolves two enablement states reported for the
// same unit name. First occurrence wins, except that an empty state or
// systemd's "bad" — its label for a unit file it could not make sense
// of — loses to any later state that says something. Ties therefore
// stay stable on the first listing while the most informative state
// survives.
func moreInformativeState(kept, next string) string {
	if kept == "" || kept == "bad" {
		return next
	}
	return kept
}

// mapUnits converts raw D-Bus unit statuses into core.Service values. It
// is pure — no bus, no clock, no filesystem — so tests exercise it with
// hand-built dbus.UnitStatus values and no connection at all.
func mapUnits(units []dbus.UnitStatus) []core.Service {
	out := make([]core.Service, 0, len(units))
	for _, u := range units {
		out = append(out, core.Service{
			Name:        u.Name,
			Description: u.Description,
			Load:        u.LoadState,
			Active:      u.ActiveState,
			Sub:         u.SubState,
		})
	}
	return out
}

// Compile-time proof that System satisfies the Services read port.
var _ core.UnitLister = System{}
