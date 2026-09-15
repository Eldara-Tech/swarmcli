// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package secretsview

import (
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/core/primitives/hash"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
	"github.com/Eldara-Tech/swarmcli/v2/ui/dialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	helpview "github.com/Eldara-Tech/swarmcli/v2/views/help"
	loading "github.com/Eldara-Tech/swarmcli/v2/views/loading"
	view "github.com/Eldara-Tech/swarmcli/v2/views/view"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

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

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	// Forward spinner ticks to loading view when it's visible
	if m.loadingView.Visible() {
		if _, ok := msg.(loading.SpinnerTickMsg); ok {
			return m.loadingView.Update(msg)
		}
	}

	switch msg := msg.(type) {
	case SpinnerTickMsg:
		// advance spinner and refresh view only if there are unknown UsedKnown items
		m.spinner++
		need := false
		for _, it := range m.secretsList.Items {
			if !it.UsedKnown {
				need = true
				break
			}
		}
		if need {
			m.secretsList.Viewport.SetContent(m.secretsList.View())
		}
		return m.spinnerTickCmd()
	case usedStatusUpdatedMsg:
		l().Infof("SecretsView: Received used status updates for %d secrets", len(msg))
		// Update m.secretsList.Items used flag based on map
		for i := range m.secretsList.Items {
			id := m.secretsList.Items[i].ID
			if used, ok := msg[id]; ok {
				m.secretsList.Items[i].Used = used
				m.secretsList.Items[i].UsedKnown = true
			}
		}

		m.secretsList.Viewport.SetContent(m.secretsList.View())
		return nil

	case tea.WindowSizeMsg:
		m.secretsList.Viewport.Width = msg.Width
		m.secretsList.Viewport.Height = msg.Height
		m.secretsList.SetOuterSize(msg.Width, msg.Height)
		if m.usedByViewActive {
			m.usedByList.Viewport.Width = msg.Width
			m.usedByList.Viewport.Height = msg.Height
			m.usedByList.SetOuterSize(msg.Width, msg.Height)
		}
		if m.firstResize {
			m.secretsList.Viewport.YOffset = 0
			m.firstResize = false
		} else if m.secretsList.Cursor == 0 {
			m.secretsList.Viewport.YOffset = 0
		}
		return nil

	case secretsLoadedMsg:
		l().Infof("SecretsView: Received secretsLoadedMsg with %d secrets", len(msg))
		// Update the hash with new data using stable fields only
		type stableSecret struct {
			ID      string
			Version uint64
			Name    string
		}
		stableSecrets := make([]stableSecret, len(msg))
		for i, s := range msg {
			stableSecrets[i] = stableSecret{
				ID:      s.Secret.ID,
				Version: s.Secret.Version.Index,
				Name:    s.Secret.Spec.Name,
			}
		}
		var err error
		m.lastSnapshot, err = hash.Compute(stableSecrets)
		if err != nil {
			l().Errorf("SecretsView: Error computing hash: %v", err)
		}

		m.secrets = msg
		items := make([]secretItem, len(msg))
		prevUsed := make(map[string]bool, len(m.secretsList.Items))
		prevKnown := make(map[string]bool, len(m.secretsList.Items))
		for _, it := range m.secretsList.Items {
			prevUsed[it.ID] = it.Used
			prevKnown[it.ID] = it.UsedKnown
		}
		for i, sec := range msg {
			used := false
			known := false
			if val, ok := prevUsed[sec.Secret.ID]; ok {
				used = val
			}
			if k, ok := prevKnown[sec.Secret.ID]; ok {
				known = k
			}
			items[i] = secretItem{
				Name:      sec.Secret.Spec.Name,
				ID:        sec.Secret.ID,
				CreatedAt: sec.Secret.CreatedAt,
				UpdatedAt: sec.Secret.UpdatedAt,
				Labels:    sec.Secret.Spec.Labels,
				Used:      used,
				UsedKnown: known,
			}
		}
		m.secretsList.Items = items
		m.setRenderItem()
		m.secretsList.ApplyFilter()
		m.applySorting()

		m.state = stateReady
		l().Info("SecretsView: Secret list updated (used status pending)")
		return m.computeSecretUsedCmd(msg)

	case TickMsg:
		if msg.Gen != m.pollGen {
			return nil // a leftover from an earlier entry — see OnEnter
		}
		l().Infof("SecretsView: Received TickMsg, state=%v, visible=%v", m.state, m.visible)
		if m.visible && m.state == stateReady && !m.confirmDialog.Visible && !m.loadingView.Visible() && !m.polling.Load() {
			return tea.Batch(
				m.checkSecretsCmd(m.lastSnapshot),
				tickCmd(m.pollGen),
			)
		}
		return tickCmd(m.pollGen)

	case PollRetryMsg:
		// Deliberately no re-arm. The TickMsg handler above always schedules
		// the next tick, so re-arming here as well gives one beat two
		// successors — and each of those does the same, so the poll rate does
		// not merely double, it doubles again on every beat.
		return nil

	case secretDeletedMsg:
		l().Infof("Secret deleted successfully: %s", msg.Name)
		return m.loadSecretsCmd()

	case secretCreatedMsg:
		l().Infof("Secret created successfully: %s", msg.Name)
		m.addSecret(msg.Secret)
		m.createDialogActive = false
		m.createDialogError = ""
		m.createNameInput.Blur()
		m.createFileInput.Blur()
		m.createSecretData = ""
		return tea.Printf("Created secret: %s", msg.Name)

	case fileBrowserMsg:
		m.fileBrowserPath = msg.Path
		m.fileBrowserFiles = msg.Files
		m.fileBrowserCursor = 0
		m.fileBrowserActive = true
		return nil

	case editorContentMsg:
		l().Infof("Editor content received: %d bytes", len(msg.Content))
		m.createSecretData = msg.Content
		// Return to create dialog with inline content
		m.createDialogActive = true
		m.createDialogStep = "details-inline"
		m.createInputFocus = 0
		m.createNameInput.Focus()
		return nil

	case usedByMsg:
		l().Infof("Secret %s is used by %d service(s)", msg.SecretName, len(msg.UsedBy))

		// Initialize usedByList with a new viewport
		w := m.secretsList.Viewport.Width
		if w <= 0 {
			w = m.width
		}
		h := m.secretsList.Viewport.Height
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
				if len(colWidths) < 2 {
					return item.StackName
				}

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
		// This ensures FilterableList.VisibleContent uses its padded empty-state rendering.
		m.usedByList.Items = msg.UsedBy
		if m.usedByList.Items == nil {
			m.usedByList.Items = []usedByItem{}
		}
		m.usedByList.Viewport.Width = vp.Width
		m.usedByList.Viewport.Height = vp.Height
		m.usedByList.ApplyFilter()

		m.usedBySecretName = msg.SecretName
		m.usedByViewActive = true
		return nil

	case errorMsg:
		l().Errorf("Error occurred: %v", msg)
		m.state = stateError
		m.err = msg
		m.errorDialogActive = true
		m.createDialogActive = false
		m.createDialogError = ""
		return nil

	case confirmdialog.ResultMsg:
		l().Debugf("Confirm dialog result: confirmed=%v (pendingAction=%s)", msg.Confirmed, m.pendingAction)
		defer func() {
			m.pendingAction = ""
			m.confirmDialog.Visible = false
			m.secretToDelete = nil
		}()
		if !msg.Confirmed {
			l().Info("Action cancelled by user")
			m.confirmDialog.Visible = false
			return nil
		}

		switch m.pendingAction {
		case "delete":
			if m.secretToDelete == nil {
				l().Warnln("Confirmed delete but secretToDelete is nil")
				return nil
			}
			name := m.secretToDelete.Secret.Spec.Name
			l().Infof("Confirmed deletion for secret %s", name)
			return m.deleteSecretCmd(name)
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
		if msg.Type == tea.KeyEsc && m.secretsList.Query != "" {
			m.secretsList.Query = ""
			m.secretsList.ApplyFilter()
			m.secretsList.Cursor = 0
			m.secretsList.Viewport.GotoTop()
			return nil
		}

		// Handle specific keys in switch, then navigation keys
		switch msg.String() {
		case "ctrl+d":
			if len(m.secretsList.Filtered) == 0 {
				return nil
			}
			secName := m.selectedSecret()
			sec, _ := m.findSecretByName(secName)
			m.pendingAction = "delete"
			m.secretToDelete = sec
			m.confirmDialog = m.confirmDialog.Show(fmt.Sprintf("Delete secret %s?", secName))
			return nil

		case "u":
			secName := m.selectedSecret()
			if secName == "" {
				l().Warn("UsedBy key pressed but no secret selected")
				return nil
			}
			l().Infof("UsedBy key pressed for secret: %s", secName)
			return m.getUsedByStacksCmd(secName)

		case "x":
			secName := m.selectedSecret()
			if secName == "" {
				l().Warn("Reveal key pressed but no secret selected")
				return nil
			}
			action, ok := view.GetAction("reveal-secret")
			if !ok {
				if cmd := view.FeatureLockedCmd("Reveal Secret"); cmd != nil {
					return cmd
				}
				m.err = view.BEUnavailableErr("Reveal Secret")
				m.errorDialogActive = true
				return nil
			}
			l().Infof("Reveal key pressed for secret: %s", secName)
			return action(secName)

		case "left":
			m.secretsList.ScrollLeft()
			m.secretsList.Viewport.SetContent(m.secretsList.View())
			return nil

		case "right":
			m.secretsList.ScrollRight()
			m.secretsList.Viewport.SetContent(m.secretsList.View())
			return nil

		case "n":
			l().Info("Create key pressed")
			m.createDialogActive = true
			m.createDialogStep = "source"
			m.createSecretSource = "file" // default
			m.createNameInput.SetValue("")
			m.createSecretData = ""
			m.createDialogError = ""
			return nil

		case "i":
			sec := m.selectedSecret()
			l().Infof("Inspect key pressed for secret: %s", sec)
			return m.inspectSecretCmd(m.selectedSecret())

		case "N":
			if m.sortField == SortByName {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByName
				m.sortAscending = true
			}
			m.applySorting()
			m.secretsList.Viewport.SetContent(m.secretsList.View())
			return nil

		case "I":
			if m.sortField == SortByID {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByID
				m.sortAscending = true
			}
			m.applySorting()
			m.secretsList.Viewport.SetContent(m.secretsList.View())
			return nil

		case "U":
			if m.sortField == SortByUsed {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByUsed
				m.sortAscending = true
			}
			m.applySorting()
			m.secretsList.Viewport.SetContent(m.secretsList.View())
			return nil

		case "C":
			if m.sortField == SortByCreated {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByCreated
				m.sortAscending = true
			}
			m.applySorting()
			m.secretsList.Viewport.SetContent(m.secretsList.View())
			return nil

		case "D":
			if m.sortField == SortByUpdated {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByUpdated
				m.sortAscending = true
			}
			m.applySorting()
			m.secretsList.Viewport.SetContent(m.secretsList.View())
			return nil

		case "L":
			if m.sortField == SortByLabels {
				m.sortAscending = !m.sortAscending
			} else {
				m.sortField = SortByLabels
				m.sortAscending = true
			}
			m.applySorting()
			m.secretsList.Viewport.SetContent(m.secretsList.View())
			return nil

		default:
			// Let FilterableList handle navigation keys (up/down/pgup/pgdown)
			oldCursor := m.secretsList.Cursor
			m.secretsList.HandleKey(msg)
			// Reset scroll offset on cursor movement
			if m.secretsList.Cursor != oldCursor {
				m.secretsList.ResetColumnScroll()
				m.secretsList.Viewport.SetContent(m.secretsList.View())
			}
			return nil
		}
	}

	// --- State-based Update ---
	switch m.state {
	case stateReady:
		return nil
	default:
		return nil
	}
}

// buildColumns declares the secrets table columns for the shared content-aware
// layout. The USED cell closure captures the model so the spinner animates.
func (m *Model) buildColumns() []filterlist.Column[secretItem] {
	return []filterlist.Column[secretItem]{
		{Label: "NAME", MinWidth: 4, Flex: true, Cell: func(s secretItem) string { return s.Name }},
		{Label: "ID", MinWidth: 4, Flex: true, Cell: func(s secretItem) string { return s.ID }},
		{Label: "USED", MinWidth: 4, Cell: func(s secretItem) string {
			switch {
			case !s.UsedKnown:
				return ui.SpinnerCharAt(m.spinner)
			case s.Used:
				return "●"
			default:
				return " "
			}
		}},
		{Label: "CREATED AT", MinWidth: 19, Cell: func(s secretItem) string { return formatTimestamp(s.CreatedAt) }},
		{Label: "UPDATED AT", MinWidth: 19, Cell: func(s secretItem) string { return formatTimestamp(s.UpdatedAt) }},
		{Label: "LABELS", MinWidth: 6, Flex: true, Cell: func(s secretItem) string { return formatLabels(s.Labels) }},
	}
}

func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		return "N/A"
	}
	return t.Format("2006-01-02 15:04:05")
}

