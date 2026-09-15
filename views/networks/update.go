// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package networksview

import (
	"context"
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/core/primitives/hash"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
	servicesview "github.com/Eldara-Tech/swarmcli/v2/views/services"
	view "github.com/Eldara-Tech/swarmcli/v2/views/view"
	"net/netip"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var networkDriverOptions = []string{"overlay", "bridge", "ipvlan", "macvlan"}

func validateNetworkName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("network name cannot be empty")
	}
	for i, r := range name {
		if i == 0 {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return fmt.Errorf("network name must start with a letter or digit")
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' && r != '-' {
			return fmt.Errorf("network name contains invalid character: %q", r)
		}
	}
	return nil
}

func validateSubnet(prefixStr string, wantIPv6 bool) error {
	prefixStr = strings.TrimSpace(prefixStr)
	if prefixStr == "" {
		return nil
	}
	pfx, err := netip.ParsePrefix(prefixStr)
	if err != nil {
		return fmt.Errorf("invalid subnet CIDR: '%s'", prefixStr)
	}
	if wantIPv6 {
		if !pfx.Addr().Is6() {
			return fmt.Errorf("ipv6 subnet must be an IPv6 CIDR")
		}
	} else {
		if !pfx.Addr().Is4() {
			return fmt.Errorf("ipv4 subnet must be an IPv4 CIDR")
		}
	}
	return nil
}

func validateGateway(addrStr string, wantIPv6 bool) error {
	addrStr = strings.TrimSpace(addrStr)
	if addrStr == "" {
		return nil
	}
	addr, err := netip.ParseAddr(addrStr)
	if err != nil {
		return fmt.Errorf("invalid gateway IP: '%s'", addrStr)
	}
	if wantIPv6 {
		if !addr.Is6() {
			return fmt.Errorf("ipv6 gateway must be an IPv6 address")
		}
	} else {
		if !addr.Is4() {
			return fmt.Errorf("ipv4 gateway must be an IPv4 address")
		}
	}
	return nil
}

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
	// best-effort: assumes single-width runes for typical ASCII docker output
	if maxWidth == 2 {
		return s[:1] + "…"
	}
	return s[:maxWidth-1] + "…"
}

