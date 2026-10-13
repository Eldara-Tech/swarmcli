// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package command

import (
	"github.com/Eldara-Tech/swarmcli/v2/args"
	"github.com/Eldara-Tech/swarmcli/v2/registry"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
)

type Connect struct{}

func (Connect) Name() string        { return "connect" }
func (Connect) Description() string { return "Get the SwarmCLI phone app and connect this swarm" }

func (Connect) Spec() registry.CommandSpec {
	return registry.CommandSpec{
		Detail: "Opens the phone-app popup: a QR code that gets the SwarmCLI app for " +
			"iPhone and Android, and, where this build can connect the current " +
			"swarm, the code to do so. The same popup as the c key, and the way " +
			"to reach it from views where c does something else.",
		Examples: []string{":connect", ":app"},
	}
}

func (Connect) Execute(ctx any, args args.Args) tea.Cmd {
	return func() tea.Msg {
		return view.OpenConnectAppMsg{}
	}
}

var connectCmd = Connect{}

func init() {
	registry.Register(connectCmd)
	registry.Register(aliasCommand{name: "app", target: connectCmd})
}