func (m *Model) setRenderItem() {
	m.secretsList.RenderItem = func(sec secretItem, selected bool, _ int) string {
		row := m.secretsList.RenderRow(sec, selected)
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
			if m.createSecretSource == "file" {
				m.createSecretSource = "inline"
			} else {
				m.createSecretSource = "file"
			}
			return nil
		case "enter":
			m.createDialogError = ""
			if m.createSecretSource == "file" {
				m.createDialogStep = "details-file"
			} else {
				m.createDialogStep = "details-inline"
			}
			m.createInputFocus = 0
			m.createNameInput.SetValue("")
			m.createFileInput.SetValue("")
			m.createSecretData = ""
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
			m.createSecretPath = ""
			m.createInputFocus = 0
			return nil
		case "tab", "shift+tab":
			// Toggle focus between name, file, labels, and encode
			if msg.String() == "tab" {
				m.createInputFocus = (m.createInputFocus + 1) % 4
			} else {
				m.createInputFocus = (m.createInputFocus + 3) % 4
			}
			switch m.createInputFocus {
			case 0:
				m.createNameInput.Focus()
				m.createFileInput.Blur()
				m.createLabelsInput.Blur()
			case 1:
				m.createNameInput.Blur()
				m.createFileInput.Focus()
				m.createLabelsInput.Blur()
			case 2:
				m.createNameInput.Blur()
				m.createFileInput.Blur()
				m.createLabelsInput.Focus()
			default:
				m.createNameInput.Blur()
				m.createFileInput.Blur()
				m.createLabelsInput.Blur()
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
			if m.createDialogError != "" {
				m.createDialogError = ""
				return nil
			}
			if m.createNameInput.Value() == "" {
				m.createDialogError = "Secret name cannot be empty"
				return nil
			}
			if err := validateSecretName(m.createNameInput.Value()); err != nil {
				m.createDialogError = err.Error()
				return nil
			}
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
			m.createDialogActive = false
			m.createDialogError = ""
			m.createNameInput.Blur()
			m.createFileInput.Blur()
			m.createLabelsInput.Blur()
			return m.createSecretFromFileCmd(m.createNameInput.Value(), filePath, labels, m.createEncodeSecret)
		case " ":
			// Toggle encode when focused on encode toggle
			if m.createInputFocus == 3 {
				m.createEncodeSecret = !m.createEncodeSecret
				return nil
			}
			// Otherwise it is just a character — route it like any other key.
			fallthrough
		default:
			var cmd tea.Cmd
			switch m.createInputFocus {
			case 0:
				m.createNameInput, cmd = m.createNameInput.Update(msg)
			case 1:
				m.createFileInput, cmd = m.createFileInput.Update(msg)
			case 2:
				m.createLabelsInput, cmd = m.createLabelsInput.Update(msg)
			}
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
			m.createSecretData = ""
			m.createInputFocus = 0
			return nil
		case "tab", "shift+tab":
			// Toggle focus between name, content, labels, and encode
			if msg.String() == "tab" {
				m.createInputFocus = (m.createInputFocus + 1) % 4
			} else {
				m.createInputFocus = (m.createInputFocus + 3) % 4
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
		case " ":
			// Toggle encode when focused on encode toggle
			if m.createInputFocus == 3 {
				m.createEncodeSecret = !m.createEncodeSecret
				return nil
			}
			return m.routeInlineKey(msg)
		case "e", "E":
			// Open editor for content - don't require name to be set yet
			if m.createInputFocus == 1 {
				m.createDialogActive = false
				m.createNameInput.Blur()
				return openEditorForContentCmd(m.createSecretData)
			}
			return m.routeInlineKey(msg)
		case "enter":
			if m.createDialogError != "" {
				m.createDialogError = ""
				return nil
			}
			if m.createNameInput.Value() == "" {
				m.createDialogError = "Secret name cannot be empty"
				return nil
			}
			if err := validateSecretName(m.createNameInput.Value()); err != nil {
				m.createDialogError = err.Error()
				return nil
			}
			if m.createSecretData == "" {
				m.createDialogError = "Please add content in editor (press Tab then E)"
				return nil
			}
			// Parse labels
			labels, err := parseLabels(m.createLabelsInput.Value())
			if err != nil {
				m.createDialogError = fmt.Sprintf("Invalid labels: %v", err)
				return nil
			}
			m.createDialogActive = false
			m.createDialogError = ""
			m.createNameInput.Blur()
			m.createLabelsInput.Blur()
			return m.createSecretFromContentCmd(m.createNameInput.Value(), []byte(m.createSecretData), labels, m.createEncodeSecret)
		default:
			return m.routeInlineKey(msg)
		}
	}

	return nil
}

// routeInlineKey passes a key to whichever input has focus in the inline create
// dialog. Space and "e" both need it as well as the default branch, and
// fallthrough only reaches the next clause, so the routing lives here — one
// copy that cannot disagree with itself when a field is added.
func (m *Model) routeInlineKey(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	switch m.createInputFocus {
	case 0:
		m.createNameInput, cmd = m.createNameInput.Update(msg)
	case 2:
		m.createLabelsInput, cmd = m.createLabelsInput.Update(msg)
	default:
		return nil
	}
	if m.createDialogError != "" {
		m.createDialogError = ""
	}
	return cmd
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

		if selected == ".." {
			parentDir := filepath.Dir(m.fileBrowserPath)
			if parentDir == m.fileBrowserPath {
				parentDir = "/"
			}
			return loadFilesCmd(parentDir)
		}

		if strings.HasSuffix(selected, "/") {
			dirPath := strings.TrimSuffix(selected, "/")
			return loadFilesCmd(dirPath)
		}

		// It's a file - set the path and return to dialog
		m.createSecretPath = selected
		m.createFileInput.SetValue(selected)
		m.fileBrowserActive = false
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
		m.usedByViewActive = false
		m.usedByList.Items = nil
		m.usedBySecretName = ""
		return nil

	case "enter":
		if len(m.usedByList.Filtered) == 0 {
			return nil
		}
		selectedStack := m.usedByList.Filtered[m.usedByList.Cursor].StackName
		l().Infof("Navigating to services in stack: %s", selectedStack)
		m.usedByViewActive = false
		m.usedByList.Items = nil
		m.usedBySecretName = ""
		return func() tea.Msg {
			payload := map[string]interface{}{"stackName": selectedStack}
			return view.NavigateToMsg{ViewName: view.NameServices, Payload: payload, Replace: false}
		}

	default:
		if m.usedByList.Mode == filterlist.ModeSearching {
			m.usedByList.HandleKey(msg)
		} else {
			m.usedByList.HandleKey(msg)
		}
		return nil
	}
}