func (m *Model) networkColWidths(totalWidth int) (nameW, driverW, scopeW, usedW, idW int) {
	if totalWidth <= 0 {
		totalWidth = 80
	}

	// Match other views: compute an effective content width (excluding separators)
	// and allocate columns as percentages of that width.
	sepLen := 2
	sepTotal := 4 * sepLen // 5 cols => 4 separators
	effWidth := totalWidth - sepTotal
	if effWidth < 20 {
		effWidth = totalWidth
	}

	// Percent weights (sum=100)
	weights := []int{30, 18, 12, 6, 34} // NAME, DRIVER, SCOPE, USED, ID
	colWidths := make([]int, 5)
	sum := 0
	for i := 0; i < 4; i++ {
		w := (effWidth * weights[i]) / 100
		if w < 1 {
			w = 1
		}
		colWidths[i] = w
		sum += w
	}
	colWidths[4] = effWidth - sum
	if colWidths[4] < 1 {
		colWidths[4] = 1
	}

	// Enforce minimums similar to other views.
	mins := []int{10, 6, 5, 1, 8}
	for i := range colWidths {
		if colWidths[i] < mins[i] {
			colWidths[i] = mins[i]
		}
	}

	// Adjust last column to ensure total equals effWidth.
	sum = 0
	for _, v := range colWidths {
		sum += v
	}
	if sum != effWidth {
		colWidths[4] += effWidth - sum
		if colWidths[4] < 1 {
			colWidths[4] = 1
		}
	}

	return colWidths[0], colWidths[1], colWidths[2], colWidths[3], colWidths[4]
}

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case SpinnerTickMsg:
		m.spinner++
		// Refresh the list view only if there are unknown UsedKnown items.
		need := false
		for _, it := range m.networksList.Items {
			if !it.UsedKnown {
				need = true
				break
			}
		}
		if need {
			m.networksList.Viewport.SetContent(m.networksList.View())
		}
		return m.spinnerTickCmd()

	case usedStatusUpdatedMsg:
		l().Infof("NetworksView: Received used status updates for %d networks", len(msg))
		selectedID := ""
		if m.networksList.Cursor >= 0 && m.networksList.Cursor < len(m.networksList.Filtered) {
			selectedID = m.networksList.Filtered[m.networksList.Cursor].ID
		}
		for i := range m.networksList.Items {
			id := m.networksList.Items[i].ID
			if used, ok := msg[id]; ok {
				m.networksList.Items[i].Used = used
				m.networksList.Items[i].UsedKnown = true
			}
		}
		m.networksList.ApplyFilter()
		m.applySorting()
		if selectedID != "" {
			for i := range m.networksList.Filtered {
				if m.networksList.Filtered[i].ID == selectedID {
					m.networksList.Cursor = i
					break
				}
			}
			m.networksList.Viewport.SetContent(m.networksList.View())
		}
		return nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.networksList.Viewport.Width = msg.Width
		m.networksList.Viewport.Height = msg.Height
		m.networksList.SetOuterSize(msg.Width, msg.Height)

		if m.usedByViewActive {
			m.usedByList.Viewport.Width = msg.Width
			m.usedByList.Viewport.Height = msg.Height
			m.usedByList.SetOuterSize(msg.Width, msg.Height)
		}

		if m.firstResize {
			m.networksList.Viewport.YOffset = 0
			m.firstResize = false
		} else if m.networksList.Cursor == 0 {
			m.networksList.Viewport.YOffset = 0
		}
		return nil

	case NetworksLoadedMsg:
		l().Infof("NetworksView: Received NetworksLoadedMsg with %d networks", len(msg.Networks))
		if msg.Err != nil {
			// If this was a background refresh and we already have data, keep the
			// current view and continue polling.
			if m.state == stateReady && len(m.networksList.Items) > 0 {
				l().Warnf("NetworksView: background refresh failed: %v", msg.Err)
				m.showToast("Refresh failed (will retry)")
				return nil
			}

			// Initial load failed: switch to error state and show dialog.
			m.state = stateError
			m.err = msg.Err
			m.errorDialogActive = true
			return nil
		}

		// Remember currently selected network (by ID) so we can restore cursor after refresh.
		selectedID := ""
		if !m.resetCursorOnNextLoad && m.networksList.Cursor < len(m.networksList.Filtered) {
			selectedID = m.networksList.Filtered[m.networksList.Cursor].ID
		}

		// Update the hash with new data
		type stableNetwork struct {
			ID     string
			Name   string
			Driver string
			Scope  string
		}
		stableNetworks := make([]stableNetwork, len(msg.Networks))
		for i, n := range msg.Networks {
			stableNetworks[i] = stableNetwork{
				ID:     n.ID,
				Name:   n.Name,
				Driver: n.Driver,
				Scope:  n.Scope,
			}
		}
		var err error
		m.lastSnapshot, err = hash.Compute(stableNetworks)
		if err != nil {
			l().Errorf("NetworksView: Error computing hash: %v", err)
		}

		// Preserve previous Used and UsedKnown state where possible to avoid UI "blinking".
		prevUsed := make(map[string]bool, len(m.networksList.Items))
		prevKnown := make(map[string]bool, len(m.networksList.Items))
		for _, it := range m.networksList.Items {
			prevUsed[it.ID] = it.Used
			prevKnown[it.ID] = it.UsedKnown
		}

		items := make([]networkItem, len(msg.Networks))
		for i, n := range msg.Networks {
			used := false
			known := false
			if val, ok := prevUsed[n.ID]; ok {
				used = val
			}
			if k, ok := prevKnown[n.ID]; ok {
				known = k
			}
			n.Used = used
			n.UsedKnown = known
			items[i] = n
		}

		m.networks = items
		m.networksList.Items = items
		m.setRenderItem()
		m.networksList.ApplyFilter()
		m.applySorting()

		if m.resetCursorOnNextLoad {
			m.networksList.Cursor = 0
			m.networksList.Viewport.YOffset = 0
			m.networksList.Viewport.SetContent(m.networksList.View())
			m.resetCursorOnNextLoad = false
		}

		// Restore cursor to previously selected network if it still exists.
		if selectedID != "" {
			for i, n := range m.networksList.Filtered {
				if n.ID == selectedID {
					m.networksList.Cursor = i
					break
				}
			}
			m.networksList.Viewport.SetContent(m.networksList.View())
		}

		m.state = stateReady
		l().Info("NetworksView: Network list updated (used status pending)")
		return m.computeNetworkUsedCmd(items)

	case TickMsg:
		if msg.Gen != m.pollGen {
			return nil // a leftover from an earlier entry — see OnEnter
		}
		l().Infof("NetworksView: Received TickMsg, state=%v, visible=%v", m.state, m.visible)
		if m.visible && m.state == stateReady && !m.confirmDialog.Visible && !m.loadingView.Visible() {
			return tea.Batch(
				m.checkNetworksCmd(m.lastSnapshot),
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

	case NetworkDeletedMsg:
		if msg.Err != nil {
			m.errorDialogActive = true
			// If Docker refuses to delete due to active endpoints, give a clearer hint.
			errStr := msg.Err.Error()
			if strings.Contains(errStr, "has active endpoints") {
				name := ""
				if m.networkToDelete != nil {
					name = m.networkToDelete.Name
				}
				if name != "" {
					m.err = fmt.Errorf(
						"cannot delete network '%s': it has active endpoints.\n\n"+
							"This usually means one or more containers/services are still attached.\n"+
							"- Press 'u' (Used By) to see services using it\n"+
							"- Or disconnect endpoints / stop services, then retry\n\n"+
							"Docker says: %s",
						name,
						errStr,
					)
				} else {
					m.err = fmt.Errorf(
						"cannot delete network: it has active endpoints.\n\n"+
							"Detach containers/services from the network and retry.\n\n"+
							"Docker says: %s",
						errStr,
					)
				}
			} else {
				m.err = msg.Err
			}
			return nil
		}
		l().Info("Network deleted successfully")
		if m.networkToDelete != nil && m.networkToDelete.Name != "" {
			m.showToast("Deleted network:\n" + m.networkToDelete.Name)
		} else {
			m.showToast("Network deleted")
		}
		return m.loadNetworksCmd()

	case NetworksPrunedMsg:
		if msg.Err != nil {
			m.errorDialogActive = true
			m.err = msg.Err
			return nil
		}
		if len(msg.Deleted) == 0 {
			m.showToast("No standalone networks to prune")
		} else {
			// Show up to a few names to avoid shrinking the list area too much.
			maxShow := 6
			lines := []string{"Deleted Networks:"}
			for i := 0; i < len(msg.Deleted) && i < maxShow; i++ {
				lines = append(lines, "- "+msg.Deleted[i])
			}
			if len(msg.Deleted) > maxShow {
				lines = append(lines, fmt.Sprintf("...and %d more", len(msg.Deleted)-maxShow))
			}
			m.showToast(strings.Join(lines, "\n"))
		}
		l().Info("Networks pruned successfully")
		return m.loadNetworksCmd()

	case NetworkCreatedMsg:
		if msg.Err != nil {
			// If the create dialog is open (or we were in the middle of submitting),
			// return the user back to the dialog so they can edit values.
			if m.createDialogActive {
				m.createDialogStep = "basic"
				m.createDialogError = msg.Err.Error()
				m.createInputFocus = 0
				m.createNameInput.Focus()
				return nil
			}
			// Fallback: show the global error dialog.
			m.errorDialogActive = true
			m.err = msg.Err
			return nil
		}
		// Success: close the dialog if it was open.
		m.createDialogActive = false
		m.createDialogStep = ""
		m.createDialogError = ""
		lines := []string{"Created network:", msg.Name}
		if len(msg.Warnings) > 0 {
			lines = append(lines, "", "Warnings:")
			for _, w := range msg.Warnings {
				lines = append(lines, "- "+w)
			}
		}
		m.showToast(strings.Join(lines, "\n"))
		return m.loadNetworksCmd()

	case UsedByLoadedMsg:
		if msg.Err != nil {
			m.errorDialogActive = true
			m.err = msg.Err
			return nil
		}

		// Create viewport for used-by list
		usedByVp := m.networksList.Viewport
		usedByVp.SetContent("")

		m.usedByList = filterlist.FilterableList[usedByItem]{
			Viewport: usedByVp,
			Match: func(item usedByItem, query string) bool {
				q := strings.ToLower(query)
				return strings.Contains(strings.ToLower(item.StackName), q) ||
					strings.Contains(strings.ToLower(item.ServiceName), q)
			},
			Header: &filterlist.HeaderConfig{
				Columns: []filterlist.ColumnDef{
					{Label: "STACK"}, {Label: "SERVICE"},
				},
				ColWidthsFunc: func(width int) []int {
					return []int{32, 50}
				},
			},
			Footer: &filterlist.FooterConfig{
				ItemLabel: "Service",
				Override: func(cursor, filteredCount int, mode filterlist.ModeType, query string) string {
					if mode == filterlist.ModeSearching {
						return ui.StatusBarStyle.Render(fmt.Sprintf("Filter: %s", query))
					}
					if filteredCount == 0 {
						return ui.StatusBarStyle.Render("No services using this network")
					}
					if query != "" {
						return ui.StatusBarStyle.Render(fmt.Sprintf("%d/%d (filtered from %d)", cursor+1, filteredCount, len(m.usedByList.Items)))
					}
					return ui.StatusBarStyle.Render(fmt.Sprintf("%d/%d", cursor+1, len(m.usedByList.Items)))
				},
			},
		}
		m.usedByList.SetOuterSize(m.width, m.height)

		m.usedByList.Items = msg.Services
		m.setUsedByRenderItem()
		m.usedByList.ApplyFilter()
		m.usedByViewActive = true
		return nil

	case tea.KeyMsg:
		// Handle create dialog
		if m.createDialogActive {
			return m.handleCreateDialogKeys(msg)
		}

		// Handle used-by view
		if m.usedByViewActive {
			return m.handleUsedByViewKeys(msg)
		}

		// Handle error dialog
		if m.errorDialogActive {
			if msg.String() == "enter" || msg.String() == "esc" {
				m.errorDialogActive = false
				return nil
			}
			return nil
		}

		// Handle confirm dialog
		if m.confirmDialog.Visible {
			switch msg.String() {
			case "y":
				m.confirmDialog.Visible = false
				return m.executeConfirmedAction()
			case "n", "esc":
				m.confirmDialog.Visible = false
				m.pendingAction = ""
				return nil
			}
			return nil
		}

		// Handle regular navigation and commands
		return m.handleNormalKeys(msg)
	}

	return nil
}

