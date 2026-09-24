// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package view

import (
	"github.com/Eldara-Tech/swarmcli/v2/views/helpbar"

	tea "github.com/charmbracelet/bubbletea"
)

type View interface {
	Update(msg tea.Msg) tea.Cmd
	View() string
	Init() tea.Cmd
	Name() string
	ShortHelpItems() []helpbar.HelpEntry

	// Lifecycle hooks:

	OnEnter() tea.Cmd // Called when view becomes active
	OnExit() tea.Cmd  // Called when view is removed/replaced

	HasErrors() bool // Returns true if the view has any errors to display

	// Frame components — views return unframed content,
	// app wraps with ui.RenderViewFrame.
	FrameTitle() string   // text for frame title bar
	FrameHeader() string  // rendered header line(s), "" if none
	FrameFooter() string  // rendered footer line(s), "" if none
	FrameContent() string // unframed content (may include dialog overlays)
}

// Warner is an opt-in interface for views whose rows can be converging: not
// failing, but not settled either. The logo turns amber when the current view
// has warnings and no errors. Checked via type assertion in app/view.go.
type Warner interface {
	HasWarnings() bool
}

// Filterable is an opt-in interface for views that support app-level "/"
// search filtering. Checked via type assertion in app/update.go.
type Filterable interface {
	ApplySearchQuery(query string)
	ClearSearchQuery()
}
