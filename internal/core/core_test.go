package core_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/actions"
	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
)

// The external test package pins the wiring between the ports and their
// production implementations at compile time: a drifted signature in
// either layer breaks the build here, before any screen notices at
// runtime.
var (
	_ core.UnitLister       = collectors.System{}
	_ core.JournalReader    = collectors.System{}
	_ core.StorageLister    = collectors.System{}
	_ core.NetLister        = collectors.System{}
	_ core.SocketLister     = collectors.System{}
	_ core.PackageCounter   = collectors.System{}
	_ core.ContainerLister  = collectors.System{}
	_ core.SessionLister    = collectors.System{}
	_ core.SSHAttemptLister = collectors.System{}
	_ core.UnitManager      = actions.Actor{}
)

// TestErrStaleCursorWrapping pins the contract the Logs screen relies on:
// readers wrap core.ErrStaleCursor so errors.Is can recognize a rotated
// cursor through any amount of context, while an unrelated sentinel with
// the same wording is not mistaken for it.
func TestErrStaleCursorWrapping(t *testing.T) {
	t.Run("wrapped sentinel is detected", func(t *testing.T) {
		err := fmt.Errorf("journal tail: %w", core.ErrStaleCursor)
		if !errors.Is(err, core.ErrStaleCursor) {
			t.Fatalf("errors.Is(%v, core.ErrStaleCursor) = false, want true", err)
		}
	})

	t.Run("unrelated sentinel is not detected", func(t *testing.T) {
		err := errors.New("journal cursor is stale: entry rotated out of the journal")
		if errors.Is(err, core.ErrStaleCursor) {
			t.Fatalf("errors.Is(%v, core.ErrStaleCursor) = true, want false", err)
		}
	})
}
