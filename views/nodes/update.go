// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package nodesview

import (
	"context"
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/core/primitives/hash"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	helpview "github.com/Eldara-Tech/swarmcli/v2/views/help"
	inspectview "github.com/Eldara-Tech/swarmcli/v2/views/inspect"
	servicesview "github.com/Eldara-Tech/swarmcli/v2/views/services"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/docker/docker/api/types/swarm"
)

const userActionTimeout = 15 * time.Second

// getFreshNodeState retrieves the current node state with a forced refresh
func (m *Model) getFreshNodeState(nodeID string) *docker.NodeEntry {
	snapshotOps := m.deps.Snapshot
	// Force a synchronous refresh to get the absolute latest state
	snap, err := snapshotOps.RefreshSnapshot()
	if err != nil {
		l().Warnf("Failed to refresh snapshot: %v", err)
		snap = snapshotOps.GetSnapshot()
		if snap == nil {
			return nil
		}
	}
	entries := snap.ToNodeEntries()
	for _, entry := range entries {
		if entry.ID == nodeID {
			return &entry
		}
	}
	return nil
}

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case confirmdialog.ResultMsg:
		if !msg.Confirmed {
			// User cancelled, just close the dialog
			m.confirmDialog.Visible = false
			return nil
		}

		if m.List.Cursor < len(m.List.Filtered) {
			node := m.List.Filtered[m.List.Cursor]
			// Check which action to perform based on message content
			if strings.Contains(m.confirmDialog.Message, "Demote") {
				nodeOps := m.deps.Nodes
				snapshotOps := m.deps.Snapshot
				// Run demote, keeping dialog visible during operation
				return func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
					defer cancel()
					if err := nodeOps.DemoteNode(ctx, node.ID); err != nil {
						return DemoteErrorMsg{NodeID: node.ID, Error: err}
					}
					// Force refresh
					if _, err := snapshotOps.RefreshSnapshot(); err != nil {
						l().Warnf("Failed to refresh snapshot: %v", err)
					}
					// Return a message that will close dialog and refresh list
					return DemoteSuccessMsg{}
				}
			} else if strings.Contains(m.confirmDialog.Message, "Promote") {
				nodeOps := m.deps.Nodes
				snapshotOps := m.deps.Snapshot
				// Run promote, keeping dialog visible during operation
				return func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
					defer cancel()
					if err := nodeOps.PromoteNode(ctx, node.ID); err != nil {
						return PromoteErrorMsg{NodeID: node.ID, Error: err}
					}
					// Force refresh
					if _, err := snapshotOps.RefreshSnapshot(); err != nil {
						l().Warnf("Failed to refresh snapshot: %v", err)
					}
					// Return a message that will close dialog and refresh list
					return PromoteSuccessMsg{}
				}
			} else if strings.Contains(m.confirmDialog.Message, "Remove") {
				nodeOps := m.deps.Nodes
				snapshotOps := m.deps.Snapshot
				// Run remove with force=true, keeping dialog visible during operation
				return func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
					defer cancel()
					if err := nodeOps.RemoveNode(ctx, node.ID, true); err != nil {
						return RemoveErrorMsg{NodeID: node.ID, Error: err}
					}
					// Force refresh
					if _, err := snapshotOps.RefreshSnapshot(); err != nil {
						l().Warnf("Failed to refresh snapshot: %v", err)
					}
					// Return a message that will close dialog and refresh list
					return RemoveSuccessMsg{}
				}
			}
		}
		m.confirmDialog.Visible = false
		return nil

	case DemoteErrorMsg:
		// Reuse confirm dialog to display error
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to demote %s:\n%v", msg.NodeID, msg.Error)
		return nil

	case PromoteErrorMsg:
		// Reuse confirm dialog to display error
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to promote %s:\n%v", msg.NodeID, msg.Error)
		return nil

	case DemoteSuccessMsg:
		// Close dialog and reload nodes with fresh data
		m.confirmDialog.Visible = false
		return m.LoadNodesCmd()

	case PromoteSuccessMsg:
		// Close dialog and reload nodes with fresh data
		m.confirmDialog.Visible = false
		return m.LoadNodesCmd()

	case RemoveErrorMsg:
		// Reuse confirm dialog to display error
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to remove %s:\n%v", msg.NodeID, msg.Error)
		return nil

	case RemoveSuccessMsg:
		// Close dialog and reload nodes with fresh data
		m.confirmDialog.Visible = false
		return m.LoadNodesCmd()

	case SetAvailabilityErrorMsg:
		// Show error in confirm dialog
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to set availability:\n%v", msg.Error)
		return nil

	case SetAvailabilitySuccessMsg:
		// Close dialog and reload nodes
		m.availabilityDialog = false
		return m.LoadNodesCmd()

	case AddLabelErrorMsg:
		// Show error in confirm dialog
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to add label:\n%v", msg.Error)
		return nil

	case AddLabelSuccessMsg:
		// Close dialog and reload nodes
		m.labelInputDialog = false
		return m.LoadNodesCmd()

	case RemoveLabelErrorMsg:
		// Show error in confirm dialog
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to remove label:\n%v", msg.Error)
		return nil

	case RemoveLabelSuccessMsg:
		// Close dialog and reload nodes
		m.labelRemoveDialog = false
		return m.LoadNodesCmd()

	case Msg:
		l().Infof("NodesView: Received Msg with %d entries", len(msg.Entries))
		// Update the hash with new data
		var err error
		newHash, err := hash.Compute(msg.Entries)
		if err != nil {
			l().Errorf("NodesView: Error computing hash: %v", err)
			return nil
		}

		// Only reset cursor on first load (when lastSnapshot is 0)
		shouldResetCursor := m.lastSnapshot == 0
		m.lastSnapshot = newHash

		// Store current cursor position before updating content
		oldCursor := m.List.Cursor
		oldYOffset := m.List.Viewport.YOffset

		m.SetContent(msg)

		// Restore cursor position unless it's the first load
		if !shouldResetCursor {
			// Make sure cursor is still valid after update
			if oldCursor < len(m.List.Filtered) {
				m.List.Cursor = oldCursor
				m.List.Viewport.YOffset = oldYOffset
			}
		}

		m.Visible = true
		// No re-arm: only a tick arms a tick, so a load issued by OnEnter or the
		// factory cannot start a second, parallel chain.
		return nil

	case TickMsg:
		if msg.Gen != m.pollGen {
			return nil // a leftover from an earlier entry — see OnEnter
		}
		l().Infof("NodesView: Received TickMsg, visible=%v", m.Visible)
		// Check for changes (this will return either a Msg or PollRetryMsg)
		if m.Visible {
			return tea.Batch(m.checkNodesCmd(m.lastSnapshot), tickCmd(m.pollGen))
		}
		// Continue polling even if not visible
		return tickCmd(m.pollGen)

	case PollRetryMsg:
		// Deliberately no re-arm. The TickMsg handler above always schedules
		// the next tick, so re-arming here as well gives one beat two
		// successors — and each of those does the same, so the poll rate does
		// not merely double, it doubles again on every beat.
		return nil

	case tea.WindowSizeMsg:
		m.List.Viewport.Width = msg.Width
		m.List.Viewport.Height = msg.Height
		m.List.SetOuterSize(msg.Width, msg.Height)
		m.ready = true
		// On first resize, reset YOffset to 0; on subsequent resizes, only reset if cursor is at top
		if m.firstResize {
			m.List.Viewport.YOffset = 0
			m.firstResize = false
		} else if m.List.Cursor == 0 {
			m.List.Viewport.YOffset = 0
		}
		return nil

	case tea.KeyMsg:
		if m.labelInputDialog {
			return m.handleLabelInputDialogKey(msg)
		}

		if m.labelRemoveDialog {
			return m.handleLabelRemoveDialogKey(msg)
		}

		if m.availabilityDialog {
			return m.handleAvailabilityDialogKey(msg)
		}

		if m.confirmDialog.Visible {
			return m.confirmDialog.Update(msg)
		}

		// --- normal mode ---
		if msg.Type == tea.KeyEsc && m.List.Query != "" {
			m.List.Query = ""
			m.List.ApplyFilter()
			m.List.Cursor = 0
			m.List.Viewport.GotoTop()
			return nil
		}

		// Handle left/right for labels scrolling
		switch msg.String() {
		case "left":
			m.List.ScrollLeft()
			m.List.Viewport.SetContent(m.List.View())
			return nil
		case "right":
			m.List.ScrollRight()
			m.List.Viewport.SetContent(m.List.View())
			return nil
		}

		oldCursor := m.List.Cursor
		m.List.HandleKey(msg) // still handle up/down/pgup/pgdown

		// Reset scroll offset on cursor movement
		if m.List.Cursor != oldCursor {
			m.List.ResetColumnScroll()
			m.List.Viewport.SetContent(m.List.View())
		}

		// Enter triggers inspect / ps
		switch msg.String() {
		case "i":
			if m.List.Cursor < len(m.List.Filtered) {
				node := m.List.Filtered[m.List.Cursor]
				inspectOps := m.deps.Inspect
				return func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
					defer cancel()
					inspectContent, err := inspectOps.Inspect(ctx, docker.InspectNode, node.ID)
					if err != nil {
						inspectContent = "Error inspecting node: " + err.Error()
					}
					return view.NavigateToMsg{
						ViewName: inspectview.ViewName,
						Payload: map[string]interface{}{
							"title": "Node: " + node.Hostname,
							"json":  inspectContent,
						},
					}
				}
			}
		case "p":
			if m.List.Cursor < len(m.List.Filtered) {
				node := m.List.Filtered[m.List.Cursor]
				return func() tea.Msg {
					return view.NavigateToMsg{
						ViewName: servicesview.ViewName,
						Payload: map[string]interface{}{
							"nodeID":   node.ID,
							"hostname": node.Hostname,
						},
					}
				}
			}
		case "ctrl+t":
			if m.List.Cursor < len(m.List.Filtered) {
				node := m.List.Filtered[m.List.Cursor]
				// Get fresh node state from snapshot to avoid stale data
				freshNode := m.getFreshNodeState(node.ID)
				if freshNode != nil {
					node = *freshNode
				}
				if node.Manager {
					m.confirmDialog.Visible = true
					m.confirmDialog.ErrorMode = false
					m.confirmDialog.Message = fmt.Sprintf("Demote node %q?", node.Hostname)
				} else {
					// Not a manager; show error dialog
					m.confirmDialog.Visible = true
					m.confirmDialog.ErrorMode = true
					m.confirmDialog.Message = fmt.Sprintf("Node %q is not a manager", node.Hostname)
				}
			}
		case "ctrl+o":
			if m.List.Cursor < len(m.List.Filtered) {
				node := m.List.Filtered[m.List.Cursor]
				// Get fresh node state from snapshot to avoid stale data
				freshNode := m.getFreshNodeState(node.ID)
				if freshNode != nil {
					node = *freshNode
				}
				if !node.Manager {
					m.confirmDialog.Visible = true
					m.confirmDialog.ErrorMode = false
					m.confirmDialog.Message = fmt.Sprintf("Promote node %q?", node.Hostname)
				} else {
					// Already a manager; show error dialog
					m.confirmDialog.Visible = true
					m.confirmDialog.ErrorMode = true
					m.confirmDialog.Message = fmt.Sprintf("Node %q is already a manager", node.Hostname)
				}
			}

		// Sort by Hostname (Shift+H)
		case "H":
			if m.sortField == SortByHostname {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByHostname
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by State (Shift+S)
		case "S":
			if m.sortField == SortByState {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByState
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by Availability (Shift+A)
		case "A":
			if m.sortField == SortByAvailability {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByAvailability
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by Role (Shift+R)
		case "R":
			if m.sortField == SortByRole {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByRole
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by Version (Shift+V)
		case "V":
			if m.sortField == SortByVersion {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByVersion
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by Address (Shift+D)
		case "D":
			if m.sortField == SortByAddress {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByAddress
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by Labels (Shift+L)
		case "L":
			if m.sortField == SortByLabels {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByLabels
				m.sortAscending = true
			}
			m.applySorting()
			return nil
		case "a":
			if m.List.Cursor < len(m.List.Filtered) {
				node := m.List.Filtered[m.List.Cursor]
				m.availabilityDialog = true
				m.availabilityNodeID = node.ID
				m.availabilitySelection = 0
			}
		case "ctrl+l":
			if m.List.Cursor < len(m.List.Filtered) {
				node := m.List.Filtered[m.List.Cursor]
				m.labelInputDialog = true
				m.labelInputNodeID = node.ID
				m.labelInputValue = ""
			}
		case "ctrl+r":
			if m.List.Cursor < len(m.List.Filtered) {
				node := m.List.Filtered[m.List.Cursor]
				if len(node.Labels) == 0 {
					m.confirmDialog.Visible = true
					m.confirmDialog.ErrorMode = true
					m.confirmDialog.Message = "Node has no labels to remove"
				} else {
					// Build label list as "key=value" strings
					labels := make([]string, 0, len(node.Labels))
					for k, v := range node.Labels {
						labels = append(labels, k+"="+v)
					}
					// Sort for consistent display
					sort.Strings(labels)
					m.labelRemoveDialog = true
					m.labelRemoveNodeID = node.ID
					m.labelRemoveSelection = 0
					m.labelRemoveLabels = labels
				}
			}
		case "ctrl+d":
			if m.List.Cursor < len(m.List.Filtered) {
				node := m.List.Filtered[m.List.Cursor]
				m.confirmDialog.Visible = true
				m.confirmDialog.ErrorMode = false
				m.confirmDialog.Message = fmt.Sprintf("Remove node %q from swarm?\nWarning: This action cannot be undone.", node.Hostname)
			}
		}

		return nil
	}

	var cmd tea.Cmd
	m.List.Viewport, cmd = m.List.Viewport.Update(msg)
	return cmd
}

// ClickRow selects the node drawn on the given content line (view.RowClicker),
// with the same reset the arrow keys do when the cursor moves.
func (m *Model) ClickRow(line int) bool {
	oldCursor := m.List.Cursor
	if !m.List.SelectLine(line) {
		return false
	}
	if m.List.Cursor != oldCursor {
		m.List.ResetColumnScroll()
		m.List.Viewport.SetContent(m.List.View())
	}
	return true
}

func (m *Model) SetContent(msg Msg) {
	l().Infof("NodesView.SetContent: Updating display with %d entries", len(msg.Entries))

	m.List.Items = msg.Entries
	m.List.ApplyFilter()
	m.applySorting()

	m.setRenderItem()

	if m.ready {
		m.List.Viewport.SetContent(m.List.View())
		l().Info("NodesView.SetContent: Viewport content updated")
	} else {
		l().Warn("NodesView.SetContent: View not ready yet, skipping viewport update")
	}
}

// buildColumns declares the nodes table columns for the shared content-aware
// layout. ID/HOSTNAME/LABELS flex (grow + horizontally scroll); the rest are
// fixed-width status columns.
func (m *Model) buildColumns() []filterlist.Column[docker.NodeEntry] {
	return []filterlist.Column[docker.NodeEntry]{
		{Label: "ID", MinWidth: 6, Flex: true, Cell: func(n docker.NodeEntry) string { return n.ID }},
		{Label: "HOSTNAME", MinWidth: 8, Flex: true, Cell: func(n docker.NodeEntry) string { return n.Hostname }},
		{Label: "ROLE", Cell: func(n docker.NodeEntry) string { return n.Role }},
		{Label: "STATE", Cell: func(n docker.NodeEntry) string { return n.State }},
		{Label: "Availability", Cell: func(n docker.NodeEntry) string { return n.Availability }},
		{Label: "MANAGER", Cell: func(n docker.NodeEntry) string {
			if n.Manager {
				return "yes"
			}
			return "no"
		}},
		{Label: "MGR STATUS", Cell: func(n docker.NodeEntry) string { return n.ManagerStatus }},
		{Label: "VERSION", Cell: func(n docker.NodeEntry) string { return n.Version }},
		{Label: "ADDRESS", Cell: func(n docker.NodeEntry) string { return n.Addr }},
		{Label: "LABELS", MinWidth: 6, Flex: true, Cell: func(n docker.NodeEntry) string { return formatLabels(n.Labels) }},
	}
}

func (m *Model) setRenderItem() {
	m.List.RenderItem = func(n docker.NodeEntry, selected bool, _ int) string {
		row := m.List.RenderRow(n, selected)
		if selected {
			return ui.ListSelectedStyle.Render(row)
		}
		if nodeIsUnhealthy(n) {
			// Color non-selected unhealthy rows red, like the services view
			return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(row)
		}
		return ui.ListItemStyle.Render(row)
	}
}

// handleAvailabilityDialogKey handles key presses when availability dialog is visible
func (m *Model) handleAvailabilityDialogKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		if m.availabilitySelection > 0 {
			m.availabilitySelection--
		}
	case "down", "j":
		if m.availabilitySelection < 2 {
			m.availabilitySelection++
		}
	case "1", "a":
		m.availabilitySelection = 0
	case "2", "p":
		m.availabilitySelection = 1
	case "3", "d":
		m.availabilitySelection = 2
	case "enter":
		// Apply the selected availability
		availability := []string{"active", "pause", "drain"}[m.availabilitySelection]
		nodeID := m.availabilityNodeID
		nodeOps := m.deps.Nodes
		snapshotOps := m.deps.Snapshot
		m.availabilityDialog = false
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
			defer cancel()
			var avail swarm.NodeAvailability
			switch availability {
			case "active":
				avail = swarm.NodeAvailabilityActive
			case "pause":
				avail = swarm.NodeAvailabilityPause
			case "drain":
				avail = swarm.NodeAvailabilityDrain
			}
			if err := nodeOps.SetNodeAvailability(ctx, nodeID, avail); err != nil {
				return SetAvailabilityErrorMsg{NodeID: nodeID, Error: err}
			}
			// Force refresh
			if _, err := snapshotOps.RefreshSnapshot(); err != nil {
				l().Warnf("Failed to refresh snapshot: %v", err)
			}
			return SetAvailabilitySuccessMsg{}
		}
	case "esc":
		m.availabilityDialog = false
	}
	return nil
}

