// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package ui

import (
	"fmt"
	"strings"

	"github.com/Eldara-Tech/swarmcli/v2/ui/dialog"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// Styles (you can override these per-view if desired)
var (
	FrameTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("81")).
			Bold(true)

	FrameHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("15")).
				Bold(true)

	FrameBorderColor = lipgloss.Color("117")

	ListItemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("117"))

	ListSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("63")).
				Bold(true)
)

// TintedCursor is the cursor for a row its status tints (failing, converging):
// the tint becomes the bar's background, so moving onto the row does not hide
// what it says, as ListSelectedStyle's fixed colours would (issue #711). A row
// with no tint keeps its view's usual cursor.
//
// The text is white, or black on amber, where white washes out. Both are
// 256-colour indices because terminals that brighten bold text turn ANSI black
// into grey.
func TintedCursor(tint lipgloss.Style) lipgloss.Style {
	text := lipgloss.Color("231")
	if tint.GetForeground() == lipgloss.Color("3") {
		text = lipgloss.Color("16")
	}
	return lipgloss.NewStyle().
		Foreground(text).
		Background(tint.GetForeground()).
		Bold(true)
}

// styleFrameTitle applies the default frame-title style to a plain title. A
// title that already carries its own ANSI styling (e.g. from ScopedTitle) is
// returned unchanged so the caller's per-segment colours are preserved rather
// than clobbered by re-wrapping.
func styleFrameTitle(title string) string {
	if ansi.Strip(title) != title {
		return title
	}
	return FrameTitleStyle.Render(title)
}

// fitTitle truncates a title to the width the frame has for it. Neither layout
// survives a title that overhangs: the bordered one clamps its padding at zero
// and draws a top line wider than the rest of the box, and the fullscreen one
// centres the title in a fixed width, which wraps it onto rows the frame never
// budgeted for.
func fitTitle(title string, width int) string {
	if width < 1 || lipgloss.Width(title) <= width {
		return title
	}
	return ansi.Truncate(title, width, "…")
}

// RenderFramedBox draws a bordered frame with title, optional header, and content.
// If width <= 0, defaults to content width + padding.
// ANSI sequences in content are preserved.
func RenderFramedBox(title, header, content, footer string, width int) string {
	lines := strings.Split(content, "\n")
	footerLines := []string{}
	if footer != "" {
		footerLines = strings.Split(footer, "\n")
	}

	// Compute content width
	contentWidth := 0
	for _, l := range append(lines, footerLines...) {
		if w := lipgloss.Width(l); w > contentWidth {
			contentWidth = w
		}
	}
	if width <= 0 {
		width = contentWidth + FrameChromeColumns
	}

	titleStyled := styleFrameTitle(" " + title + " ")
	headerStyled := FrameHeaderStyle.Render(header)

	borderWidth := width - BorderColumns

	// Border style
	borderStyle := lipgloss.NewStyle().Foreground(FrameBorderColor)

	// Top border: if title is empty render a solid line between corners;
	// otherwise center the title in the top border.
	var topLine string
	if strings.TrimSpace(title) == "" {
		topLine = fmt.Sprintf("%s%s%s",
			borderStyle.Render("┌"),
			borderStyle.Render(strings.Repeat("─", borderWidth)),
			borderStyle.Render("┐"),
		)
	} else {
		titleStyled = fitTitle(titleStyled, borderWidth)

		// Top border with centered title
		leftPad := (borderWidth - lipgloss.Width(titleStyled)) / 2
		if leftPad < 0 {
			leftPad = 0
		}
		rightPad := borderWidth - leftPad - lipgloss.Width(titleStyled)
		if rightPad < 0 {
			rightPad = 0
		}

		topLine = fmt.Sprintf(
			"%s%s%s%s%s",
			borderStyle.Render("┌"),
			borderStyle.Render(strings.Repeat("─", leftPad)),
			titleStyled,
			borderStyle.Render(strings.Repeat("─", rightPad)),
			borderStyle.Render("┐"),
		)
	}

	// Box lines start with top border
	boxLines := []string{topLine}

	// Optional header. Bordered a line at a time, like the content below it:
	// framing a multi-line header as one line put the opening border on its
	// first row and the closing one on its last, and measured the whole string
	// for padding.
	if header != "" {
		for _, l := range strings.Split(headerStyled, "\n") {
			boxLines = append(boxLines, fmt.Sprintf("%s%s%s",
				borderStyle.Render("│"),
				padLine(l, borderWidth),
				borderStyle.Render("│")))
		}
	}

	// Content
	for _, l := range lines {
		boxLines = append(boxLines, fmt.Sprintf("%s%s%s",
			borderStyle.Render("│"),
			padLine(l, borderWidth),
			borderStyle.Render("│")))
	}

	// Optional footer (above bottom border)
	for _, fl := range footerLines {
		boxLines = append(boxLines, fmt.Sprintf("%s%s%s",
			borderStyle.Render("│"),
			padLine(fl, borderWidth),
			borderStyle.Render("│")))
	}

	// Bottom border
	bottomLine := fmt.Sprintf("%s%s%s",
		borderStyle.Render("└"),
		borderStyle.Render(strings.Repeat("─", borderWidth)),
		borderStyle.Render("┘"))
	boxLines = append(boxLines, bottomLine)

	return strings.Join(boxLines, "\n")
}

