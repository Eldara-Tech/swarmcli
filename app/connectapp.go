// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"github.com/Eldara-Tech/swarmcli/v2/settings"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"

	tea "github.com/charmbracelet/bubbletea"
)

// connectAppKey opens the phone-app popup from any view that does not use the
// key itself; :connect reaches it from the ones that do.
const connectAppKey = "c"

const (
	connectNotice      = "New: press c to put the SwarmCLI app on your phone"
	connectNoticeShort = "New: press c for the phone app"
)

// connectNoticeDueFn reports whether the one-time phone-app line is still to
// be shown, and records that it now has been. Overridable for tests.
var connectNoticeDueFn = func() bool {
	s := settings.Load()
	if s.ConnectAppNoticeShown {
		return false
	}
	s.ConnectAppNoticeShown = true
	if err := s.Save(); err != nil {
		// Unsaved means shown again next start, which beats never.
		swarmlog.L().Infow("failed to persist the phone-app notice", "error", err)
	}
	return true
}

// openConnectApp opens the phone-app popup over the current view.
func (m *Model) openConnectApp() tea.Cmd {
	return m.connectApp.Open(m.terminalWidth, m.terminalHeight)
}

// viewOwnsKey reports whether the current view advertises key in its own help
// bar, which is also how it says it handles that key itself.
func (m *Model) viewOwnsKey(key string) bool {
	for _, entry := range m.currentView.ShortHelpItems() {
		if entry.Key == key {
			return true
		}
	}
	return false
}

// overlayConnectApp overlays the phone-app popup on top of the rendered output.
func (m *Model) overlayConnectApp(base string) string {
	if !m.connectApp.Visible() {
		return base
	}
	return ui.OverlayCentered(base, m.connectApp.View(), m.terminalWidth, 0)
}
