// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package command

import (
	"github.com/Eldara-Tech/swarmcli/v2/args"
	"github.com/Eldara-Tech/swarmcli/v2/registry"
	"github.com/Eldara-Tech/swarmcli/v2/telemetry"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
)

// Telemetry is the :telemetry command: what usage reporting sends, and whether
// it is on in this process.
//
// It is the other half of moving the first-run disclosure onto one line of the
// stack bar. A line has room for the fact and not for the enumeration, so the
// enumeration needs somewhere to live that a reader can reach on purpose —
// asked for rather than pushed, and available on every run instead of only the
// first.
type Telemetry struct{}

func (Telemetry) Name() string { return "telemetry" }

func (Telemetry) Description() string {
	return "Show what usage reporting sends, and whether it is on"
}

func (Telemetry) Spec() registry.CommandSpec {
	return registry.CommandSpec{
		Detail: "Prints whether usage reporting is on for this process, " +
			"everything the startup request carries, everything it does not, " +
			"and how to switch it off. The same text the first run shows in " +
			"short form on the stack bar.\n\n" +
			"Reporting is governed by " + telemetry.Env + ": unset reports, " +
			"'off' stops the report and keeps the update check, 'none' makes " +
			"no outbound request at all.",
		Examples: []string{":telemetry"},
	}
}

func (Telemetry) Execute(_ any, _ args.Args) tea.Cmd {
	return func() tea.Msg {
		return view.AppInfoMsg{Message: telemetry.StatusText()}
	}
}

func init() {
	registry.Register(Telemetry{})
}
