// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package logsview

import (
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	"github.com/Eldara-Tech/swarmcli/v2/utils"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
)

// Update processes Tea messages.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {

	case InitStreamMsg:
		// store channels and begin the read-once pump
		m.linesChan = msg.Lines
		m.errChan = msg.Errs
		m.Visible = true
		// A re-opened stream replays its backlog again, so everything that
		// decides when the replay is over starts over with it — otherwise the
		// second open would highlight its own history.
		m.mu.Lock()
		m.linesSinceInit = 0
		m.lastLineAt = time.Time{}
		m.highlightArmed = false
		m.mu.Unlock()
		l().Debugf("[logsview] stream initialized")
		return m.readOneLineCmd()

	case LineMsg:
		// Parse node name and task ID (format: "nodename\x00taskid\x00actual_line")
		parts := strings.SplitN(msg.Line, "\x00", 3)
		var nodeName, taskID, actualLine string
		if len(parts) == 3 {
			nodeName = parts[0]
			taskID = parts[1]
			actualLine = parts[2]
		} else {
			actualLine = msg.Line
		}

		// Strip carriage returns that break frame rendering (progress bars, CRLF)
		actualLine = strings.ReplaceAll(actualLine, "\r", "")

		// append line into bounded buffer (store line, node and task)
		now := time.Now()
		m.mu.Lock()
		m.linesSinceInit++
		// Read before lastLineAt moves: the gap that ends the backlog replay is
		// the one between this line and the one before it.
		m.armHighlight(now)
		// store line as-is (no newline); rendering will join with '\n'
		m.appendLine(actualLine, nodeName, taskID, lineLog, m.stamp(now))
		m.lastLineAt = now
		linesDropped := m.trimToMaxLines()
		m.mu.Unlock()

		m.syncViewport(linesDropped)
		return tea.Batch(m.readOneLineCmd(), m.fadeTickCmd())

	case StreamErrMsg:
		// append an error line and stop
		now := time.Now()
		m.mu.Lock()
		m.appendLine(fmt.Sprintf("Error: %v", msg.Err), "", "", lineNotice, m.stamp(now))
		linesDropped := m.trimToMaxLines()
		m.mu.Unlock()
		l().Errorf("[logsview] stream error: %v", msg.Err)
		m.syncViewport(linesDropped)
		return m.fadeTickCmd()

	case StreamDoneMsg:
		now := time.Now()
		m.mu.Lock()
		m.appendLine("--- stream closed ---", "", "", lineNotice, m.stamp(now))
		linesDropped := m.trimToMaxLines()
		m.mu.Unlock()
		l().Debugf("[logsview] stream closed")
		m.syncViewport(linesDropped)
		return m.fadeTickCmd()

	case WrapToggledMsg:
		// Refresh viewport content with new wrap setting
		if m.ready {
			// Reset to a safe position when toggling wrap
			// because line count changes dramatically with wrapping
			savedOffset := m.viewport.YOffset
			m.viewport.SetContent(m.buildContent())

			// Ensure YOffset is within bounds
			maxOffset := m.viewport.TotalLineCount() - m.viewport.Height
			if maxOffset < 0 {
				maxOffset = 0
			}

			// If we were following, go to bottom
			shouldFollow := m.getFollow()
			if shouldFollow {
				m.viewport.GotoBottom()
			} else if savedOffset > maxOffset {
				// Adjust offset to stay within bounds
				m.viewport.YOffset = maxOffset
			} else {
				m.viewport.YOffset = savedOffset
			}
		}
		return nil

	case NodeFilterToggledMsg:
		// When node filter changes, we need to rebuild the content
		// because existing lines need to be filtered/unfiltered
		if m.ready {
			m.viewport.SetContent(m.buildContent())
			if m.getFollow() {
				m.viewport.GotoBottom()
			}
		}
		return nil

	case HideStoppedToggledMsg:
		// The visible set changed; recompute search-match indices (this also
		// rebuilds the viewport content) and re-anchor to the bottom if following.
		if m.ready {
			m.highlightContent()
			if m.getFollow() {
				m.viewport.GotoBottom()
			}
		}
		return nil

	case MarkInsertedMsg:
		// The separator goes in at the end of the buffer, so it lands under
		// everything read so far and above everything still to come.
		m.syncViewport(0)
		return nil

	case FadeTickMsg:
		m.mu.Lock()
		m.fadeArmed = false
		m.mu.Unlock()
		if m.ready {
			// Only the highlight expired: the rendered row count is unchanged,
			// so SetContent leaves a reader who scrolled up exactly where they
			// were, and a follower already at the bottom stays there.
			m.viewport.SetContent(m.buildContent())
		}
		return m.fadeTickCmd()

	case waitingTickMsg:
		return m.handleWaitingTick(msg)

	case waitingTasksMsg:
		return m.handleWaitingTasks(msg)

	case stoppedCheckedMsg:
		return m.handleStoppedChecked(msg)

	case tea.WindowSizeMsg:
		// Safety check: ensure dimensions are positive
		if msg.Width < 1 {
			msg.Width = 1
		}
		if msg.Height < 1 {
			msg.Height = 1
		}

		m.viewport.Width = msg.Width
		// msg.Height is the frame's height; the viewport only gets what is left
		// after the frame's own rows and the header. Sizing it to the frame
		// instead would park the newest lines below the cut — GotoBottom would
		// anchor a window taller than the rows actually drawn.
		m.viewport.Height = max(1, ui.ContentRows(msg.Height, ui.FramedChromeRows, m.FrameHeader(), m.FrameFooter()))
		if !m.ready {
			m.ready = true
		}
		// reset viewport content so the internal content height updates
		m.viewport.SetContent(m.buildContent())
		// A resize changes how many lines fit, but not the offset the viewport
		// scrolls from, so it keeps drawing the lines it drew before: growing
		// leaves the rows it gained blank, shrinking cuts the newest lines off.
		// Re-anchor while following, and otherwise only when the offset now sits
		// past the end — a reader who scrolled up stays where they were.
		if m.getFollow() || m.viewport.PastBottom() {
			m.viewport.GotoBottom()
		}
		return nil

	case tea.KeyMsg:
		// Check if node select dialog is visible first
		if m.getNodeSelectVisible() {
			// When dialog is visible, handle ALL keys through HandleKey
			// to prevent any keys from falling through to the viewport
			cmd := HandleKey(m, msg)
			// Force a return here to prevent any further processing
			if cmd != nil {
				return cmd
			}
			// Even if cmd is nil, don't process the key further
			return nil
		}

		// In search mode, don't intercept any keys for viewport - let HandleKey process them all
		if m.mode == "search" {
			cmd := HandleKey(m, msg)
			return cmd
		}

		// 1) allow viewport to handle scrolling keys (only in normal mode)
		switch msg.String() {
		case "up", "down", "pgup", "pgdown", "home", "end", "k", "j":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return cmd
		}

		// 2) other keys -> our handler
		cmd := HandleKey(m, msg)
		return cmd
	}

	// default: let viewport handle other messages
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return cmd
}