// RenderFramedBoxHeight renders a framed box constrained to `frameHeight` lines
// (including borders). If `frameHeight` <= 0 the function falls back to the
// unconstrained `RenderFramedBox` behavior. This helper pads the content so
// the resulting framed box occupies exactly `frameHeight` lines when possible.
func RenderFramedBoxHeight(title, header, content, footer string, width, frameHeight int) string {
	if frameHeight <= 0 {
		return RenderFramedBox(title, header, content, footer, width)
	}

	// The rows left for content once the frame, header and footer have taken
	// theirs. ContentRows is the one place that budget is worked out; counting
	// it again here is how the two came to disagree about a header of more
	// than one line.
	desiredContentLines := ContentRows(frameHeight, FramedChromeRows, header, footer)

	// Current content lines
	contentLines := strings.Split(content, "\n")
	// Trim trailing empty lines for stable calculation
	for len(contentLines) > 0 && contentLines[len(contentLines)-1] == "" {
		contentLines = contentLines[:len(contentLines)-1]
	}

	// Pad or trim content lines to desired length. The bound is re-read each
	// pass, so it has to be the length itself rather than a gap computed from
	// it — a gap shrinks as the appends land, and the loop stops half way.
	for len(contentLines) < desiredContentLines {
		contentLines = append(contentLines, "")
	}
	if len(contentLines) > desiredContentLines {
		contentLines = contentLines[:desiredContentLines]
	}

	paddedContent := strings.Join(contentLines, "\n")
	return RenderFramedBox(title, header, paddedContent, footer, width)
}

// TrimOrPadContentToLines returns content limited to exactly `lines` rows,
// padding with empty lines when shorter. Useful when framing viewport
// content to a fixed height.
func TrimOrPadContentToLines(content string, lines int) string {
	if lines < 1 {
		lines = 1
	}

	parts := strings.Split(content, "\n")

	if len(parts) > lines {
		parts = parts[:lines]
	}

	for len(parts) < lines {
		parts = append(parts, "")
	}

	return strings.Join(parts, "\n")
}

// padLine fits a line to width, preserving ANSI sequences
func padLine(line string, width int) string {
	l := lipgloss.Width(line)
	if l >= width {
		// Truncate but ensure we leave room for proper ending if needed
		// Use MaxWidth to handle ANSI sequences properly
		truncated := lipgloss.NewStyle().MaxWidth(width).Render(line)
		// Ensure the truncated line is exactly the visual width requested
		truncatedWidth := lipgloss.Width(truncated)
		if truncatedWidth < width {
			truncated += strings.Repeat(" ", width-truncatedWidth)
		}
		return truncated
	}
	return line + strings.Repeat(" ", width-l)
}

// RenderColumnHeader builds a single-line header from labels and column widths.
// `labels` and `colWidths` must have the same length. It applies the
// FrameHeaderStyle to the resulting line so callers can place it in the
// framed header slot.
func RenderColumnHeader(labels []string, colWidths []int) string {
	if len(labels) == 0 || len(colWidths) == 0 || len(labels) != len(colWidths) {
		return ""
	}

	parts := make([]string, len(labels))
	for i := range labels {
		parts[i] = fmt.Sprintf("%-*s", colWidths[i], labels[i])
	}
	line := strings.Join(parts, "")
	return FrameHeaderStyle.Render(line)
}