// handleLabelInputDialogKey handles key presses when label input dialog is visible
func (m *Model) handleLabelInputDialogKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.labelInputDialog = false
		m.labelInputValue = ""
		return nil
	case tea.KeyEnter:
		// Parse and apply the label
		input := strings.TrimSpace(m.labelInputValue)
		if input == "" {
			m.labelInputDialog = false
			return nil
		}

		// Parse key=value format
		parts := strings.SplitN(input, "=", 2)
		if len(parts) != 2 {
			m.confirmDialog.Visible = true
			m.confirmDialog.ErrorMode = true
			m.confirmDialog.Message = "Invalid format. Use: key=value"
			m.labelInputDialog = false
			return nil
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if key == "" {
			m.confirmDialog.Visible = true
			m.confirmDialog.ErrorMode = true
			m.confirmDialog.Message = "Label key cannot be empty"
			m.labelInputDialog = false
			return nil
		}

		nodeID := m.labelInputNodeID
		nodeOps := m.deps.Nodes
		snapshotOps := m.deps.Snapshot
		m.labelInputDialog = false
		m.labelInputValue = ""

		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
			defer cancel()
			if err := nodeOps.AddNodeLabel(ctx, nodeID, key, value); err != nil {
				return AddLabelErrorMsg{NodeID: nodeID, Error: err}
			}
			// Force refresh
			if _, err := snapshotOps.RefreshSnapshot(); err != nil {
				l().Warnf("Failed to refresh snapshot: %v", err)
			}
			return AddLabelSuccessMsg{}
		}
	case tea.KeyBackspace:
		if len(m.labelInputValue) > 0 {
			m.labelInputValue = m.labelInputValue[:len(m.labelInputValue)-1]
		}
	case tea.KeyRunes:
		m.labelInputValue += string(msg.Runes)
	}
	return nil
}