// readOneLineCmd returns a cmd that waits for one line from the line channel.
func (m *Model) readOneLineCmd() tea.Cmd {
	if m.linesChan == nil && m.errChan == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case line, ok := <-m.linesChan:
			if !ok {
				return StreamDoneMsg{}
			}
			return LineMsg{Line: line}
		case err, ok := <-m.errChan:
			if !ok {
				return StreamDoneMsg{}
			}
			if err != nil {
				return StreamErrMsg{Err: err}
			}
			return StreamDoneMsg{}
		}
	}
}

func (m *Model) SetContent(content string) {
	m.mu.Lock()
	content = strings.ReplaceAll(content, "\r", "")
	m.lines = strings.Split(content, "\n")
	if m.MaxLines > 0 && len(m.lines) > m.MaxLines {
		// keep only last MaxLines
		start := len(m.lines) - m.MaxLines
		m.lines = append([]string{}, m.lines[start:]...)
	}
	// SetContent carries no per-line node/task metadata; reset the parallel
	// slices to empty (length-aligned) so all lines are treated as
	// unfilterable (always visible) and indices stay aligned. The zero lineKind
	// is an ordinary log line and the zero time is "not new", which is what
	// content that did not arrive over the stream should be.
	m.lineNodes = make([]string, len(m.lines))
	m.lineTasks = make([]string, len(m.lines))
	m.lineKinds = make([]lineKind, len(m.lines))
	m.lineAt = make([]time.Time, len(m.lines))
	m.searchMatches = nil
	m.searchTerm = ""
	m.searchIndex = 0
	m.mode = "normal"
	m.mu.Unlock()

	if !m.ready {
		return
	}
	m.viewport.GotoTop()
	m.viewport.SetContent(m.buildContent())
	m.viewport.YOffset = 0
	l().Debugf("[logsview] SetContent called: total lines=%d", len(m.lines))
}

