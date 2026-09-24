// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	"github.com/Eldara-Tech/swarmcli/v2/views/helpbar"
	systeminfoview "github.com/Eldara-Tech/swarmcli/v2/views/systeminfo"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	"github.com/charmbracelet/lipgloss"
)

// appChromeRows is what the app draws around a normal-mode view: the system
// info help bar above it and the breadcrumb stack bar below. Fullscreen drops
// both. The resize math in update.go deducts the same rows.
const appChromeRows = systeminfoview.Height + 1

// inputBarHeight is the framed box the ":" command bar and the "/" search bar
// are rendered into. Both modes reserve it while one of them is open.
const inputBarHeight = 3

func (m *Model) View() string {
	title := m.currentView.FrameTitle()
	header := m.currentView.FrameHeader()
	content := m.currentView.FrameContent()
	footer := m.currentView.FrameFooter()
	// m.viewport already has the input bar's rows deducted, if one is open.
	inputBar := m.renderInputBar()

	// Fullscreen mode: show only the current view (no helpbar, no stackbar).
	// The input bar stays: it is the only feedback for what is being typed.
	if m.fullscreen {
		out := ui.RenderViewFrame(title, header, content, footer,
			m.terminalWidth, m.viewport.Height, true)
		if inputBar != "" {
			out = lipgloss.JoinVertical(lipgloss.Left, inputBar, out)
		}
		return m.overlayStartup(m.overlayUnlock(m.overlayAppError(m.overlayUpdate(m.overlayContextDrift(out)))))
	}

	systemInfo := m.systemInfo.View()

	// Check if current view has errors or warnings for logo color
	hasError := m.currentView.HasErrors()
	hasWarning := false
	if w, ok := m.currentView.(view.Warner); ok {
		hasWarning = w.HasWarnings()
	}

	help := helpbar.New(m.viewport.Width, systeminfoview.Height).
		WithGlobalHelp(m.globalHelpEntries()).
		WithViewHelp(m.currentView.ShortHelpItems()).
		View(systemInfo, hasError, hasWarning)

	// Subtract help bar and stack bar from the viewport height so the frame
	// fits between the chrome elements.
	frameHeight := m.viewport.Height - appChromeRows
	if frameHeight < 1 {
		frameHeight = 1
	}
	framedView := ui.RenderViewFrame(title, header, content, footer, m.viewport.Width, frameHeight, false)

	rows := []string{help}
	if inputBar != "" {
		rows = append(rows, inputBar)
	}
	rows = append(rows, framedView, m.renderStackBar())

	out := lipgloss.JoinVertical(lipgloss.Left, rows...)
	return m.overlayStartup(m.overlayUnlock(m.overlayAppError(m.overlayUpdate(m.overlayContextDrift(out)))))
}

// renderInputBar renders whichever of the ":" command bar and the "/" search
// bar is open, or "" when neither is. They are mutually exclusive.
func (m *Model) renderInputBar() string {
	switch {
	case m.commandInput.Visible():
		return ui.RenderFramedBoxHeight("", "", m.commandInput.View(), "", m.terminalWidth, inputBarHeight)
	case m.searchInput.Visible():
		return ui.RenderFramedBoxHeight("", "", m.searchInput.View(), "", m.terminalWidth, inputBarHeight)
	default:
		return ""
	}
}

// globalHelpEntries are the app's own keybindings, and the single list both the
// help bar and the "?" screen are built from — the app must not advertise a key
// on one surface that the other has never heard of. Empty for a view that
// suppresses them, which is also what stops the app answering "?" for it.
func (m *Model) globalHelpEntries() []helpbar.HelpEntry {
	if m.hidesGlobalKeys() {
		return nil
	}
	return []helpbar.HelpEntry{
		{Key: "f", Desc: "Fullscreen"},
		{Key: "?", Desc: "Help"},
		{Key: helpbar.KeyQuit, Desc: "Quit"},
	}
}

// hidesGlobalKeys reports whether the current view suppresses the app-level
// keybindings: the help view already lists them, and a view may capture every
// keystroke (implementing HidesGlobalHelp), which makes ":"/"?" unreachable.
func (m *Model) hidesGlobalKeys() bool {
	if m.currentView.Name() == view.NameHelp {
		return true
	}
	vc, ok := m.currentView.(interface{ HidesGlobalHelp() bool })
	return ok && vc.HidesGlobalHelp()
}

// overlayAppError overlays the app-level error dialog on top of the rendered output.
func (m *Model) overlayAppError(base string) string {
	if !m.errorDialog.Visible {
		return base
	}
	return ui.OverlayCentered(base, m.errorDialog.View(), m.terminalWidth, 0)
}

// overlayUnlock overlays the swarm unlock-key dialog on top of the rendered output.
func (m *Model) overlayUnlock(base string) string {
	if !m.unlockDialog.Visible {
		return base
	}
	return ui.OverlayCentered(base, m.unlockDialog.View(), m.terminalWidth, 0)
}

// overlayUpdate overlays the "update available" notice on top of the output.
func (m *Model) overlayUpdate(base string) string {
	if !m.updateDialog.Visible {
		return base
	}
	return ui.OverlayCentered(base, m.updateDialog.View(), m.terminalWidth, 0)
}

// overlayContextDrift overlays the "context changed outside swarmcli" prompt.
func (m *Model) overlayContextDrift(base string) string {
	if !m.contextDriftDialog.Visible {
		return base
	}
	return ui.OverlayCentered(base, m.contextDriftDialog.View(), m.terminalWidth, 0)
}

// overlayStartup composites the startup overlay on top of the rendered output.
func (m *Model) overlayStartup(base string) string {
	if startupOverlay == nil || !startupOverlay.Active() {
		return base
	}
	return ui.OverlayCentered(base, startupOverlay.View(), m.terminalWidth, 0)
}
