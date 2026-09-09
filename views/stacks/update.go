// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"context"
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/core/primitives/hash"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	"github.com/Eldara-Tech/swarmcli/v2/ui/dialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	helpview "github.com/Eldara-Tech/swarmcli/v2/views/help"
	servicesview "github.com/Eldara-Tech/swarmcli/v2/views/services"
	"github.com/Eldara-Tech/swarmcli/v2/views/taskutil"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/docker/docker/api/types/swarm"
)

const userActionTimeout = 15 * time.Second

// saveDirSentinel is a special file browser entry that selects the current directory.
const saveDirSentinel = "[Save here]"

// Update handles all messages for the stacks view.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {

	case Msg:
		l().Infof("[update]: Received Msg with %d entries", len(msg.Stacks))
		// Update the hash with new data
		var err error
		m.lastSnapshot, err = hash.Compute(msg.Stacks)
		if err != nil {
			l().Errorf("[update] Error computing hash: %v", err)
			return nil
		}
		m.nodeID = msg.NodeID
		m.setStacks(msg.Stacks)
		m.Visible = true

		// Start background fetches for stacks to surface errors without pressing 'p'.
		// Create commands that will load tasks for each stack asynchronously.
		taskOps := m.deps.Tasks
		var cmds []tea.Cmd
		// No re-arm: only a tick arms a tick, so a load issued by OnEnter or the
		// factory cannot start a second, parallel chain.

		for _, s := range msg.Stacks {
			stackName := s.Name
			// If we already have tasks cached, skip
			if _, ok := m.stackTasks[stackName]; ok {
				continue
			}
			// Launch async fetch for this stack
			cmds = append(cmds, func(name string) tea.Cmd {
				return func() tea.Msg {
					tasks, err := taskOps.GetTasksForStack(name)
					return StackTasksLoadedMsg{StackName: name, Tasks: tasks, Error: err}
				}
			}(stackName))
		}
		return tea.Batch(cmds...)

	case TickMsg:
		if msg.Gen != m.pollGen {
			return nil // a leftover from an earlier entry — see OnEnter
		}
		l().Infof("StacksView: Received TickMsg, visible=%v", m.Visible)
		// Check for changes (this will return either a Msg or PollRetryMsg)
		if m.Visible {
			return tea.Batch(
				m.checkStacksCmd(m.lastSnapshot, m.nodeID),
				m.refreshExpandedStackTasksCmd(m.expandedStacks),
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

	case SpinnerTickMsg:
		m.spinner++
		if m.animating() {
			return m.spinnerTickCmd()
		}
		return nil

	case stackDeployedMsg:
		m.endDeploy()
		m.showToast(fmt.Sprintf("✓ Stack %q deployed — services updating", msg.StackName))
		// Keep ticking through the toast window so it clears on time rather than
		// lingering until the next 5s poll.
		return tea.Batch(m.LoadStacksCmd(m.nodeID), m.spinnerTickCmd())

	case RefreshErrorMsg:
		m.Visible = true
		m.List.Viewport.SetContent(fmt.Sprintf("Error refreshing stacks: %v", msg.Err))
		return nil

	case StackTasksLoadedMsg:
		// Store loaded tasks for the stack and re-render
		if msg.Error != nil {
			l().Errorf("Failed to fetch tasks for stack %s: %v", msg.StackName, msg.Error)
			m.stackTasks[msg.StackName] = []docker.TaskEntry{}
		} else {
			m.stackTasks[msg.StackName] = msg.Tasks
		}
		// If the stack is expanded and currently selected, default into first task
		if m.expandedStacks[msg.StackName] {
			// If the currently focused item is this stack and we have tasks, select first
			if m.List.Cursor < len(m.List.Filtered) && m.List.Filtered[m.List.Cursor].Name == msg.StackName {
				if len(m.stackTasks[msg.StackName]) > 0 {
					m.selectedTaskIndex = 0
				}
			}
		}
		m.setRenderItem()
		return nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.List.Viewport.Width = msg.Width
		m.List.Viewport.Height = msg.Height
		m.List.SetOuterSize(msg.Width, msg.Height)
		m.ready = true

		// On first resize (initialization), always reset YOffset to 0
		// This fixes the issue where the view is created with small dimensions,
		// then resized, causing YOffset to be incorrectly set
		if m.firstResize {
			m.List.Viewport.YOffset = 0
			m.firstResize = false
			l().Info("First WindowSizeMsg: resetting YOffset to 0")
		} else if m.List.Cursor == 0 {
			// On subsequent resizes, only reset YOffset if cursor is at top
			m.List.Viewport.YOffset = 0
		}
		return nil

	case confirmdialog.ResultMsg:
		m.confirmDialog.Visible = false
		m.confirmDialog.CheckboxLabel = "" // Clear checkbox for next use
		m.confirmDialog.InfoMode = false

		if m.pendingAction == "save-overwrite" {
			if msg.Confirmed {
				filePath := m.saveFileInput.Value()
				if !filepath.IsAbs(filePath) {
					if abs, err := filepath.Abs(filePath); err == nil {
						filePath = abs
					}
				}
				m.pendingAction = ""
				return m.saveStackToFileCmd(m.saveStackName, filePath)
			}
			// Cancelled — return to save dialog
			m.pendingAction = ""
			m.saveDialogActive = true
			m.saveFileInput.CursorEnd()
			m.saveFileInput.Focus()
			return nil
		}

		if msg.Confirmed && m.List.Cursor < len(m.List.Filtered) {
			selected := m.List.Filtered[m.List.Cursor]

			if m.pendingAction == "remove" {
				l().Debugln("Starting remove for stack", selected.Name)
				removeNetworks := msg.CheckboxChecked // Capture checkbox state
				stackOps := m.deps.Stacks
				snapOps := m.deps.Snapshot
				checkCmd := m.checkStacksCmd(m.lastSnapshot, m.nodeID)
				return func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
					defer cancel()
					l().Infof("Executing remove for stack: %s (remove networks: %v)", selected.Name, removeNetworks)
					if err := stackOps.RemoveStack(ctx, selected.Name); err != nil {
						l().Errorf("Failed to remove stack %s: %v", selected.Name, err)
						return RemoveErrorMsg{
							StackName: selected.Name,
							Error:     err,
						}
					}
					l().Infof("Successfully removed stack: %s", selected.Name)

					// Remove networks if checkbox was checked
					if removeNetworks {
						l().Infof("Removing networks for stack: %s", selected.Name)
						if err := stackOps.RemoveStackNetworks(ctx, selected.Name); err != nil {
							l().Warnf("Failed to remove networks for stack %s: %v", selected.Name, err)
							// Don't fail the whole operation if network removal fails
						}
					}

					// Force immediate snapshot refresh
					if _, err := snapOps.RefreshSnapshot(); err != nil {
						l().Warnf("Failed to refresh snapshot: %v", err)
					}
					return checkCmd()
				}
			}
		}
		m.pendingAction = ""
		return nil

	case RemoveErrorMsg:
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to remove stack %q:\n%v", msg.StackName, msg.Error)
		return nil

	case StackDeleteIntentMsg:
		m.pendingAction = "remove"
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = false
		if msg.ChartRelease != "" {
			m.confirmDialog.Message = fmt.Sprintf("Stack %q belongs to chart release %q.\n\nRemoving it here may corrupt the release — prefer `charts uninstall`.\nThis removes all services in the stack and cannot be undone!", msg.StackName, msg.ChartRelease)
		} else {
			m.confirmDialog.Message = fmt.Sprintf("Remove stack %q?\n\nThis will remove all services in the stack.\nThis action cannot be undone!", msg.StackName)
		}
		m.confirmDialog.CheckboxLabel = "Also remove associated networks"
		m.confirmDialog.CheckboxChecked = true // Checked by default
		return nil

	case ChartJumpMsg:
		if msg.ChartRelease == "" {
			m.showToast(fmt.Sprintf("Stack %q is not managed by a chart release", msg.StackName))
			return nil
		}
		release := msg.ChartRelease
		return func() tea.Msg {
			return view.NavigateToMsg{
				ViewName: view.NameCharts,
				Payload:  map[string]any{"release": release},
			}
		}

	case editorContentMsg:
		preview := msg.Content
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		l().Infof("Editor content received: %d bytes, preview: %q", len(msg.Content), preview)

		// Check if we're editing an existing stack or creating new
		if m.editStackName != "" {
			// Edit mode: redeploy the stack with updated YAML
			stackName := m.editStackName
			m.editStackName = "" // Clear edit mode

			if msg.Content == msg.OriginalContent {
				l().Infof("No changes to stack %s, skipping redeploy", stackName)
				return nil
			}

			stackOps := m.deps.Stacks
			snapOps := m.deps.Snapshot
			return tea.Batch(m.beginDeploy(stackName), func() tea.Msg {
				l().Infof("Redeploying edited stack: %s", stackName)
				err := stackOps.DeployStack(stackName, msg.Content)
				if err != nil {
					l().Errorf("Failed to redeploy stack %s: %v", stackName, err)
					return stackUpdateErrorMsg{StackName: stackName, Err: err}
				}
				l().Infof("Successfully redeployed stack: %s", stackName)
				// Force snapshot refresh
				if _, err := snapOps.RefreshSnapshot(); err != nil {
					l().Warnf("Failed to refresh snapshot: %v", err)
				}
				return stackDeployedMsg{StackName: stackName}
			})
		}

		// Create mode: show create dialog with content
		m.createDialogContent = msg.Content
		l().Infof("Updated m.createDialogContent, now: %d bytes", len(m.createDialogContent))
		m.createDialogError = "" // Clear any previous error
		// Return to create dialog with inline content
		m.createDialogActive = true
		m.createDialogStep = "details-inline"
		m.createInputFocus = 0
		m.createNameInput.Focus()
		return nil

	case stackUpdateErrorMsg:
		l().Errorf("Error updating stack %s: %v", msg.StackName, msg.Err)
		m.endDeploy()
		m.confirmDialog.Visible = true
		m.confirmDialog.ErrorMode = true
		m.confirmDialog.Message = fmt.Sprintf("Failed to update stack %q:\n%v", msg.StackName, msg.Err)
		return nil

	case stackCreateErrorMsg:
		l().Errorf("Error deploying stack: %v", msg.Err)
		m.endDeploy()
		// Return to create dialog with error message
		// Determine which step to return to based on available data
		if m.createFileInput.Value() != "" {
			m.createDialogStep = "details-file"
			m.createInputFocus = 1
			m.createFileInput.Focus()
		} else {
			m.createDialogStep = "details-inline"
			m.createInputFocus = 0
			m.createNameInput.Focus()
		}
		m.createDialogActive = true
		m.createDialogError = msg.Err.Error()
		m.fileBrowserActive = false
		return nil

	case stackSavedMsg:
		m.saveDialogActive = false
		m.saveDialogError = ""
		m.saveFileInput.Blur()
		m.confirmDialog.Visible = true
		m.confirmDialog.InfoMode = true
		m.confirmDialog.Message = fmt.Sprintf("Stack YAML saved to:\n%s", msg.Path)
		return nil

	case stackSaveErrorMsg:
		m.saveDialogActive = true
		m.saveDialogError = msg.Err.Error()
		m.saveFileInput.CursorEnd()
		m.saveFileInput.Focus()
		return nil

	case filesLoadedMsg:
		if msg.Error != nil {
			l().Errorf("Error loading files: %v", msg.Error)
			m.fileBrowserActive = false
			if m.fileBrowserContext == "save" {
				m.saveDialogActive = true
				m.saveDialogError = fmt.Sprintf("Failed to load directory: %v", msg.Error)
			} else {
				m.createDialogActive = true
				m.createDialogError = fmt.Sprintf("Failed to load directory: %v", msg.Error)
			}
			return nil
		}
		m.fileBrowserPath = msg.Path
		m.fileBrowserFiles = msg.Files
		m.fileBrowserCursor = 0
		// In save context, inject "[Save here]" entry after ".."
		if m.fileBrowserContext == "save" {
			idx := 0
			if len(m.fileBrowserFiles) > 0 && m.fileBrowserFiles[0] == ".." {
				idx = 1
			}
			m.fileBrowserFiles = slices.Insert(m.fileBrowserFiles, idx, saveDirSentinel)
		}
		m.fileBrowserActive = true // Ensure browser stays active
		return nil

	case tea.KeyMsg:
		// If save dialog is active, handle its keys
		if m.saveDialogActive {
			return m.handleSaveDialogKey(msg)
		}

		// If create dialog is active, handle its keys
		if m.createDialogActive {
			return m.handleCreateDialogKey(msg)
		}

		// If file browser is active, handle its keys
		if m.fileBrowserActive {
			return m.handleFileBrowserKey(msg)
		}

		// If confirm dialog is visible, let it handle the key
		if m.confirmDialog.Visible {
			return m.confirmDialog.Update(msg)
		}

		// Handle keyboard shortcuts
		if msg.String() == "c" {
			m.createDialogActive = true
			m.createDialogStep = "source"
			m.createStackSource = "file"
			m.createNameInput.SetValue("")
			m.createFileInput.SetValue("")
			m.createDialogContent = defaultStackTemplate
			m.createDialogError = ""
			return nil
		}

		// --- normal mode ---
		// If ESC is pressed and there's an active filter, clear it instead of quitting
		if msg.Type == tea.KeyEsc && m.List.Query != "" {
			m.List.Query = ""
			m.List.ApplyFilter()
			m.List.Cursor = 0
			m.List.Viewport.GotoTop()
			return nil
		}

		// Note: task navigation is handled below to allow entering/exiting
		// the inline task list with up/down keys. We call HandleKey later
		// after giving task-navigation a chance to intercept the key.

		// Enter triggers navigation to services
		if msg.String() == "enter" {
			if m.List.Cursor < len(m.List.Filtered) {
				selected := m.List.Filtered[m.List.Cursor]
				return func() tea.Msg {
					return view.NavigateToMsg{
						ViewName: servicesview.ViewName,
						Payload:  map[string]interface{}{"stackName": selected.Name},
					}
				}
			}
		}

		// 'p' shows tasks for selected stack
		if msg.String() == "p" {
			if m.List.Cursor < len(m.List.Filtered) {
				selected := m.List.Filtered[m.List.Cursor]
				// Toggle expanded state for this stack to show inline tasks
				m.expandedStacks[selected.Name] = !m.expandedStacks[selected.Name]
				if m.expandedStacks[selected.Name] {
					// Load tasks for this stack asynchronously
					stackName := selected.Name
					taskOps := m.deps.Tasks
					return func() tea.Msg {
						tasks, err := taskOps.GetTasksForStack(stackName)
						return StackTasksLoadedMsg{StackName: stackName, Tasks: tasks, Error: err}
					}
				} else {
					// collapsing: keep cached tasks (don't delete) to preserve error state, just reset selection
					m.selectedTaskIndex = -1
					m.setRenderItem()
				}
			}
		}

		// 'g' goes to the chart release this stack belongs to, if any.
		// Lowercase because capitals in this view are its sort keys, and 'c'
		// is already create-stack.
		if msg.String() == "g" {
			if m.List.Cursor < len(m.List.Filtered) {
				return m.chartJumpCmd(m.List.Filtered[m.List.Cursor].Name)
			}
		}

		// 'i' inspects the stack (shows detailed JSON)
		if msg.String() == "i" {
			if m.List.Cursor < len(m.List.Filtered) {
				selected := m.List.Filtered[m.List.Cursor]
				stackName := selected.Name
				stackOps := m.deps.Stacks
				return func() tea.Msg {
					l().Infof("Inspecting stack: %s", stackName)
					yamlContent, err := stackOps.InspectStack(stackName)
					if err != nil {
						l().Errorf("Failed to inspect stack %s: %v", stackName, err)
						// Show error in inspect view
						return view.NavigateToMsg{
							ViewName: "inspect",
							Payload: map[string]interface{}{
								"title":  fmt.Sprintf("Stack: %s (inspect failed)", stackName),
								"json":   fmt.Sprintf("# Error inspecting stack:\n# %v", err),
								"format": "raw",
							},
						}
					}
					l().Infof("Successfully inspected stack: %s (%d bytes)", stackName, len(yamlContent))
					return view.NavigateToMsg{
						ViewName: "inspect",
						Payload: map[string]interface{}{
							"title":  fmt.Sprintf("Stack: %s", stackName),
							"json":   yamlContent,
							"format": "yml",
						},
					}
				}
			}
		}

		// 'e' opens editor to edit the selected stack
		if msg.String() == "e" {
			if m.List.Cursor < len(m.List.Filtered) {
				selected := m.List.Filtered[m.List.Cursor]
				stackName := selected.Name
				m.editStackName = stackName // Mark that we're editing
				l().Infof("Opening editor for stack: %s", stackName)

				// Reconstruct YAML in background and then open editor
				yamlContent, err := m.deps.Stacks.ReconstructStackCompose(stackName)
				if err != nil {
					l().Errorf("Failed to reconstruct YAML for stack %s: %v", stackName, err)
					m.editStackName = "" // Clear edit mode on error
					m.confirmDialog.Visible = true
					m.confirmDialog.ErrorMode = true
					m.confirmDialog.Message = fmt.Sprintf("Failed to load stack %q for editing:\n%v", stackName, err)
					return nil
				}
				l().Infof("Reconstructed YAML for editing: %s (%d bytes)", stackName, len(yamlContent))
				return openEditorForStackCmd(yamlContent)
			}
		}

		// 's' saves stack YAML to a local file
		if msg.String() == "s" {
			if m.List.Cursor < len(m.List.Filtered) {
				selected := m.List.Filtered[m.List.Cursor]
				m.saveStackName = selected.Name
				m.saveDialogActive = true
				m.saveDialogError = ""
				m.saveFileInput.SetValue(selected.Name + ".yml")
				m.saveFileInput.CursorEnd()
				m.saveFileInput.Focus()
				return nil
			}
		}

		// 'n' opens create stack dialog
		if msg.String() == "n" {
			l().Info("Create stack key pressed")
			m.createDialogActive = true
			m.createDialogStep = "source"
			m.createStackSource = "file" // default
			m.createNameInput.SetValue("")
			m.createFileInput.SetValue("")
			m.createStackPath = "" // Clear any previous file path
			m.createDialogContent = defaultStackTemplate
			m.createDialogError = ""
			return nil
		}

		// 'ctrl+d' removes selected stack. First check (async) whether the stack
		// belongs to a chart release so the confirm dialog can warn.
		if msg.String() == "ctrl+d" {
			if m.List.Cursor < len(m.List.Filtered) {
				selected := m.List.Filtered[m.List.Cursor]
				return m.stackDeleteIntentCmd(selected.Name)
			}
		}

		// Handle task navigation for expanded stacks (up/down into tasks)
		if m.List.Cursor < len(m.List.Filtered) {
			entry := m.List.Filtered[m.List.Cursor]
			if m.expandedStacks[entry.Name] {
				// Distinguish between "tasks not yet loaded" and "loaded but empty".
				tasks, loaded := m.stackTasks[entry.Name]
				switch msg.String() {
				case "down":
					// If tasks not yet loaded, enter task-selection and wait for load
					if !loaded {
						m.selectedTaskIndex = 0
						m.setRenderItem()
						return nil
					}
					if m.selectedTaskIndex < len(tasks)-1 {
						m.selectedTaskIndex++
						m.setRenderItem()
						return nil
					} else if m.selectedTaskIndex == len(tasks)-1 {
						// At last task, move to next stack
						m.selectedTaskIndex = -1
						m.List.HandleKey(msg)
						return nil
					}
				case "up":
					if !loaded {
						// nothing loaded yet, just keep focus on stack row
						m.selectedTaskIndex = -1
						m.setRenderItem()
						return nil
					}
					if m.selectedTaskIndex > 0 {
						m.selectedTaskIndex--
						m.setRenderItem()
						return nil
					} else if m.selectedTaskIndex == 0 {
						// Move back to stack row
						m.selectedTaskIndex = -1
						m.setRenderItem()
						return nil
					}
				}
			} else if msg.String() == "up" && m.selectedTaskIndex == -1 && m.List.Cursor > 0 {
				prevEntry := m.List.Filtered[m.List.Cursor-1]
				if m.expandedStacks[prevEntry.Name] {
					prevTasks := m.stackTasks[prevEntry.Name]
					if len(prevTasks) > 0 {
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

		// Reset task selection and error scroll when moving to different stack
		if oldCursor != m.List.Cursor {
			m.selectedTaskIndex = -1
			m.errorScrollOffset = 0
		}

		// Horizontal scrolling for error messages (like services view)
		if msg.String() == "left" {
			if m.errorScrollOffset > 0 {
				m.errorScrollOffset -= 5
				if m.errorScrollOffset < 0 {
					m.errorScrollOffset = 0
				}
			}
			return nil
		}
		if msg.String() == "right" {
			// Only scroll if current stack has an error
			if m.List.Cursor < len(m.List.Filtered) {
				entry := m.List.Filtered[m.List.Cursor]
				if tasks, ok := m.stackTasks[entry.Name]; ok {
					for _, t := range tasks {
						if strings.ToLower(t.DesiredState) == "running" && t.Error != "" {
							m.errorScrollOffset += 5
							break
						}
					}
				}
			}
			return nil
		}

		// Sort by Stack name (Shift+S)
		if msg.String() == "S" {
			m.userSetSort = true
			if m.sortField == SortByName {
				// Toggle ascending/descending
				m.sortAscending = !m.sortAscending
			} else {
				// Switch to this field and reset to ascending
				m.sortField = SortByName
				m.sortAscending = true
			}
			m.applySorting()
			return nil
		}

		// Sort by Error (Shift+R)
		if msg.String() == "R" {
			m.userSetSort = true
			if m.sortField == SortByError {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByError
				m.sortAscending = true
			}
			m.applySorting()
			return nil
		}

		// Sort by Services (Shift+E)
		if msg.String() == "E" {
			m.userSetSort = true
			if m.sortField == SortByServices {
				// Toggle ascending/descending
				m.sortAscending = !m.sortAscending
			} else {
				// Switch to this field and reset to ascending
				m.sortField = SortByServices
				m.sortAscending = true
			}
			m.applySorting()
			return nil
		}

		// Sort by Tasks (Shift+T)
		if msg.String() == "T" {
			m.userSetSort = true
			if m.sortField == SortByTasks {
				// Toggle ascending/descending
				m.sortAscending = !m.sortAscending
			} else {
				// Switch to this field and reset to ascending
				m.sortField = SortByTasks
				m.sortAscending = true
			}
			m.applySorting()
			return nil
		}

		return nil
	}

	var cmd tea.Cmd
	m.List.Viewport, cmd = m.List.Viewport.Update(msg)
	return cmd
}

// handleCreateDialogKey handles key presses inside the create dialog
func (m *Model) handleCreateDialogKey(msg tea.KeyMsg) tea.Cmd {
	switch m.createDialogStep {
	case "source":
		switch msg.String() {
		case "esc":
			m.createDialogActive = false
			m.createDialogError = ""
			return nil
		case "up", "down":
			// Toggle between file and inline
			if m.createStackSource == "file" {
				m.createStackSource = "inline"
			} else {
				m.createStackSource = "file"
			}
			return nil
		case "enter":
			// Move to name entry
			m.createDialogError = ""
			if m.createStackSource == "file" {
				m.createDialogStep = "details-file"
			} else {
				m.createDialogStep = "details-inline"
			}
			m.createInputFocus = 0
			m.createNameInput.SetValue("")
			m.createFileInput.SetValue("")
			m.createStackPath = "" // Clear any previous file path
			m.createDialogContent = defaultStackTemplate
			m.createNameInput.Focus()
			m.createFileInput.Blur()
			return nil
		}

	case "details-file":
		switch msg.String() {
		case "esc":
			m.createDialogActive = false
			m.createDialogError = ""
			m.createNameInput.Blur()
			m.createFileInput.Blur()
			m.createInputFocus = 0
			return nil
		case "tab", "shift+tab":
			// Toggle focus between name and file inputs
			if m.createInputFocus == 0 {
				m.createInputFocus = 1
				m.createNameInput.Blur()
				m.createFileInput.Focus()
			} else {
				m.createInputFocus = 0
				m.createFileInput.Blur()
				m.createNameInput.Focus()
			}
			return nil
		case dialog.BrowseKey:
			// The dialog has one path input, so browsing needs no focus check.
			m.createDialogActive = false
			m.fileBrowserActive = true
			m.fileBrowserContext = "create"
			homeDir, _ := os.UserHomeDir()
			if homeDir == "" {
				homeDir = "/"
			}
			return loadFilesCmd(homeDir)
		case "enter":
			// If there's an error, clear it and stay in editing mode
			if m.createDialogError != "" {
				l().Infof("Clearing error and staying in editing mode")
				m.createDialogError = ""
				return nil
			}
			// Validate name
			stackName := m.createNameInput.Value()
			if stackName == "" {
				l().Warn("Stack name is empty")
				m.createDialogError = "Stack name cannot be empty"
				return nil
			}
			l().Infof("Stack name: %s", stackName)
			// Validate file path
			filePath := m.createFileInput.Value()
			if filePath == "" {
				l().Warn("File path is empty")
				m.createDialogError = "Please enter or select a file path"
				return nil
			}
			l().Infof("File path: %s", filePath)
			// Read file and validate YAML
			fileContent, err := os.ReadFile(filePath)
			if err != nil {
				l().Errorf("Failed to read file %s: %v", filePath, err)
				m.createDialogError = fmt.Sprintf("Cannot read file: %v", err)
				return nil
			}
			l().Infof("File read successfully (%d bytes), validating YAML", len(fileContent))
			if err := m.deps.Stacks.ValidateStackYAML(string(fileContent)); err != nil {
				l().Errorf("YAML validation failed: %v", err)
				m.createDialogError = fmt.Sprintf("Invalid YAML: %v", err)
				return nil
			}
			l().Infof("YAML validation passed, deploying stack %s", stackName)
			// Deploy the stack
			m.createDialogActive = false
			m.createDialogError = ""
			m.createNameInput.Blur()
			m.createFileInput.Blur()
			stackOps := m.deps.Stacks
			snapOps := m.deps.Snapshot
			return tea.Batch(m.beginDeploy(stackName), func() tea.Msg {
				l().Infof("Deploying stack %s from file %s", stackName, filePath)
				err := stackOps.DeployStack(stackName, string(fileContent))
				if err != nil {
					l().Errorf("Stack deployment failed: %v", err)
					return stackCreateErrorMsg{err}
				}
				l().Infof("Stack %s deployed successfully", stackName)
				// Force immediate snapshot refresh
				if _, err := snapOps.RefreshSnapshot(); err != nil {
					l().Warnf("Failed to refresh snapshot: %v", err)
				}
				return stackDeployedMsg{StackName: stackName}
			})
		default:
			// Pass keys to the focused textinput
			var cmd tea.Cmd
			switch m.createInputFocus {
			case 0:
				m.createNameInput, cmd = m.createNameInput.Update(msg)
			case 1:
				m.createFileInput, cmd = m.createFileInput.Update(msg)
			}
			// Clear error when user types
			if m.createDialogError != "" {
				m.createDialogError = ""
			}
			return cmd
		}

	case "details-inline":
		switch msg.String() {
		case "esc":
			m.createDialogActive = false
			m.createDialogError = ""
			m.createNameInput.Blur()
			m.createDialogContent = ""
			m.createInputFocus = 0
			return nil
		case "tab", "shift+tab":
			// Toggle focus between name and content
			if m.createInputFocus == 0 {
				m.createInputFocus = 1
				m.createNameInput.Blur()
			} else {
				m.createInputFocus = 0
				m.createNameInput.Focus()
			}
			return nil
		case "enter":
			// If there's an error, clear it and stay in editing mode
			if m.createDialogError != "" {
				l().Infof("Clearing error and staying in editing mode")
				m.createDialogError = ""
				return nil
			}
			// Validate name
			stackName := m.createNameInput.Value()
			if stackName == "" {
				l().Warn("Stack name is empty")
				m.createDialogError = "Stack name cannot be empty"
				return nil
			}
			l().Infof("Stack name: %s (inline mode)", stackName)
			// Check if we have content
			if m.createDialogContent == "" {
				l().Warn("No YAML content provided")
				m.createDialogError = "Please add YAML content (press Tab then E to edit)"
				return nil
			}
			l().Infof("YAML content provided (%d bytes), validating", len(m.createDialogContent))
			preview := m.createDialogContent
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			l().Infof("Content preview before validation: %q", preview)
			// Validate YAML
			if err := m.deps.Stacks.ValidateStackYAML(m.createDialogContent); err != nil {
				l().Errorf("YAML validation failed: %v", err)
				m.createDialogError = fmt.Sprintf("Invalid YAML: %v", err)
				return nil
			}
			l().Infof("YAML validation passed, deploying stack %s", stackName)
			// Deploy the stack
			// IMPORTANT: Capture content NOW before creating closure
			contentToDeploy := m.createDialogContent
			deployPreview := contentToDeploy
			if len(deployPreview) > 100 {
				deployPreview = deployPreview[:100] + "..."
			}
			l().Infof("Captured content for deployment (%d bytes), preview: %q", len(contentToDeploy), deployPreview)
			m.createDialogActive = false
			m.createDialogError = ""
			m.createNameInput.Blur()
			stackOps := m.deps.Stacks
			snapOps := m.deps.Snapshot
			return tea.Batch(m.beginDeploy(stackName), func() tea.Msg {
				l().Infof("Deploying stack %s from inline editor (%d bytes)", stackName, len(contentToDeploy))
				err := stackOps.DeployStack(stackName, contentToDeploy)
				if err != nil {
					l().Errorf("Stack deployment failed: %v", err)
					return stackCreateErrorMsg{err}
				}
				l().Infof("Stack %s deployed successfully", stackName)
				// Force immediate snapshot refresh
				if _, err := snapOps.RefreshSnapshot(); err != nil {
					l().Warnf("Failed to refresh snapshot: %v", err)
				}
				return stackDeployedMsg{StackName: stackName}
			})
		case "e", "E":
			// Open editor for content when focused on content
			if m.createInputFocus == 1 {
				preview := m.createDialogContent
				if len(preview) > 100 {
					preview = preview[:100] + "..."
				}
				l().Infof("Opening editor with content (%d bytes), preview: %q", len(m.createDialogContent), preview)
				m.createDialogActive = false
				m.createNameInput.Blur()
				return openEditorForStackCmd(m.createDialogContent)
			}
			// Otherwise it is just a letter — route it like any other key.
			fallthrough
		default:
			// Pass keys to the focused textinput (name only in inline mode)
			var cmd tea.Cmd
			if m.createInputFocus == 0 {
				m.createNameInput, cmd = m.createNameInput.Update(msg)
			}
			// Clear error when user types
			if m.createDialogError != "" {
				m.createDialogError = ""
			}
			return cmd
		}
	}

	return nil
}

// handleSaveDialogKey handles key presses inside the save dialog
func (m *Model) handleSaveDialogKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.saveDialogActive = false
		m.saveDialogError = ""
		m.saveFileInput.Blur()
		return nil
	case dialog.BrowseKey:
		// Open file browser for directory selection
		m.saveDialogActive = false
		m.fileBrowserActive = true
		m.fileBrowserContext = "save"
		startDir, _ := os.Getwd()
		val := m.saveFileInput.Value()
		if val != "" {
			dir := filepath.Dir(val)
			if filepath.IsAbs(dir) {
				startDir = dir
			} else if abs, err := filepath.Abs(dir); err == nil {
				startDir = abs
			}
		}
		return loadFilesCmd(startDir)
	case "enter":
		// If there's an error, clear it and stay
		if m.saveDialogError != "" {
			m.saveDialogError = ""
			return nil
		}
		filePath := m.saveFileInput.Value()
		if filePath == "" {
			m.saveDialogError = "File path cannot be empty"
			return nil
		}
		// Resolve to absolute path
		if !filepath.IsAbs(filePath) {
			if abs, err := filepath.Abs(filePath); err == nil {
				filePath = abs
			}
		}
		// Check if file exists — ask for overwrite confirmation
		if _, err := os.Stat(filePath); err == nil {
			m.saveDialogActive = false
			m.saveFileInput.Blur()
			m.pendingAction = "save-overwrite"
			m.confirmDialog.Visible = true
			m.confirmDialog.ErrorMode = false
			m.confirmDialog.Message = fmt.Sprintf("File already exists:\n%s\n\nOverwrite?", filePath)
			return nil
		}
		// File does not exist — proceed with save
		m.saveDialogActive = false
		m.saveFileInput.Blur()
		return m.saveStackToFileCmd(m.saveStackName, filePath)
	default:
		var cmd tea.Cmd
		m.saveFileInput, cmd = m.saveFileInput.Update(msg)
		if m.saveDialogError != "" {
			m.saveDialogError = ""
		}
		return cmd
	}
}

// saveStackToFileCmd reconstructs and saves stack YAML to a file
// stackDeleteIntentCmd checks whether a stack about to be removed belongs to a
// chart release (by looking for a non-uninstalled release config labeled with
// the stack name) and emits a StackDeleteIntentMsg so the confirm dialog can
// warn. The lookup runs off the main loop so the UI never blocks on it.
func (m *Model) stackDeleteIntentCmd(name string) tea.Cmd {
	return m.chartReleaseOfStackCmd(name, func(release string) tea.Msg {
		return StackDeleteIntentMsg{StackName: name, ChartRelease: release}
	})
}

// chartReleaseOfStackCmd resolves whether a stack belongs to a chart release
// and hands the answer to mk. Two callers need it — the delete guard and the
// jump to the charts view — and the lookup runs off the main loop either way,
// so the UI never blocks on a config listing.
func (m *Model) chartReleaseOfStackCmd(name string, mk func(release string) tea.Msg) tea.Cmd {
	configOps := m.deps.Configs
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
		defer cancel()
		release := ""
		if cfgs, err := configOps.ListConfigs(ctx); err != nil {
			l().Warnf("chartReleaseOfStackCmd: ListConfigs failed: %v", err)
		} else {
			for _, c := range cfgs {
				lbl := c.Spec.Labels
				if lbl[charts.LabelType] == charts.TypeRelease &&
					lbl[charts.LabelRelease] == name &&
					lbl[charts.LabelStatus] != charts.StatusUninstalled {
					release = name
					break
				}
			}
		}
		return mk(release)
	}
}

