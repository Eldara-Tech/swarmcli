// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/commands/api"
	cmdpkg "github.com/Eldara-Tech/swarmcli/v2/commands/command"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/settings"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"
	"github.com/Eldara-Tech/swarmcli/v2/views/commandinput"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	contextsview "github.com/Eldara-Tech/swarmcli/v2/views/contexts"
	helpview "github.com/Eldara-Tech/swarmcli/v2/views/help"
	loadingview "github.com/Eldara-Tech/swarmcli/v2/views/loading"
	nodesview "github.com/Eldara-Tech/swarmcli/v2/views/nodes"
	"github.com/Eldara-Tech/swarmcli/v2/views/searchinput"
	stacksview "github.com/Eldara-Tech/swarmcli/v2/views/stacks"
	systeminfoview "github.com/Eldara-Tech/swarmcli/v2/views/systeminfo"
	"github.com/Eldara-Tech/swarmcli/v2/views/unlockdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Startup overlay captures KeyMsg exclusively while active.
	if startupOverlay != nil && startupOverlay.Active() {
		if _, isKey := msg.(tea.KeyMsg); isKey {
			return m, startupOverlay.Update(msg)
		}
		if wsm, ok := msg.(tea.WindowSizeMsg); ok {
			_ = startupOverlay.Update(wsm)
			// Fall through — app also handles resize
		}
	}

	// The first-run usage-reporting line clears on the first key the app itself
	// handles — not the first key of the session. While a startup overlay is up
	// it covers the screen and takes every keystroke, so retiring the notice
	// there would retire something nobody could have read.
	if m.telemetryNoticeActive {
		if _, isKey := msg.(tea.KeyMsg); isKey {
			m.telemetryNoticeActive = false
		}
	}

	for _, hook := range preUpdateHooks {
		if handled, cmd := hook(m.currentView.Name(), msg); handled {
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case docker.Event:
		// On Docker events, trigger a background refresh and, if currently
		// viewing stacks/nodes, trigger a reload so the UI updates quickly using cached data.
		docker.TriggerRefreshIfNeeded()
		// If node event, refresh nodes view; if stacks view, refresh stacks.
		switch msg.Type {
		case "node":
			if nv, ok := m.currentView.(*nodesview.Model); ok {
				return m, tea.Batch(nv.LoadNodesCmd(), watchEventsCmd())
			}
		case "service", "config", "network":
			if sv, ok := m.currentView.(*stacksview.Model); ok {
				// Use cached snapshot to update stacks quickly
				return m, tea.Batch(sv.LoadStacksCmd(""), watchEventsCmd())
			}
		}
		// Re-issue watcher after handling
		return m, watchEventsCmd()
	case snapshotLoadedMsg:
		if msg.Err != nil {
			if m.previousContext != "" {
				if m.revertWritesConfig {
					_ = docker.UseContext(m.previousContext)
				} else {
					// The config file still names the context that would not
					// load, and it is not ours to change. Stop offering it, or
					// the drift prompt asks again on the next tick and every
					// tick after that.
					failed, _ := docker.SessionContext()
					m.declinedDriftContext = failed
				}
				// The pin comes back either way, or the session is left
				// addressing the context whose snapshot just failed.
				docker.SetSessionContext(m.previousContext)
				docker.ResetClient()
				m.previousContext = ""
				m.showAppError(
					fmt.Sprintf("Error loading snapshot: %v\n\nReverted to previous context.", msg.Err),
					contextsview.ViewName,
				)
			} else {
				m.showAppError(fmt.Sprintf("Error loading snapshot: %v", msg.Err), contextsview.ViewName)
			}
			return m, nil
		}
		m.previousContext = ""
		// Replace loading with stacks view
		cmd := m.replaceView(stacksview.ViewName, nil)
		// A locked swarm loads as an empty, flagged snapshot. Tell the user why
		// the lists are empty and how to unlock — without reverting the switch.
		if snap := docker.GetSnapshot(); snap != nil && snap.Locked {
			m.showAppInfo(
				"Swarm is locked — its resources stay hidden until it is unlocked.\n\n"+
					"Type :unlock to enter the unlock key, or run `docker swarm unlock` in a terminal.",
				"",
			)
		}
		return m, cmd
	case commandinput.SubmitMsg:
		raw := strings.TrimSpace(msg.Command)
		if raw == "" {
			return m, nil
		}

		cmd, parsedArgs, err := api.ParseInput(raw)
		if err != nil {
			var helpErr api.ErrHelpRequested
			if errors.As(err, &helpErr) {
				return m, cmdpkg.CommandHelpCmd(helpErr.Cmd)
			}
			m.commandInput.ShowError(err.Error())
			return m, nil
		}

		ctx := api.Context{App: &m}

		return m, cmd.Execute(ctx, parsedArgs)

	case view.NavigateToMsg:
		// Use Replace flag to decide whether to replace current view
		if msg.Replace {
			cmd := m.replaceView(msg.ViewName, msg.Payload)
			return m, cmd
		}
		cmd := m.switchToView(msg.ViewName, msg.Payload)
		return m, cmd

	case view.GoBackMsg:
		cmd := m.goBack()
		return m, cmd

	case tea.WindowSizeMsg:
		cmd := m.updateForResize(msg)
		return m, cmd

	case searchinput.SearchQueryMsg:
		if fv, ok := m.currentView.(view.Filterable); ok {
			fv.ApplySearchQuery(msg.Query)
		}
		return m, nil

	case searchinput.SearchClearedMsg:
		if fv, ok := m.currentView.(view.Filterable); ok {
			fv.ClearSearchQuery()
		}
		return m, nil

	case tea.KeyMsg:
		// If an app-level dialog is active, route all keys to handleKey
		// which forwards them to the dialog exclusively.
		if m.appErrorDialogActive || m.unlockDialogActive || m.updateDialogActive {
			return m.handleKey(msg)
		}

		if msg.String() == ":" {
			// If current view is capturing input, don't intercept
			if viewWithDialog, ok := m.currentView.(interface {
				CapturesInput() bool
			}); ok {
				if viewWithDialog.CapturesInput() {
					// Let the view handle it
					cmd := m.currentView.Update(msg)
					return m, cmd
				}
			}

			// If command input is already visible, forward ":" as text
			if m.commandInput.Visible() {
				cmd := m.commandInput.Update(msg)
				return m, cmd
			}
			// If search input is actively being edited, forward ":" as text
			if m.searchInput.Visible() && m.searchInput.Editing() {
				cmd := m.searchInput.Update(msg)
				return m, cmd
			}
			// If view has active internal search (ctrl+f), let it handle ":"
			if searchView, ok := m.currentView.(interface{ IsSearching() bool }); ok {
				if searchView.IsSearching() {
					cmd := m.currentView.Update(msg)
					return m, cmd
				}
			}

			if m.searchInput.Visible() {
				return m, nil
			}

			if !m.commandInput.Visible() {
				cmd := m.commandInput.Show()
				// Resize with the bar now open so the view is passed a height
				// reduced by the rows it takes.
				resizeCmd := m.resizeToTerminal()
				return m, tea.Batch(cmd, resizeCmd)
			}
			return m, nil
		}

		if msg.String() == "/" {
			// If current view is capturing input, let it handle
			if viewWithDialog, ok := m.currentView.(interface {
				CapturesInput() bool
			}); ok {
				if viewWithDialog.CapturesInput() {
					cmd := m.currentView.Update(msg)
					return m, cmd
				}
			}
			// If command input is visible, forward "/" as text
			if m.commandInput.Visible() {
				cmd := m.commandInput.Update(msg)
				return m, cmd
			}
			// If search input is actively being edited, forward "/" as text
			if m.searchInput.Visible() && m.searchInput.Editing() {
				cmd := m.searchInput.Update(msg)
				return m, cmd
			}
			// If view doesn't implement Filterable, let it handle / itself
			// (logs and inspect views have their own / search)
			if _, ok := m.currentView.(view.Filterable); !ok {
				cmd := m.currentView.Update(msg)
				return m, cmd
			}
			// If view is in a sub-view (IsSearching returns true for sub-views),
			// let the view handle it
			if searchView, ok := m.currentView.(interface{ IsSearching() bool }); ok {
				if searchView.IsSearching() {
					cmd := m.currentView.Update(msg)
					return m, cmd
				}
			}
			// If search box is passive (visible but not editing), resume editing
			if m.searchInput.Visible() && !m.searchInput.Editing() {
				cmd := m.searchInput.Resume()
				return m, cmd
			}
			if !m.searchInput.Visible() {
				cmd := m.searchInput.Show()
				resizeCmd := m.resizeToTerminal()
				return m, tea.Batch(cmd, resizeCmd)
			}
			return m, nil
		}

		// If command input is visible, forward all keys to it exclusively.
		// When the command input hides (e.g., on Enter or Esc) we need to
		// trigger a resize so the current view regains its full height. The
		// commandinput.Update will return a cmd that may include hiding the
		// input; we detect visibility change by checking before/after Update.
		if m.commandInput.Visible() {
			prevVisible := m.commandInput.Visible()
			cmd := m.commandInput.Update(msg)
			// If visibility changed from true -> false, trigger resize to restore height
			if prevVisible && !m.commandInput.Visible() {
				// Command input just hid: restore the full usable viewport height.
				resizeCmd := m.resizeToTerminal()
				return m, tea.Batch(cmd, resizeCmd)
			}
			return m, cmd
		}

		// If search input is actively being edited, forward all keys to it exclusively.
		if m.searchInput.Visible() && m.searchInput.Editing() {
			prevVisible := m.searchInput.Visible()
			cmd := m.searchInput.Update(msg)
			if prevVisible && !m.searchInput.Visible() {
				resizeCmd := m.resizeToTerminal()
				return m, tea.Batch(cmd, resizeCmd)
			}
			return m, cmd
		}

		return m.handleKey(msg)

	case tickMsg:
		return m.handleTick(msg)

	case systeminfoview.LatestVersionMsg:
		// Keep the header badge in sync first, then raise the one-time startup
		// notice — unless a startup overlay (e.g. a BE proactive nudge) already
		// owns the screen, another app dialog is up, or the user opted out of
		// this version.
		cmd := m.systemInfo.Update(msg)
		if m.dialogActive() {
			return m, cmd
		}
		if msg.LatestVersion != settings.Load().DismissedUpdateVersion {
			m.showUpdateNotice(msg.LatestVersion)
		}
		return m, cmd

	case systeminfoview.HeartbeatMsg:
		// A session still open a day later. Reported, and the answer discarded:
		// raising an update notice under somebody who has been working in the
		// TUI since yesterday would be an interruption rather than news.
		return m, m.systemInfo.SendHeartbeat()

	case systeminfoview.HeartbeatSentMsg:
		// Re-armed on the round finishing rather than on it starting, so a slow
		// or failed report cannot leave two rounds in flight.
		return m, m.systemInfo.HeartbeatCmd()

	case systeminfoview.SystemInfoMsg:
		return m, m.systemInfo.Update(msg)

	case contextsview.ContextChangedNotification:
		// Context has changed - show loading view then navigate to stacks
		return m, m.enterContext(msg.PreviousContext, true)

	case contextDriftMsg:
		return m, m.handleContextDrift(msg)

	case view.AppErrorMsg:
		m.showAppError(msg.Error, msg.FallbackView)
		return m, nil

	case view.AppInfoMsg:
		m.showAppInfo(msg.Message, msg.FallbackView)
		return m, nil

	case confirmdialog.ResultMsg:
		if m.contextDriftDialogActive {
			return m, m.resolveContextDrift(msg.Confirmed)
		}
		if m.updateDialogActive {
			m.updateDialogActive = false
			m.updateDialog.Visible = false
			if msg.CheckboxChecked && m.pendingUpdateVersion != "" {
				if err := (settings.Settings{DismissedUpdateVersion: m.pendingUpdateVersion}).Save(); err != nil {
					swarmlog.L().Infow("failed to persist update-notice dismissal", "error", err)
				}
			}
			m.pendingUpdateVersion = ""
			return m, nil
		}
		if m.appErrorDialogActive {
			m.appErrorDialogActive = false
			m.errorDialog.Visible = false
			fallback := m.errorFallbackView
			m.errorFallbackView = ""
			if fallback != "" {
				cmd := m.replaceView(fallback, nil)
				return m, cmd
			}
			return m, nil
		}
		cmd := m.delegateToCurrentView(msg)
		return m, cmd

	case view.OpenUnlockDialogMsg:
		m.unlockDialog.Show()
		m.unlockDialogActive = true
		return m, m.unlockDialog.Init()

	case view.OpenUpdateDialogMsg:
		// On-demand (dev-only) preview: force-show regardless of dismissal.
		latest := strings.TrimSpace(msg.Version)
		if latest == "" {
			latest = m.systemInfo.Latest()
		}
		if latest == "" {
			latest = version + " (preview)"
		}
		m.showUpdateNotice(latest)
		return m, nil

	case unlockdialog.ResultMsg:
		m.unlockDialogActive = false
		m.unlockDialog.Hide()
		if !msg.Confirmed || strings.TrimSpace(msg.Key) == "" {
			return m, nil
		}
		key := msg.Key
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			return unlockResultMsg{Err: docker.UnlockSwarm(ctx, key)}
		}

	case unlockResultMsg:
		if msg.Err != nil {
			m.showAppError("Failed to unlock swarm: "+msg.Err.Error(), "")
			return m, nil
		}
		docker.InvalidateSnapshot()
		return m, tea.Batch(loadSnapshotAsync(), m.systemInfo.LoadStatus())

	default:
		cmd := m.delegateToCurrentView(msg)
		return m, cmd
	}
}

