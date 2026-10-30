package screens

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/felipecastillo-b/serverctl/internal/ui/format"
)

// The shared status-line states of ARCHITECTURE.md §5. Every screen
// that keeps its last good round on a failed collection renders the
// round error plus the age of the data still on screen; the loading
// state renders muted — a first collection round is progress, not an
// error.

// statusErrorMax caps the plain error text of a status line — the whole
// "n/a (...)" string before styling — at 60 runes. The shell truncates
// entire rendered lines at the terminal edge, so an uncapped error can
// push the trailing stale age out of view. At 80 columns 60 runes plus
// " stale 123s" (11 more) still fits, keeping the age visible behind
// even a very long error.
const statusErrorMax = 60

// errStatus renders the status line's error state: the round error in
// the danger role and, when a last good round exists (§5's
// keeps-last-good state), the muted age of the data still on screen.
// A screen whose first round failed has no last good data to age and
// renders the error alone. The error text is capped (statusErrorMax)
// so the stale age survives the shell's line truncation.
func errStatus(err error, lastGood time.Time, muted, danger lipgloss.Style) string {
	line := danger.Render(statusErrorText(err))
	if !lastGood.IsZero() {
		line += " " + muted.Render(staleSince(lastGood))
	}
	return line
}

// statusErrorText renders the plain "n/a (err)" fragment shared by the
// status lines and the dashboard section notes, so both surfaces cap
// long errors identically.
func statusErrorText(err error) string {
	return clipRunes(fmt.Sprintf("n/a (%v)", err), statusErrorMax)
}

// clipRunes truncates s to at most max runes, marking the cut with an
// ellipsis. Rune-based on purpose: error text can carry multi-byte
// messages, and a byte slice would split a sequence mid-character. The
// ellipsis counts toward max, so the result never exceeds the cap.
func clipRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
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
