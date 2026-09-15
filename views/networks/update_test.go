// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package networksview

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/require"
)

// --- State machine tests ---

func TestUpdate_NetworksLoaded_SetsReady(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("alpha", "beta"))
	require.Equal(t, stateReady, m.state)
	require.Len(t, m.networks, 2)
	require.Len(t, m.networksList.Items, 2)
}

func TestUpdate_NetworksLoaded_Error_Initial(t *testing.T) {
	m := testModel()
	m.Update(NetworksLoadedMsg{Err: fmt.Errorf("connection refused")})
	require.Equal(t, stateError, m.state)
	require.True(t, m.errorDialogActive)
}

func TestUpdate_NetworksLoaded_Error_BackgroundRefresh(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("net1"))
	// Now simulate a background refresh error
	m.Update(NetworksLoadedMsg{Err: fmt.Errorf("timeout")})
	// Should stay in ready state with existing data
	require.Equal(t, stateReady, m.state)
	require.False(t, m.errorDialogActive)
	require.Len(t, m.networksList.Items, 1)
}

func TestUpdate_NetworkDeletedMsg_Success(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("net1"))
	cmd := m.Update(NetworkDeletedMsg{Err: nil})
	require.NotNil(t, cmd) // reloads
}

func TestUpdate_NetworkDeletedMsg_Error(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("net1"))
	m.Update(NetworkDeletedMsg{Err: fmt.Errorf("has active endpoints")})
	require.True(t, m.errorDialogActive)
}

func TestUpdate_NetworkDeletedMsg_ActiveEndpointsHint(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("net1"))
	net := m.networksList.Items[0]
	m.networkToDelete = &net
	m.Update(NetworkDeletedMsg{Err: fmt.Errorf("has active endpoints")})
	require.True(t, m.errorDialogActive)
	require.Contains(t, m.err.Error(), "net1")
	require.Contains(t, m.err.Error(), "active endpoints")
}

func TestUpdate_NetworksPrunedMsg_Success(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("net1"))
	cmd := m.Update(NetworksPrunedMsg{Deleted: []string{"unused1"}, Err: nil})
	require.NotNil(t, cmd) // reloads
	require.Contains(t, m.toastMessage, "unused1")
}

func TestUpdate_NetworksPrunedMsg_NoneDeleted(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("net1"))
	m.Update(NetworksPrunedMsg{Deleted: nil, Err: nil})
	require.Contains(t, m.toastMessage, "No standalone")
}

func TestUpdate_NetworksPrunedMsg_Error(t *testing.T) {
	m := testModel()
	m.Update(NetworksPrunedMsg{Err: fmt.Errorf("prune failed")})
	require.True(t, m.errorDialogActive)
}

func TestUpdate_NetworkCreatedMsg_Success(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "creating"
	cmd := m.Update(NetworkCreatedMsg{Name: "newnet", ID: "new-id", Err: nil})
	require.False(t, m.createDialogActive)
	require.Contains(t, m.toastMessage, "newnet")
	require.NotNil(t, cmd) // reloads
}

func TestUpdate_NetworkCreatedMsg_Error_DialogOpen(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "creating"
	m.Update(NetworkCreatedMsg{Err: fmt.Errorf("already exists")})
	require.True(t, m.createDialogActive)
	require.Equal(t, "basic", m.createDialogStep)
	require.Contains(t, m.createDialogError, "already exists")
}

func TestUpdate_NetworkCreatedMsg_Error_NoDialog(t *testing.T) {
	m := testModel()
	m.Update(NetworkCreatedMsg{Err: fmt.Errorf("fail")})
	require.True(t, m.errorDialogActive)
}

func TestUpdate_UsedByLoadedMsg_Success(t *testing.T) {
	m := testModel()
	m.networksList.Viewport.Width = 80
	m.networksList.Viewport.Height = 20
	m.Update(UsedByLoadedMsg{Services: []usedByItem{{StackName: "s1", ServiceName: "svc1"}}})
	require.True(t, m.usedByViewActive)
	require.Len(t, m.usedByList.Items, 1)
}