// ClickRow selects the row drawn on the given content line (view.RowClicker)
// in whichever list is on screen, the used-by list while it is open, and
// refreshes it the way the arrow keys do.
func (m *Model) ClickRow(line int) bool {
	if m.usedByViewActive {
		if !m.usedByList.SelectLine(line) {
			return false
		}
		m.usedByList.Viewport.SetContent(m.usedByList.View())
		return true
	}
	if !m.networksList.SelectLine(line) {
		return false
	}
	m.networksList.Viewport.SetContent(m.networksList.View())
	return true
}

func (m *Model) handleUsedByViewKeys(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.usedByViewActive = false
		return nil
	case "up", "k":
		if m.usedByList.Cursor > 0 {
			m.usedByList.Cursor--
			m.usedByList.Viewport.SetContent(m.usedByList.View())
		}
	case "down", "j":
		if m.usedByList.Cursor < len(m.usedByList.Filtered)-1 {
			m.usedByList.Cursor++
			m.usedByList.Viewport.SetContent(m.usedByList.View())
		}
	case "pgup":
		page := m.usedByList.Viewport.Height
		if page < 1 {
			page = 10
		}
		m.usedByList.Cursor -= page
		if m.usedByList.Cursor < 0 {
			m.usedByList.Cursor = 0
		}
		m.usedByList.Viewport.SetContent(m.usedByList.View())
	case "pgdown":
		page := m.usedByList.Viewport.Height
		if page < 1 {
			page = 10
		}
		m.usedByList.Cursor += page
		if m.usedByList.Cursor >= len(m.usedByList.Filtered) {
			m.usedByList.Cursor = len(m.usedByList.Filtered) - 1
		}
		m.usedByList.Viewport.SetContent(m.usedByList.View())
	case "enter":
		// Navigate to the services view (stack-scoped) and pre-filter to the service.
		if m.usedByList.Cursor < len(m.usedByList.Filtered) {
			item := m.usedByList.Filtered[m.usedByList.Cursor]
			return func() tea.Msg {
				payload := map[string]interface{}{
					"selectServiceName": item.ServiceName,
				}
				if item.StackName == "" || item.StackName == "N/A" {
					payload["noStack"] = true
				} else {
					payload["stackName"] = item.StackName
				}
				return view.NavigateToMsg{
					ViewName: servicesview.ViewName,
					Payload:  payload,
				}
			}
		}
	case "/":
		m.usedByList.Mode = filterlist.ModeSearching
		m.usedByList.Query = ""
		m.setUsedByRenderItem()
	}
	return nil
}

