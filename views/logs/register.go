// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package logsview

import (
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
)

func init() {
	view.RegisterView(ViewName, factory)
}

func factory(deps docker.Deps, w, h int, payload any) (view.View, tea.Cmd) {
	service := payload.(docker.ServiceEntry)
	v := New(w, h, 10000, service)
	v.deps = deps
	return v, tea.Batch(v.startStreamingCmd(v.StreamCtx, service, backlogTail, v.MaxLines), v.loadWaitingCmd())
}
