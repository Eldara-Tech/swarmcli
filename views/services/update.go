// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package servicesview

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
	logsview "github.com/Eldara-Tech/swarmcli/v2/views/logs"
	"github.com/Eldara-Tech/swarmcli/v2/views/scaledialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/taskutil"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/docker/docker/api/types/swarm"
)

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {

	case Msg:
		l().Infof("ServicesView: Received Msg with %d entries", len(msg.Entries))

		// If we're viewing a specific stack or node and all services are gone, show message
		// This handles cases where:
		// - All services in a stack are deleted (stack no longer exists)
		// - All services on a node are removed
		if len(msg.Entries) == 0 && (msg.FilterType == StackFilter || msg.FilterType == NodeFilter) {
			l().Info("ServicesView: No services remaining in filtered view, showing dialog")
			m.pendingAction = "empty-stack"
			m.confirmDialog.Visible = true
			m.confirmDialog.ErrorMode = true
			var target string
			if msg.FilterType == StackFilter {
				target = "Stack"
			} else {
				target = "Node"
			}
			m.confirmDialog.Message = fmt.Sprintf("%s no longer has services, returning to stacks view.\n\nPress Enter to continue.", target)
			// Don't update content or set visible - just show the dialog
			return nil
		}

		// Update the hash with new data
		var err error
		m.lastSnapshot, err = hash.Compute(msg.Entries)
		if err != nil {
			l().Errorf("ServicesView: Error computing hash: %v", err)
			return nil
		}
		m.SetContent(msg)
		m.Visible = true
		// No re-arm: only a tick arms a tick, so a load issued by OnEnter or the
		// factory cannot start a second, parallel chain.
		return m.refreshExpandedTasksCmd(m.expandedServices)

	case TickMsg:
		if msg.Gen != m.pollGen {
			return nil // a leftover from an earlier entry — see OnEnter
		}
		l().Infof("ServicesView: Received TickMsg, visible=%v", m.Visible)
		// Check for changes and refresh expanded tasks
		if m.Visible {
			m.refreshServiceErrorsFromSnapshot()
			if m.ready {
				m.List.Viewport.SetContent(m.List.View())
			}
			return tea.Batch(
				m.checkServicesCmd(m.lastSnapshot, m.filterType, m.nodeID, m.stackName),
				m.refreshExpandedTasksCmd(m.expandedServices),
				tickCmd(m.pollGen),
			)
		}
		// Continue polling even if not visible
		return tickCmd(m.pollGen)

	case PollRetryMsg:
		// Deliberately no re-arm. The TickMsg handler above always schedules
		// the next tick, so re-arming here as well gives one beat two
		// successors — and each of those does the same, so the poll rate does
		// not merely double, it doubles again on every beat.
		return nil

	case TasksLoadedMsg:
		// Store loaded tasks - view will automatically re-render
		m.serviceTasks[msg.ServiceID] = msg.Tasks
		m.setRenderItem()
		return nil

	case AllTasksLoadedMsg:
		for sid, tasks := range msg.Tasks {
			m.serviceTasks[sid] = tasks
		}
		m.setRenderItem()
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
	case scaledialog.ResultMsg:
		m.scaleDialog.Visible = false
		if msg.Confirmed && m.List.Cursor < len(m.List.Filtered) {
			entry := m.List.Filtered[m.List.Cursor]
			l().Infof("Scaling service %s to %d replicas", entry.ServiceName, msg.Replicas)
			serviceOps := m.deps.Services
			snapshotOps := m.deps.Snapshot
			refreshCmd := m.refreshServicesCmd(m.nodeID, m.stackName, m.filterType)
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
				defer cancel()
				if err := serviceOps.ScaleService(ctx, entry.ServiceID, msg.Replicas); err != nil {
					l().Errorf("Failed to scale service %s: %v", entry.ServiceName, err)
					return ScaleErrorMsg{
						ServiceName: entry.ServiceName,
						Error:       err,
					}
				}
				l().Infof("Successfully scaled service %s to %d replicas", entry.ServiceName, msg.Replicas)
				// Force immediate snapshot refresh
				if _, err := snapshotOps.RefreshSnapshot(); err != nil {
					l().Warnf("Failed to refresh snapshot: %v", err)
				}
				return refreshCmd()
			}
		}
		return nil

	case confirmdialog.ResultMsg:
		m.confirmDialog.Visible = false

		if msg.Confirmed && m.List.Cursor < len(m.List.Filtered) {
			entry := m.List.Filtered[m.List.Cursor]
			serviceOps := m.deps.Services
			snapshotOps := m.deps.Snapshot
			refreshCmd := m.refreshServicesCmd(m.nodeID, m.stackName, m.filterType)

			switch m.pendingAction {
			case "remove":
				l().Debugln("Starting remove for", entry.ServiceName)
				return func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
					defer cancel()
					l().Infof("Executing remove for service: %s", entry.ServiceName)
					if err := serviceOps.RemoveService(ctx, entry.ServiceName); err != nil {
						l().Errorf("Failed to remove service %s: %v", entry.ServiceName, err)
						return RemoveErrorMsg{
							ServiceName: entry.ServiceName,
							Error:       err,
						}
					}
					l().Infof("Successfully removed service: %s", entry.ServiceName)
					// Force immediate snapshot refresh
					if _, err := snapshotOps.RefreshSnapshot(); err != nil {
						l().Warnf("Failed to refresh snapshot: %v", err)
					}
					return refreshCmd()
				}
			case "rollback":
				l().Debugln("Starting rollback for", entry.ServiceName)
				return func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
					defer cancel()
					l().Infof("Executing rollback for service: %s", entry.ServiceName)
					if err := serviceOps.RollbackService(ctx, entry.ServiceName); err != nil {
						l().Errorf("Failed to rollback service %s: %v", entry.ServiceName, err)
						return RollbackErrorMsg{
							ServiceName: entry.ServiceName,
							Error:       err,
						}
					}
					l().Infof("Successfully rolled back service: %s", entry.ServiceName)
					// Force immediate snapshot refresh
					if _, err := snapshotOps.RefreshSnapshot(); err != nil {
						l().Warnf("Failed to refresh snapshot: %v", err)
					}
					return refreshCmd()
				}
			default:
				// Default to restart
				l().Debugln("Starting restart for", entry.ServiceName)
				return func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
					defer cancel()
					l().Infof("Executing restart for service: %s", entry.ServiceName)
					if err := serviceOps.RestartService(ctx, entry.ServiceName); err != nil {
						l().Errorf("Failed to restart service %s: %v", entry.ServiceName, err)
						return RestartErrorMsg{
							ServiceName: entry.ServiceName,
							Error:       err,
						}
					}
					l().Infof("Successfully restarted service: %s", entry.ServiceName)
					// Force immediate snapshot refresh
					if _, err := snapshotOps.RefreshSnapshot(); err != nil {
						l().Warnf("Failed to refresh snapshot: %v", err)
					}
					return refreshCmd()
				}
			}
		}

		// Handle empty-stack navigation
		if m.pendingAction == "empty-stack" {
			m.pendingAction = ""
			m.Visible = false
			return func() tea.Msg {
				return view.NavigateToMsg{ViewName: view.NameStacks, Payload: nil, Replace: true}
			}
		}

		m.pendingAction = ""
		return nil

	case RestartErrorMsg:
		// Show error in a confirm dialog (reusing it as an error display)
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to restart %s:\n%v", msg.ServiceName, msg.Error)
		return nil

	case ScaleErrorMsg:
		// Show error in a confirm dialog (reusing it as an error display)
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to scale %s:\n%v", msg.ServiceName, msg.Error)
		return nil

	case RemoveErrorMsg:
		// Show error in a confirm dialog (reusing it as an error display)
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to remove %s:\n%v", msg.ServiceName, msg.Error)
		return nil

	case RollbackErrorMsg:
		// Show error in a confirm dialog (reusing it as an error display)
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to rollback %s:\n%v", msg.ServiceName, msg.Error)
		return nil

	case tea.KeyMsg:
		// If this view is not visible, don't process keys
		// This allows overlaid views (like logs) to handle input
		if !m.Visible {
			return nil
		}

		if m.confirmDialog.Visible {
			return m.confirmDialog.Update(msg)
		}

		if m.scaleDialog.Visible {
			return m.scaleDialog.Update(msg)
		}

		// Handle left/right for column scrolling
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

		// --- normal mode ---
		if msg.Type == tea.KeyEsc && m.List.Query != "" {
			m.List.Query = ""
			m.List.ApplyFilter()
			m.List.Cursor = 0
			m.List.Viewport.GotoTop()
			m.selectedTaskIndex = -1
			return nil
		}

		// Handle task navigation for expanded services
		if m.List.Cursor < len(m.List.Filtered) {
			entry := m.List.Filtered[m.List.Cursor]
			if m.expandedServices[entry.ServiceID] {
				tasks := m.serviceTasks[entry.ServiceID]
				switch msg.String() {
				case "down":
					if m.selectedTaskIndex < len(tasks)-1 {
						// Move down within tasks or from service to first task
						m.selectedTaskIndex++
						m.setRenderItem()
						return nil
					} else if m.selectedTaskIndex == len(tasks)-1 {
						// At last task, move to next service
						m.selectedTaskIndex = -1
						m.List.HandleKey(msg)
						return nil
					}
					// If selectedTaskIndex == -1, fall through to normal handling
				case "up":
					if m.selectedTaskIndex > 0 {
						// Move up within tasks
						m.selectedTaskIndex--
						m.setRenderItem()
						return nil
					} else if m.selectedTaskIndex == 0 {
						// At first task, move back to service row
						m.selectedTaskIndex = -1
						m.setRenderItem()
						return nil
					}
					// If selectedTaskIndex == -1, fall through to normal handling
				}
			} else if msg.String() == "up" && m.selectedTaskIndex == -1 && m.List.Cursor > 0 {
				// At a service row, check if previous service has expanded tasks
				prevEntry := m.List.Filtered[m.List.Cursor-1]
				if m.expandedServices[prevEntry.ServiceID] {
					prevTasks := m.serviceTasks[prevEntry.ServiceID]
					if len(prevTasks) > 0 {
						// Move to last task of previous service
						m.List.Cursor--
						m.selectedTaskIndex = len(prevTasks) - 1
						m.setRenderItem()
						return nil
					}
				}
			}
		}

		// Store old cursor to detect changes
		oldCursor := m.List.Cursor
		m.List.HandleKey(msg) // handle up/down/pgup/pgdown

		// Reset task selection when moving to different service
		if oldCursor != m.List.Cursor {
			m.selectedTaskIndex = -1
			// Reset horizontal scroll when moving cursor
			m.List.ResetColumnScroll()
		}

		switch msg.String() {
		case "s":
			if m.List.Cursor < len(m.List.Filtered) {
				entry := m.List.Filtered[m.List.Cursor]
				m.scaleDialog.Show(entry.ServiceName, uint64(entry.ReplicasTotal))
			}
		case "p":
			// Toggle tasks expansion for selected service
			if m.List.Cursor < len(m.List.Filtered) {
				entry := m.List.Filtered[m.List.Cursor]
				// Toggle expansion state
				m.expandedServices[entry.ServiceID] = !m.expandedServices[entry.ServiceID]

				// If expanding, fetch tasks
				if m.expandedServices[entry.ServiceID] {
					taskOps := m.deps.Tasks
					return func() tea.Msg {
						tasks, err := taskOps.GetTasksForService(entry.ServiceID)
						if err != nil {
							l().Errorf("Failed to fetch tasks for service %s: %v", entry.ServiceName, err)
							// Still toggle to show empty state
							tasks = []docker.TaskEntry{}
						}
						return TasksLoadedMsg{
							ServiceID: entry.ServiceID,
							Tasks:     tasks,
						}
					}
				} else {
					// Collapsing - remove cached tasks and let view re-render
					delete(m.serviceTasks, entry.ServiceID)
					m.selectedTaskIndex = -1
					m.setRenderItem()
				}
			}
		case "i":
			if m.List.Cursor < len(m.List.Filtered) {
				entry := m.List.Filtered[m.List.Cursor]
				inspectOps := m.deps.Inspect
				return func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
					defer cancel()
					content, err := inspectOps.Inspect(ctx, docker.InspectService, entry.ServiceID)
					if err != nil {
						content = fmt.Sprintf("Error inspecting service %q: %v", entry.ServiceName, err)
					}
					return view.NavigateToMsg{
						ViewName: inspectview.ViewName,
						Payload: map[string]interface{}{
							"title": fmt.Sprintf("Service: %s", entry.ServiceName),
							"json":  content,
						},
					}
				}
			}
		case "r":
			if m.List.Cursor < len(m.List.Filtered) {
				entry := m.List.Filtered[m.List.Cursor]
				m.pendingAction = "restart"
				m.confirmDialog.Visible = true
				m.confirmDialog.ErrorMode = false
				m.confirmDialog.Message = fmt.Sprintf("Restart service %q?", entry.ServiceName)
			}
		case "ctrl+d":
			if m.List.Cursor < len(m.List.Filtered) {
				entry := m.List.Filtered[m.List.Cursor]
				m.pendingAction = "remove"
				m.confirmDialog.Visible = true
				m.confirmDialog.ErrorMode = false
				m.confirmDialog.Message = fmt.Sprintf("Remove service %q?\n\nThis action cannot be undone!", entry.ServiceName)
			}
		case "ctrl+r":
			if m.List.Cursor < len(m.List.Filtered) {
				entry := m.List.Filtered[m.List.Cursor]
				m.pendingAction = "rollback"
				m.confirmDialog.Visible = true
				m.confirmDialog.ErrorMode = false
				m.confirmDialog.Message = fmt.Sprintf("Rollback service %q to previous configuration?", entry.ServiceName)
			}
		case "l":
			if m.List.Cursor < len(m.List.Filtered) {
				entry := m.List.Filtered[m.List.Cursor]
				return func() tea.Msg {
					return view.NavigateToMsg{
						Payload:  entry,
						ViewName: logsview.ViewName,
					}
				}
			}
		case "x":
			if m.List.Cursor >= len(m.List.Filtered) {
				return nil
			}
			entry := m.List.Filtered[m.List.Cursor]
			return m.dispatchAction("shell", "Shell", entry.ServiceName)
		case "w":
			if m.List.Cursor >= len(m.List.Filtered) {
				return nil
			}
			entry := m.List.Filtered[m.List.Cursor]
			return m.dispatchAction("port-forwards", "Active Forwards", entry.ServiceName)
		case "W":
			if m.List.Cursor >= len(m.List.Filtered) {
				return nil
			}
			entry := m.List.Filtered[m.List.Cursor]
			return m.dispatchAction("port-forward", "Port Forward", entry.ServiceName)
		// t opens live resource statistics for the selection. The ref carries the
		// service name and, when a task row is highlighted, that replica's task
		// ID; an empty second field means "the service — pick a replica". The
		// Swarm API reports no container CPU, memory, network or block-IO at all
		// (those counters exist only on the node running the container), so the
		// default build supplies none and the key stays inert unless an extension
		// that can reach per-node container state registers the action.
		case "t":
			if m.List.Cursor >= len(m.List.Filtered) {
				return nil
			}
			entry := m.List.Filtered[m.List.Cursor]
			return m.dispatchAction("container-stats", "Stats",
				view.EncodeRef(entry.ServiceName, m.selectedTaskID()))
		case "esc":
			// ESC should also go back to stacks view
			m.Visible = false
			return func() tea.Msg { return view.NavigateToMsg{ViewName: view.NameStacks, Payload: nil} }

		// Sort by Name (Shift+N)
		case "N":
			if m.sortField == SortByName {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByName
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by Status (Shift+S)
		case "S":
			if m.sortField == SortByStatus {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByStatus
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by Image (Shift+I)
		case "I":
			if m.sortField == SortByImage {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByImage
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by Ports (Shift+P)
		case "P":
			if m.sortField == SortByPorts {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByPorts
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by Created (Shift+C)
		case "C":
			if m.sortField == SortByCreated {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByCreated
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by Updated (Shift+U)
		case "U":
			if m.sortField == SortByUpdated {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByUpdated
				m.sortAscending = true
			}
			m.applySorting()
			return nil

		// Sort by Error (Shift+R)
		case "R":
			if m.sortField == SortByError {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByError
				m.sortAscending = true
			}
			m.applySorting()
			return nil
		}

		m.List.Viewport.SetContent(m.List.View())
		return nil
	}

	var cmd tea.Cmd
	m.List.Viewport, cmd = m.List.Viewport.Update(msg)
	return cmd
}

// dispatchAction invokes a registered action, or surfaces the standard
// "Business Edition feature" dialog when it is not available. The action
// keybindings are inert in builds that do not register them, and an
// unlicensed build takes this same path because the registry's guard is
// evaluated on every lookup.
func (m *Model) dispatchAction(actionName, label, arg string) tea.Cmd {
	action, ok := view.GetAction(actionName)
	if !ok {
		if cmd := view.FeatureLockedCmd(label); cmd != nil {
			return cmd
		}
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = view.BEUnavailableErr(label).Error()
		return nil
	}
	return action(arg)
}

// selectedTaskID returns the ID of the highlighted task row, or "" when the
// cursor is on the service row itself. An action that can act on either gets
// both: the service name always, and one replica only when the user picked one.
func (m *Model) selectedTaskID() string {
	if m.selectedTaskIndex < 0 || m.List.Cursor >= len(m.List.Filtered) {
		return ""
	}
	tasks := m.serviceTasks[m.List.Filtered[m.List.Cursor].ServiceID]
	if m.selectedTaskIndex >= len(tasks) {
		return ""
	}
	return tasks[m.selectedTaskIndex].ID
}

func (m *Model) SetContent(msg Msg) {
	l().Infof("ServicesView.SetContent: Updating display with %d services", len(msg.Entries))

	m.titleScope = msg.Scope

	m.List.Items = msg.Entries
	m.List.ApplyFilter()

	// Compute per-service error summary from cached snapshot so errors appear
	// without expanding tasks.
	m.refreshServiceErrorsFromSnapshot()

	// Reapply sorting to maintain sort order after data refresh
	m.applySorting()

	// If we were navigated here with a target service to select, select it now
	// (after filtering/sorting so indices match what the user sees).
	if m.pendingSelectServiceName != "" {
		for i := range m.List.Filtered {
			if m.List.Filtered[i].ServiceName == m.pendingSelectServiceName {
				m.List.Cursor = i
				break
			}
		}
		m.pendingSelectServiceName = ""
	}

	m.filterType = msg.FilterType
	m.nodeID = msg.NodeID
	m.stackName = msg.StackName

	// Rebuild the columns to match the active set for this scope (STACK is
	// dropped outside AllFilter). Widths, header, sort indices, and rows all
	// derive from the same set so they stay consistent.
	cols := m.layoutColumns()
	m.List.Columns = cols
	if m.List.Header != nil {
		m.List.Header.Columns = filterlist.ColumnDefs(cols)
	}

	m.setRenderItem()

	if m.ready {
		m.List.Viewport.SetContent(m.List.View())
		// Preserve cursor position on refresh, don't call GotoTop
	}
}

func (m *Model) refreshServiceErrorsFromSnapshot() {
	for k := range m.serviceHasError {
		delete(m.serviceHasError, k)
	}
	for k := range m.serviceErrorText {
		delete(m.serviceErrorText, k)
	}

	snap := m.deps.Snapshot.GetSnapshot()
	if snap == nil {
		return
	}

	svcDesired := make(map[string]int)
	svcRunning := make(map[string]int)
	for _, svc := range snap.Services {
		// Shared with the loaders rather than duplicated: counting every node as
		// a global service's target flagged a fully healthy service as erroring
		// for as long as one node stayed drained or down (issue #480).
		svcDesired[svc.ID] = snap.DesiredReplicas(svc)
		// Initialize svcRunning with 0 for all services
		svcRunning[svc.ID] = 0
	}

	latestTasks := taskutil.LatestTasksByServiceKey(snap.Tasks)

	for _, t := range latestTasks {
		if t.DesiredState == swarm.TaskStateRunning && t.Status.State == swarm.TaskStateRunning {
			svcRunning[t.ServiceID]++
		}
	}

	for _, t := range latestTasks {
		desired := svcDesired[t.ServiceID]
		running := svcRunning[t.ServiceID]
		if desired == 0 || running >= desired {
			continue
		}
		if t.DesiredState == swarm.TaskStateShutdown && t.Status.State == swarm.TaskStateComplete {
			continue
		}
		hasError := false
		// Only count explicit errors: Status.Err or Failed/Rejected states
		if t.Status.Err != "" {
			hasError = true
		} else if t.Status.State == swarm.TaskStateFailed || t.Status.State == swarm.TaskStateRejected {
			hasError = true
		}
		if !hasError {
			continue
		}
		m.serviceHasError[t.ServiceID] = true
		if m.serviceErrorText[t.ServiceID] == "" {
			m.serviceErrorText[t.ServiceID] = t.Status.Err
		}
	}

	// If service is under-replicated with no explicit error from latest task,
	// check recent tasks for the most recent error
	for serviceID, running := range svcRunning {
		desired := svcDesired[serviceID]
		if desired > 0 && running < desired && m.serviceErrorText[serviceID] == "" {
			// Find most recent task timestamp for this service
			var newestTaskTime time.Time
			for _, t := range snap.Tasks {
				if t.ServiceID != serviceID {
					continue
				}
				at := t.Status.Timestamp
				if at.IsZero() {
					at = t.CreatedAt
				}
				if newestTaskTime.IsZero() || at.After(newestTaskTime) {
					newestTaskTime = at
				}
			}

			// Only check tasks within 5 minutes of the newest task
			cutoff := newestTaskTime.Add(-5 * time.Minute)

			// Find most recent task with an actual error (not just non-running)
			var mostRecentErr string
			var mostRecentErrTime time.Time
			for _, t := range snap.Tasks {
				if t.ServiceID != serviceID {
					continue
				}
				if t.Status.Err == "" {
					continue
				}
				at := t.Status.Timestamp
				if at.IsZero() {
					at = t.CreatedAt
				}
				if at.Before(cutoff) {
					continue
				}
				if mostRecentErr == "" || at.After(mostRecentErrTime) {
					mostRecentErr = t.Status.Err
					mostRecentErrTime = at
				}
			}
			if mostRecentErr != "" {
				m.serviceHasError[serviceID] = true
				m.serviceErrorText[serviceID] = mostRecentErr
			}
		}
	}

	// Also detect active deployment failures: slots where the newest error task
	// is more recent than the newest running task, even when running >= desired.
	// This catches a failed rolling update where old tasks are still running.
	for svcID, errMsg := range taskutil.ActiveDeploymentErrorsByService(snap.Tasks) {
		if !m.serviceHasError[svcID] {
			m.serviceHasError[svcID] = true
			m.serviceErrorText[svcID] = errMsg
		}
	}
}

func (m *Model) setRenderItem() {
	// The shared layout computes widths and the base row text; this view keeps
	// its own decoration (error coloring, full-width highlight, task sub-rows).
	m.List.RenderItem = func(e docker.ServiceEntry, selected bool, _ int) string {
		rowText := m.List.RenderRow(e, selected)

		itemStyle := ui.ListItemStyle

		var lineStr string
		if selected && m.selectedTaskIndex == -1 {
			// Only highlight service row if no task is selected
			selBase := ui.ListSelectedStyle
			lineStr = selBase.Render(rowText)

			// Ensure highlight background fills the full viewport width
			if m.List.Viewport.Width > 0 {
				w := lipgloss.Width(lineStr)
				if m.List.Viewport.Width > w {
					pad := m.List.Viewport.Width - w
					lineStr += selBase.Render(strings.Repeat(" ", pad))
				}
			}
		} else if m.serviceHasError[e.ServiceID] {
			// Color non-selected error rows red
			errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
			lineStr = errStyle.Render(rowText)
		} else {
			// Normal rendering
			lineStr = itemStyle.Render(rowText)
		}

		// Check if service is expanded and add task rows
		if m.expandedServices[e.ServiceID] {
			tasks := m.serviceTasks[e.ServiceID]
			if len(tasks) > 0 {
				// HEALTH and PORTS are per-container data the swarm API does not
				// expose; they are populated by a TaskOps decorator and stay
				// empty otherwise, so only show those columns when present.
				// HEALTH is gated on the cell taskHealthCell would render rather
				// than on the raw fields, so a service whose tasks are all over
				// drops the column instead of showing a dash on every row.
				// IMAGE earns its width only while the replicas disagree about it:
				// that is a rollout, and it is the column that says which row is
				// the outgoing generation. In a settled service every row would
				// repeat the image already on the service row above.
				show := taskRowColumns{}
				for _, t := range tasks {
					if taskHealthCell(t) != "" {
						show.health = true
					}
					if t.Ports != "" {
						show.ports = true
					}
					if t.Image != tasks[0].Image {
						show.image = true
					}
				}

				// Add task header (include ERROR column)
				taskHeaderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
				// Align header columns with task row formatting
				taskHeader := formatTaskRow(taskRow{
					replica: "REPLICA", image: "IMAGE", node: "NODE",
					desired: "DESIRED STATE", current: "CURRENT STATE",
					health: "HEALTH", ports: "PORTS", errText: "ERROR",
				}, show)
				lineStr += "\n" + taskHeaderStyle.Render(taskHeader)

				// Add each task as a row
				for taskIdx, task := range tasks {
					rowText := formatTaskRow(taskRow{
						replica: replicaCell(task),
						image:   filterlist.TruncateRunes(shortImage(task.Image), 20),
						node:    filterlist.TruncateRunes(task.NodeName, 12),
						desired: filterlist.TruncateRunes(task.DesiredState, 13),
						current: filterlist.TruncateRunes(task.StatusText(), 40),
						health:  dashIfEmpty(filterlist.TruncateRunes(taskHealthCell(task), 9)),
						ports:   dashIfEmpty(filterlist.TruncateRunes(task.Ports, 20)),
						errText: filterlist.TruncateRunes(task.Error, 30),
					}, show)

					// Check if this task is selected
					taskSelected := selected && m.selectedTaskIndex == taskIdx
					var taskLine string
					if taskSelected {
						// Lighter highlight for task rows
						taskSelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")).Bold(true)
						taskLine = taskSelStyle.Render(rowText)
						// Pad task highlight to full width
						if m.List.Viewport.Width > 0 {
							tw := lipgloss.Width(taskLine)
							if m.List.Viewport.Width > tw {
								pad := m.List.Viewport.Width - tw
								taskLine += taskSelStyle.Render(strings.Repeat(" ", pad))
							}
						}
					} else {
						// Tint the whole row by what the task is doing, so a
						// failing replica stands out from one swarm retired on
						// purpose, mirroring the service-level error coloring
						// above.
						taskLine = taskutil.TaskRowStyle(task, taskRowBaseStyle).Render(rowText)
					}
					lineStr += "\n" + taskLine
				}
			} else {
				// Show "no tasks" message
				noTasksStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
				lineStr += "\n" + noTasksStyle.Render("   (no tasks)")
			}
		}

		return lineStr
	}
}

// taskRow is one expanded task sub-row's cells, or the header's labels.
type taskRow struct {
	replica, image, node, desired, current, health, ports, errText string
}

// taskRowColumns says which optional columns the caller found data for.
type taskRowColumns struct {
	image, health, ports bool
}

// formatTaskRow lays out one expanded task sub-row (or the header when passed
// labels). The IMAGE, HEALTH and PORTS columns are only emitted when the caller
// found data worth showing, so a settled service renders no wider than before.
func formatTaskRow(r taskRow, show taskRowColumns) string {
	s := fmt.Sprintf("   %-7s", r.replica)
	if show.image {
		s += fmt.Sprintf("  %-20s", r.image)
	}
	s += fmt.Sprintf("  %-12s  %-13s  %-40s", r.node, r.desired, r.current)
	if show.health {
		s += fmt.Sprintf("  %-9s", r.health)
	}
	if show.ports {
		s += fmt.Sprintf("  %-20s", r.ports)
	}
	return s + "  " + r.errText
}

// replicaCell identifies which replica a task belongs to. A global service's
// tasks carry no slot; the NODE column is what distinguishes them there.
func replicaCell(t docker.TaskEntry) string {
	if t.Slot <= 0 {
		return "—"
	}
	return strconv.Itoa(t.Slot)
}

// shortImage drops the registry and repository path, keeping the last segment
// and its tag — the part that differs between two generations of one service.
// The service row above already carries the full reference.
func shortImage(image string) string {
	if i := strings.LastIndex(image, "/"); i >= 0 {
		return image[i+1:]
	}
	return image
}

// taskRowBaseStyle is an expanded task sub-row with nothing wrong with it; the
// tints for the rest come from taskutil.TaskRowStyle.
var taskRowBaseStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))