func (m *Model) handleCreateDialogKeys(msg tea.KeyMsg) tea.Cmd {
	// Keep textinput focus in sync
	if m.createDialogStep == "basic" {
		// Blur all first
		m.createNameInput.Blur()
		m.createIPv4Subnet.Blur()
		m.createIPv4Gateway.Blur()
		m.createIPv6Subnet.Blur()
		m.createIPv6Gateway.Blur()
		// Focus the active one
		switch m.createInputFocus {
		case 0:
			m.createNameInput.Focus()
		case 2:
			m.createIPv4Subnet.Focus()
		case 3:
			m.createIPv4Gateway.Focus()
		case 5:
			m.createIPv6Subnet.Focus()
		case 6:
			m.createIPv6Gateway.Focus()
		}
	}

	switch msg.String() {
	case "esc":
		m.createDialogActive = false
		m.createDialogStep = ""
		m.createDialogError = ""
		m.createInputFocus = 0
		m.createNameInput.Blur()
		m.createIPv4Subnet.Blur()
		m.createIPv4Gateway.Blur()
		m.createIPv6Subnet.Blur()
		m.createIPv6Gateway.Blur()
		return nil
	case "enter":
		if m.createDialogStep == "review" {
			name := strings.TrimSpace(m.createNameInput.Value())
			driver := networkDriverOptions[m.createDriverIndex]
			ipv4Subnet := strings.TrimSpace(m.createIPv4Subnet.Value())
			ipv4Gateway := strings.TrimSpace(m.createIPv4Gateway.Value())
			ipv6Subnet := strings.TrimSpace(m.createIPv6Subnet.Value())
			ipv6Gateway := strings.TrimSpace(m.createIPv6Gateway.Value())
			enableIPv6 := m.createEnableIPv6
			// Keep the dialog open while the create request is in flight.
			m.createDialogStep = "creating"
			m.createDialogError = ""
			m.createNameInput.Blur()
			m.createIPv4Subnet.Blur()
			m.createIPv4Gateway.Blur()
			m.createIPv6Subnet.Blur()
			m.createIPv6Gateway.Blur()
			return m.createNetworkCmd(name, driver, m.createAttachable, m.createInternal, ipv4Subnet, ipv4Gateway, enableIPv6, ipv6Subnet, ipv6Gateway)
		}

		name := strings.TrimSpace(m.createNameInput.Value())
		if err := validateNetworkName(name); err != nil {
			m.createDialogError = err.Error()
			return nil
		}

		ipv4Subnet := strings.TrimSpace(m.createIPv4Subnet.Value())
		ipv4Gateway := strings.TrimSpace(m.createIPv4Gateway.Value())
		if ipv4Gateway != "" && ipv4Subnet == "" {
			m.createDialogError = "IPv4 gateway requires an IPv4 subnet"
			return nil
		}
		if err := validateSubnet(ipv4Subnet, false); err != nil {
			m.createDialogError = err.Error()
			return nil
		}
		if err := validateGateway(ipv4Gateway, false); err != nil {
			m.createDialogError = err.Error()
			return nil
		}

		ipv6Subnet := strings.TrimSpace(m.createIPv6Subnet.Value())
		ipv6Gateway := strings.TrimSpace(m.createIPv6Gateway.Value())
		if (ipv6Subnet != "" || ipv6Gateway != "") && !m.createEnableIPv6 {
			m.createDialogError = "Enable IPv6 to set IPv6 subnet/gateway"
			return nil
		}
		if ipv6Gateway != "" && ipv6Subnet == "" {
			m.createDialogError = "IPv6 gateway requires an IPv6 subnet"
			return nil
		}
		if err := validateSubnet(ipv6Subnet, true); err != nil {
			m.createDialogError = err.Error()
			return nil
		}
		if err := validateGateway(ipv6Gateway, true); err != nil {
			m.createDialogError = err.Error()
			return nil
		}

		m.createDialogError = ""
		m.createDialogStep = "review"
		m.createNameInput.Blur()
		m.createIPv4Subnet.Blur()
		m.createIPv4Gateway.Blur()
		m.createIPv6Subnet.Blur()
		m.createIPv6Gateway.Blur()
		return nil
	case "tab", "shift+tab":
		if m.createDialogStep != "basic" {
			return nil
		}
		if msg.String() == "tab" {
			m.createInputFocus = (m.createInputFocus + 1) % 9
		} else {
			m.createInputFocus = (m.createInputFocus + 8) % 9
		}
		m.createDialogError = ""
		return nil
	case " ":
		if m.createDialogStep != "basic" {
			return nil
		}
		if m.createInputFocus == 4 {
			m.createEnableIPv6 = !m.createEnableIPv6
			return nil
		}
		if m.createInputFocus == 7 {
			m.createInternal = !m.createInternal
			return nil
		}
		if m.createInputFocus == 8 {
			m.createAttachable = !m.createAttachable
			return nil
		}
	}

	// Review step: allow going back
	if m.createDialogStep == "review" {
		switch msg.String() {
		case "b", "backspace":
			m.createDialogStep = "basic"
			m.createDialogError = ""
			m.createInputFocus = 0
			m.createNameInput.Focus()
			return nil
		}
		return nil
	}

	// Driver selection when focused
	if m.createInputFocus == 1 {
		switch msg.String() {
		case "left", "h", "up", "k":
			m.createDriverIndex--
			if m.createDriverIndex < 0 {
				m.createDriverIndex = 0
			}
			if networkDriverOptions[m.createDriverIndex] == "overlay" {
				m.createAttachable = true
			}
			return nil
		case "right", "l", "down", "j":
			m.createDriverIndex++
			if m.createDriverIndex >= len(networkDriverOptions) {
				m.createDriverIndex = len(networkDriverOptions) - 1
			}
			if networkDriverOptions[m.createDriverIndex] == "overlay" {
				m.createAttachable = true
			}
			return nil
		}
	}

	// Delegate typing to name input when focused
	if m.createDialogStep == "basic" {
		var cmd tea.Cmd
		switch m.createInputFocus {
		case 0:
			m.createNameInput, cmd = m.createNameInput.Update(msg)
		case 2:
			m.createIPv4Subnet, cmd = m.createIPv4Subnet.Update(msg)
		case 3:
			m.createIPv4Gateway, cmd = m.createIPv4Gateway.Update(msg)
		case 5:
			m.createIPv6Subnet, cmd = m.createIPv6Subnet.Update(msg)
		case 6:
			m.createIPv6Gateway, cmd = m.createIPv6Gateway.Update(msg)
		}
		if cmd != nil {
			m.createDialogError = ""
			return cmd
		}
	}

	return nil
}

