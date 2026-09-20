// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package filterlist

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
)

type FilterableList[T any] struct {
	Viewport viewport.Model

	Items    []T
	Filtered []T
	Cursor   int
	Query    string
	Mode     ModeType

	// Function to render a single item (pass in computed colWidth)
	RenderItem func(item T, selected bool, colWidth int) string

	// Match function for filtering
	Match func(item T, query string) bool

	// SkipOffsetAdjustment disables automatic viewport scrolling in VisibleContent.
	// When false (default): VisibleContent automatically adjusts YOffset to keep the cursor visible.
	// When true: The caller manually controls YOffset, useful for sub-item navigation (e.g., tasks within a service)
	// where the cursor position doesn't reflect the actual line being viewed.
	SkipOffsetAdjustment bool

	// Header configures column headers with sort indicators.
	// Nil means no header is rendered by RenderHeader/RenderFramedView.
	Header *HeaderConfig

	// Footer configures the status bar and filter line.
	// Nil means no footer is rendered by RenderFooter/RenderFramedView.
	Footer *FooterConfig

	// Columns, when non-nil, enables the shared content-aware table layout:
	// ColWidths sizes columns to content, RenderRow builds gapped/scrolled rows,
	// and ScrollLeft/Right drive a unified horizontal scroll. Leave nil to keep
	// the legacy equal/Pct width behavior unchanged.
	Columns []Column[T]

	outerWidth   int
	outerHeight  int
	colWidth     int
	columnScroll int // unified horizontal scroll offset for flex columns (shared layout)
}

// SetOuterSize stores fallback dimensions from tea.WindowSizeMsg.
func (f *FilterableList[T]) SetOuterSize(width, height int) {
	f.outerWidth = width
	f.outerHeight = height
}

// effectiveWidth returns the best available width.
func (f *FilterableList[T]) effectiveWidth() int {
	if f.Viewport.Width > 0 {
		return f.Viewport.Width
	}
	if f.outerWidth > 0 {
		return f.outerWidth
	}
	return 80
}

// effectiveHeight returns the best available height.
func (f *FilterableList[T]) effectiveHeight() int {
	if f.Viewport.Height > 0 {
		return f.Viewport.Height
	}
	if f.outerHeight > 0 {
		return f.outerHeight
	}
	return 20
}

// ColWidths returns column widths for the current header configuration.
// Returns nil when Header is nil.
func (f *FilterableList[T]) ColWidths() []int {
	if f.Header == nil {
		return nil
	}
	w := f.effectiveWidth()
	if f.Header.ColWidthsFunc != nil {
		return f.Header.ColWidthsFunc(w)
	}
	if f.Columns != nil {
		return LayoutWidths(f.Columns, f.Filtered, w, f.sortColIndex())
	}
	return computeColWidths(f.Header.Columns, w)
}

// sortColIndex returns the active sort column index from the header's
// SortIndicator, or -1 when there is none.
func (f *FilterableList[T]) sortColIndex() int {
	if f.Header == nil || f.Header.SortIndicator == nil {
		return -1
	}
	col, _ := f.Header.SortIndicator()
	return col
}

// RenderRow builds the plain (unstyled) row text for an item using the shared
// content-aware layout. Callers apply their own styling/decoration.
func (f *FilterableList[T]) RenderRow(item T, selected bool) string {
	return RenderRow(f.Columns, f.ColWidths(), item, f.columnScroll, selected)
}

// ScrollLeft / ScrollRight / ResetColumnScroll drive the unified horizontal
// scroll for flex columns on the selected row.
func (f *FilterableList[T]) ScrollLeft() {
	f.columnScroll -= 5
	if f.columnScroll < 0 {
		f.columnScroll = 0
	}
}

func (f *FilterableList[T]) ScrollRight() {
	f.columnScroll += 5
	if end := f.maxColumnScroll(); f.columnScroll > end {
		f.columnScroll = end
	}
}

// maxColumnScroll is the offset at which the widest overflowing flex cell on the
// selected row ends exactly at its right edge.
//
// Without it the offset climbs for as long as the key is pressed, and once it
// passes the end of the text the cells render blank — which reads as the data
// disappearing rather than as the end of it, and takes as many presses back to
// undo as it took to get there.
func (f *FilterableList[T]) maxColumnScroll() int {
	widths := f.ColWidths()
	if f.Columns == nil || len(widths) != len(f.Columns) {
		return 0
	}
	if f.Cursor < 0 || f.Cursor >= len(f.Filtered) {
		return 0
	}
	item := f.Filtered[f.Cursor]
	widest := 0
	for i, c := range f.Columns {
		if !c.Flex {
			continue
		}
		// The cell width RenderRow lays this column out in: its share less the
		// gap, and less column 0's leading space.
		cw := widths[i] - ColGap
		if i == 0 {
			cw--
		}
		if over := displayWidth(c.Cell(item)) - cw; over > widest {
			widest = over
		}
	}
	return widest
}