// enterContext takes the session into the context the pin now names: it drops
// the client and the snapshot built for the previous one, shows the loading
// view, and reloads. previous is remembered so a snapshot that fails to load
// can be reverted, and writesConfig says whether that revert may write
// ~/.docker/config.json — true only when this switch wrote it in the first
// place.
//
// Both ways into a new context go through here — a switch made from the
// contexts view and an accepted drift prompt — because a second copy of this
// sequence is a second chance to forget one of the four things it does.
func (m *Model) enterContext(previous string, writesConfig bool) tea.Cmd {
	m.previousContext = previous
	m.revertWritesConfig = writesConfig
	// Close cached Docker client so a fresh one is created for the new context
	docker.ResetClient()
	// Invalidate snapshot cache so stacks load fresh data for new context
	docker.InvalidateSnapshot()
	cmd := m.replaceView(loadingview.ViewName, map[string]string{
		"title":   "Loading",
		"header":  "Fetching cluster info",
		"message": "Loading Swarm nodes and stacks...",
	})
	return tea.Batch(
		m.systemInfo.LoadStatus(),
		cmd,
		// Load snapshot and navigate to stacks when ready
		loadSnapshotAsync(),
	)
}

// unlockResultMsg carries the outcome of a docker.UnlockSwarm call.
type unlockResultMsg struct{ Err error }

