// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
)

func init() {
	view.RegisterView(ViewName, factory)
}

func factory(deps docker.Deps, w, h int, payload any) (view.View, tea.Cmd) {
	var nodeID string
	if payload != nil {
		nodeID, _ = payload.(string)
	}
	model := New(w, h)
	model.deps = deps
	model.Visible = true

	// Pre-populate from cached snapshot so keys work immediately.
	if snap := deps.Snapshot.GetSnapshot(); snap != nil {
		stacks := snap.ToStackEntries()
		model.lastSnapshot, _ = pollHash(stacks, snap)
		model.nodeID = nodeID
		model.setStacks(stacks)
	}

	return model, tea.Batch(model.Init(), model.LoadStacksCmd(nodeID))
}