// RenderConfirmDialog renders a standard confirmation dialog with y/n options
func RenderConfirmDialog(message string) string {
	contentWidth := 60

	// Helper function to ensure exact width
	ensureWidth := func(s string, width int) string {
		currentWidth := lipgloss.Width(s)
		if currentWidth < width {
			return s + strings.Repeat(" ", width-currentWidth)
		}
		return s
	}

	var lines []string
	lines = append(lines, ensureWidth(dialog.TitleStyle.Render(" Confirmation "), contentWidth))
	lines = append(lines, ensureWidth(dialog.ItemStyle.Render(""), contentWidth))
	lines = append(lines, ensureWidth(dialog.ItemStyle.Render(message), contentWidth))
	lines = append(lines, ensureWidth(dialog.ItemStyle.Render(""), contentWidth))

	helpText := fmt.Sprintf(" %s Yes • %s No",
		dialog.KeyStyle.Render("<y>"),
		dialog.KeyStyle.Render("<n>"))
	lines = append(lines, ensureWidth(dialog.HelpStyle.Render(helpText), contentWidth))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return dialog.BorderStyle.Render(content)
}

// RenderFileBrowserDialog renders a file browser dialog with common styling
func RenderFileBrowserDialog(title, currentPath string, files []string, cursor int) string {
	var lines []string
	lines = append(lines, dialog.TitleStyle.Render(fmt.Sprintf(" %s - Directory: %s ", title, currentPath)))
	lines = append(lines, dialog.ItemStyle.Render(""))

	// Show files with cursor
	maxVisible := 10
	start := cursor - maxVisible/2
	if start < 0 {
		start = 0
	}
	end := start + maxVisible
	if end > len(files) {
		end = len(files)
		start = end - maxVisible
		if start < 0 {
			start = 0
		}
	}

	for i := start; i < end; i++ {
		item := files[i]
		displayName := ""

		// Handle special entries and parent directory
		if item == "[Save here]" {
			displayName = "✓ [Save here]"
		} else if item == ".." {
			displayName = "📁 .."
		} else if strings.HasSuffix(item, "/") {
			// Directory
			baseName := strings.TrimSuffix(item, "/")
			if idx := strings.LastIndex(baseName, "/"); idx >= 0 {
				baseName = baseName[idx+1:]
			}
			displayName = "📁 " + baseName
		} else {
			// File
			baseName := item
			if idx := strings.LastIndex(baseName, "/"); idx >= 0 {
				baseName = baseName[idx+1:]
			}
			displayName = baseName
		}

		if i == cursor {
			lines = append(lines, dialog.SelectedStyle.Render("→ "+displayName))
		} else {
			lines = append(lines, dialog.ItemStyle.Render("  "+displayName))
		}
	}

	lines = append(lines, dialog.ItemStyle.Render(""))
	helpText := fmt.Sprintf(" %s Select/Navigate • %s / %s Move • %s Cancel",
		dialog.KeyStyle.Render("<Enter>"),
		dialog.KeyStyle.Render("<↑/↓>"),
		dialog.KeyStyle.Render("<PgUp/PgDn>"),
		dialog.KeyStyle.Render("<Esc>"))
	lines = append(lines, dialog.HelpStyle.Render(helpText))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return dialog.BorderStyle.Render(content)
}

func OverlayCentered(base, overlay string, width, height int) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	dialogHeight := len(overlayLines)
	dialogWidth := 0
	for _, line := range overlayLines {
		if w := lipgloss.Width(line); w > dialogWidth {
			dialogWidth = w
		}
	}

	// Center vertically
	startRow := (len(baseLines) - dialogHeight) / 2
	if startRow < 0 {
		startRow = 0
	}

	// Center horizontally
	startCol := (width - dialogWidth) / 2
	if startCol < 0 {
		startCol = 0
	}

	// Overlay dialog lines
	for i, dialogLine := range overlayLines {
		row := startRow + i
		if row < 0 || row >= len(baseLines) {
			continue
		}

		baseLine := baseLines[row]
		baseWidth := lipgloss.Width(baseLine)

		// Build new line with dialog centered
		var newLine strings.Builder

		if baseWidth < startCol {
			// Base line is shorter than where dialog should start
			newLine.WriteString(baseLine)
			newLine.WriteString(strings.Repeat(" ", startCol-baseWidth))
			newLine.WriteString(dialogLine)
		} else {
			// Overlay dialog in the middle using width-aware truncation
			leftPart := TruncateANSI(baseLine, startCol)
			// A wide grapheme straddling startCol leaves leftPart one cell
			// short; pad so the dialog's left border is column-aligned on
			// every row (otherwise borders jitter on wide-char lines).
			if pad := startCol - lipgloss.Width(leftPart); pad > 0 {
				leftPart += strings.Repeat(" ", pad)
			}
			rightStart := startCol + dialogWidth
			rightPart := ""
			if rightStart < baseWidth {
				// Skip the overlay width and get the rest
				rightPart = TruncateANSIAfter(baseLine, rightStart)
			}

			newLine.WriteString(leftPart)
			newLine.WriteString(dialogLine)
			newLine.WriteString(rightPart)
		}

		baseLines[row] = newLine.String()
	}

	return strings.Join(baseLines, "\n")
}

