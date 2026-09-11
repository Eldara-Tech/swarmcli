// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package secret

import (
	"github.com/Eldara-Tech/swarmcli/v2/args"
	"github.com/Eldara-Tech/swarmcli/v2/registry"
	secretsview "github.com/Eldara-Tech/swarmcli/v2/views/secrets"

	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
)

type DockerSecretLs struct{}

func (DockerSecretLs) Name() string        { return "secret" }
func (DockerSecretLs) Description() string { return "docker secret ls" }

func (DockerSecretLs) Spec() registry.CommandSpec {
	return registry.CommandSpec{
		Detail: "Opens the Docker Secrets list, where you can create, " +
			"inspect and delete secrets and see which stacks use them. " +
			"With a Business Edition license you can also reveal a " +
			"secret's value.",
		Examples: []string{":secret"},
	}
}

func (DockerSecretLs) Execute(ctx any, args args.Args) tea.Cmd {
	return func() tea.Msg {
		return view.NavigateToMsg{
			ViewName: secretsview.ViewName,
			Payload:  nil,
		}
	}
}

var lsCmd = DockerSecretLs{}

func init() {
	registry.Register(lsCmd)
}