func (m *Model) handleNormalKeys(msg tea.KeyMsg) tea.Cmd {
	// Clear active filter with ESC
	if msg.Type == tea.KeyEsc && m.networksList.Query != "" {
		m.networksList.Query = ""
		m.networksList.ApplyFilter()
		m.networksList.Cursor = 0
		m.networksList.Viewport.GotoTop()
		m.setRenderItem()
		m.networksList.Viewport.SetContent(m.networksList.View())
		return nil
	}

	switch msg.String() {
	case "esc":
		// Networks is a root view, no back navigation
		return nil
	case "N": // Shift+N: Sort by Name
		if m.sortField == SortByName {
			m.sortAscending = !m.sortAscending
		} else {
			m.sortField = SortByName
			m.sortAscending = true
		}
		m.applySorting()
		return nil
	case "I": // Shift+I: Sort by ID
		if m.sortField == SortByID {
			m.sortAscending = !m.sortAscending
		} else {
			m.sortField = SortByID
			m.sortAscending = true
		}
		m.applySorting()
		return nil
	case "D": // Shift+D: Sort by Driver
		if m.sortField == SortByDriver {
			m.sortAscending = !m.sortAscending
		} else {
			m.sortField = SortByDriver
			m.sortAscending = true
		}
		m.applySorting()
		return nil
	case "S": // Shift+S: Sort by Scope
		if m.sortField == SortByScope {
			m.sortAscending = !m.sortAscending
		} else {
			m.sortField = SortByScope
			m.sortAscending = true
		}
		m.applySorting()
		return nil
	case "U": // Shift+U: Sort by Used
		if m.sortField == SortByUsed {
			m.sortAscending = !m.sortAscending
		} else {
			m.sortField = SortByUsed
			m.sortAscending = true
		}
		m.applySorting()
		return nil
	case "C": // Shift+C: Sort by Created
		if m.sortField == SortByCreated {
			m.sortAscending = !m.sortAscending
		} else {
			m.sortField = SortByCreated
			m.sortAscending = true
		}
		m.applySorting()
		return nil
	case "up", "k":
		if m.networksList.Cursor > 0 {
			m.networksList.Cursor--
			m.networksList.Viewport.SetContent(m.networksList.View())
		}
	case "down", "j":
		if m.networksList.Cursor < len(m.networksList.Filtered)-1 {
			m.networksList.Cursor++
			m.networksList.Viewport.SetContent(m.networksList.View())
		}
	case "pgup":
		page := m.networksList.Viewport.Height
		if page < 1 {
			page = 10
		}
		m.networksList.Cursor -= page
		if m.networksList.Cursor < 0 {
			m.networksList.Cursor = 0
		}
		m.networksList.Viewport.SetContent(m.networksList.View())
	case "pgdown":
		page := m.networksList.Viewport.Height
		if page < 1 {
			page = 10
		}
		m.networksList.Cursor += page
		if m.networksList.Cursor >= len(m.networksList.Filtered) {
			m.networksList.Cursor = len(m.networksList.Filtered) - 1
		}
		m.networksList.Viewport.SetContent(m.networksList.View())
	case "/":
		m.networksList.Mode = filterlist.ModeSearching
		m.networksList.Query = ""
		m.setRenderItem()
	case "c":
		m.createDialogActive = true
		m.createDialogStep = "basic"
		m.createDialogError = ""
		m.createInputFocus = 0
		m.createNameInput.SetValue("")
		m.createIPv4Subnet.SetValue("")
		m.createIPv4Gateway.SetValue("")
		m.createEnableIPv6 = false
		m.createIPv6Subnet.SetValue("")
		m.createIPv6Gateway.SetValue("")
		m.createNameInput.Focus()
		m.createDriverIndex = 0 // overlay
		m.createInternal = false
		m.createAttachable = true
		return nil
	case "i":
		// Inspect network
		if len(m.networksList.Filtered) == 0 {
			return nil
		}
		selected := m.networksList.Filtered[m.networksList.Cursor]
		return m.inspectNetworkCmd(selected.ID)
	case "u":
		// Show used by
		if len(m.networksList.Filtered) == 0 {
			return nil
		}
		selected := m.networksList.Filtered[m.networksList.Cursor]
		m.usedByNetworkName = selected.Name
		return m.loadUsedByCmd(selected.ID, selected.Name)
	case "ctrl+d":
		// Delete network
		if len(m.networksList.Filtered) == 0 {
			return nil
		}
		selected := m.networksList.Filtered[m.networksList.Cursor]
		m.networkToDelete = &selected
		m.pendingAction = "delete"
		m.confirmDialog.Message = fmt.Sprintf("Delete network '%s'?", selected.Name)
		m.confirmDialog.Visible = true
		return nil
	case "ctrl+u":
		// Prune networks
		m.pendingAction = "prune"
		m.confirmDialog.Message = "Prune all unused networks?"
		m.confirmDialog.Visible = true
		return nil
	}
	return nil
}

