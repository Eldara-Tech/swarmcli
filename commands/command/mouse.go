// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package command

import (
	"github.com/Eldara-Tech/swarmcli/v2/args"
	"github.com/Eldara-Tech/swarmcli/v2/registry"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
)

// Mouse is the :mouse command: switch mouse capture off, or back on, for the
// rest of the session.
type Mouse struct{}

func (Mouse) Name() string { return "mouse" }

func (Mouse) Description() string {
	return "Turn mouse support on or off for this session"
}

func (Mouse) Spec() registry.CommandSpec {
	return registry.CommandSpec{
		Detail: "Switches mouse support off, or back on, until swarmcli exits. " +
			"With it on, the wheel moves the selection, a click selects a row " +
			"and a double click opens it; hold Shift (Option in iTerm2) to " +
			"select text. It starts on unless SWARMCLI_MOUSE=off.",
		Examples: []string{":mouse"},
	}
}

func (Mouse) Execute(_ any, _ args.Args) tea.Cmd {
	return func() tea.Msg {
		return view.ToggleMouseMsg{}
	}
}

func init() {
	registry.Register(Mouse{})
}