func (m *Model) delegateToCurrentView(msg tea.Msg) tea.Cmd {
	cmd := m.currentView.Update(msg)

	vpCmd := m.updateViewports(msg)

	return tea.Batch(cmd, vpCmd)
}

// resizeToTerminal re-runs the layout for the current terminal size. Anything
// that changes how much room the current view gets — toggling fullscreen,
// opening or closing an input bar, navigating — goes through here rather than
// adjusting a height by hand, so the layout mode and the input bar are only
// ever accounted for in one place.
func (m *Model) resizeToTerminal() tea.Cmd {
	return m.updateForResize(tea.WindowSizeMsg{
		Width:  m.terminalWidth,
		Height: m.terminalHeight,
	})
}

func (m *Model) updateForResize(msg tea.WindowSizeMsg) tea.Cmd {
	var cmd tea.Cmd

	// Store terminal dimensions
	m.terminalWidth = msg.Width
	m.terminalHeight = msg.Height

	isFullscreen := m.fullscreen

	var usableWidth, usableHeight int
	if isFullscreen {
		// Fullscreen draws no frame borders and no help/stack bars, so the
		// whole terminal is usable.
		usableWidth = msg.Width
		usableHeight = msg.Height
	} else {
		// Normal mode:
		// - Width: subtract what the frame spends on itself
		// - Height: pass full height, handleViewResize will subtract systeminfo header
		usableWidth = msg.Width - ui.FrameChromeColumns
		usableHeight = msg.Height
	}
	// If an input bar is visible, reserve its lines so the main view is reduced
	// instead of moving the header. This keeps the header fixed at the top
	// while space for the bar is deducted from the usableHeight passed to
	// views. View() reads the same reduced height, so it must not deduct again.
	if (m.commandInput != nil && m.commandInput.Visible()) ||
		(m.searchInput != nil && m.searchInput.Visible()) {
		usableHeight = usableHeight - inputBarHeight
		if usableHeight < 0 {
			usableHeight = 0
		}
	}

	m.viewport.Width = usableWidth
	m.viewport.Height = usableHeight
	// Ensure the viewport's YPosition sits below the system info header.
	// If the command input is visible, push the viewport down by 0 (we
	// keep header fixed) and reserve 3 lines by reducing usableHeight above.
	m.viewport.YPosition = systeminfoview.Height

	cmd = handleViewResize(m.currentView, usableWidth, usableHeight, isFullscreen)
	return cmd
}

