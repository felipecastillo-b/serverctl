package screens

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestDetailViewClipsToBounds(t *testing.T) {
	d := NewDetail("Process 42", []string{
		strings.Repeat("a", 50),
		strings.Repeat("b", 50),
		"third line",
	})

	got := d.View(10, 2)
	lines := strings.Split(got, "\n")
	if len(lines) > 2 {
		t.Fatalf("View rendered %d lines, want at most 2:\n%s", len(lines), got)
	}
	for _, line := range lines {
		if w := lipgloss.Width(line); w > 10 {
			t.Errorf("line %q is %d cells wide, want at most 10", line, w)
		}
	}
}

func TestDetailViewEmptyBounds(t *testing.T) {
	d := NewDetail("Process 42", []string{"pid 42"})

	if got := d.View(0, 0); got != "" {
		t.Fatalf("View(0,0) = %q, want empty", got)
	}
}

func TestDetailTitle(t *testing.T) {
	d := NewDetail("Process 42", nil)

	if got := d.Title(); got != "Process 42" {
		t.Fatalf("Title() = %q, want %q", got, "Process 42")
	}
}

// TestRenderDetailFieldsAlignsLabels pins the field rendering: one line
// per pair, labels padded to the widest one plus the colon, and every
// value starting at the same column.
func TestRenderDetailFieldsAlignsLabels(t *testing.T) {
	lines := renderDetailFields([][2]string{
		{"PID", "42"},
		{"Description", "a longer label"},
	})
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "PID:") {
		t.Errorf("line %q must carry its colon", lines[0])
	}
	if !strings.HasPrefix(lines[1], "Description:") {
		t.Errorf("line %q must carry its colon", lines[1])
	}
	first := strings.Index(lines[0], "42")
	second := strings.Index(lines[1], "a longer label")
	if first != second {
		t.Errorf("values start at columns %d and %d:\n%s\n%s", first, second, lines[0], lines[1])
	}
}
