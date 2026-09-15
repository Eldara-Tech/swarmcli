// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"os"
	"strings"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/ui"
	systeminfoview "github.com/Eldara-Tech/swarmcli/v2/views/systeminfo"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
)

// MouseEnv switches mouse capture off. It is on unless the variable says off in
// one of the spellings SWARMCLI_TELEMETRY accepts for off, so a value this does
// not recognise leaves the default alone. `:mouse` toggles it for a session.
const MouseEnv = "SWARMCLI_MOUSE"

// doubleClickWindow is how soon a second click on the same line counts as a
// double click. Terminals report presses, never clicks, so the app times them.
const doubleClickWindow = 500 * time.Millisecond

// now is the clock double clicks are timed against. Overridden in tests.
var now = time.Now

// Mouse notices for the stack bar, shown after `:mouse` until the next key.
const (
	mouseOnNotice       = "Mouse on · hold Shift (Option in iTerm2) to select text · :mouse"
	mouseOnNoticeShort  = "Mouse on · :mouse"
	mouseOffNotice      = "Mouse off · text selection works as usual · :mouse to turn it back on"
	mouseOffNoticeShort = "Mouse off · :mouse"
)

func mouseFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(MouseEnv))) {
	case "off", "false", "0", "no":
		return false
	}
	return true
}

// ProgramOptions are the tea.Program options a TUI entry point starts with:
// the alternate screen, and mouse capture unless MouseEnv switches it off. An
// extension build's own main passes them too, so both builds agree on the
// terminal they set up.
//
// Cell motion rather than all motion: clicks, releases and the wheel are all
// the app uses, and all-motion delivers an Update for every cell the pointer
// crosses.
func ProgramOptions() []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if mouseFromEnv() {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	return opts
}

// handleMouse is where every mouse event ends. None is delegated as it arrives:
// the app's dialogs and a startup overlay only block keys, and the list views
// hand unknown messages to a viewport that scrolls and then snaps back to the
// cursor. The wheel becomes up/down keypresses for the current view; a left
// click inside the frame selects a row, and a second one on the same line
// opens it the way Enter does.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if !m.mouseOn || msg.Action != tea.MouseActionPress || m.mouseBlocked() {
		return nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.currentView.Update(tea.KeyMsg{Type: tea.KeyUp})
	case tea.MouseButtonWheelDown:
		return m.currentView.Update(tea.KeyMsg{Type: tea.KeyDown})
	case tea.MouseButtonLeft:
		return m.clickRow(msg.Y)
	}
	return nil
}

// mouseBlocked reports whether something is holding the keyboard: a dialog or
// startup overlay, the ":" bar, the "/" bar being typed in, or a view capturing
// input. A click must not act on what is underneath, and the wheel as up/down
// would move a dialog's own selection — the scale dialog's replica count.
func (m *Model) mouseBlocked() bool {
	if m.dialogActive() || m.commandInput.Visible() {
		return true
	}
	if m.searchInput.Visible() && m.searchInput.Editing() {
		return true
	}
	v, ok := m.currentView.(interface{ CapturesInput() bool })
	return ok && v.CapturesInput()
}

func (m *Model) clickRow(y int) tea.Cmd {
	clicker, ok := m.currentView.(view.RowClicker)
	if !ok {
		return nil
	}
	line, ok := m.contentLine(y)
	if !ok || !clicker.ClickRow(line) {
		m.lastClickAt = time.Time{}
		return nil
	}
	t := now()
	if line == m.lastClickLine && t.Sub(m.lastClickAt) <= doubleClickWindow {
		// A third click starts over rather than opening again.
		m.lastClickAt = time.Time{}
		return m.currentView.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}
	m.lastClickAt, m.lastClickLine = t, line
	return nil
}

// contentLine turns a screen row into a line of the current view's content,
// counted from the first line below its header, and reports whether the row is
// inside the content at all. It mirrors the layout View draws: the help bar,
// then the input bar if one is open, then the frame's title row and the
// header; fullscreen drops the help bar and spends one row on the title.
func (m *Model) contentLine(y int) (int, bool) {
	header := m.currentView.FrameHeader()
	footer := m.currentView.FrameFooter()

	top, frameHeight, chrome := systeminfoview.Height+1, m.viewport.Height-appChromeRows, ui.FramedChromeRows
	if m.fullscreen {
		top, frameHeight, chrome = ui.FullscreenChromeRows, m.viewport.Height, ui.FullscreenChromeRows
	}
	if frameHeight < 1 {
		frameHeight = 1
	}
	if m.commandInput.Visible() || m.searchInput.Visible() {
		top += inputBarHeight
	}
	if header != "" {
		top += strings.Count(header, "\n") + 1
	}

	line := y - top
	return line, line >= 0 && line < ui.ContentRows(frameHeight, chrome, header, footer)
}