func TestUpdate_UsedByLoadedMsg_Error(t *testing.T) {
	m := testModel()
	m.Update(UsedByLoadedMsg{Err: fmt.Errorf("fail")})
	require.True(t, m.errorDialogActive)
	require.False(t, m.usedByViewActive)
}

func TestUpdate_UsedStatusUpdatedMsg(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("net1", "net2"))
	m.Update(usedStatusUpdatedMsg{"id-net1": true, "id-net2": false})
	require.True(t, m.networksList.Items[0].Used)
	require.True(t, m.networksList.Items[0].UsedKnown)
	require.False(t, m.networksList.Items[1].Used)
	require.True(t, m.networksList.Items[1].UsedKnown)
}

func TestUpdate_WindowSizeMsg(t *testing.T) {
	m := testModel()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	require.Equal(t, 120, m.networksList.Viewport.Width)
	require.Equal(t, 40, m.networksList.Viewport.Height)
}

func TestUpdate_TickMsg(t *testing.T) {
	m := testModel()
	m.visible = true
	m.state = stateReady
	cmd := m.Update(TickMsg{Gen: m.pollGen})
	require.NotNil(t, cmd)
}

func TestUpdate_SpinnerTickMsg(t *testing.T) {
	m := testModel()
	old := m.spinner
	m.Update(SpinnerTickMsg(time.Now()))
	require.Equal(t, old+1, m.spinner)
}

// --- Key routing tests ---

func TestKey_C_OpensCreateDialog(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("net1"))
	m.Update(key("c"))
	require.True(t, m.createDialogActive)
	require.Equal(t, "basic", m.createDialogStep)
}

func TestKey_CtrlD_OpensConfirmDialog(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("mynet"))
	m.Update(key("ctrl+d"))
	require.True(t, m.confirmDialog.Visible)
	require.Contains(t, m.confirmDialog.Message, "mynet")
	require.Equal(t, "delete", m.pendingAction)
}

func TestKey_CtrlU_OpensPruneConfirm(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("net1"))
	m.Update(key("ctrl+u"))
	require.True(t, m.confirmDialog.Visible)
	require.Equal(t, "prune", m.pendingAction)
}

func TestKey_I_NavigatesToInspectView(t *testing.T) {
	mock := noopNetworkOps()
	mock.inspectNetworkFn = func(_ context.Context, networkID string) (network.Inspect, error) {
		return network.Inspect{Name: "mynet", ID: networkID, Driver: "overlay", Scope: "swarm"}, nil
	}
	m := testModel(func(m *Model) { m.deps.Networks = mock })
	loadNetworks(m, fakeNetworks("mynet"))
	cmd := m.Update(key("i"))
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "inspect", nav.ViewName)
}

func TestKey_U_OpensUsedByView(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("mynet"))
	cmd := m.Update(key("u"))
	require.NotNil(t, cmd)
}

// The "?" key is routed by the app, not by this view — see app.Model.openHelp
// and its tests. What the view still owns is the content the app asks it for.
func TestHelpContent(t *testing.T) {
	m := testModel()
	require.NotEmpty(t, m.HelpContent())
}

func TestKey_ErrorDialog_Dismiss(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("net1"))
	m.errorDialogActive = true
	m.err = fmt.Errorf("boom")
	m.Update(key("enter"))
	require.False(t, m.errorDialogActive)
}

func TestKey_ErrorDialog_EscDismiss(t *testing.T) {
	m := testModel()
	m.errorDialogActive = true
	m.err = fmt.Errorf("boom")
	m.Update(key("esc"))
	require.False(t, m.errorDialogActive)
}

// --- Confirm dialog tests ---

