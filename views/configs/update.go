// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package configsview

import (
	"context"
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/core/primitives/hash"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
	"github.com/Eldara-Tech/swarmcli/v2/ui/dialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	helpview "github.com/Eldara-Tech/swarmcli/v2/views/help"
	view "github.com/Eldara-Tech/swarmcli/v2/views/view"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// chartReleaseOf returns the name of the chart release a config backs, or "" if
// the config is not a chart-managed release record (labeled by the charts
// engine). Used to guard destructive TUI actions on chart-owned configs.
func chartReleaseOf(cfg *docker.ConfigWithDecodedData) string {
	if cfg != nil && cfg.Config.Spec.Labels[charts.LabelType] == charts.TypeRelease {
		return cfg.Config.Spec.Labels[charts.LabelRelease]
	}
	return ""
}

// deleteConfirmPrompt is the confirmation shown before deleting a config. When
// the config backs a chart release it warns that deleting it can corrupt the
// release's recorded state and points at the proper `charts uninstall` path, but
// still lets the user proceed.
func deleteConfirmPrompt(cfgName string, cfg *docker.ConfigWithDecodedData) string {
	if rel := chartReleaseOf(cfg); rel != "" {
		return fmt.Sprintf("Config %s belongs to chart release %q — deleting it may corrupt the release (use `charts uninstall`). Delete anyway?", cfgName, rel)
	}
	return fmt.Sprintf("Delete config %s?", cfgName)
}

// parseLabels parses a comma-separated list of key=value pairs into a map
// Example: "a=b,c=d" -> map[string]string{"a": "b", "c": "d"}
func parseLabels(input string) (map[string]string, error) {
	labels := make(map[string]string)
	if strings.TrimSpace(input) == "" {
		return labels, nil
	}

	pairs := strings.Split(input, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid label format: '%s' (expected key=value)", pair)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			return nil, fmt.Errorf("label key cannot be empty in: '%s'", pair)
		}
		labels[key] = value
	}
	return labels, nil
}