// handleLabelRemoveDialogKey handles key presses when label remove dialog is visible
func (m *Model) handleLabelRemoveDialogKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		if m.labelRemoveSelection > 0 {
			m.labelRemoveSelection--
		}
	case "down", "j":
		if m.labelRemoveSelection < len(m.labelRemoveLabels)-1 {
			m.labelRemoveSelection++
		}
	case "enter":
		// Parse selected label and remove it
		if m.labelRemoveSelection < len(m.labelRemoveLabels) {
			selected := m.labelRemoveLabels[m.labelRemoveSelection]
			// Extract key from "key=value"
			parts := strings.SplitN(selected, "=", 2)
			if len(parts) < 1 {
				m.labelRemoveDialog = false
				return nil
			}
			key := parts[0]
			nodeID := m.labelRemoveNodeID
			nodeOps := m.deps.Nodes
			snapshotOps := m.deps.Snapshot
			m.labelRemoveDialog = false

			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
				defer cancel()
				if err := nodeOps.RemoveNodeLabel(ctx, nodeID, key); err != nil {
					return RemoveLabelErrorMsg{NodeID: nodeID, Error: err}
				}
				// Force refresh
				if _, err := snapshotOps.RefreshSnapshot(); err != nil {
					l().Warnf("Failed to refresh snapshot: %v", err)
				}
				return RemoveLabelSuccessMsg{}
			}
		}
	case "esc":
		m.labelRemoveDialog = false
	}
	return nil
}