// chartJumpCmd opens the charts view on the release that owns this stack, or
// reports that nothing owns it.
func (m *Model) chartJumpCmd(name string) tea.Cmd {
	return m.chartReleaseOfStackCmd(name, func(release string) tea.Msg {
		return ChartJumpMsg{StackName: name, ChartRelease: release}
	})
}

func (m *Model) saveStackToFileCmd(stackName, filePath string) tea.Cmd {
	stackOps := m.deps.Stacks
	return func() tea.Msg {
		l().Infof("Reconstructing YAML for stack: %s", stackName)
		yamlContent, err := stackOps.ReconstructStackCompose(stackName)
		if err != nil {
			l().Errorf("Failed to reconstruct YAML for stack %s: %v", stackName, err)
			return stackSaveErrorMsg{Err: fmt.Errorf("failed to reconstruct stack YAML: %w", err)}
		}
		l().Infof("Writing %d bytes to %s", len(yamlContent), filePath)
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return stackSaveErrorMsg{Err: fmt.Errorf("failed to create directory: %w", err)}
		}
		if err := os.WriteFile(filePath, []byte(yamlContent), 0o644); err != nil {
			l().Errorf("Failed to write file %s: %v", filePath, err)
			return stackSaveErrorMsg{Err: fmt.Errorf("failed to write file: %w", err)}
		}
		l().Infof("Stack %s YAML saved to %s", stackName, filePath)
		return stackSavedMsg{Path: filePath}
	}
}