func TestConfirm_Delete_Y(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("target"))
	net := m.networksList.Items[0]
	m.pendingAction = "delete"
	m.networkToDelete = &net
	m.confirmDialog.Visible = true
	cmd := m.Update(key("y"))
	require.False(t, m.confirmDialog.Visible)
	require.NotNil(t, cmd)
}

func TestConfirm_Prune_Y(t *testing.T) {
	m := testModel()
	m.pendingAction = "prune"
	m.confirmDialog.Visible = true
	cmd := m.Update(key("y"))
	require.False(t, m.confirmDialog.Visible)
	require.NotNil(t, cmd)
}

func TestConfirm_N_Cancels(t *testing.T) {
	m := testModel()
	m.pendingAction = "delete"
	m.confirmDialog.Visible = true
	m.Update(key("n"))
	require.False(t, m.confirmDialog.Visible)
	require.Equal(t, "", m.pendingAction)
}

func TestConfirm_Esc_Cancels(t *testing.T) {
	m := testModel()
	m.pendingAction = "prune"
	m.confirmDialog.Visible = true
	m.Update(key("esc"))
	require.False(t, m.confirmDialog.Visible)
	require.Equal(t, "", m.pendingAction)
}

// --- Sort key tests ---

func TestSortKey_N_Name(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("b", "a"))
	// Default is SortByName+asc, N toggles to desc
	m.Update(key("N"))
	require.Equal(t, SortByName, m.sortField)
	require.False(t, m.sortAscending)
}

func TestSortKey_I_ID(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("a", "b"))
	m.Update(key("I"))
	require.Equal(t, SortByID, m.sortField)
}

func TestSortKey_D_Driver(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("a", "b"))
	m.Update(key("D"))
	require.Equal(t, SortByDriver, m.sortField)
}

func TestSortKey_S_Scope(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("a", "b"))
	m.Update(key("S"))
	require.Equal(t, SortByScope, m.sortField)
}

func TestSortKey_U_Used(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("a", "b"))
	m.Update(key("U"))
	require.Equal(t, SortByUsed, m.sortField)
}

func TestSortKey_C_Created(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("a", "b"))
	m.Update(key("C"))
	require.Equal(t, SortByCreated, m.sortField)
}

// --- Create dialog key tests ---

func TestCreateDialog_Tab_Cycles(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "basic"
	m.createInputFocus = 0
	m.Update(key("tab"))
	require.Equal(t, 1, m.createInputFocus)
	m.Update(key("tab"))
	require.Equal(t, 2, m.createInputFocus)
}

func TestCreateDialog_ShiftTab(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "basic"
	m.createInputFocus = 2
	m.Update(key("shift+tab"))
	require.Equal(t, 1, m.createInputFocus)
}

func TestCreateDialog_Esc(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "basic"
	m.Update(key("esc"))
	require.False(t, m.createDialogActive)
}

func TestCreateDialog_Enter_EmptyName(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "basic"
	m.createNameInput.SetValue("")
	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "name")
}

func TestCreateDialog_Enter_ValidName_GoesToReview(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "basic"
	m.createNameInput.SetValue("mynet")
	m.Update(key("enter"))
	require.Equal(t, "review", m.createDialogStep)
	require.Empty(t, m.createDialogError)
}

func TestCreateDialog_Review_Enter_Submits(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "review"
	m.createNameInput.SetValue("mynet")
	cmd := m.Update(key("enter"))
	require.Equal(t, "creating", m.createDialogStep)
	require.NotNil(t, cmd)
}

func TestCreateDialog_Review_B_GoesBack(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "review"
	m.Update(key("b"))
	require.Equal(t, "basic", m.createDialogStep)
}

func TestCreateDialog_Space_TogglesIPv6(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "basic"
	m.createInputFocus = 4
	require.False(t, m.createEnableIPv6)
	m.Update(key(" "))
	require.True(t, m.createEnableIPv6)
}