// highlightContent rebuilds the rendered content, which is also what recomputes
// the search matches, and re-anchors the selected match inside the new set.
func (m *Model) highlightContent() {
	content := m.buildContent()
	if m.searchIndex >= len(m.searchMatches) {
		m.searchIndex = 0
	}
	if m.ready {
		m.viewport.SetContent(content)
	}
}

// syncViewport rebuilds the content and keeps the reader where they were:
// pinned to the bottom while following, and otherwise on the same lines, which
// means taking off the offset however many lines the trim just dropped.
func (m *Model) syncViewport(linesDropped int) {
	if !m.ready {
		return
	}
	if m.getFollow() {
		m.viewport.SetContent(m.buildContent())
		m.viewport.GotoBottom()
		return
	}
	savedOffset := m.viewport.YOffset
	m.viewport.SetContent(m.buildContent())

	newOffset := savedOffset - linesDropped
	if newOffset < 0 {
		newOffset = 0
	}
	// Keep the offset in bounds — wrapping changes how many rows the same
	// lines occupy, so an offset that was valid before need not be now.
	maxOffset := m.viewport.TotalLineCount() - m.viewport.Height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if newOffset > maxOffset {
		newOffset = maxOffset
	}
	m.viewport.YOffset = newOffset
}

// fadeTickCmd schedules the redraw that lets a highlight expire, and only when
// one is live and no beat is already in flight. Arming from every arriving line
// would give one beat as many successors as there were lines, and each of those
// would do the same — the rate would not merely multiply once, it would
// multiply again on every beat.
func (m *Model) fadeTickCmd() tea.Cmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fadeArmed || !m.anyFresh(time.Now()) {
		return nil
	}
	m.fadeArmed = true
	return tea.Tick(fadeTickInterval, func(t time.Time) tea.Msg { return FadeTickMsg(t) })
}

// buildContent returns the full content (required by viewport) — rows may carry
// search highlighting and the bold of a freshly arrived line.
func (m *Model) buildContent() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	rows, isMark, isFresh := m.renderRows(time.Now())

	// Apply wrapping based on wrap setting
	// BUT: skip wrapping if node selection dialog is visible to avoid overlay issues
	if m.wrap && m.viewport.Width > 0 && !m.nodeSelectVisible {
		return m.wrapRows(rows, isFresh)
	}
	if m.viewport.Width > 0 {
		m.scrollRows(rows, isMark)
	}
	// Paint last, so the band covers the row the reader actually sees rather
	// than the text the scroll then cuts out of it.
	for i, fresh := range isFresh {
		if fresh {
			rows[i] = m.highlightRow(rows[i])
		}
	}
	return strings.Join(rows, "\n")
}

// wrapRows wraps each row to the viewport width and paints the fresh ones.
// Wrapping row by row rather than over the joined buffer is what keeps the two
// in step: after a single wrap of everything, which output row came from which
// line is no longer knowable. A row that already fits is handed back untouched
// — wrapping one that cannot break costs an allocation per line of the buffer.
// Callers hold m.mu.
func (m *Model) wrapRows(rows []string, isFresh []bool) string {
	out := make([]string, 0, len(rows))
	for i, row := range rows {
		wrapped := []string{row}
		if lipgloss.Width(row) > m.viewport.Width {
			wrapped = strings.Split(wordwrap.String(row, m.viewport.Width), "\n")
		}
		if isFresh[i] {
			// Every row a new line wrapped onto is new as well.
			for j := range wrapped {
				wrapped[j] = m.highlightRow(wrapped[j])
			}
		}
		out = append(out, wrapped...)
	}
	return strings.Join(out, "\n")
}