// dashIfEmpty renders an em dash for empty cells so aligned columns stay legible.
func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// taskHealthCell is what the HEALTH column has to say about one task: the
// healthcheck verdict when the decorator recorded one, else the container's live
// docker-ps state, so a container error surfaces even for an image that declares
// no HEALTHCHECK. That fallback stops once the swarm task state is terminal. The
// container has of course exited by then, and whether the cell can say so turns
// only on whether the node has pruned it out of the decorator's inventory yet —
// so it advertises a difference between rows where there is none between tasks
// (issue #616). CURRENT STATE already carries how the task ended, and ERROR
// carries why.
func taskHealthCell(t docker.TaskEntry) string {
	if taskutil.IsTerminal(t.State) {
		return t.Health
	}
	return firstNonEmpty(t.Health, t.ContainerState)
}

// firstNonEmpty returns the first non-empty string, used to fall back from the
// healthcheck token to the container's live state in the HEALTH cell.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// formatRelativeTime formats a time as a relative duration (e.g., "2h ago", "3d ago")
func formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}

	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	} else if d < time.Hour {
		mins := int(d.Minutes())
		return fmt.Sprintf("%dm ago", mins)
	} else if d < 24*time.Hour {
		hours := int(d.Hours())
		return fmt.Sprintf("%dh ago", hours)
	} else if d < 7*24*time.Hour {
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	} else if d < 30*24*time.Hour {
		weeks := int(d.Hours() / 24 / 7)
		return fmt.Sprintf("%dw ago", weeks)
	} else if d < 365*24*time.Hour {
		months := int(d.Hours() / 24 / 30)
		return fmt.Sprintf("%dmo ago", months)
	} else {
		years := int(d.Hours() / 24 / 365)
		return fmt.Sprintf("%dy ago", years)
	}
}