// usedStatusUpdatedMsg carries a map of config ID -> used boolean
type usedStatusUpdatedMsg map[string]bool

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case SpinnerTickMsg:
		// advance spinner and refresh view only if there are unknown UsedKnown items
		m.spinner++
		need := false
		for _, it := range m.configsList.Items {
			if !it.UsedKnown {
				need = true
				break
			}
		}
		if need {
			m.configsList.Viewport.SetContent(m.configsList.View())
		}
		return m.spinnerTickCmd()
	case usedStatusUpdatedMsg:
		l().Infof("ConfigsView: Received used status updates for %d configs", len(msg))
		// Update m.configsList.Items used flag based on map
		for i := range m.configsList.Items {
			id := m.configsList.Items[i].ID
			if used, ok := msg[id]; ok {
				m.configsList.Items[i].Used = used
				m.configsList.Items[i].UsedKnown = true
			}
		}

		m.configsList.Viewport.SetContent(m.configsList.View())
		return nil

	case tea.WindowSizeMsg:
		m.configsList.Viewport.Width = msg.Width
		// msg.Height is already adjusted by the app to account for the
		// systeminfo header; avoid subtracting extra lines here.
		m.configsList.Viewport.Height = msg.Height
		m.configsList.SetOuterSize(msg.Width, msg.Height)
		// Size the confirm dialog so the component renderer (used for
		// dismiss-only Info/Error dialogs) word-wraps to the terminal width.
		m.confirmDialog.Width = msg.Width
		m.confirmDialog.Height = msg.Height
		if m.usedByViewActive {
			m.usedByList.Viewport.Width = msg.Width
			m.usedByList.Viewport.Height = msg.Height
			m.usedByList.SetOuterSize(msg.Width, msg.Height)
		}
		// On first resize, reset YOffset to 0; on subsequent resizes, only reset if cursor is at top
		if m.firstResize {
			m.configsList.Viewport.YOffset = 0
			m.firstResize = false
		} else if m.configsList.Cursor == 0 {
			m.configsList.Viewport.YOffset = 0
		}
		return nil

	case configsLoadedMsg:
		l().Infof("ConfigsView: Received configsLoadedMsg with %d configs", len(msg))
		// Update the hash with new data using stable fields only
		type stableConfig struct {
			ID      string
			Version uint64
			Name    string
		}
		stableConfigs := make([]stableConfig, len(msg))
		for i, c := range msg {
			stableConfigs[i] = stableConfig{
				ID:      c.Config.ID,
				Version: c.Config.Version.Index,
				Name:    c.Config.Spec.Name,
			}
		}
		var err error
		m.lastSnapshot, err = hash.Compute(stableConfigs)
		if err != nil {
			l().Errorf("ConfigsView: Error computing hash: %v", err)
		}

		m.configs = msg
		items := make([]configItem, len(msg))
		// Preserve previous Used state where possible to avoid UI "blinking".
		// Build a lookup of existing Used values by ID and reuse them until
		// the background computation finishes and sends an update.
		// Preserve previous Used and UsedKnown state where possible to avoid UI "blinking".
		prevUsed := make(map[string]bool, len(m.configsList.Items))
		prevKnown := make(map[string]bool, len(m.configsList.Items))
		for _, it := range m.configsList.Items {
			prevUsed[it.ID] = it.Used
			prevKnown[it.ID] = it.UsedKnown
		}
		for i, cfg := range msg {
			used := false
			known := false
			if val, ok := prevUsed[cfg.Config.ID]; ok {
				used = val
			}
			if k, ok := prevKnown[cfg.Config.ID]; ok {
				known = k
			}
			items[i] = configItem{
				Name:      cfg.Config.Spec.Name,
				ID:        cfg.Config.ID,
				CreatedAt: cfg.Config.CreatedAt,
				UpdatedAt: cfg.Config.UpdatedAt,
				Labels:    cfg.Config.Spec.Labels,
				Used:      used,
				UsedKnown: known,
			}
		}
		m.configsList.Items = items
		m.setRenderItem()
		m.configsList.ApplyFilter()
		m.applySorting()

		m.state = stateReady
		l().Info("ConfigsView: Config list updated (used status pending)")
		// Start background computation of Used flags
		return m.computeConfigUsedCmd(msg)

	case TickMsg:
		if msg.Gen != m.pollGen {
			return nil // a leftover from an earlier entry — see OnEnter
		}
		l().Infof("ConfigsView: Received TickMsg, state=%v, visible=%v", m.state, m.visible)
		// Only check for changes if view is visible, ready, and not showing dialogs
		if m.visible && m.state == stateReady && !m.confirmDialog.Visible && !m.loadingView.Visible() && !m.polling.Load() {
			return tea.Batch(
				m.checkConfigsCmd(m.lastSnapshot),
				tickCmd(m.pollGen),
			)
		}
		// Continue ticking even if not visible/ready
		return tickCmd(m.pollGen)

	case PollRetryMsg:
		// Deliberately no re-arm. The TickMsg handler above always schedules
		// the next tick, so re-arming here as well gives one beat two
		// successors — and each of those does the same, so the poll rate does
		// not merely double, it doubles again on every beat.
		return nil

	case configRotatedMsg:
		l().Infof("Config rotated: %s → %s", msg.Old.Config.Spec.Name, msg.New.Config.Spec.Name)
		// After rotating a config, reload the config list so the "Used" state
		// is recalculated (services may have been updated to reference the new config).
		return tea.Batch(
			m.loadConfigsCmd(),
			tea.Printf("Rotated %s → %s", msg.Old.Config.Spec.Name, msg.New.Config.Spec.Name),
		)

	case configDeletedMsg:
		l().Infof("Config deleted successfully: %s", msg.Name)
		return m.loadConfigsCmd()

	case editConfigMsg:
		cfg := m.selectedConfig()
		l().Infof("Editing config: %s", cfg)
		return m.editConfigInEditorCmd(cfg)

	case editConfigDoneMsg:
		oldName := msg.OldConfig.Config.Spec.Name
		if !msg.Changed {
			l().Debugf("Edit finished: no changes detected for %s", oldName)
			return tea.Printf("No changes made to config: %s", oldName)
		}
		newName := msg.NewConfig.Config.Spec.Name

		l().Infof("Edit finished: config changed, inserting new version %s", newName)

		m.addConfig(msg.NewConfig)
		m.setRenderItem()
		m.pendingAction = "rotate"
		m.configToRotateFrom = &msg.OldConfig
		m.configToRotateInto = &msg.NewConfig
		m.confirmDialog = m.confirmDialog.Show(
			fmt.Sprintf("Rotate from %s → %s now?", oldName, newName),
		)
		return tea.Printf("Config %s edited and queued for rotation", newName)

	case editConfigErrorMsg:
		l().Errorf("Error editing config: %v", msg.err)
		m.state = stateError
		m.err = msg.err
		return tea.Printf("Error editing config: %v", msg.err)

	case configCreatedMsg:
		l().Infof("Config created successfully: %s", msg.Config.Spec.Name)
		return m.loadConfigsCmd()

	case editorContentMsg:
		l().Infof("Editor content received: %d bytes", len(msg.Content))
		m.createConfigData = msg.Content
		// Return to create dialog with inline content
		m.createDialogActive = true
		m.createDialogStep = "details-inline"
		m.createInputFocus = 0
		m.createNameInput.Focus()
		return nil

	case editorContentReadyMsg:
		if msg.Err != nil {
			l().Errorf("Error creating config from editor: %v", msg.Err)
			// Preserve the content and return to name entry
			m.createDialogActive = true
			m.createDialogStep = "details-inline"
			m.createConfigData = string(msg.Data)
			m.createDialogError = msg.Err.Error()
			m.createInputFocus = 0
			m.createNameInput.Focus()
			return nil
		}
		// Success
		return m.loadConfigsCmd()

	case fileContentReadyMsg:
		if msg.Err != nil {
			l().Errorf("Error creating config from file: %v", msg.Err)
			// Preserve the file path and return to name entry
			m.createDialogActive = true
			m.createDialogStep = "details-file"
			m.createFileInput.SetValue(msg.FilePath)
			m.createDialogError = msg.Err.Error()
			m.createInputFocus = 0
			m.createNameInput.Focus()
			return nil
		}
		// Success
		return m.loadConfigsCmd()

	case configCreateErrorMsg:
		l().Errorf("Error creating config: %v", msg.err)
		// Return to create dialog with error message
		m.createDialogActive = true
		m.createDialogError = msg.err.Error()
		m.fileBrowserActive = false
		return nil

	case filesLoadedMsg:
		if msg.Error != nil {
			l().Errorf("Error loading files: %v", msg.Error)
			m.fileBrowserActive = false
			m.createDialogActive = true
			m.createDialogError = fmt.Sprintf("Failed to load directory: %v", msg.Error)
			return nil
		}
		m.fileBrowserPath = msg.Path
		m.fileBrowserFiles = msg.Files
		m.fileBrowserCursor = 0
		m.fileBrowserActive = true // Ensure browser stays active
		return nil

	case usedByMsg:
		if msg.Error != nil {
			l().Errorf("Error getting used by stacks: %v", msg.Error)
			m.errorDialogActive = true
			m.err = msg.Error
			return nil
		}
		l().Infof("Config %s is used by %d service(s)", msg.ConfigName, len(msg.UsedBy))

		// Initialize usedByList with a new viewport. Use sensible fallbacks
		// (model width/height) if configsList viewport hasn't been sized yet.
		w := m.configsList.Viewport.Width
		if w <= 0 {
			w = m.width
		}
		h := m.configsList.Viewport.Height
		if h <= 0 {
			if m.height > 0 {
				h = m.height - 2
			}
			if h <= 0 {
				h = 20
			}
		}

		vp := viewport.New(w, h)
		vp.SetContent("")

		m.usedByList = filterlist.FilterableList[usedByItem]{
			Viewport: vp,
			Match: func(item usedByItem, query string) bool {
				return strings.Contains(strings.ToLower(item.StackName), strings.ToLower(query)) ||
					strings.Contains(strings.ToLower(item.ServiceName), strings.ToLower(query))
			},
			Header: &filterlist.HeaderConfig{
				Columns: []filterlist.ColumnDef{
					{Label: "STACK NAME"},
					{Label: "SERVICE NAME"},
				},
			},
			Footer: &filterlist.FooterConfig{ItemLabel: "Stack"},
			RenderItem: func(item usedByItem, selected bool, _ int) string {
				colWidths := m.usedByList.ColWidths()

				// Prepare truncated texts
				stackText := item.StackName
				if len(stackText) > colWidths[0] {
					stackText = stackText[:colWidths[0]-1] + "…"
				}
				svcText := item.ServiceName
				if len(svcText) > colWidths[1] {
					svcText = svcText[:colWidths[1]-1] + "…"
				}

				col0 := fmt.Sprintf(" %-*s", colWidths[0]-1, stackText)
				col1 := fmt.Sprintf("%-*s", colWidths[1], svcText)
				line := col0 + col1

				if selected {
					return ui.ListSelectedStyle.Render(line)
				}
				return ui.ListItemStyle.Render(line)
			},
		}
		m.usedByList.SetOuterSize(w, h)

		// Important: keep Items as a non-nil slice even when empty.
		// If Items is nil, FilterableList.VisibleContent bypasses its padded empty-state,
		// which can cause the framed view to render too few lines and visually "break"
		// the header/frame.
		m.usedByList.Items = msg.UsedBy
		if m.usedByList.Items == nil {
			m.usedByList.Items = []usedByItem{}
		}
		m.usedByList.ApplyFilter()

		m.usedByConfigName = msg.ConfigName
		m.usedByViewActive = true
		return nil

	case createConfigMsg:
		l().Infof("Opening editor to create config: %s", msg.Name)
		m.createDialogActive = false
		m.createNameInput.Blur()
		m.createLabelsInput.Blur()
		return openEditorForContentCmd(m.createConfigData)

	case errorMsg:
		l().Errorf("Error occurred: %v", msg)
		m.state = stateError
		m.err = msg
		m.errorDialogActive = true
		return nil

	case confirmdialog.ResultMsg:
		l().Debugf("Confirm dialog result: confirmed=%v (pendingAction=%s)", msg.Confirmed, m.pendingAction)
		defer func() {
			m.pendingAction = ""
			m.confirmDialog.Visible = false
			// Reset transient dialog modes so the next dialog (e.g. a delete
			// confirm) isn't left in dismiss-only Info/Error mode.
			m.confirmDialog.InfoMode = false
			m.confirmDialog.ErrorMode = false
			m.confirmDialog.CheckboxLabel = ""
			m.configToRotateFrom = nil
			m.configToRotateInto = nil
			m.configToDelete = nil
		}()
		if !msg.Confirmed {
			l().Info("Action cancelled by user")
			m.confirmDialog.Visible = false
			return nil
		}

		switch m.pendingAction {
		case "rotate":
			if m.configToRotateInto == nil {
				l().Warnln("Confirmed rotation but configToRotate is nil")
				return nil
			}
			l().Infof("Confirmed rotation for %s", m.configToRotateInto.Config.Spec.Name)
			return m.rotateConfigCmd(m.configToRotateFrom, m.configToRotateInto)
		case "delete":
			if m.configToDelete == nil {
				l().Warnln("Confirmed delete but configToDelete is nil")
				return nil
			}
			name := m.configToDelete.Config.Spec.Name
			l().Infof("Confirmed deletion for config %s", name)
			return m.deleteConfigCmd(name)
		}
		return nil

	case tea.KeyMsg:
		if m.errorDialogActive {
			if msg.String() == "enter" || msg.String() == "esc" {
				m.errorDialogActive = false
				m.err = nil
				m.state = stateReady
				return nil
			}
			return nil
		}

		if m.createDialogActive {
			return m.handleCreateDialogKey(msg)
		}

		if m.usedByViewActive {
			return m.handleUsedByViewKey(msg)
		}

		if m.fileBrowserActive {
			return m.handleFileBrowserKey(msg)
		}

		if m.confirmDialog.Visible {
			l().Debugf("Key input routed to confirm dialog: %q", msg.String())
			return m.confirmDialog.Update(msg)
		}

		// --- normal mode ---
		if msg.Type == tea.KeyEsc && m.configsList.Query != "" {
			m.configsList.Query = ""
			m.configsList.ApplyFilter()
			m.configsList.Cursor = 0
			m.configsList.Viewport.GotoTop()
			return nil
		}

		// Handle specific keys in switch, then navigation keys
		switch msg.String() {
		case "ctrl+d":
			if len(m.configsList.Filtered) == 0 {
				return nil
			}
			cfgName := m.selectedConfig()
			cfg, _ := m.findConfigByName(cfgName)
			m.pendingAction = "delete"
			m.configToDelete = cfg
			m.confirmDialog = m.confirmDialog.Show(deleteConfirmPrompt(cfgName, cfg))
			return nil

		case "e":
			cfgName := m.selectedConfig()
			// Editing/rotating a chart release record would corrupt the install;
			// block it from the TUI and explain why via a dismissable popup.
			if cfg, _ := m.findConfigByName(cfgName); chartReleaseOf(cfg) != "" {
				m.pendingAction = ""
				m.confirmDialog.ErrorMode = false
				m.confirmDialog.InfoMode = true
				m.confirmDialog.CheckboxLabel = ""
				m.confirmDialog = m.confirmDialog.Show(fmt.Sprintf(
					"Config %s belongs to chart release %q.\n\nEditing or rotating it here would corrupt the release.\nUse `charts upgrade` instead.", cfgName, chartReleaseOf(cfg)))
				return nil
			}
			l().Infof("Edit key pressed for config: %s", cfgName)
			// Start editor; the editCmd will send back editConfigDoneMsg or editConfigErrorMsg
			return m.editConfigInEditorCmd(cfgName)
		case "u":
			cfgName := m.selectedConfig()
			if cfgName == "" {
				l().Warn("UsedBy key pressed but no config selected")
				return nil
			}
			l().Infof("UsedBy key pressed for config: %s", cfgName)
			return m.getUsedByStacksCmd(cfgName)

		case "left":
			m.configsList.ScrollLeft()
			m.configsList.Viewport.SetContent(m.configsList.View())
			return nil

		case "right":
			m.configsList.ScrollRight()
			m.configsList.Viewport.SetContent(m.configsList.View())
			return nil

		case "n":
			l().Info("Create key pressed")
			m.createDialogActive = true
			m.createDialogStep = "source"
			m.createConfigSource = "file" // default
			m.createNameInput.SetValue("")
			m.createLabelsInput.SetValue("")
			m.createConfigData = ""
			m.createDialogError = ""
			return nil

		case "c":
			// Clone selected config: ask for new name, prefill editor with existing content
			cfgName := m.selectedConfig()
			if cfgName == "" {
				l().Warn("Clone key pressed but no config selected")
				return nil
			}
			l().Infof("Clone key pressed for config: %s", cfgName)
			// Inspect to get content
			ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
			defer cancel()
			cfg, err := m.deps.Configs.InspectConfig(ctx, cfgName)
			if err != nil {
				l().Errorf("Failed to inspect config for clone: %v", err)
				m.err = err
				m.errorDialogActive = true
				return nil
			}
			// Prefill create dialog with existing content and suggested name
			suggested := cfg.Config.Spec.Name + "_clone"
			m.createDialogActive = true
			m.createDialogStep = "details-inline"
			m.createNameInput.SetValue(suggested)
			m.createConfigData = string(cfg.Data)
			m.createDialogError = ""
			m.createInputFocus = 0
			m.createNameInput.Focus()
			return nil
		case "i":
			cfg := m.selectedConfig()
			l().Infof("Inspect key pressed for config: %s", cfg)
			return m.inspectConfigCmd(m.selectedConfig())
		case "enter":
			cfg := m.selectedConfig()
			l().Infof("Inspect key pressed for config: %s", cfg)
			return m.inspectRawConfigCmd(m.selectedConfig())

		case "N":
			if m.sortField == SortByName {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByName
				m.sortAscending = true
			}
			m.applySorting()
			m.configsList.Viewport.SetContent(m.configsList.View())
			return nil

		case "I":
			if m.sortField == SortByID {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByID
				m.sortAscending = true
			}
			m.applySorting()
			m.configsList.Viewport.SetContent(m.configsList.View())
			return nil

		case "U":
			if m.sortField == SortByUsed {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByUsed
				m.sortAscending = true
			}
			m.applySorting()
			m.configsList.Viewport.SetContent(m.configsList.View())
			return nil

		case "C":
			if m.sortField == SortByCreated {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByCreated
				m.sortAscending = true
			}
			m.applySorting()
			m.configsList.Viewport.SetContent(m.configsList.View())
			return nil

		case "D":
			if m.sortField == SortByUpdated {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByUpdated
				m.sortAscending = true
			}
			m.applySorting()
			m.configsList.Viewport.SetContent(m.configsList.View())
			return nil

		case "L":
			if m.sortField == SortByLabels {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByLabels
				m.sortAscending = true
			}
			m.applySorting()
			m.configsList.Viewport.SetContent(m.configsList.View())
			return nil
		default:
			// Let FilterableList handle navigation keys (up/down/pgup/pgdown)
			oldCursor := m.configsList.Cursor
			m.configsList.HandleKey(msg)
			// Reset horizontal scroll offset on cursor movement
			if m.configsList.Cursor != oldCursor {
				m.configsList.ResetColumnScroll()
				m.configsList.Viewport.SetContent(m.configsList.View())
			}
			return nil
		}
	}

	// --- State-based Update ---
	switch m.state {
	case stateReady:
		// nothing extra for now; viewport already handled
		return nil
	default:
		return nil
	}
}

