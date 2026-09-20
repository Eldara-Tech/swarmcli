// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package view

import (
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

// RowClicker is an opt-in interface for views whose content is a list of rows.
// The app turns a left click inside the frame into ClickRow, with line counted
// from the first content line below the header, and a second click on the same
// line in quick succession into an Enter keypress. ClickRow selects whatever
// row that line belongs to and reports whether there was one; a click on
// padding, a placeholder or anything else that is not a row returns false and
// changes nothing.
//
// The app only calls it when nothing is capturing input, so a view does not
// have to check its own dialogs. Views without it still get the wheel, as
// up/down keypresses. Checked via type assertion in app/mouse.go.
type RowClicker interface {
	ClickRow(line int) bool
}

// ToggleMouseMsg asks the app to switch mouse capture off, or back on, for the
// rest of the session.
type ToggleMouseMsg struct{}

// RestoreMouseMsg asks the app to switch mouse reporting back on if the session
// has it on. It follows a terminal handover; see ExecProcess.
type RestoreMouseMsg struct{}

// ExecProcess is tea.ExecProcess for a session that may have the mouse on.
//
// Bubble Tea v1 switches mouse reporting off when it hands the terminal to the
// process and does not switch it back on when it takes the terminal back
// (charmbracelet/bubbletea#1424), so without this the mouse stays dead after an
// editor closes. The restore runs after the process has exited: Bubble Tea
// executes a sequence in order and the handover blocks its event loop.
func ExecProcess(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
	return tea.Sequence(tea.ExecProcess(c, fn), restoreMouse)
}

// Exec is ExecProcess for a tea.ExecCommand.
func Exec(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd {
	return tea.Sequence(tea.Exec(c, fn), restoreMouse)
}

func restoreMouse() tea.Msg { return RestoreMouseMsg{} }