func TestCreateDialog_Space_TogglesInternal(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "basic"
	m.createInputFocus = 7
	require.False(t, m.createInternal)
	m.Update(key(" "))
	require.True(t, m.createInternal)
}

func TestCreateDialog_Space_TogglesAttachable(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "basic"
	m.createInputFocus = 8
	m.createAttachable = false
	m.Update(key(" "))
	require.True(t, m.createAttachable)
}

func TestCreateDialog_GatewayWithoutSubnet(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "basic"
	m.createNameInput.SetValue("mynet")
	m.createIPv4Gateway.SetValue("10.0.0.1")
	m.createIPv4Subnet.SetValue("")
	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "requires")
}

func TestCreateDialog_IPv6WithoutEnable(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "basic"
	m.createNameInput.SetValue("mynet")
	m.createEnableIPv6 = false
	m.createIPv6Subnet.SetValue("fd00::/64")
	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "Enable IPv6")
}

// --- Used-by view key tests ---

func TestUsedByView_Esc_Returns(t *testing.T) {
	m := testModel()
	m.usedByViewActive = true
	m.usedByList.Viewport.Width = 80
	m.usedByList.Viewport.Height = 20
	m.Update(key("esc"))
	require.False(t, m.usedByViewActive)
}

func TestUsedByView_Enter_Navigates(t *testing.T) {
	m := testModel()
	m.usedByViewActive = true
	m.usedByList.Viewport.Width = 80
	m.usedByList.Viewport.Height = 20
	m.usedByList.Items = []usedByItem{{StackName: "mystack", ServiceName: "svc1"}}
	m.usedByList.Filtered = m.usedByList.Items
	m.usedByList.RenderItem = func(item usedByItem, _ bool, _ int) string {
		return item.StackName + " " + item.ServiceName
	}
	cmd := m.Update(key("enter"))
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "services", nav.ViewName)
}

// --- Mouse ---

func TestClickRow_SelectsTheLineInTheScrolledWindow(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("alpha", "beta", "delta", "gamma"))
	m.networksList.Viewport.YOffset = 1

	require.True(t, m.ClickRow(1))
	require.Equal(t, "delta", m.networksList.Filtered[m.networksList.Cursor].Name)
}

func TestClickRow_SelectsTheFilteredRow(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("alpha", "beta", "gamma"))
	m.ApplySearchQuery("gamma")

	require.True(t, m.ClickRow(0))
	require.Equal(t, "gamma", m.networksList.Filtered[m.networksList.Cursor].Name)
}

func TestClickRow_BelowTheLastRowSelectsNothing(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("alpha", "beta", "gamma"))
	m.networksList.Cursor = 1

	require.False(t, m.ClickRow(3))
	require.Equal(t, 1, m.networksList.Cursor)
}

// Before the first load the content is the loading placeholder, not a row.
func TestClickRow_WhileLoadingSelectsNothing(t *testing.T) {
	require.False(t, testModel().ClickRow(0))
}

// While the used-by list is open it is what is on screen, so a click selects
// in it and leaves the networks cursor where it was.
func TestClickRow_SelectsInTheUsedByList(t *testing.T) {
	m := testModel()
	loadNetworks(m, fakeNetworks("alpha", "beta"))
	m.networksList.Viewport.Width = 80
	m.networksList.Viewport.Height = 20
	m.Update(UsedByLoadedMsg{Services: []usedByItem{
		{StackName: "s1", ServiceName: "svc1"},
		{StackName: "s1", ServiceName: "svc2"},
		{StackName: "s2", ServiceName: "svc3"},
	}})
	require.True(t, m.usedByViewActive)

	require.True(t, m.ClickRow(2))
	require.Equal(t, "svc3", m.usedByList.Filtered[m.usedByList.Cursor].ServiceName)
	require.Equal(t, 0, m.networksList.Cursor)

	require.False(t, m.ClickRow(3))
	require.Equal(t, 2, m.usedByList.Cursor)
}