// buildColumns declares the configs table columns for the shared content-aware
// layout. The USED cell closure captures the model so the spinner animates.
func (m *Model) buildColumns() []filterlist.Column[configItem] {
	return []filterlist.Column[configItem]{
		{Label: "NAME", MinWidth: 4, Flex: true, Cell: func(c configItem) string { return c.Name }},
		{Label: "ID", MinWidth: 4, Flex: true, Cell: func(c configItem) string { return c.ID }},
		{Label: "USED", MinWidth: 4, Cell: func(c configItem) string {
			switch {
			case !c.UsedKnown:
				return ui.SpinnerCharAt(m.spinner)
			case c.Used:
				return "●"
			default:
				return " "
			}
		}},
		{Label: "CREATED AT", MinWidth: 19, Cell: func(c configItem) string { return formatTimestamp(c.CreatedAt) }},
		{Label: "UPDATED AT", MinWidth: 19, Cell: func(c configItem) string { return formatTimestamp(c.UpdatedAt) }},
		{Label: "LABELS", MinWidth: 6, Flex: true, Cell: func(c configItem) string { return formatLabels(c.Labels) }},
	}
}

func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		return "N/A"
	}
	return t.Format("2006-01-02 15:04:05")
}

func (m *Model) setRenderItem() {
	m.configsList.RenderItem = func(cfg configItem, selected bool, _ int) string {
		row := m.configsList.RenderRow(cfg, selected)
		if selected {
			return ui.ListSelectedStyle.Render(row)
		}
		return ui.ListItemStyle.Render(row)
	}
}

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
			if m.createConfigSource == "file" {
				m.createConfigSource = "inline"
			} else {
				m.createConfigSource = "file"
			}
			return nil
		case "enter":
			// Move to name entry
			m.createDialogError = "" // Clear any previous error
			if m.createConfigSource == "file" {
				m.createDialogStep = "details-file"
			} else {
				m.createDialogStep = "details-inline"
			}
			m.createInputFocus = 0
			m.createNameInput.SetValue("")
			m.createFileInput.SetValue("")
			m.createLabelsInput.SetValue("")
			m.createConfigData = ""
			m.createNameInput.Focus()
			m.createFileInput.Blur()
			m.createLabelsInput.Blur()
			return nil
		}

	case "details-file":
		switch msg.String() {
		case "esc":
			m.createDialogActive = false
			m.createDialogError = ""
			m.createNameInput.Blur()
			m.createFileInput.Blur()
			m.createLabelsInput.Blur()
			m.createConfigPath = ""
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
			homeDir, _ := os.UserHomeDir()
			if homeDir == "" {
				homeDir = "/"
			}
			return loadFilesCmd(homeDir)
		case "enter":
			// If there's an error, clear it and stay in editing mode
			if m.createDialogError != "" {
				m.createDialogError = ""
				return nil
			}
			// Validate name
			if m.createNameInput.Value() == "" {
				m.createDialogError = "Config name cannot be empty"
				return nil
			}
			if err := validateConfigName(m.createNameInput.Value()); err != nil {
				m.createDialogError = err.Error()
				return nil
			}
			// Validate file path
			filePath := m.createFileInput.Value()
			if filePath == "" {
				m.createDialogError = "Please enter or select a file path"
				return nil
			}
			// Parse labels
			labels, err := parseLabels(m.createLabelsInput.Value())
			if err != nil {
				m.createDialogError = fmt.Sprintf("Invalid labels: %v", err)
				return nil
			}
			// All valid, create config
			m.createDialogActive = false
			m.createDialogError = ""
			m.createNameInput.Blur()
			m.createFileInput.Blur()
			m.createLabelsInput.Blur()
			return m.createConfigFromFileCmd(m.createNameInput.Value(), filePath, labels)
		default:
			// Pass keys to the focused textinput
			var cmd tea.Cmd
			switch m.createInputFocus {
			case 0:
				m.createNameInput, cmd = m.createNameInput.Update(msg)
			case 1:
				m.createFileInput, cmd = m.createFileInput.Update(msg)
			case 2:
				m.createLabelsInput, cmd = m.createLabelsInput.Update(msg)
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
			m.createLabelsInput.Blur()
			m.createConfigData = ""
			m.createInputFocus = 0
			return nil
		case "tab", "shift+tab":
			// Toggle focus between name, content, and labels
			if msg.String() == "tab" {
				m.createInputFocus = (m.createInputFocus + 1) % 3
			} else {
				m.createInputFocus = (m.createInputFocus + 2) % 3
			}
			switch m.createInputFocus {
			case 0:
				m.createNameInput.Focus()
				m.createLabelsInput.Blur()
			case 2:
				m.createNameInput.Blur()
				m.createLabelsInput.Focus()
			default:
				m.createNameInput.Blur()
				m.createLabelsInput.Blur()
			}
			return nil
		case "enter":
			// If there's an error, clear it and stay in editing mode
			if m.createDialogError != "" {
				m.createDialogError = ""
				return nil
			}
			// Validate name
			if m.createNameInput.Value() == "" {
				m.createDialogError = "Config name cannot be empty"
				return nil
			}
			if err := validateConfigName(m.createNameInput.Value()); err != nil {
				m.createDialogError = err.Error()
				return nil
			}
			// Check if we have data
			if m.createConfigData == "" {
				m.createDialogError = "Please add content in editor (press Tab then E)"
				return nil
			}
			// Parse labels
			labels, err := parseLabels(m.createLabelsInput.Value())
			if err != nil {
				m.createDialogError = fmt.Sprintf("Invalid labels: %v", err)
				return nil
			}
			// All valid, create config with existing data
			m.createDialogActive = false
			m.createDialogError = ""
			m.createNameInput.Blur()
			m.createLabelsInput.Blur()
			return m.createConfigFromContentCmd(m.createNameInput.Value(), []byte(m.createConfigData), labels)
		case "e", "E":
			// Open editor for content - don't require name to be set yet
			if m.createInputFocus == 1 {
				m.createDialogActive = false
				m.createNameInput.Blur()
				m.createLabelsInput.Blur()
				return openEditorForContentCmd(m.createConfigData)
			}
			// Otherwise it is just a letter — route it like any other key.
			fallthrough
		default:
			// Pass keys to the focused textinput
			var cmd tea.Cmd
			switch m.createInputFocus {
			case 0:
				m.createNameInput, cmd = m.createNameInput.Update(msg)
			case 2:
				m.createLabelsInput, cmd = m.createLabelsInput.Update(msg)
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

func (m *Model) handleFileBrowserKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.fileBrowserActive = false
		m.createDialogActive = true
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

		// It's a file - set the path and close the file browser
		m.createConfigPath = selected
		m.createFileInput.SetValue(selected)
		m.fileBrowserActive = false

		// If a name is already provided and valid, create immediately
		name := m.createNameInput.Value()
		if name != "" {
			if err := validateConfigName(name); err == nil {
				// Parse labels
				labels, err := parseLabels(m.createLabelsInput.Value())
				if err != nil {
					// Return to dialog with error
					m.createDialogActive = true
					m.createDialogStep = "details-file"
					m.createDialogError = fmt.Sprintf("Invalid labels: %v", err)
					m.createInputFocus = 2
					m.createLabelsInput.Focus()
					return nil
				}
				m.createDialogActive = false
				m.createDialogError = ""
				m.createNameInput.Blur()
				m.createFileInput.Blur()
				m.createLabelsInput.Blur()
				return m.createConfigFromFileCmd(name, selected, labels)
			}
		}

		// Otherwise return focus to create dialog so user can enter a name
		m.createDialogActive = true
		m.createDialogStep = "details-file"
		m.createInputFocus = 1
		m.createFileInput.Focus()
		return nil
	}
	return nil
}

func (m *Model) handleUsedByViewKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		if m.usedByList.Query != "" {
			m.usedByList.Query = ""
			m.usedByList.Mode = filterlist.ModeNormal
			m.usedByList.ApplyFilter()
			m.usedByList.Cursor = 0
			m.usedByList.Viewport.GotoTop()
			return nil
		}
		// Go back to configs view
		m.usedByViewActive = false
		m.usedByList.Items = nil
		m.usedByConfigName = ""
		return nil

	case "enter":
		// Navigate to the services in the selected stack
		if len(m.usedByList.Filtered) == 0 {
			return nil
		}
		selectedStack := m.usedByList.Filtered[m.usedByList.Cursor].StackName
		l().Infof("Navigating to services in stack: %s", selectedStack)
		m.usedByViewActive = false
		m.usedByList.Items = nil
		m.usedByConfigName = ""
		return func() tea.Msg {
			// Send a generic navigation message with a payload for services view.
			// Use Replace=false to indicate this should be pushed onto the view stack.
			payload := map[string]interface{}{"stackName": selectedStack}
			return view.NavigateToMsg{ViewName: view.NameServices, Payload: payload, Replace: false}
		}

	default:
		// Handle navigation in the used by list
		if m.usedByList.Mode == filterlist.ModeSearching {
			m.usedByList.HandleKey(msg)
		} else {
			m.usedByList.HandleKey(msg)
		}
		return nil
	}
}