func (m *Model) executeConfirmedAction() tea.Cmd {
	switch m.pendingAction {
	case "delete":
		if m.networkToDelete != nil {
			return m.deleteNetworkCmd(m.networkToDelete.ID)
		}
	case "prune":
		return m.pruneNetworksCmd()
	}
	m.pendingAction = ""
	return nil
}

func (m *Model) applySorting() {
	if len(m.networksList.Filtered) == 0 {
		return
	}

	// Remember cursor position
	cursorID := ""
	if m.networksList.Cursor < len(m.networksList.Filtered) {
		cursorID = m.networksList.Filtered[m.networksList.Cursor].ID
	}

	switch m.sortField {
	case SortByName:
		sort.Slice(m.networksList.Filtered, func(i, j int) bool {
			ai := strings.ToLower(m.networksList.Filtered[i].Name)
			aj := strings.ToLower(m.networksList.Filtered[j].Name)
			if m.sortAscending {
				return ai < aj
			}
			return ai > aj
		})
	case SortByID:
		sort.Slice(m.networksList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.networksList.Filtered[i].ID < m.networksList.Filtered[j].ID
			}
			return m.networksList.Filtered[i].ID > m.networksList.Filtered[j].ID
		})
	case SortByDriver:
		sort.Slice(m.networksList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.networksList.Filtered[i].Driver < m.networksList.Filtered[j].Driver
			}
			return m.networksList.Filtered[i].Driver > m.networksList.Filtered[j].Driver
		})
	case SortByScope:
		sort.Slice(m.networksList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.networksList.Filtered[i].Scope < m.networksList.Filtered[j].Scope
			}
			return m.networksList.Filtered[i].Scope > m.networksList.Filtered[j].Scope
		})
	case SortByUsed:
		sort.Slice(m.networksList.Filtered, func(i, j int) bool {
			// Unknown values treated as false but keep stable ordering via name
			if m.networksList.Filtered[i].Used == m.networksList.Filtered[j].Used {
				return strings.ToLower(m.networksList.Filtered[i].Name) < strings.ToLower(m.networksList.Filtered[j].Name)
			}
			if m.sortAscending {
				return !m.networksList.Filtered[i].Used && m.networksList.Filtered[j].Used
			}
			return m.networksList.Filtered[i].Used && !m.networksList.Filtered[j].Used
		})
	case SortByCreated:
		sort.Slice(m.networksList.Filtered, func(i, j int) bool {
			if m.sortAscending {
				return m.networksList.Filtered[i].CreatedAt.Before(m.networksList.Filtered[j].CreatedAt)
			}
			return m.networksList.Filtered[i].CreatedAt.After(m.networksList.Filtered[j].CreatedAt)
		})
	}

	// Restore cursor position
	if cursorID != "" {
		for i, n := range m.networksList.Filtered {
			if n.ID == cursorID {
				m.networksList.Cursor = i
				break
			}
		}
	}

	m.networksList.Viewport.SetContent(m.networksList.View())
}