// handleFileBrowserKey handles key presses in file browser mode
func (m *Model) handleFileBrowserKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.fileBrowserActive = false
		if m.fileBrowserContext == "save" {
			// Update save path with the browsed directory
			baseName := filepath.Base(m.saveFileInput.Value())
			if baseName == "" || baseName == "." {
				baseName = m.saveStackName + ".yml"
			}
			m.saveFileInput.SetValue(filepath.Join(m.fileBrowserPath, baseName))
			m.saveFileInput.CursorEnd()
			m.saveDialogActive = true
			m.saveFileInput.Focus()
		} else {
			m.createDialogActive = true
		}
		return nil

	case "up":
		if m.fileBrowserCursor > 0 {
			m.fileBrowserCursor--
		}
		return nil

	case "down":
		if m.fileBrowserCursor < len(m.fileBrowserFiles)-1 {
			m.fileBrowserCursor++
		}
		return nil

	case "pgup":
		m.fileBrowserCursor -= 10
		if m.fileBrowserCursor < 0 {
			m.fileBrowserCursor = 0
		}
		return nil

	case "pgdown":
		m.fileBrowserCursor += 10
		if m.fileBrowserCursor >= len(m.fileBrowserFiles) {
			m.fileBrowserCursor = len(m.fileBrowserFiles) - 1
		}
		return nil

	case "enter":
		if len(m.fileBrowserFiles) == 0 {
			return nil
		}

		selected := m.fileBrowserFiles[m.fileBrowserCursor]

		// Handle "[Save here]" sentinel — select current directory
		if selected == saveDirSentinel {
			baseName := filepath.Base(m.saveFileInput.Value())
			if baseName == "" || baseName == "." {
				baseName = m.saveStackName + ".yml"
			}
			m.saveFileInput.SetValue(filepath.Join(m.fileBrowserPath, baseName))
			m.saveFileInput.CursorEnd()
			m.fileBrowserActive = false
			m.saveDialogActive = true
			m.saveFileInput.Focus()
			return nil
		}

		// Handle parent directory
		if selected == ".." {
			parentDir := filepath.Dir(m.fileBrowserPath)
			if parentDir == m.fileBrowserPath {
				parentDir = "/"
			}
			return loadFilesCmd(parentDir)
		}

		// Handle directory
		if strings.HasSuffix(selected, "/") {
			dirPath := strings.TrimSuffix(selected, "/")
			return loadFilesCmd(dirPath)
		}

		// In save mode: selecting a file uses its parent directory as destination
		if m.fileBrowserContext == "save" {
			dir := filepath.Dir(selected)
			baseName := filepath.Base(m.saveFileInput.Value())
			if baseName == "" || baseName == "." {
				baseName = m.saveStackName + ".yml"
			}
			m.saveFileInput.SetValue(filepath.Join(dir, baseName))
			m.saveFileInput.CursorEnd()
			m.fileBrowserActive = false
			m.saveDialogActive = true
			m.saveFileInput.Focus()
			return nil
		}

		// It's a file - read the content and load it into the editor
		m.createStackPath = selected
		m.fileBrowserActive = false
		l().Infof("File selected: %s, loading content for review", selected)

		// Read file content and switch to inline mode for review/editing
		fileContent, err := os.ReadFile(selected)
		if err != nil {
			l().Errorf("Failed to read file %s: %v", selected, err)
			// Return to file dialog with error
			m.createDialogActive = true
			m.createDialogStep = "details-file"
			m.createDialogError = fmt.Sprintf("Cannot read file: %v", err)
			m.createInputFocus = 1
			m.createFileInput.SetValue(selected)
			m.createFileInput.Focus()
			return tea.Printf("Error reading file: %v", err)
		}
		l().Infof("File read successfully (%d bytes), switching to inline mode for review", len(fileContent))

		// Load content and switch to inline mode
		m.createDialogContent = string(fileContent)
		m.createDialogStep = "details-inline"
		m.createInputFocus = 1 // Focus on content so editor can be opened
		m.createNameInput.Blur()
		m.createDialogError = ""

		// Suggest stack name from filename if not set
		name := m.createNameInput.Value()
		if name == "" {
			// Extract filename without extension as suggested name
			baseName := filepath.Base(selected)
			if idx := strings.LastIndex(baseName, "."); idx > 0 {
				baseName = baseName[:idx]
			}
			m.createNameInput.SetValue(baseName)
		}

		// Automatically open editor for review/editing before deployment
		l().Infof("Opening editor for review of loaded file (%d bytes)", len(fileContent))
		return openEditorForStackCmd(m.createDialogContent)
	}
	return nil
}