// handleViewResize tells a view the height of the frame it has to fill. Views
// size their content for the bordered frame, deducting ui.FramedChromeRows for
// its borders.
func handleViewResize(view view.View, width, height int, isFullscreen bool) tea.Cmd {
	var adjustedHeight int
	if isFullscreen {
		// Fullscreen spends ui.FullscreenChromeRows on a title line instead of
		// those borders, so the frame it fills is that much taller than the
		// terminal — otherwise the view holds back rows for borders that are
		// never drawn and the bottom of the screen goes unused.
		adjustedHeight = height + ui.FramedChromeRows - ui.FullscreenChromeRows
	} else {
		// Normal mode: subtract the help bar and the breadcrumb bar.
		adjustedHeight = height - appChromeRows
	}

	var adjustedMsg = tea.WindowSizeMsg{
		Width:  width,
		Height: adjustedHeight,
	}

	cmd := view.Update(adjustedMsg)
	return cmd
}

func (m *Model) updateViewports(msg tea.Msg) tea.Cmd {
	var cmd1 tea.Cmd
	m.viewport, cmd1 = m.viewport.Update(msg)
	return cmd1
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If app-level error dialog is active, forward keys to it exclusively
	if m.appErrorDialogActive {
		cmd := m.errorDialog.Update(msg)
		return m, cmd
	}

	// If the unlock dialog is active, forward keys to it exclusively
	if m.unlockDialogActive {
		cmd := m.unlockDialog.Update(msg)
		return m, cmd
	}

	// If the update notice is active, forward keys to it exclusively
	if m.updateDialogActive {
		cmd := m.updateDialog.Update(msg)
		return m, cmd
	}

	// If the context drift prompt is active, forward keys to it exclusively
	if m.contextDriftDialogActive {
		cmd := m.contextDriftDialog.Update(msg)
		return m, cmd
	}

	// If current view is capturing input, forward keys to it first
	if viewWithDialog, ok := m.currentView.(interface{ CapturesInput() bool }); ok {
		if viewWithDialog.CapturesInput() {
			cmd := m.currentView.Update(msg)
			return m, cmd
		}
	}
	// Check if current view is in fullscreen or search mode before handling global esc
	if msg.Type == tea.KeyEsc || msg.String() == "esc" {
		// If in fullscreen, exit fullscreen first
		if m.fullscreen {
			m.fullscreen = false
			cmd := m.resizeToTerminal()
			return m, cmd
		}

		// If a view is actively searching/filtering, let it handle ESC (and other keys)
		if searchView, ok := m.currentView.(interface{ IsSearching() bool }); ok {
			if searchView.IsSearching() {
				cmd := m.currentView.Update(msg)
				return m, cmd
			}
		}

		// If search box is passive (visible but not editing), Esc clears and hides it
		if m.searchInput.Visible() && !m.searchInput.Editing() {
			m.searchInput.Hide()
			if fv, ok := m.currentView.(view.Filterable); ok {
				fv.ClearSearchQuery()
			}
			resizeCmd := m.resizeToTerminal()
			return m, resizeCmd
		}

		// Check if stacks view has an active filter
		if stacksView, ok := m.currentView.(interface {
			HasActiveFilter() bool
		}); ok {
			if stacksView.HasActiveFilter() {
				// Let the view handle esc to clear the filter
				cmd := m.currentView.Update(msg)
				return m, cmd
			}
		}
		// Check if logs view has node select open
		if logsView, ok := m.currentView.(interface {
			GetNodeSelectVisible() bool
		}); ok {
			if logsView.GetNodeSelectVisible() {
				// Let the view handle esc
				cmd := m.currentView.Update(msg)
				return m, cmd
			}
		}
		// Check if logs view is in search mode
		if logsView, ok := m.currentView.(interface {
			GetSearchMode() bool
		}); ok {
			if logsView.GetSearchMode() {
				// Let the view handle esc to exit search mode
				cmd := m.currentView.Update(msg)
				return m, cmd
			}
		}
		// Check if current view is capturing input
		if contextsView, ok := m.currentView.(interface {
			CapturesInput() bool
		}); ok {
			if contextsView.CapturesInput() {
				// Let the view handle esc
				cmd := m.currentView.Update(msg)
				return m, cmd
			}
		}
		// Check if configs view is in UsedBy view
		if configsView, ok := m.currentView.(interface {
			IsInUsedByView() bool
		}); ok {
			if configsView.IsInUsedByView() {
				// Let the configs view handle esc to close UsedBy view
				cmd := m.currentView.Update(msg)
				return m, cmd
			}
		}
		// Check if the current view has an expanded row to step out of first
		// (charts: from a child row back to the release, then collapse it)
		if expandableView, ok := m.currentView.(interface {
			IsRowExpanded() bool
		}); ok {
			if expandableView.IsRowExpanded() {
				// Let the view handle esc to walk back out of the expansion
				cmd := m.currentView.Update(msg)
				return m, cmd
			}
		}
		// Check if services view is in stack services mode
		if servicesView, ok := m.currentView.(interface {
			IsInStackServicesView() bool
		}); ok {
			if servicesView.IsInStackServicesView() {
				// Let the services view handle esc to go back to stacks
				cmd := m.currentView.Update(msg)
				return m, cmd
			}
		}
		// Otherwise, go back - but don't quit from the root stacks view with ESC
		// ESC should only go back through the navigation stack, not exit the app
		if m.viewStack.Len() == 0 {
			// At root view (stacks), ESC does nothing - only 'q' or Ctrl+C exits
			return m, nil
		}
		cmd := m.goBack()
		return m, cmd
	}

	// Global help. The gate is the same condition that decides whether the help
	// bar advertises "?" at all, so the app answers exactly the keys it offers —
	// a view that suppresses the global keys gets the keystroke instead, and can
	// do what it likes with it.
	if msg.String() == "?" && !m.hidesGlobalKeys() {
		// Don't intercept if the view is searching: "?" is a character there.
		if searchView, ok := m.currentView.(interface{ IsSearching() bool }); ok && searchView.IsSearching() {
			cmd := m.currentView.Update(msg)
			return m, cmd
		}
		return m, m.openHelp()
	}

	// Global fullscreen toggle
	if msg.String() == "f" {
		// Don't intercept if view is searching
		if searchView, ok := m.currentView.(interface{ IsSearching() bool }); ok && searchView.IsSearching() {
			cmd := m.currentView.Update(msg)
			return m, cmd
		}
		m.fullscreen = !m.fullscreen
		cmd := m.resizeToTerminal()
		return m, cmd
	}

	// Global quit handler: Ctrl+Q (primary) and Ctrl+C (standard terminal fallback)
	if msg.String() == "ctrl+q" || msg.Type == tea.KeyCtrlC {
		exitCmd := m.currentView.OnExit()
		return m, tea.Batch(exitCmd, tea.Quit)
	}

	cmd := m.currentView.Update(msg)
	return m, cmd
}