// HelpContent implements the app's optional help-screen contract: "?" is
// handled centrally, and a view carrying its own screen supplies it here.
func (m *Model) HelpContent() []helpview.HelpCategory { return GetNodesHelpContent() }

// GetNodesHelpContent returns categorized help for the nodes view
func GetNodesHelpContent() []helpview.HelpCategory {
	return []helpview.HelpCategory{
		{
			Title: "General",
			Items: []helpview.HelpItem{
				{Keys: "<i>", Description: "Inspect node"},
				{Keys: "<p>", Description: "Show services on node"},
				{Keys: "<a>", Description: "Change availability"},
				{Keys: "<ctrl+l>", Description: "Add label to node"},
				{Keys: "<ctrl+r>", Description: "Remove label from node"},
				{Keys: "<ctrl+o>", Description: "Promote to manager"},
				{Keys: "<ctrl+t>", Description: "Demote to worker"},
				{Keys: "<ctrl+d>", Description: "Remove node"},
				{Keys: "</>", Description: "Filter"},
			},
		},
		{
			Title: "View",
			Items: []helpview.HelpItem{
				{Keys: "<shift+h>", Description: "Order by Hostname"},
				{Keys: "<shift+r>", Description: "Order by Role"},
				{Keys: "<shift+s>", Description: "Order by State"},
				{Keys: "<shift+a>", Description: "Order by Availability"},
				{Keys: "<shift+v>", Description: "Order by Version"},
				{Keys: "<shift+d>", Description: "Order by Address"},
				{Keys: "<shift+l>", Description: "Order by Labels"},
			},
		},
		{
			Title: "Navigation",
			Items: []helpview.HelpItem{
				{Keys: "<↑/↓>", Description: "Navigate"},
				{Keys: "<pgup>", Description: "Page up"},
				{Keys: "<pgdown>", Description: "Page down"},
				{Keys: "<esc>", Description: "Back"},
			},
		},
	}
}

