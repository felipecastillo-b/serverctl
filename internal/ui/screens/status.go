package screens

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/ui/format"
)

// The shared status-line states of ARCHITECTURE.md §5. Every screen
// that keeps its last good round on a failed collection renders the
// round error plus the age of the data still on screen; the loading
// state renders muted — a first collection round is progress, not an
// error.

// errStatus renders the status line's error state: the round error in
// the danger role and, when a last good round exists (§5's
// keeps-last-good state), the muted age of the data still on screen.
// A screen whose first round failed has no last good data to age and
// renders the error alone.
func errStatus(err error, lastGood time.Time, muted, danger lipgloss.Style) string {
	line := danger.Render(fmt.Sprintf("n/a (%v)", err))
	if !lastGood.IsZero() {
		line += " " + muted.Render(staleSince(lastGood))
	}
	return line
}

// staleSince renders the age of a screen's last good round in whole
// seconds: "stale 45s". Screens stamp their own last-good time on
// each successful round — screen-local bookkeeping, no shell plumbing
// — and render this fragment while a failed round keeps the last good
// data standing. The next successful round clears both the error and
// the fragment.
func staleSince(good time.Time) string {
	return "stale " + format.Duration(time.Since(good))
}

// loadingStatus renders the status line's loading state in the muted
// role: collecting the first round is not an error, so it must not
// wear the error color.
func loadingStatus(muted lipgloss.Style) string {
	return muted.Render("collecting...")
}