// ClickRow selects the row under a mouse click: in the UsedBy list while it is
// open, otherwise in the configs list, resetting the horizontal scroll the way a
// cursor key does.
func (m *Model) ClickRow(line int) bool {
	if m.usedByViewActive {
		return m.usedByList.SelectLine(line)
	}
	oldCursor := m.configsList.Cursor
	if !m.configsList.SelectLine(line) {
		return false
	}
	if m.configsList.Cursor != oldCursor {
		m.configsList.ResetColumnScroll()
		m.configsList.Viewport.SetContent(m.configsList.View())
	}
	return true
}

// HelpContent implements the app's optional help-screen contract: "?" is
// handled centrally, and a view carrying its own screen supplies it here.
func (m *Model) HelpContent() []helpview.HelpCategory { return GetConfigsHelpContent() }

// GetConfigsHelpContent returns categorized help for the configs view
func GetConfigsHelpContent() []helpview.HelpCategory {
	return []helpview.HelpCategory{
		{
			Title: "General",
			Items: []helpview.HelpItem{
				{Keys: "<n>", Description: "Create new config"},
				{Keys: "<c>", Description: "Clone config"},
				{Keys: "<i>", Description: "Inspect config (YAML)"},
				{Keys: "<enter>", Description: "View config data"},
				{Keys: "<u>", Description: "Show Used By"},
				{Keys: "<e>", Description: "Edit & Rotate config"},
				{Keys: "<ctrl+d>", Description: "Delete config"},
				{Keys: "</>", Description: "Filter"},
			},
		},
		{
			Title: "View",
			Items: []helpview.HelpItem{
				{Keys: "<shift+n>", Description: "Order by Name"},
				{Keys: "<shift+i>", Description: "Order by ID"},
				{Keys: "<shift+u>", Description: "Order by Config Used"},
				{Keys: "<shift+c>", Description: "Order by Created"},
				{Keys: "<shift+d>", Description: "Order by Updated"},
				{Keys: "<shift+l>", Description: "Order by Labels"},
			},
		},
		{
			Title: "Navigation",
			Items: []helpview.HelpItem{
				{Keys: "<↑/↓>", Description: "Navigate"},
				{Keys: "<pgup>", Description: "Page up"},
				{Keys: "<pgdown>", Description: "Page down"},
				{Keys: "<esc>", Description: "Back to stacks"},
			},
		},
	}
}