// applySorting applies the current sort configuration to the filtered list
func (m *Model) applySorting() {
	if len(m.List.Filtered) == 0 {
		return
	}

	// Remember cursor position
	cursorID := ""
	if m.List.Cursor < len(m.List.Filtered) {
		cursorID = m.List.Filtered[m.List.Cursor].ID
	}

	// Sort the filtered list
	switch m.sortField {
	case SortByHostname:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].Hostname < m.List.Filtered[j].Hostname
			}
			return m.List.Filtered[i].Hostname > m.List.Filtered[j].Hostname
		})
	case SortByState:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].State < m.List.Filtered[j].State
			}
			return m.List.Filtered[i].State > m.List.Filtered[j].State
		})
	case SortByAvailability:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].Availability < m.List.Filtered[j].Availability
			}
			return m.List.Filtered[i].Availability > m.List.Filtered[j].Availability
		})
	case SortByRole:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			roleI := "worker"
			if m.List.Filtered[i].Manager {
				roleI = "manager"
			}
			roleJ := "worker"
			if m.List.Filtered[j].Manager {
				roleJ = "manager"
			}
			if m.sortAscending {
				return roleI < roleJ
			}
			return roleI > roleJ
		})
	case SortByVersion:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].Version < m.List.Filtered[j].Version
			}
			return m.List.Filtered[i].Version > m.List.Filtered[j].Version
		})
	case SortByAddress:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].Addr < m.List.Filtered[j].Addr
			}
			return m.List.Filtered[i].Addr > m.List.Filtered[j].Addr
		})
	case SortByLabels:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			labelsI := formatLabels(m.List.Filtered[i].Labels)
			labelsJ := formatLabels(m.List.Filtered[j].Labels)
			if m.sortAscending {
				return labelsI < labelsJ
			}
			return labelsI > labelsJ
		})
	}

	// Restore cursor position
	if cursorID != "" {
		for i, n := range m.List.Filtered {
			if n.ID == cursorID {
				m.List.Cursor = i
				return
			}
		}
	}

	m.List.Cursor = 0
	m.List.Viewport.GotoTop()
}
