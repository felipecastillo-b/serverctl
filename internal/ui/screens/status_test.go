package screens

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// TestErrStatusCapsLongErrors pins the status-line error cap: the plain
// "n/a (...)" fragment never exceeds statusErrorMax runes, truncation
// stays on rune boundaries, and the stale age survives behind a capped
// error. The cap exists because the shell truncates whole lines at the
// terminal edge.
func TestErrStatusCapsLongErrors(t *testing.T) {
	muted := lipgloss.NewStyle()
	danger := lipgloss.NewStyle()

	// "n/a (" plus ")" are six runes, so this error puts the plain
	// fragment exactly on the cap.
	exactErr := errors.New(strings.Repeat("b", statusErrorMax-utf8.RuneCountInString("n/a ()")))
	lastGood := time.Now().Add(-90 * time.Second)

	tests := []struct {
		name     string
		err      error
		lastGood time.Time
		exact    string
		check    func(t *testing.T, got string)
	}{
		{
			name:  "short error renders unchanged",
			err:   errors.New("dbus went away"),
			exact: "n/a (dbus went away)",
		},
		{
			name:  "error at the cap renders unchanged",
			err:   exactErr,
			exact: "n/a (" + strings.Repeat("b", 54) + ")",
		},
		{
			name: "long error truncates at the cap",
			err:  errors.New(strings.Repeat("x", 200)),
			check: func(t *testing.T, got string) {
				if n := utf8.RuneCountInString(got); n != statusErrorMax {
					t.Errorf("capped status = %q has %d runes, want %d", got, n, statusErrorMax)
				}
				if !strings.HasSuffix(got, "…") {
					t.Errorf("capped status = %q must end with an ellipsis", got)
				}
			},
		},
		{
			name: "multi-byte error truncates on rune boundaries",
			err:  errors.New(strings.Repeat("é", 200)),
			check: func(t *testing.T, got string) {
				if !utf8.ValidString(got) {
					t.Errorf("capped status = %q is not valid UTF-8", got)
				}
				if n := utf8.RuneCountInString(got); n != statusErrorMax {
					t.Errorf("capped status = %q has %d runes, want %d", got, n, statusErrorMax)
				}
				if !strings.HasSuffix(got, "…") {
					t.Errorf("capped status = %q must end with an ellipsis", got)
				}
			},
		},
		{
			name:     "stale age survives behind a capped error",
			err:      errors.New(strings.Repeat("x", 200)),
			lastGood: lastGood,
			check: func(t *testing.T, got string) {
				errPart, suffix, ok := strings.Cut(got, " stale ")
				if !ok || suffix == "" {
					t.Fatalf("stale status = %q lost the age suffix", got)
				}
				if n := utf8.RuneCountInString(errPart); n != statusErrorMax {
					t.Errorf("error part %q has %d runes, want the %d-rune cap", errPart, n, statusErrorMax)
				}
			},
		},
		{
			name: "zero last good renders no suffix",
			err:  errors.New("dbus went away"),
			check: func(t *testing.T, got string) {
				if strings.Contains(got, "stale") {
					t.Errorf("first-round failure = %q must not carry a stale age", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errStatus(tt.err, tt.lastGood, muted, danger)
			if tt.exact != "" && got != tt.exact {
				t.Errorf("errStatus() = %q, want %q", got, tt.exact)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestDashboardSectionNoteCapsLongErrors pins that the dashboard routes
// its own section note through the shared cap: section notes form
// independently of errStatus, so they must not reintroduce uncapped
// errors.
func TestDashboardSectionNoteCapsLongErrors(t *testing.T) {
	muted := lipgloss.NewStyle()
	danger := lipgloss.NewStyle()
	d := &dashboard{
		errs:     map[string]error{"cpu": errors.New(strings.Repeat("x", 200))},
		lastGood: map[string]time.Time{"cpu": time.Now().Add(-90 * time.Second)},
	}

	note, showValues := d.sectionNote("cpu", muted, danger)
	if !showValues {
		t.Fatal("a section with last good values must render its values")
	}
	errPart, suffix, ok := strings.Cut(note, " stale ")
	if !ok || suffix == "" {
		t.Fatalf("section note = %q lost the stale age", note)
	}
	if n := utf8.RuneCountInString(errPart); n != statusErrorMax {
		t.Errorf("section error part %q has %d runes, want the %d-rune cap", errPart, n, statusErrorMax)
	}
}
