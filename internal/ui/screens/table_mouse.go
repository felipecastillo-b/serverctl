package screens

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
)

// sgrPattern matches the SGR escape sequences lipgloss emits when it
// styles a rendered row.
var sgrPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// anchorTableCursor normalizes the table's private viewport window after
// a programmatic cursor move. bubbles/table v1.0.0's SetCursor recomputes
// the window start but keeps the previous YOffset, which can leave the
// selected row outside the visible window. Zero-step moves run the
// widget's public window adjustment without changing the cursor, so the
// cursor row is visible after sorts, filters, resizes and clicks.
func anchorTableCursor(tbl *table.Model) {
	tbl.MoveUp(0)
	tbl.MoveDown(0)
}

// plainTableRow renders row as the table widget renders an unselected
// row: every visible column padded to its width, cells joined and the
// trailing padding trimmed. The screens leave table.Styles.Cell zero and
// already clip their cell values to the column width, so no truncation
// is applied here. A screen that ever sets a cell style with padding
// must teach this helper that style too.
func plainTableRow(row table.Row, cols []table.Column) string {
	var b strings.Builder
	for i, col := range cols {
		if col.Width <= 0 {
			continue
		}
		value := ""
		if i < len(row) {
			value = row[i]
		}
		style := lipgloss.NewStyle().Width(col.Width).MaxWidth(col.Width).Inline(true)
		b.WriteString(style.Render(value))
	}
	return strings.TrimRight(b.String(), " ")
}

// plainTableLine turns a styled rendered row into its plain text: the
// selected row's SGR sequences are stripped and the width padding
// trimmed.
func plainTableLine(line string) string {
	return strings.TrimRight(sgrPattern.ReplaceAllString(line, ""), " ")
}

// tableRowAt maps a content-local click at x, y to the table row under
// it, reporting false when the click is outside the table or the row is
// not visible. bubbles/table v1.0.0 keeps its viewport window (start,
// end and YOffset) private, so the mapping anchors on the cursor row:
// it locates that row's rendered line, derives how many visible data
// lines precede it (k), and offsets from the cursor index by the clicked
// line. The click must therefore happen while the cursor row is visible,
// which anchorTableCursor guarantees.
func tableRowAt(tbl table.Model, cols []table.Column, x, y int) (int, bool) {
	if x < 0 {
		return 0, false
	}
	if tbl.Width() > 0 && x >= tbl.Width() {
		return 0, false
	}
	if y < 1 {
		return 0, false
	}
	if y-1 >= tbl.Height() {
		return 0, false
	}

	rows := tbl.Rows()
	cursor := tbl.Cursor()
	if cursor < 0 || cursor >= len(rows) {
		return 0, false
	}

	want := plainTableRow(rows[cursor], cols)
	if want == "" {
		return 0, false
	}

	// View is the header line followed by the visible data lines; the
	// cursor's offset among the latter is the scroll offset the widget
	// hides.
	k := -1
	for i, line := range strings.Split(tbl.View(), "\n")[1:] {
		if plainTableLine(line) == want {
			k = i
			break
		}
	}
	if k < 0 {
		return 0, false
	}

	row := cursor - k + (y - 1)
	if row < 0 || row >= len(rows) {
		return 0, false
	}
	return row, true
}