func (m *Model) setStacks(stacks []docker.StackEntry) {
	l().Infof("StacksView.setStacks: Updating display with %d stacks", len(stacks))

	// Preserve filter query and cursor position
	oldQuery := m.List.Query
	oldMode := m.List.Mode
	oldCursor := m.List.Cursor

	m.List.Items = stacks

	// Restore filter query and mode
	m.List.Query = oldQuery
	m.List.Mode = oldMode

	// Re-apply filter to update filtered list
	if oldQuery != "" {
		m.List.ApplyFilter()
	} else {
		m.List.Filtered = stacks
	}

	// Compute stack-level error summary from snapshot so errors are visible
	// without expanding the stack (background check).
	// Reset maps first
	for k := range m.stackHasError {
		delete(m.stackHasError, k)
	}
	for k := range m.stackErrorText {
		delete(m.stackErrorText, k)
	}

	snap := m.deps.Snapshot.GetSnapshot()
	if snap != nil {
		// map service ID -> stack name
		svcToStack := make(map[string]string)
		svcDesired := make(map[string]int)
		svcRunning := make(map[string]int)
		for _, svc := range snap.Services {
			if svc.Spec.Labels != nil {
				if stackName, ok := svc.Spec.Labels["com.docker.stack.namespace"]; ok {
					svcToStack[svc.ID] = stackName
				}
			}
			// Shared with the loaders rather than duplicated: counting every
			// node as a global service's target painted an error badge on a
			// healthy service for as long as one node stayed drained, and on
			// every service pinned by a placement constraint (issues #480, #643).
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
			stackName := svcToStack[t.ServiceID]
			if stackName == "" {
				continue
			}
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
			m.stackHasError[stackName] = true
			if m.stackErrorText[stackName] == "" {
				m.stackErrorText[stackName] = t.Status.Err
			}
		}

		// If a service is under-replicated with no explicit error from latest task,
		// check recent tasks for the most recent error
		for serviceID, running := range svcRunning {
			desired := svcDesired[serviceID]
			if desired > 0 && running < desired {
				stackName := svcToStack[serviceID]
				if stackName == "" || m.stackErrorText[stackName] != "" {
					continue
				}
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
					m.stackHasError[stackName] = true
					m.stackErrorText[stackName] = mostRecentErr
				}
			}
		}

		// Also detect active deployment failures: slots where newest error task
		// is more recent than the newest running task, even when service is at capacity.
		for svcID, errMsg := range taskutil.ActiveDeploymentErrorsByService(snap.Tasks) {
			stackName := svcToStack[svcID]
			if stackName == "" || m.stackHasError[stackName] {
				continue
			}
			m.stackHasError[stackName] = true
			m.stackErrorText[stackName] = errMsg
		}
	}

	// Auto-sort by error if errors exist and user hasn't manually set sort
	if !m.userSetSort {
		hasAnyError := false
		for _, hasErr := range m.stackHasError {
			if hasErr {
				hasAnyError = true
				break
			}
		}
		if hasAnyError {
			m.sortField = SortByError
			m.sortAscending = true
		}
	}

	// Reapply sorting to maintain sort order after data refresh
	m.applySorting()

	// Restore cursor position if still valid
	if oldCursor < len(m.List.Filtered) {
		m.List.Cursor = oldCursor
	} else if len(m.List.Filtered) > 0 {
		m.List.Cursor = len(m.List.Filtered) - 1
	} else {
		m.List.Cursor = 0
	}

	// If cursor is at 0 (initial state), ensure YOffset is also 0
	if m.List.Cursor == 0 {
		m.List.Viewport.YOffset = 0
	}

	m.setRenderItem()

	// Note: We don't call SetContent here because the View() method uses
	// VisibleContent() to render only the visible portion. Calling SetContent
	// with View() would cause conflicting YOffset adjustments.
	if m.ready {
		l().Info("StacksView.setStacks: Content ready for rendering")
	} else {
		l().Warn("StacksView.setStacks: View not ready yet")
	}
}

