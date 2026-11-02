package screens

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/table"
)

// mouseTable builds the 20-row, one-column table the mapping tests click
// on: rows clipped to the column width, zero cell styles, viewport
// height = height-1.
func mouseTable(t *testing.T, height int) table.Model {
	t.Helper()
	cols := []table.Column{{Title: "NAME", Width: 8}}
	rows := make([]table.Row, 20)
	for i := range rows {
		rows[i] = table.Row{fmt.Sprintf("row-%02d", i)}
	}
	tbl := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithStyles(table.Styles{}),
	)
	tbl.SetWidth(8)
	tbl.SetHeight(height)
	return tbl
}

// renderedDataLine reports the data-line offset of want in the table's
// rendered view, or -1 when it is not visible.
func renderedDataLine(tbl table.Model, want string) int {
	for i, line := range strings.Split(tbl.View(), "\n")[1:] {
		if plainTableLine(line) == want {
			return i
		}
	}
	return -1
}

func TestTableRowAtMapsTopRows(t *testing.T) {
	cols := []table.Column{{Title: "NAME", Width: 8}}
	tbl := mouseTable(t, 10)
	anchorTableCursor(&tbl)

	if got := tbl.Height(); got != 9 {
		t.Fatalf("viewport height = %d, want 9", got)
	}
	for y := 1; y <= tbl.Height(); y++ {
		row, ok := tableRowAt(tbl, cols, 2, y)
		if !ok {
			t.Errorf("y=%d: tableRowAt rejected a visible data line", y)
			continue
		}
		if want := y - 1; row != want {
			t.Errorf("y=%d: row = %d, want %d", y, row, want)
		}
	}
}

func TestTableRowAtMapsThroughCursorLine(t *testing.T) {
	cols := []table.Column{{Title: "NAME", Width: 8}}
	tbl := mouseTable(t, 10)
	tbl.SetCursor(10)
	anchorTableCursor(&tbl)

	want := plainTableRow(tbl.Rows()[10], cols)
	k := renderedDataLine(tbl, want)
	if k < 0 {
		t.Fatalf("cursor row %q is not visible after anchoring:\n%s", want, tbl.View())
	}

	row, ok := tableRowAt(tbl, cols, 2, 1)
	if !ok {
		t.Fatalf("tableRowAt rejected y=1 with the cursor row at data line %d", k)
	}
	if want := 10 - k; row != want {
		t.Errorf("y=1: row = %d, want %d (cursor 10 at data line %d)", row, want, k)
	}

	row, ok = tableRowAt(tbl, cols, 2, k+1)
	if !ok {
		t.Fatalf("tableRowAt rejected the cursor row at y=%d", k+1)
	}
	if row != 10 {
		t.Errorf("y=%d: row = %d, want the cursor row 10", k+1, row)
	}

	prev, ok := tableRowAt(tbl, cols, 2, 1)
	if !ok {
		t.Fatal("tableRowAt rejected y=1")
	}
	for y := 2; y <= tbl.Height(); y++ {
		next, ok := tableRowAt(tbl, cols, 2, y)
		if !ok {
			t.Fatalf("y=%d: tableRowAt rejected a visible data line", y)
		}
		if next != prev+1 {
			t.Errorf("y=%d: row = %d, want %d (consecutive)", y, next, prev+1)
		}
		prev = next
	}
}

func TestTableRowAtRejectsOutsideTable(t *testing.T) {
	cols := []table.Column{{Title: "NAME", Width: 8}}
	tbl := mouseTable(t, 10)
	anchorTableCursor(&tbl)

	cases := []struct {
		name string
		x, y int
	}{
		{"header", 2, 0},
		{"past data rows", 2, tbl.Height() + 1},
		{"negative x", -1, 2},
		{"right edge", tbl.Width(), 2},
	}
	for _, tc := range cases {
		if row, ok := tableRowAt(tbl, cols, tc.x, tc.y); ok {
			t.Errorf("%s: tableRowAt(%d, %d) = %d, want rejection", tc.name, tc.x, tc.y, row)
		}
	}
}

func TestAnchorTableCursorKeepsSelectionVisible(t *testing.T) {
	cols := []table.Column{{Title: "NAME", Width: 8}}
	tbl := mouseTable(t, 10)
	want := plainTableRow(tbl.Rows()[19], cols)

	tbl.SetCursor(19)
	// bubbles/table v1.0.0 keeps the stale YOffset across SetCursor; on
	// this version row 19 lands past the rendered window. If a future
	// version happens to keep it visible anyway, only the after-anchor
	// guarantee below is contractual.
	if renderedDataLine(tbl, want) < 0 {
		t.Log("selected row hidden by the stale window offset before anchoring")
	} else {
		t.Log("selected row already visible before anchoring on this version")
	}

	anchorTableCursor(&tbl)
	if renderedDataLine(tbl, want) < 0 {
		t.Fatalf("selected row %q must be visible after anchoring:\n%s", want, tbl.View())
	}
}