// applySorting applies the current sort configuration to the filtered list
func (m *Model) applySorting() {
	if len(m.configsList.Filtered) == 0 {
		return
	}

	// Remember cursor position
	cursorID := ""
	if m.configsList.Cursor < len(m.configsList.Filtered) {
		cursorID = m.configsList.Filtered[m.configsList.Cursor].ID
	}

	// Sort the filtered list
	switch m.sortField {
	case SortByName:
		sort.Slice(m.configsList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.configsList.Filtered[i].Name < m.configsList.Filtered[j].Name
			}
			return m.configsList.Filtered[i].Name > m.configsList.Filtered[j].Name
		})
	case SortByID:
		sort.Slice(m.configsList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.configsList.Filtered[i].ID < m.configsList.Filtered[j].ID
			}
			return m.configsList.Filtered[i].ID > m.configsList.Filtered[j].ID
		})
	case SortByUsed:
		sort.Slice(m.configsList.Filtered, func(i, j int) bool {
			// Unknown values treated as false but keep stable ordering via name
			if m.sortAscending {
				if m.configsList.Filtered[i].Used == m.configsList.Filtered[j].Used {
					return m.configsList.Filtered[i].Name < m.configsList.Filtered[j].Name
				}
				return !m.configsList.Filtered[i].Used && m.configsList.Filtered[j].Used
			}
			if m.configsList.Filtered[i].Used == m.configsList.Filtered[j].Used {
				return m.configsList.Filtered[i].Name < m.configsList.Filtered[j].Name
			}
			return m.configsList.Filtered[i].Used && !m.configsList.Filtered[j].Used
		})
	case SortByCreated:
		sort.Slice(m.configsList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.configsList.Filtered[i].CreatedAt.Before(m.configsList.Filtered[j].CreatedAt)
			}
			return m.configsList.Filtered[i].CreatedAt.After(m.configsList.Filtered[j].CreatedAt)
		})
	case SortByUpdated:
		sort.Slice(m.configsList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.configsList.Filtered[i].UpdatedAt.Before(m.configsList.Filtered[j].UpdatedAt)
			}
			return m.configsList.Filtered[i].UpdatedAt.After(m.configsList.Filtered[j].UpdatedAt)
		})
	case SortByLabels:
		sort.Slice(m.configsList.Filtered, func(i, j int) bool {
			iLabels := formatLabels(m.configsList.Filtered[i].Labels)
			jLabels := formatLabels(m.configsList.Filtered[j].Labels)
			if m.sortAscending {
				return iLabels < jLabels
			}
			return iLabels > jLabels
		})
	}

	// Restore cursor position
	if cursorID != "" {
		for i, c := range m.configsList.Filtered {
			if c.ID == cursorID {
				m.configsList.Cursor = i
				return
			}
		}
	}

	m.configsList.Cursor = 0
	m.configsList.Viewport.GotoTop()
}