// After loading stacks, set RenderItem dynamically with correct column width
func (m *Model) setRenderItem() {
	defer func() {
		if r := recover(); r != nil {
			l().Errorf("panic in Stacks.setRenderItem: %v", r)
			l().Errorf("%s", debug.Stack())
		}
	}()
	// Build RenderItem to match the header layout used by the view (4 columns: STACK, SERVICES, TASKS, ERROR)
	itemStyle := ui.ListItemStyle

	m.List.RenderItem = func(s docker.StackEntry, selected bool, _ int) string {
		colWidths := m.List.ColWidths()
		if len(colWidths) < 4 {
			return s.Name
		}

		// Prepare stack name
		nameMax := colWidths[0] - 2
		if nameMax < 0 {
			nameMax = 0
		}
		name := s.Name
		if len(name) > nameMax {
			if nameMax > 3 {
				name = name[:nameMax-3] + "..."
			} else {
				name = name[:nameMax]
			}
		}

		svcStr := fmt.Sprintf("%d", s.ServiceCount)
		if len(svcStr) > colWidths[1] {
			svcStr = svcStr[:colWidths[1]]
		}

		nodeStr := fmt.Sprintf("%d", s.NodeCount)
		if len(nodeStr) > colWidths[2] {
			nodeStr = nodeStr[:colWidths[2]]
		}

		// Get error status from the stack-level error maps (populated from snapshot)
		stackHasError := m.stackHasError[s.Name]
		stackErrorText := m.stackErrorText[s.Name]

		// For selected row, apply horizontal scroll to error text
		errorDisplayText := stackErrorText
		if selected && len(stackErrorText) > colWidths[3] {
			errorDisplayText = formatErrorWithScroll(stackErrorText, m.errorScrollOffset, colWidths[3])
		} else if len(stackErrorText) > colWidths[3] {
			errorDisplayText = truncateWithEllipsis(stackErrorText, colWidths[3])
		}

		// Render all columns in one format string using precision to truncate if needed
		if selected {
			selStyle := ui.ListSelectedStyle
			line := selStyle.Render(fmt.Sprintf(" %-*.*s%-*.*s%-*.*s%-*.*s",
				colWidths[0]-1, colWidths[0]-1, name,
				colWidths[1], colWidths[1], svcStr,
				colWidths[2], colWidths[2], nodeStr,
				colWidths[3], colWidths[3], errorDisplayText,
			))

			// If selected and expanded, render tasks below
			if m.expandedStacks[s.Name] {
				tasks := m.stackTasks[s.Name]
				taskHeaderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
				taskHeader := fmt.Sprintf("   %-22s  %-12s  %-13s  %-40s  %s",
					"NAME", "NODE", "DESIRED STATE", "CURRENT STATE", "ERROR")
				line += "\n" + taskHeaderStyle.Render(taskHeader)
				if len(tasks) > 0 {
					for ti, task := range tasks {
						taskName := truncateWithEllipsis(task.Name, 22)
						taskNode := truncateWithEllipsis(task.NodeName, 12)
						taskDesired := truncateWithEllipsis(task.DesiredState, 13)
						taskCurrent := truncateWithEllipsis(task.StatusText(), 40)
						taskErr := truncateWithEllipsis(task.Error, 30)
						taskSelected := m.selectedTaskIndex == ti
						if taskSelected {
							taskSelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")).Bold(true)
							line += "\n" + taskSelStyle.Render(fmt.Sprintf("   %-22s  %-12s  %-13s  %-40s  %s", taskName, taskNode, taskDesired, taskCurrent, taskErr))
						} else {
							taskStyle := taskutil.TaskRowStyle(task, taskRowBaseStyle)
							line += "\n" + taskStyle.Render(fmt.Sprintf("   %-22s  %-12s  %-13s  %-40s  %s", taskName, taskNode, taskDesired, taskCurrent, taskErr))
						}
					}
				} else {
					line += "\n" + selStyle.Render("   (no tasks)")
				}
			}
			return line
		}

		// Non-selected: color entire row red if there's an error (like configs view)
		var baseStyle = itemStyle
		if stackHasError {
			baseStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
		}

		line := baseStyle.Render(fmt.Sprintf(" %-*.*s%-*.*s%-*.*s%-*.*s",
			colWidths[0]-1, colWidths[0]-1, name,
			colWidths[1], colWidths[1], svcStr,
			colWidths[2], colWidths[2], nodeStr,
			colWidths[3], colWidths[3], errorDisplayText,
		))

		if m.expandedStacks[s.Name] {
			tasks := m.stackTasks[s.Name]
			taskHeaderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
			taskHeader := fmt.Sprintf("   %-22s  %-12s  %-13s  %-40s  %s",
				"NAME", "NODE", "DESIRED STATE", "CURRENT STATE", "ERROR")
			line += "\n" + taskHeaderStyle.Render(taskHeader)
			if len(tasks) > 0 {
				for _, task := range tasks {
					taskName := truncateWithEllipsis(task.Name, 22)
					taskNode := truncateWithEllipsis(task.NodeName, 12)
					taskDesired := truncateWithEllipsis(task.DesiredState, 13)
					taskCurrent := truncateWithEllipsis(task.StatusText(), 40)
					taskErr := truncateWithEllipsis(task.Error, 30)
					taskStyle := taskutil.TaskRowStyle(task, taskRowBaseStyle)
					line += "\n" + taskStyle.Render(fmt.Sprintf("   %-22s  %-12s  %-13s  %-40s  %s", taskName, taskNode, taskDesired, taskCurrent, taskErr))
				}
			} else {
				line += "\n" + baseStyle.Render("   (no tasks)")
			}
		}
		return line
	}
}