// ClickRow selects the row under a mouse click: in the UsedBy list while it is
// open, otherwise in the secrets list, resetting the horizontal scroll the way a
// cursor key does.
func (m *Model) ClickRow(line int) bool {
	if m.usedByViewActive {
		return m.usedByList.SelectLine(line)
	}
	oldCursor := m.secretsList.Cursor
	if !m.secretsList.SelectLine(line) {
		return false
	}
	if m.secretsList.Cursor != oldCursor {
		m.secretsList.ResetColumnScroll()
		m.secretsList.Viewport.SetContent(m.secretsList.View())
	}
	return true
}

func revealDetailedHelpDesc() string {
	return view.BEHelpDesc("reveal-secret", "Reveal secret content")
}

// HelpContent implements the app's optional help-screen contract: "?" is
// handled centrally, and a view carrying its own screen supplies it here.
func (m *Model) HelpContent() []helpview.HelpCategory { return GetSecretsHelpContent() }

// GetSecretsHelpContent returns categorized help for the secrets view
func GetSecretsHelpContent() []helpview.HelpCategory {
	return []helpview.HelpCategory{
		{
			Title: "General",
			Items: []helpview.HelpItem{
				{Keys: "<n>", Description: "Create new secret"},
				{Keys: "<i>", Description: "Inspect secret (YAML)"},
				{Keys: "<x>", Description: revealDetailedHelpDesc()},
				{Keys: "<u>", Description: "Show Used By"},
				{Keys: "<ctrl+d>", Description: "Delete secret"},
				{Keys: "</>", Description: "Filter"},
			},
		},
		{
			Title: "View",
			Items: []helpview.HelpItem{
				{Keys: "<shift+n>", Description: "Order by Name"},
				{Keys: "<shift+i>", Description: "Order by ID"},
				{Keys: "<shift+u>", Description: "Order by Used"},
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
	if len(m.secretsList.Filtered) == 0 {
		return
	}

	// Remember cursor position
	cursorID := ""
	if m.secretsList.Cursor < len(m.secretsList.Filtered) {
		cursorID = m.secretsList.Filtered[m.secretsList.Cursor].ID
	}

	// Sort the filtered list
	switch m.sortField {
	case SortByName:
		sort.Slice(m.secretsList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.secretsList.Filtered[i].Name < m.secretsList.Filtered[j].Name
			}
			return m.secretsList.Filtered[i].Name > m.secretsList.Filtered[j].Name
		})
	case SortByID:
		sort.Slice(m.secretsList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.secretsList.Filtered[i].ID < m.secretsList.Filtered[j].ID
			}
			return m.secretsList.Filtered[i].ID > m.secretsList.Filtered[j].ID
		})
	case SortByUsed:
		sort.Slice(m.secretsList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				if m.secretsList.Filtered[i].Used == m.secretsList.Filtered[j].Used {
					return m.secretsList.Filtered[i].Name < m.secretsList.Filtered[j].Name
				}
				return !m.secretsList.Filtered[i].Used && m.secretsList.Filtered[j].Used
			}
			if m.secretsList.Filtered[i].Used == m.secretsList.Filtered[j].Used {
				return m.secretsList.Filtered[i].Name < m.secretsList.Filtered[j].Name
			}
			return m.secretsList.Filtered[i].Used && !m.secretsList.Filtered[j].Used
		})
	case SortByCreated:
		sort.Slice(m.secretsList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.secretsList.Filtered[i].CreatedAt.Before(m.secretsList.Filtered[j].CreatedAt)
			}
			return m.secretsList.Filtered[i].CreatedAt.After(m.secretsList.Filtered[j].CreatedAt)
		})
	case SortByUpdated:
		sort.Slice(m.secretsList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.secretsList.Filtered[i].UpdatedAt.Before(m.secretsList.Filtered[j].UpdatedAt)
			}
			return m.secretsList.Filtered[i].UpdatedAt.After(m.secretsList.Filtered[j].UpdatedAt)
		})
	case SortByLabels:
		sort.Slice(m.secretsList.Filtered, func(i, j int) bool {
			iLabels := formatLabels(m.secretsList.Filtered[i].Labels)
			jLabels := formatLabels(m.secretsList.Filtered[j].Labels)
			if m.sortAscending {
				return iLabels < jLabels
			}
			return iLabels > jLabels
		})
	}

	// Restore cursor position
	if cursorID != "" {
		for i, s := range m.secretsList.Filtered {
			if s.ID == cursorID {
				m.secretsList.Cursor = i
				return
			}
		}
	}

	m.secretsList.Cursor = 0
	m.secretsList.Viewport.GotoTop()
}