func (m *Model) setRenderItem() {
	itemStyle := ui.ListItemStyle

	m.networksList.RenderItem = func(item networkItem, selected bool, colWidth int) string {
		style := itemStyle
		if selected {
			style = ui.ListSelectedStyle
		}

		colWidths := m.networksList.ColWidths()
		if len(colWidths) < 5 {
			return item.Name
		}
		nameWidth, driverWidth, scopeWidth, usedWidth, idWidth := colWidths[0], colWidths[1], colWidths[2], colWidths[3], colWidths[4]
		// Match header style: keep a small left padding for the first column so
		// it doesn't touch the frame border.
		innerNameWidth := nameWidth
		if innerNameWidth > 1 {
			innerNameWidth--
		}
		name := " " + truncateWithEllipsis(item.Name, innerNameWidth)
		driver := truncateWithEllipsis(item.Driver, driverWidth)
		scope := truncateWithEllipsis(item.Scope, scopeWidth)

		usedText := " "
		if !item.UsedKnown {
			usedText = ui.SpinnerCharAt(m.spinner)
		} else if item.Used {
			usedText = "●"
		}

		id := truncateWithEllipsis(item.ID, idWidth)

		sep := strings.Repeat(" ", 2)
		line := fmt.Sprintf("%-*s%s%-*s%s%-*s%s%-*s%s%-*s",
			nameWidth, name,
			sep,
			driverWidth, driver,
			sep,
			scopeWidth, scope,
			sep,
			usedWidth, usedText,
			sep,
			idWidth, id,
		)

		return style.Render(line)
	}
	m.networksList.Viewport.SetContent(m.networksList.View())
}