// HelpContent implements the app's optional help-screen contract: "?" is
// handled centrally, and a view carrying its own screen supplies it here.
func (m *Model) HelpContent() []helpview.HelpCategory { return GetServicesHelpContent() }

// GetServicesHelpContent returns categorized help for the services view
func GetServicesHelpContent() []helpview.HelpCategory {
	return []helpview.HelpCategory{
		{
			Title: "General",
			Items: []helpview.HelpItem{
				{Keys: "<i>", Description: "Inspect service"},
				{Keys: "<p>", Description: "Show/hide tasks"},
				{Keys: "<l>", Description: "View logs"},
				{Keys: "<s>", Description: "Scale service"},
				{Keys: "<r>", Description: "Restart service"},
				{Keys: "<ctrl+r>", Description: "Rollback service"},
				{Keys: "<ctrl+d>", Description: "Remove service"},
				{Keys: "<x>", Description: view.BEHelpDesc("shell", "Open shell into service container")},
				{Keys: "<w>", Description: view.BEHelpDesc("port-forwards", "Show active port-forwards")},
				{Keys: "<shift+w>", Description: view.BEHelpDesc("port-forward", "Forward service ports to localhost")},
				{Keys: "<t>", Description: view.BEHelpDesc("container-stats", "CPU, memory, network and I/O over time")},
				{Keys: "</>", Description: "Filter"},
			},
		},
		{
			Title: "View",
			Items: []helpview.HelpItem{
				{Keys: "<shift+n>", Description: "Order by Name"},
				{Keys: "<shift+s>", Description: "Order by Status"},
				{Keys: "<shift+i>", Description: "Order by Image"},
				{Keys: "<shift+p>", Description: "Order by Ports"},
				{Keys: "<shift+c>", Description: "Order by Created"},
				{Keys: "<shift+u>", Description: "Order by Updated"},
				{Keys: "<shift+r>", Description: "Order by Error"},
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
	cursorService := ""
	if m.List.Cursor < len(m.List.Filtered) {
		cursorService = m.List.Filtered[m.List.Cursor].ServiceName
	}

	// Sort the filtered list
	switch m.sortField {
	case SortByName:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].ServiceName < m.List.Filtered[j].ServiceName
			}
			return m.List.Filtered[i].ServiceName > m.List.Filtered[j].ServiceName
		})
	case SortByStatus:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].Status < m.List.Filtered[j].Status
			}
			return m.List.Filtered[i].Status > m.List.Filtered[j].Status
		})
	case SortByImage:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].Image < m.List.Filtered[j].Image
			}
			return m.List.Filtered[i].Image > m.List.Filtered[j].Image
		})
	case SortByPorts:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].Ports < m.List.Filtered[j].Ports
			}
			return m.List.Filtered[i].Ports > m.List.Filtered[j].Ports
		})
	case SortByCreated:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].CreatedAt.Before(m.List.Filtered[j].CreatedAt)
			}
			return m.List.Filtered[i].CreatedAt.After(m.List.Filtered[j].CreatedAt)
		})
	case SortByUpdated:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].UpdatedAt.Before(m.List.Filtered[j].UpdatedAt)
			}
			return m.List.Filtered[i].UpdatedAt.After(m.List.Filtered[j].UpdatedAt)
		})
	case SortByError:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			iHas := m.serviceHasError[m.List.Filtered[i].ServiceID]
			jHas := m.serviceHasError[m.List.Filtered[j].ServiceID]
			if iHas == jHas {
				iText := m.serviceErrorText[m.List.Filtered[i].ServiceID]
				jText := m.serviceErrorText[m.List.Filtered[j].ServiceID]
				if iText == jText {
					if m.sortAscending {
						return m.List.Filtered[i].ServiceName < m.List.Filtered[j].ServiceName
					}
					return m.List.Filtered[i].ServiceName > m.List.Filtered[j].ServiceName
				}
				if m.sortAscending {
					return iText < jText
				}
				return iText > jText
			}
			if m.sortAscending {
				return iHas && !jHas
			}
			return !iHas && jHas
		})
	}

	// Restore cursor position to previously selected item
	if cursorService != "" {
		for i, s := range m.List.Filtered {
			if s.ServiceName == cursorService {
				m.List.Cursor = i
				return
			}
		}
	}

	// If previous item not found, reset cursor to 0
	m.List.Cursor = 0
	m.List.Viewport.GotoTop()
}
