package actions

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// unitNamePattern is the complete whitelist of unit names serverctl will
// operate on (ARCHITECTURE.md §7): a letter or digit first, then letters,
// digits or the separators systemd itself uses in unit names (:_.@-).
// Shell metacharacters, whitespace, slashes and any other byte are
// rejected before a bus connection is ever dialed — a hostile name never
// leaves the process. Loosening this pattern is a deliberate, reviewed
// security decision, exactly like adding to allowedSignals.
var unitNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9:_.@-]*$`)

// maxUnitName is systemd's own unit-name length bound (UNIT_NAME_MAX).
// Names longer than 255 bytes are rejected up front, which also bounds
// what an audit line can carry.
const maxUnitName = 255

// jobMode "replace" is the plain, non-interactive queueing mode: enqueue
// our job and supersede any pending job for the same unit.
const jobMode = "replace"

// jobWait bounds the whole lifecycle call — dial, queue and the wait for
// systemd's JobRemoved result — so a wedged job can never hang the UI.
// 30 s covers slow-stopping services (databases flushing) while staying
// inside the patience of a confirmation modal.
const jobWait = 30 * time.Second

// validateUnitName enforces the whitelist above. It runs BEFORE any bus
// connection is dialed, and a rejected name is returned as an error
// without an audit write: the backend audits executed/failed attempts,
// while denials are recorded by the UI layer that shows the refusal
// (ARCHITECTURE.md §7 leaves "denied" attempts to the caller).
func validateUnitName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("actions: unit name is empty")
	case len(name) > maxUnitName:
		return fmt.Errorf("actions: unit name is %d bytes, over the %d-byte limit", len(name), maxUnitName)
	case !unitNamePattern.MatchString(name):
		return fmt.Errorf("actions: unit name %q rejected: must start with a letter or digit and contain only letters, digits and :_.@-", name)
	}
	return nil
}

// unitCall enqueues one lifecycle job over an established connection;
// the three verbs pass their own StartUnitContext/StopUnitContext/
// RestartUnitContext here. There is no generic method passthrough: a
// fourth verb means a fourth explicitly written Actor method.
type unitCall func(ctx context.Context, conn *dbus.Conn, name, mode string, result chan<- string) (int, error)

// Start starts the named systemd unit over the system D-Bus. The unit
// name is validated against the whitelist before any connection is
// dialed; every executed or failed attempt is written to the local audit
// log. The zero value of Actor is ready to use.
func (Actor) Start(name string) error {
	return runUnitJob("start", name, func(ctx context.Context, conn *dbus.Conn, name, mode string, result chan<- string) (int, error) {
		return conn.StartUnitContext(ctx, name, mode, result)
	})
}

// Stop stops the named systemd unit over the system D-Bus, with the same
// validation-first and audit-always contract as Start.
func (Actor) Stop(name string) error {
	return runUnitJob("stop", name, func(ctx context.Context, conn *dbus.Conn, name, mode string, result chan<- string) (int, error) {
		return conn.StopUnitContext(ctx, name, mode, result)
	})
}

// Restart restarts the named systemd unit over the system D-Bus, with
// the same validation-first and audit-always contract as Start.
func (Actor) Restart(name string) error {
	return runUnitJob("restart", name, func(ctx context.Context, conn *dbus.Conn, name, mode string, result chan<- string) (int, error) {
		return conn.RestartUnitContext(ctx, name, mode, result)
	})
}

// runUnitJob is the shared body of the three lifecycle verbs: validate
// the name, dial the system bus (per call — mutations are rare, a
// standing connection is not worth it), enqueue the job with mode
// "replace", then wait on the JobRemoved result channel. The channel is
// buffered so go-systemd's signal goroutine can always deliver its
// single value even if we time out first.
//
// Errors are wrapped with the operation and unit for context. Every
// executed or failed attempt is audited; an audit write failure is
// folded into the returned error (see auditOutcome).
func runUnitJob(verb, name string, call unitCall) error {
	if err := validateUnitName(name); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), jobWait)
	defer cancel()

	conn, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return auditOutcome("service."+verb, name,
			fmt.Errorf("actions: %s unit %q: connect to system bus: %w", verb, name, err))
	}
	defer conn.Close()

	result := make(chan string, 1)
	if _, err := call(ctx, conn, name, jobMode, result); err != nil {
		return auditOutcome("service."+verb, name,
			fmt.Errorf("actions: %s unit %q: %w", verb, name, err))
	}

	// systemd reports the job outcome on JobRemoved: "done" on success;
	// "failed", "canceled", "timeout", "dependency" or "skipped" are all
	// failures for our purposes.
	select {
	case res := <-result:
		if res != "done" {
			return auditOutcome("service."+verb, name,
				fmt.Errorf("actions: %s unit %q: job finished with result %q", verb, name, res))
		}
	case <-ctx.Done():
		return auditOutcome("service."+verb, name,
			fmt.Errorf("actions: %s unit %q: no job result within %s", verb, name, jobWait))
	}
	return auditOutcome("service."+verb, name, nil)
}

// Compile-time proof that Actor satisfies the Services mutate port
// declared in internal/core (ARCHITECTURE.md §2: ports live in core).
var _ core.UnitManager = Actor{}
