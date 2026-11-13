// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package logsview

import (
	"fmt"
	"strings"

	"github.com/Eldara-Tech/swarmcli/v2/ui"
	"github.com/Eldara-Tech/swarmcli/v2/ui/dialog"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func (m *Model) FrameTitle() string {
	scope := m.ServiceEntry.ServiceName
	if m.ServiceEntry.StackName != "" {
		scope = m.ServiceEntry.StackName + "/" + scope
	}
	return ui.ScopedTitleFiltered("Logs", scope, m.getVisibleCount(), m.getFilterQuery())
}

// FrameHeader renders the view's options as a status row, so what each toggle
// is doing reads at a glance rather than out of a sentence in the title. It is
// also the only place they appear in fullscreen, where the helpbar that names
// the keys is not drawn.
func (m *Model) FrameHeader() string {
	node := "all"
	if filter := m.getNodeFilter(); filter != "" {
		node = filter
	}
	// "Hidden" is the engaged state: hiding stopped tasks is what the toggle
	// does, and it is the default.
	stopped := ui.Toggle{Label: "Stopped", Value: "Shown", Tone: ui.ToggleOff}
	if m.getHideStopped() {
		stopped = ui.Toggle{Label: "Stopped", Value: "Hidden", Tone: ui.ToggleOn}
	}

	items := []ui.Toggle{
		onOffToggle("Autoscroll", m.getFollow()),
		onOffToggle("Wrap", m.getWrap()),
		{Label: "Node", Value: node, Tone: ui.ToggleInfo},
		stopped,
	}
	if search, ok := m.searchToggle(); ok {
		items = append(items, search)
	}
	return ui.ToggleRow(items, m.viewport.Width)
}

func onOffToggle(label string, on bool) ui.Toggle {
	if on {
		return ui.Toggle{Label: label, Value: "On", Tone: ui.ToggleOn}
	}
	return ui.Toggle{Label: label, Value: "Off", Tone: ui.ToggleOff}
}

// maxSearchTermRunes caps the search term shown in the row. An uncapped term
// pushes the item past the row's width, and ToggleRow drops from the right —
// losing the very feedback the item exists to give.
const maxSearchTermRunes = 16

// searchToggle reports the "ctrl+f" search as a row item: the term while it is
// being typed, then the match counter. It is absent when no search is running.
// The app-level "/" filter is not here — it rides in the title, as in every
// other view.
func (m *Model) searchToggle() (ui.Toggle, bool) {
	term := m.searchTerm
	if r := []rune(term); len(r) > maxSearchTermRunes {
		term = string(r[:maxSearchTermRunes-1]) + "…"
	}

	switch {
	case m.mode == "search":
		// The trailing caret marks the field as live: the term is empty for as
		// long as it takes to type the first character.
		return ui.Toggle{Label: "Search", Value: term + "_", Tone: ui.ToggleInfo}, true
	case m.searchTerm == "":
		return ui.Toggle{}, false
	case len(m.searchMatches) == 0:
		return ui.Toggle{Label: "Search", Value: fmt.Sprintf("%s(0)", term), Tone: ui.ToggleOff}, true
	default:
		return ui.Toggle{
			Label: "Search",
			Value: fmt.Sprintf("%s(%d/%d)", term, m.searchIndex+1, len(m.searchMatches)),
			Tone:  ui.ToggleInfo,
		}, true
	}
}

func (m *Model) FrameFooter() string { return "" }

// markStyle dims a separator: it is punctuation between reads, and has to stay
// quieter than the log lines it separates.
var markStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

// freshBackground is the band a line that has just arrived is drawn over —
// the colour a picked-out row already carries elsewhere in the app. Only the
// background is set: the line keeps the colours it arrived with, which is the
// point of highlighting it at all.
var freshBackground = termenv.ANSI256Color(24)

// markTimeFormat is what a separator records — the wall-clock time it was made,
// which is the one thing about it worth reading.
const markTimeFormat = "15:04:05"

// renderMark draws a separator as a rule carrying the time it was made, filling
// width exactly. It is called at build time rather than stored, so a resize
// re-cuts it instead of leaving a rule from the old width behind.
func renderMark(stamp string, width int) string {
	if width <= 0 {
		return ""
	}
	label := " " + stamp + " "
	if len(label)+2 > width {
		return markStyle.Render(strings.Repeat("─", width))
	}
	left := (width - len(label)) / 2
	right := width - len(label) - left
	return markStyle.Render(strings.Repeat("─", left) + label + strings.Repeat("─", right))
}

func (m *Model) FrameContent() string {
	width := m.viewport.Width
	if width <= 0 {
		width = 80
	}

	// The viewport is already sized to the rows the frame will draw.
	viewportContent := m.viewport.View()
	if waiting := m.waitingContent(width, m.viewport.Height); waiting != "" {
		viewportContent = waiting
	}

	if m.getNodeSelectVisible() && m.viewport.Height >= 5 {
		availableHeight := m.viewport.Height
		popup := m.renderNodeSelectDialog(availableHeight)
		viewportContent = ui.OverlayCentered(viewportContent, popup, width, 0)
	}

	return viewportContent
}

// View renders the view on its own; the app composes it from the Frame* parts
// instead. The box is drawn around the content rather than to a height, since
// the viewport is already sized to the rows it should fill.
func (m *Model) View() string {
	if !m.Visible {
		return ""
	}
	return ui.RenderFramedBox(m.FrameTitle(), m.FrameHeader(), m.FrameContent(), m.FrameFooter(),
		m.viewport.Width+ui.FrameChromeColumns)
}

// renderNodeSelectDialog renders the node selection popup
func (m *Model) renderNodeSelectDialog(availableHeight int) string {
	// Lock to safely access dialog state
	m.mu.Lock()
	nodes := make([]string, len(m.nodeSelectNodes))
	copy(nodes, m.nodeSelectNodes)
	cursor := m.nodeSelectCursor
	m.mu.Unlock()

	// Safety check: if no nodes, return empty string
	if len(nodes) == 0 {
		return ""
	}

	// Ensure cursor is within bounds
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(nodes) {
		cursor = len(nodes) - 1
	}

	// Calculate required width based on help text
	helpTextPlain := fmt.Sprintf(" %s Navigate • %s Select • %s Cancel",
		"<↑/↓/PgUp/PgDn>",
		"<Enter>",
		"<Esc>")
	helpTextMinWidth := lipgloss.Width(helpTextPlain) + 2 // add padding

	// Set minimum width to accommodate help text
	contentWidth := helpTextMinWidth
	if contentWidth < 40 {
		contentWidth = 40 // minimum for usability
	}

	// Check if any node names are longer
	for _, node := range nodes {
		nodeWidth := lipgloss.Width(node) + 4 // " > " prefix + space
		if nodeWidth > contentWidth {
			contentWidth = nodeWidth
		}
	}

	titleWidth := lipgloss.Width(" Select Node to Filter ")
	if titleWidth > contentWidth {
		contentWidth = titleWidth
	}

	titleStyle := dialog.TitleStyle.Width(contentWidth)
	itemStyle := dialog.ItemStyle.Width(contentWidth)
	selectedStyle := dialog.SelectedStyle.Width(contentWidth)
	borderStyle := dialog.BorderStyle.Width(contentWidth + dialog.BoxInsetColumns)
	helpStyle := dialog.HelpStyle.Width(contentWidth)

	// Build the content
	var lines []string
	lines = append(lines, titleStyle.Render(" Select Node to Filter "))

	// Calculate visible window for scrollable list
	maxVisibleItems := availableHeight - 4
	if maxVisibleItems < 1 {
		maxVisibleItems = 1
	}

	totalItems := len(nodes)

	// If all items fit, show them all
	if totalItems <= maxVisibleItems {
		for i, node := range nodes {
			if i == cursor {
				lines = append(lines, selectedStyle.Render(" > "+node))
			} else {
				lines = append(lines, itemStyle.Render("   "+node))
			}
		}
	} else {
		// Scrolling needed - calculate visible window
		effectiveVisibleItems := maxVisibleItems

		halfWindow := effectiveVisibleItems / 2
		startIdx := cursor - halfWindow
		if startIdx < 0 {
			startIdx = 0
		}
		endIdx := startIdx + effectiveVisibleItems
		if endIdx > totalItems {
			endIdx = totalItems
			startIdx = endIdx - effectiveVisibleItems
			if startIdx < 0 {
				startIdx = 0
			}
		}

		linesAdded := 0

		if startIdx > 0 {
			indicatorLine := fmt.Sprintf("   ↑ %d more above", startIdx)
			lines = append(lines, itemStyle.Render(indicatorLine))
			linesAdded++
		}

		remainingSlots := maxVisibleItems - linesAdded
		if endIdx > totalItems {
			endIdx = totalItems
		}
		if endIdx < totalItems {
			remainingSlots--
		}

		actualEndIdx := startIdx + remainingSlots
		if actualEndIdx > totalItems {
			actualEndIdx = totalItems
		}

		for i := startIdx; i < actualEndIdx; i++ {
			node := nodes[i]
			if i == cursor {
				lines = append(lines, selectedStyle.Render(" > "+node))
			} else {
				lines = append(lines, itemStyle.Render("   "+node))
			}
			linesAdded++
		}

		if actualEndIdx < totalItems {
			indicatorLine := fmt.Sprintf("   ↓ %d more below", totalItems-actualEndIdx)
			lines = append(lines, itemStyle.Render(indicatorLine))
		}
	}

	// Build help text with styled keys
	helpText := fmt.Sprintf(" %s Navigate • %s Select • %s Cancel",
		dialog.KeyStyle.Render("<↑/↓/PgUp/PgDn>"),
		dialog.KeyStyle.Render("<Enter>"),
		dialog.KeyStyle.Render("<Esc>"))
	lines = append(lines, helpStyle.Render(helpText))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return borderStyle.Render(content)
}