// highlightRow draws a freshly arrived row over freshBackground, padded to the
// viewport width so the band spans the frame instead of stopping wherever the
// text happens to end. It is applied after the search highlight, which ends in
// a reset the band is re-asserted past; the other order would leave the rest of
// a line holding a match unpainted. Callers hold m.mu.
func (m *Model) highlightRow(row string) string {
	if pad := m.viewport.Width - lipgloss.Width(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	return ui.BackgroundANSI(row, freshBackground)
}

// renderRows turns the line buffer into the rows to draw, and reports which of
// them belong to a separator. It also collects the search matches and the
// visible count in the same pass: both are positions in the rows it is
// building, so deriving them anywhere else means a second copy of this walk,
// and the two drifting apart is what froze the match counter (#586).
// Callers hold m.mu.
func (m *Model) renderRows(now time.Time) (rows []string, isMark, isFresh []bool) {
	// Apply all active filters (node, "/" text, hide-stopped) in a single pass
	// via the shared predicate so the visible set matches highlightContent.
	var stopped map[string]bool
	if m.hideStopped {
		stopped = m.stoppedTaskIDs()
	}
	m.stoppedAtBuild = stopped
	lower := strings.ToLower(m.searchTerm)
	m.searchMatches = nil
	visible, notices := 0, 0

	for i, line := range m.lines {
		if !m.lineVisible(i, stopped) {
			continue
		}
		if m.kindAt(i) == lineNotice {
			notices++
		}
		// A separator is drawn, not stored: rendering it here is what lets it
		// follow the viewport across a resize. It is not a log line, so it is
		// out of the count in the title and out of the search matches — a
		// query of "0" must not land on a timestamp the reader did not write.
		if m.kindAt(i) == lineMark {
			rows = append(rows, "", renderMark(line, m.viewport.Width))
			isMark = append(isMark, true, true)
			isFresh = append(isFresh, false, false)
			continue
		}
		if lower != "" && strings.Contains(strings.ToLower(line), lower) {
			m.searchMatches = append(m.searchMatches, len(rows))
		}
		if m.searchTerm != "" {
			line = utils.HighlightMatches(line, m.searchTerm)
		}
		rows = append(rows, line)
		isMark = append(isMark, false)
		// The band itself is painted later, once the wrap or the scroll has
		// settled what the row is; here it is only recorded.
		isFresh = append(isFresh, m.isFresh(i, now))
		if m.kindAt(i) == lineLog {
			visible++
		}
	}
	m.visibleCount = visible
	m.onScreen = visible > 0 || notices > 0
	// Counted only for an empty screen, the one place they are used: the
	// second walk is then over lines the first one already found hidden.
	m.anyLogLine, m.hiddenStopped = false, 0
	if !m.onScreen {
		for i := range m.lines {
			if m.kindAt(i) != lineLog {
				continue
			}
			m.anyLogLine = true
			if stopped != nil && i < len(m.lineTasks) && stopped[m.lineTasks[i]] && m.lineVisible(i, nil) {
				m.hiddenStopped++
			}
		}
	}
	return rows, isMark, isFresh
}

// scrollRows applies the horizontal offset in place, using ANSI-aware
// operations so an escape sequence in a coloured log line is never split.
// Separators are left alone: they carry no content that scrolls, and one that
// slid out of frame would leave the reader nothing to have marked.
// Callers hold m.mu.
func (m *Model) scrollRows(rows []string, isMark []bool) {
	for i, line := range rows {
		if isMark[i] {
			continue
		}
		lineWidth := lipgloss.Width(line)
		if lineWidth <= m.horizontalOffset {
			rows[i] = ""
			continue
		}
		visiblePart := ui.TruncateANSIAfter(line, m.horizontalOffset)
		if lipgloss.Width(visiblePart) <= m.viewport.Width {
			rows[i] = visiblePart
			continue
		}
		if m.viewport.Width > 1 {
			rows[i] = ui.TruncateANSI(visiblePart, m.viewport.Width-1) + ">"
		} else {
			rows[i] = ">"
		}
	}
}

// scrollToMatch centers the viewport on the selected match
func (m *Model) scrollToMatch() {
	if len(m.searchMatches) == 0 {
		return
	}
	idx := m.searchMatches[m.searchIndex]
	offset := idx - m.viewport.Height/2
	if offset < 0 {
		offset = 0
	}
	m.viewport.GotoTop()
	m.viewport.SetYOffset(offset)
	m.viewport.SetContent(m.buildContent())
	l().Debugf("[logsview] scrollToMatch idx=%d newYOffset=%d", idx, offset)
}