// openHelp navigates to the help view with whatever the current view can say
// about itself. A view carrying its own screen implements HelpContent; every
// other view is described by the keys it already publishes to the help bar,
// which is a contract every view satisfies — so "?" always lands somewhere.
func (m *Model) openHelp() tea.Cmd {
	var categories []helpview.HelpCategory
	if provider, ok := m.currentView.(interface {
		HelpContent() []helpview.HelpCategory
	}); ok {
		categories = provider.HelpContent()
	} else {
		categories = helpview.FromKeys(
			m.currentView.FrameTitle(),
			m.currentView.ShortHelpItems(),
			m.globalHelpEntries(),
		)
	}
	return func() tea.Msg {
		return view.NavigateToMsg{ViewName: view.NameHelp, Payload: categories}
	}
}

func (m *Model) goBack() tea.Cmd {
	// If no parent view exists → quit the app
	if m.viewStack.Len() == 0 {
		exitCmd := m.currentView.OnExit()
		return tea.Batch(exitCmd, tea.Quit)
	}

	// The view being left
	oldView := m.currentView
	exitCmd := oldView.OnExit()

	// Pop the previous view
	m.currentView = m.viewStack.Pop()

	// The view you are returning to
	enterCmd := m.currentView.OnEnter()

	// Optionally notify the view about terminal size again
	resizeCmd := m.resizeToTerminal()

	// Execute all lifecycle commands
	return tea.Batch(exitCmd, enterCmd, resizeCmd)
}

// watchEventsCmd wraps docker.WatchEvent in a tea.Cmd for the Bubble Tea event loop.
func watchEventsCmd() tea.Cmd {
	return func() tea.Msg {
		return docker.WatchEvent()
	}
}