// TruncateANSI truncates a string with ANSI codes to a specific visual (cell)
// width. It is grapheme/display-width aware (wide East-Asian chars and emoji
// count as 2 cells) and never splits an escape sequence.
func TruncateANSI(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "")
}

// TruncateANSIAfter skips skipWidth visual cells and returns the rest,
// re-emitting the active SGR state at the cut so colors survive. It is
// grapheme/display-width aware and never splits an escape sequence.
func TruncateANSIAfter(s string, skipWidth int) string {
	if skipWidth <= 0 {
		return s
	}
	return ansi.TruncateLeft(s, skipWidth, "")
}

// sgrReset ends the attribute this file asserts over a line.
const sgrReset = "\x1b[0m"

// BackgroundANSI draws s over background colour bg without discarding the
// colours it already carries. A log line arrives with its own escapes — our own
// node prefix ends in a reset — and a reset clears the background along with
// everything else, so a plain sequence+s would tint the text only as far as the
// first one. The background is therefore re-asserted after everything that
// turns one off.
func BackgroundANSI(s string, bg termenv.Color) string {
	if s == "" {
		return ""
	}
	sgrBg := termenv.CSI + bg.Sequence(true) + "m"
	var b strings.Builder
	b.Grow(len(s) + 2*len(sgrBg) + len(sgrReset))
	b.WriteString(sgrBg)
	for i := 0; i < len(s); {
		seq, ok := csiAt(s, i)
		if !ok {
			b.WriteByte(s[i])
			i++
			continue
		}
		b.WriteString(seq)
		i += len(seq)
		// seq is "\x1b[" + params + final; re-assert only where the line has
		// turned the background off.
		if seq[len(seq)-1] == 'm' && sgrClearsBackground(seq[2:len(seq)-1]) {
			b.WriteString(sgrBg)
		}
	}
	b.WriteString(sgrReset)
	return b.String()
}

// csiAt returns the complete CSI sequence starting at i, if one starts there.
// Parameter and intermediate bytes run 0x20–0x3f and the final byte ends the
// sequence; an unterminated escape at the end of the string is not one.
func csiAt(s string, i int) (string, bool) {
	if s[i] != 0x1b || i+1 >= len(s) || s[i+1] != '[' {
		return "", false
	}
	j := i + 2
	for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
		j++
	}
	if j >= len(s) {
		return "", false
	}
	return s[i : j+1], true
}

// sgrClearsBackground reports whether an SGR parameter list turns the
// background off: a reset — 0, an omitted parameter, or the empty "\x1b[m"
// shorthand — or 49, which restores the default background without touching
// anything else. The arguments of an extended colour are stepped over rather
// than read: the 0 in "38;5;0" is a colour index, and a line that set its own
// background must keep it across one.
func sgrClearsBackground(params string) bool {
	if params == "" {
		return true
	}
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		switch strings.TrimLeft(ps[i], "0") {
		case "": // "0", "00" and an omitted parameter all reset
			return true
		case "49":
			return true
		case "38", "48", "58":
			i += extendedColorArgs(ps[i+1:])
		}
	}
	return false
}

// extendedColorArgs is how many parameters follow a 38/48/58 selector: two for
// "5;n" and four for "2;r;g;b". A form that is neither consumes nothing, which
// leaves the walk exactly where it was.
func extendedColorArgs(rest []string) int {
	if len(rest) == 0 {
		return 0
	}
	switch strings.TrimLeft(rest[0], "0") {
	case "5":
		return min(2, len(rest))
	case "2":
		return min(4, len(rest))
	}
	return 0
}