func (f *FilterableList[T]) ResetColumnScroll() {
	f.columnScroll = 0
}

type ModeType int

const (
	ModeNormal ModeType = iota
	ModeSearching
)

// countLines returns the number of lines in a string (1 + number of newlines)
func countLines(s string) int {
	if s == "" {
		return 1
	}
	return strings.Count(s, "\n") + 1
}

// SelectLine moves the cursor to the item drawn on the given line of the
// visible window, line 0 being the first line VisibleContent returned, and
// reports whether that line belongs to an item. A line below the last item
// selects nothing. The line counts are the ones VisibleContent lays out, so an
// item spanning several lines is selected from any of them.
func (f *FilterableList[T]) SelectLine(line int) bool {
	if line < 0 {
		return false
	}
	target := f.Viewport.YOffset + line
	start := 0
	for i := range f.Filtered {
		start += f.itemLines(i)
		if target < start {
			f.Cursor = i
			return true
		}
	}
	return false
}

// itemLines is how many lines VisibleContent spends on item i.
func (f *FilterableList[T]) itemLines(i int) int {
	if f.RenderItem == nil {
		return countLines(fmt.Sprintf("%v", f.Filtered[i]))
	}
	return countLines(f.RenderItem(f.Filtered[i], i == f.Cursor, f.colWidth))
}

// VisibleContent returns a string containing exactly `lines` rows of
// rendered items starting at the current viewport Y offset. It will adjust
// the internal Y offset to ensure the cursor is visible within the given
// number of lines. This avoids mutating the viewport's Height during
// rendering and prevents jitter when parent views trim content.
func (f *FilterableList[T]) VisibleContent(lines int) string {
	if lines < 1 {
		lines = 1
	}

	// If no items or filtered empty, return appropriate placeholder
	if len(f.Filtered) == 0 {
		if f.Items == nil {
			return f.Viewport.View()
		}
		var msg string
		if f.Mode == ModeSearching && f.Query != "" {
			msg = "No items match: " + f.Query
		} else {
			msg = "No items found."
		}
		// Pad to requested lines
		parts := make([]string, lines)
		parts[0] = msg
		for i := 1; i < lines; i++ {
			parts[i] = ""
		}
		return strings.Join(parts, "\n")
	}

	// Pre-render all items to know their line counts
	renderedItems := make([]string, len(f.Filtered))
	itemLineCounts := make([]int, len(f.Filtered))
	totalLines := 0
	for i := range f.Filtered {
		if f.RenderItem != nil {
			renderedItems[i] = f.RenderItem(f.Filtered[i], i == f.Cursor, f.colWidth)
		} else {
			renderedItems[i] = fmt.Sprintf("%v", f.Filtered[i])
		}
		itemLineCounts[i] = countLines(renderedItems[i])
		totalLines += itemLineCounts[i]
	}

	// Find which item contains the cursor and ensure it's visible
	// Calculate line offset for cursor item
	cursorLineStart := 0
	for i := 0; i < f.Cursor && i < len(itemLineCounts); i++ {
		cursorLineStart += itemLineCounts[i]
	}
	cursorLineEnd := cursorLineStart + itemLineCounts[f.Cursor] - 1

	// Adjust viewport offset to keep cursor visible (unless manually managed)
	if !f.SkipOffsetAdjustment {
		// If cursor item is taller than viewport, prioritize showing the start
		if cursorLineStart < f.Viewport.YOffset {
			// Cursor item starts above viewport - scroll up to show it
			f.Viewport.YOffset = cursorLineStart
		} else if itemLineCounts[f.Cursor] > lines {
			// Cursor item is taller than viewport - show from its start to prevent oscillation
			if f.Viewport.YOffset > cursorLineStart {
				f.Viewport.YOffset = cursorLineStart
			}
		} else if cursorLineEnd >= f.Viewport.YOffset+lines {
			// Cursor item ends below viewport - scroll down to show it
			f.Viewport.YOffset = cursorLineEnd - lines + 1
			if f.Viewport.YOffset < 0 {
				f.Viewport.YOffset = 0
			}
		}
	}

	// Collect lines for display, starting from YOffset
	var result []string
	currentLine := 0
	for i := 0; i < len(renderedItems); i++ {
		itemLines := strings.Split(renderedItems[i], "\n")
		for _, line := range itemLines {
			if currentLine >= f.Viewport.YOffset && len(result) < lines {
				result = append(result, line)
			}
			currentLine++
			if len(result) >= lines {
				break
			}
		}
		if len(result) >= lines {
			break
		}
	}

	// Pad with empty lines if needed
	for len(result) < lines {
		result = append(result, "")
	}

	return strings.Join(result, "\n")
}