func (m *Model) setUsedByRenderItem() {
	m.usedByList.RenderItem = func(item usedByItem, selected bool, colWidth int) string {
		style := ui.ListItemStyle
		if selected {
			style = ui.ListSelectedStyle
		}

		stackWidth := 30
		serviceWidth := 50

		stack := item.StackName
		if len(stack) > stackWidth {
			stack = stack[:stackWidth-3] + "..."
		}

		service := item.ServiceName
		if len(service) > serviceWidth {
			service = service[:serviceWidth-3] + "..."
		}

		line := fmt.Sprintf("%-*s  %-*s", stackWidth, stack, serviceWidth, service)
		return style.Render(line)
	}
	m.usedByList.Viewport.SetContent(m.usedByList.View())
}

// checkNetworksCmd checks if networks have changed by comparing hashes
func (m *Model) checkNetworksCmd(lastHash uint64) tea.Cmd {
	networkOps := m.deps.Networks
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		nets, err := networkOps.ListNetworks(ctx)
		if err != nil {
			return NetworksLoadedMsg{Err: err}
		}

		networks := make([]networkItem, 0, len(nets))
		for _, net := range nets {
			networks = append(networks, networkItem{
				Name:      net.Name,
				ID:        net.ID,
				Driver:    net.Driver,
				Scope:     net.Scope,
				CreatedAt: net.Created,
				Ingress:   net.Ingress,
				Used:      false,
				UsedKnown: false,
			})
		}

		// Compute new hash
		type stableNetwork struct {
			ID     string
			Name   string
			Driver string
			Scope  string
		}
		stableNetworks := make([]stableNetwork, len(networks))
		for i, n := range networks {
			stableNetworks[i] = stableNetwork{
				ID:     n.ID,
				Name:   n.Name,
				Driver: n.Driver,
				Scope:  n.Scope,
			}
		}
		newHash, err := hash.Compute(stableNetworks)
		if err != nil {
			l().Errorf("Error computing hash: %v", err)
			return PollRetryMsg{}
		}

		// Only reload if hash changed
		if newHash != lastHash {
			l().Info("Networks changed, reloading")
			return NetworksLoadedMsg{Networks: networks}
		}
		return PollRetryMsg{}
	}
}