// taskRowBaseStyle is a task sub-row with nothing wrong with it; the tints for
// the rest come from taskutil.TaskRowStyle.
var taskRowBaseStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))

// formatErrorWithScroll formats error text with horizontal scroll offset (like services view)
func formatErrorWithScroll(full string, offset int, maxWidth int) string {
	if full == "" {
		return ""
	}
	if offset > len(full) {
		offset = len(full)
	}
	visible := full[offset:]

	// Truncate to maxWidth
	if len(visible) > maxWidth {
		visible = truncateWithEllipsis(visible, maxWidth)
	}
	return visible
}

// truncateWithEllipsis matches the services view truncation semantics
func truncateWithEllipsis(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if maxWidth <= 1 {
		return "…"
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	ell := "…"
	ellW := lipgloss.Width(ell)
	var outRunes []rune
	cur := ""
	for _, r := range s {
		cur += string(r)
		if lipgloss.Width(cur)+ellW > maxWidth {
			break
		}
		outRunes = append(outRunes, r)
	}
	if len(outRunes) == 0 {
		return ell
	}
	return string(outRunes) + ell
}

// refreshExpandedStackTasksCmd refreshes tasks for any expanded stacks.
func (m *Model) refreshExpandedStackTasksCmd(expandedStacks map[string]bool) tea.Cmd {
	if len(expandedStacks) == 0 {
		return nil
	}

	taskOps := m.deps.Tasks
	var cmds []tea.Cmd
	for stackName, expanded := range expandedStacks {
		if !expanded {
			continue
		}
		name := stackName
		cmds = append(cmds, func() tea.Msg {
			tasks, err := taskOps.GetTasksForStack(name)
			return StackTasksLoadedMsg{StackName: name, Tasks: tasks, Error: err}
		})
	}

	if len(cmds) == 0 {
		return nil
	}

	return tea.Batch(cmds...)
}

// HelpContent implements the app's optional help-screen contract: "?" is
// handled centrally, and a view carrying its own screen supplies it here.
func (m *Model) HelpContent() []helpview.HelpCategory { return GetStacksHelpContent() }

// GetStacksHelpContent returns categorized help for the stacks view
func GetStacksHelpContent() []helpview.HelpCategory {
	return []helpview.HelpCategory{
		{
			Title: "General",
			Items: []helpview.HelpItem{
				{Keys: "<enter>", Description: "Show services for Stack"},
				{Keys: "<i>", Description: "Inspect stack"},
				{Keys: "<e>", Description: "Edit stack (opens editor)"},
				{Keys: "<s>", Description: "Save stack YAML to file"},
				{Keys: "<p>", Description: "Show tasks for Stack"},
				{Keys: "<n>", Description: "Create new stack"},
				{Keys: "<g>", Description: "Go to the chart release this stack belongs to"},
				{Keys: "<ctrl+d>", Description: "Delete stack"},
				{Keys: "</>", Description: "Filter"},
			},
		},
		{
			Title: "View",
			Items: []helpview.HelpItem{
				{Keys: "<shift+s>", Description: "Order by Stack name"},
				{Keys: "<shift+e>", Description: "Order by Services"},
				{Keys: "<shift+t>", Description: "Order by Tasks"},
				{Keys: "<shift+r>", Description: "Order by Error"},
			},
		},
		{
			Title: "Navigation",
			Items: []helpview.HelpItem{
				{Keys: "<↑/↓>", Description: "Navigate"},
				{Keys: "<pgup>", Description: "Page up"},
				{Keys: "<pgdown>", Description: "Page down"},
				{Keys: "<ctrl+q>", Description: "Quit"},
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
	cursorStack := ""
	if m.List.Cursor < len(m.List.Filtered) {
		cursorStack = m.List.Filtered[m.List.Cursor].Name
	}

	// Sort the filtered list
	switch m.sortField {
	case SortByName:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].Name < m.List.Filtered[j].Name
			}
			return m.List.Filtered[i].Name > m.List.Filtered[j].Name
		})
	case SortByServices:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].ServiceCount < m.List.Filtered[j].ServiceCount
			}
			return m.List.Filtered[i].ServiceCount > m.List.Filtered[j].ServiceCount
		})
	case SortByTasks:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.List.Filtered[i].NodeCount < m.List.Filtered[j].NodeCount
			}
			return m.List.Filtered[i].NodeCount > m.List.Filtered[j].NodeCount
		})
	case SortByError:
		sort.Slice(m.List.Filtered, func(i, j int) bool {
			iHas := m.stackHasError[m.List.Filtered[i].Name]
			jHas := m.stackHasError[m.List.Filtered[j].Name]
			if iHas == jHas {
				// fallback to error text then name
				iText := m.stackErrorText[m.List.Filtered[i].Name]
				jText := m.stackErrorText[m.List.Filtered[j].Name]
				if iText == jText {
					if m.sortAscending {
						return m.List.Filtered[i].Name < m.List.Filtered[j].Name
					}
					return m.List.Filtered[i].Name > m.List.Filtered[j].Name
				}
				if m.sortAscending {
					return iText < jText
				}
				return iText > jText
			}
			// Place stacks with errors first when ascending
			if m.sortAscending {
				return iHas && !jHas
			}
			return !iHas && jHas
		})
	}

	// Restore cursor position to previously selected item
	if cursorStack != "" {
		for i, s := range m.List.Filtered {
			if s.Name == cursorStack {
				m.List.Cursor = i
				return
			}
		}
	}

	// If previous item not found, reset cursor to 0
	m.List.Cursor = 0
	m.List.Viewport.GotoTop()
}
