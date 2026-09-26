// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package servicesview

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/scaledialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// --- mocks ---

type mockServiceOps struct {
	scaleServiceFn      func(ctx context.Context, serviceID string, replicas uint64) error
	restartServiceFn    func(ctx context.Context, serviceName string) error
	removeServiceFn     func(ctx context.Context, serviceName string) error
	rollbackServiceFn   func(ctx context.Context, serviceName string) error
	loadNodeServicesFn  func(nodeID string) []docker.ServiceEntry
	loadStackServicesFn func(stackName string) []docker.ServiceEntry
	loadAllServicesFn   func() []docker.ServiceEntry
	getServiceLogsFn    func(ctx context.Context, serviceID string) (string, error)
	createServiceFn     func(ctx context.Context, spec swarm.ServiceSpec) (string, error)
}

func (m *mockServiceOps) ScaleService(ctx context.Context, serviceID string, replicas uint64) error {
	return m.scaleServiceFn(ctx, serviceID, replicas)
}
func (m *mockServiceOps) ScaleServiceByName(_ context.Context, _ string, _ uint64) error {
	panic("not mocked")
}
func (m *mockServiceOps) RestartService(ctx context.Context, serviceName string) error {
	return m.restartServiceFn(ctx, serviceName)
}
func (m *mockServiceOps) RemoveService(ctx context.Context, serviceName string) error {
	return m.removeServiceFn(ctx, serviceName)
}
func (m *mockServiceOps) RollbackService(ctx context.Context, serviceName string) error {
	return m.rollbackServiceFn(ctx, serviceName)
}
func (m *mockServiceOps) RestartServiceAndWait(_ context.Context, _ string) error {
	panic("not mocked")
}
func (m *mockServiceOps) RestartServiceWithProgress(_ context.Context, _ string, _ chan<- docker.ProgressUpdate) error {
	panic("not mocked")
}
func (m *mockServiceOps) LoadNodeServices(nodeID string) []docker.ServiceEntry {
	return m.loadNodeServicesFn(nodeID)
}
func (m *mockServiceOps) LoadStackServices(stackName string) []docker.ServiceEntry {
	return m.loadStackServicesFn(stackName)
}
func (m *mockServiceOps) LoadAllServices() []docker.ServiceEntry {
	return m.loadAllServicesFn()
}
func (m *mockServiceOps) GetServiceLogs(ctx context.Context, serviceID string) (string, error) {
	if m.getServiceLogsFn != nil {
		return m.getServiceLogsFn(ctx, serviceID)
	}
	return "", nil
}
func (m *mockServiceOps) GetServiceTaskDiagnostics(_ context.Context, _ string) (string, error) {
	panic("not mocked")
}
func (m *mockServiceOps) CreateService(ctx context.Context, spec swarm.ServiceSpec) (string, error) {
	if m.createServiceFn != nil {
		return m.createServiceFn(ctx, spec)
	}
	return "", nil
}

type mockSnapshotOps struct {
	getSnapshotFn            func() *docker.SwarmSnapshot
	refreshSnapshotFn        func() (*docker.SwarmSnapshot, error)
	triggerRefreshIfNeededFn func()
}

func (m *mockSnapshotOps) GetSnapshot() *docker.SwarmSnapshot {
	if m.getSnapshotFn != nil {
		return m.getSnapshotFn()
	}
	return nil
}
func (m *mockSnapshotOps) SetSnapshot(_ *docker.SwarmSnapshot) {}
func (m *mockSnapshotOps) InvalidateSnapshot()                 {}
func (m *mockSnapshotOps) RefreshSnapshot() (*docker.SwarmSnapshot, error) {
	if m.refreshSnapshotFn != nil {
		return m.refreshSnapshotFn()
	}
	return nil, nil
}
func (m *mockSnapshotOps) RefreshSnapshotAsync() {}
func (m *mockSnapshotOps) TriggerRefreshIfNeeded() {
	if m.triggerRefreshIfNeededFn != nil {
		m.triggerRefreshIfNeededFn()
	}
}
func (m *mockSnapshotOps) GetOrRefreshSnapshot() (*docker.SwarmSnapshot, error) {
	return m.RefreshSnapshot()
}

type mockTaskOps struct {
	getTasksForServiceFn func(serviceID string) ([]docker.TaskEntry, error)
	getTasksForStackFn   func(stackName string) ([]docker.TaskEntry, error)
}

func (m *mockTaskOps) GetTasksForService(serviceID string) ([]docker.TaskEntry, error) {
	return m.getTasksForServiceFn(serviceID)
}
func (m *mockTaskOps) GetTasksForStack(stackName string) ([]docker.TaskEntry, error) {
	if m.getTasksForStackFn != nil {
		return m.getTasksForStackFn(stackName)
	}
	return nil, nil
}

type mockInspectOps struct {
	inspectFn func(ctx context.Context, t docker.InspectType, id string) (string, error)
}

func (m *mockInspectOps) Inspect(ctx context.Context, t docker.InspectType, id string) (string, error) {
	return m.inspectFn(ctx, t, id)
}

// --- helpers ---

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	}
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func noopServiceOps() *mockServiceOps {
	return &mockServiceOps{
		scaleServiceFn:      func(_ context.Context, _ string, _ uint64) error { return nil },
		restartServiceFn:    func(_ context.Context, _ string) error { return nil },
		removeServiceFn:     func(_ context.Context, _ string) error { return nil },
		rollbackServiceFn:   func(_ context.Context, _ string) error { return nil },
		loadNodeServicesFn:  func(_ string) []docker.ServiceEntry { return nil },
		loadStackServicesFn: func(_ string) []docker.ServiceEntry { return nil },
		loadAllServicesFn:   func() []docker.ServiceEntry { return nil },
	}
}

func testModel(opts ...func(*Model)) *Model {
	m := New(80, 24)
	m.deps = docker.Deps{
		Services: noopServiceOps(),
		Snapshot: &mockSnapshotOps{},
		Tasks:    &mockTaskOps{getTasksForServiceFn: func(_ string) ([]docker.TaskEntry, error) { return nil, nil }},
		Inspect:  &mockInspectOps{inspectFn: func(_ context.Context, _ docker.InspectType, _ string) (string, error) { return "{}", nil }},
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func fakeEntries(names ...string) []docker.ServiceEntry {
	now := time.Now()
	entries := make([]docker.ServiceEntry, len(names))
	for i, name := range names {
		entries[i] = docker.ServiceEntry{
			ServiceName:    name,
			ServiceID:      "id-" + name,
			StackName:      "mystack",
			ReplicasOnNode: 1,
			ReplicasTotal:  1,
			Status:         "running",
			Mode:           "replicated",
			Image:          "img:latest",
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}
	return entries
}

func loadServices(m *Model, entries []docker.ServiceEntry) {
	m.Update(Msg{
		Scope:      "all",
		Entries:    entries,
		FilterType: AllFilter,
	})
}

// --- Tests ---

func TestNew(t *testing.T) {
	m := New(80, 24)
	require.Equal(t, 80, m.width)
	require.Equal(t, 24, m.height)
	require.False(t, m.Visible)
	require.Equal(t, SortByName, m.sortField)
	require.True(t, m.sortAscending)
}

func TestName(t *testing.T) {
	m := testModel()
	require.Equal(t, "services", m.Name())
}

func TestCapturesInput_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.CapturesInput())
}

func TestHasActiveFilter_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.HasActiveFilter())
}

func TestHasErrors_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.HasErrors())
}

func TestShortHelpItems(t *testing.T) {
	m := testModel()
	items := m.ShortHelpItems()
	require.True(t, len(items) > 5)
	keys := make(map[string]bool)
	for _, item := range items {
		keys[item.Key] = true
	}
	require.True(t, keys["s"])
	require.True(t, keys["r"])
	require.True(t, keys["ctrl+d"])
}

func TestMsg_SetsContentAndVisible(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web", "api"))
	require.True(t, m.Visible)
	require.Len(t, m.List.Items, 2)
}

func TestMsg_EmptyStackFilter_ShowsDialog(t *testing.T) {
	m := testModel()
	m.Update(Msg{
		Scope:      "mystack",
		Entries:    nil,
		FilterType: StackFilter,
	})
	require.True(t, m.confirmDialog.Visible)
	require.True(t, m.confirmDialog.ErrorMode)
	require.Equal(t, "empty-stack", m.pendingAction)
}

func TestWindowSizeMsg(t *testing.T) {
	m := testModel()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	require.Equal(t, 120, m.List.Viewport.Width)
	require.Equal(t, 40, m.List.Viewport.Height)
	require.True(t, m.ready)
}

func TestTickMsg_Visible_Polls(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	cmd := m.Update(TickMsg{Gen: m.pollGen})
	require.NotNil(t, cmd)
}

func TestTickMsg_NotVisible_SchedulesTick(t *testing.T) {
	m := testModel()
	m.Visible = false
	cmd := m.Update(TickMsg{Gen: m.pollGen})
	require.NotNil(t, cmd)
}

func TestOnEnter_RestartsTickLoop(t *testing.T) {
	m := testModel()
	m.Visible = true
	loadServices(m, fakeEntries("web"))

	// Simulate returning from another view (e.g. logs) via goBack → OnEnter.
	cmd := m.OnEnter()
	require.NotNil(t, cmd, "OnEnter must return a tick command to restart polling")
}

func TestTasksLoadedMsg_StoresTasks(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.expandedServices["id-web"] = true
	m.Update(TasksLoadedMsg{
		ServiceID: "id-web",
		Tasks:     []docker.TaskEntry{{ID: "t1", Name: "web.1"}},
	})
	require.Len(t, m.serviceTasks["id-web"], 1)
}

func TestAllTasksLoadedMsg_StoresAllTasks(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web", "api"))
	m.expandedServices["id-web"] = true
	m.expandedServices["id-api"] = true
	m.Update(AllTasksLoadedMsg{
		Tasks: map[string][]docker.TaskEntry{
			"id-web": {{ID: "t1", Name: "web.1"}},
			"id-api": {{ID: "t2", Name: "api.1"}, {ID: "t3", Name: "api.2"}},
		},
	})
	require.Len(t, m.serviceTasks["id-web"], 1)
	require.Len(t, m.serviceTasks["id-api"], 2)
}

// --- Key routing tests ---

func TestKey_S_OpensScaleDialog(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("s"))
	require.True(t, m.scaleDialog.Visible)
}

func TestKey_R_OpensRestartConfirm(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("r"))
	require.True(t, m.confirmDialog.Visible)
	require.Equal(t, "restart", m.pendingAction)
}

func TestKey_CtrlD_OpensRemoveConfirm(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("ctrl+d"))
	require.True(t, m.confirmDialog.Visible)
	require.Equal(t, "remove", m.pendingAction)
}

func TestKey_CtrlR_OpensRollbackConfirm(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("ctrl+r"))
	require.True(t, m.confirmDialog.Visible)
	require.Equal(t, "rollback", m.pendingAction)
}

func TestKey_I_InspectsService(t *testing.T) {
	inspected := ""
	m := testModel(func(m *Model) {
		m.deps.Inspect = &mockInspectOps{
			inspectFn: func(_ context.Context, _ docker.InspectType, id string) (string, error) {
				inspected = id
				return `{"service": "data"}`, nil
			},
		}
	})
	loadServices(m, fakeEntries("web"))
	cmd := m.Update(key("i"))
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "inspect", nav.ViewName)
	require.Equal(t, "id-web", inspected)
}

func TestKey_P_TogglesExpansion(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	// First press: expand
	cmd := m.Update(key("p"))
	require.True(t, m.expandedServices["id-web"])
	require.NotNil(t, cmd) // fetches tasks
	// Second press: collapse
	m.Update(key("p"))
	require.False(t, m.expandedServices["id-web"])
}

func TestKey_Enter_TogglesExpansion(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	cmd := m.Update(key("enter"))
	require.True(t, m.expandedServices["id-web"])
	require.NotNil(t, cmd) // fetches tasks
	m.Update(key("enter"))
	require.False(t, m.expandedServices["id-web"])
}

func TestKey_L_NavigatesToLogs(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	cmd := m.Update(key("l"))
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "logs", nav.ViewName)
}

// The "?" key is routed by the app, not by this view — see app.Model.openHelp
// and its tests. What the view still owns is the content the app asks it for.
func TestHelpContent(t *testing.T) {
	m := testModel()
	require.NotEmpty(t, m.HelpContent())
}

func TestKey_Q_Disabled(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	cmd := m.Update(key("q"))
	require.Nil(t, cmd) // q is disabled, does nothing
}

// --- Confirm dialog result tests ---

func TestConfirmRestart_Success(t *testing.T) {
	restarted := ""
	m := testModel(func(m *Model) {
		m.deps.Services = &mockServiceOps{
			scaleServiceFn:      func(_ context.Context, _ string, _ uint64) error { return nil },
			restartServiceFn:    func(_ context.Context, name string) error { restarted = name; return nil },
			removeServiceFn:     func(_ context.Context, _ string) error { return nil },
			rollbackServiceFn:   func(_ context.Context, _ string) error { return nil },
			loadNodeServicesFn:  func(_ string) []docker.ServiceEntry { return nil },
			loadStackServicesFn: func(_ string) []docker.ServiceEntry { return nil },
			loadAllServicesFn:   func() []docker.ServiceEntry { return nil },
		}
	})
	loadServices(m, fakeEntries("web"))
	m.pendingAction = "restart"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.NotNil(t, cmd)
	runCmd(cmd)
	require.Equal(t, "web", restarted)
}

func TestConfirmRemove_Success(t *testing.T) {
	removed := ""
	m := testModel(func(m *Model) {
		m.deps.Services = &mockServiceOps{
			scaleServiceFn:      func(_ context.Context, _ string, _ uint64) error { return nil },
			restartServiceFn:    func(_ context.Context, _ string) error { return nil },
			removeServiceFn:     func(_ context.Context, name string) error { removed = name; return nil },
			rollbackServiceFn:   func(_ context.Context, _ string) error { return nil },
			loadNodeServicesFn:  func(_ string) []docker.ServiceEntry { return nil },
			loadStackServicesFn: func(_ string) []docker.ServiceEntry { return nil },
			loadAllServicesFn:   func() []docker.ServiceEntry { return nil },
		}
	})
	loadServices(m, fakeEntries("web"))
	m.pendingAction = "remove"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.NotNil(t, cmd)
	runCmd(cmd)
	require.Equal(t, "web", removed)
}

func TestConfirmRollback_Success(t *testing.T) {
	rolledBack := ""
	m := testModel(func(m *Model) {
		m.deps.Services = &mockServiceOps{
			scaleServiceFn:      func(_ context.Context, _ string, _ uint64) error { return nil },
			restartServiceFn:    func(_ context.Context, _ string) error { return nil },
			removeServiceFn:     func(_ context.Context, _ string) error { return nil },
			rollbackServiceFn:   func(_ context.Context, name string) error { rolledBack = name; return nil },
			loadNodeServicesFn:  func(_ string) []docker.ServiceEntry { return nil },
			loadStackServicesFn: func(_ string) []docker.ServiceEntry { return nil },
			loadAllServicesFn:   func() []docker.ServiceEntry { return nil },
		}
	})
	loadServices(m, fakeEntries("web"))
	m.pendingAction = "rollback"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.NotNil(t, cmd)
	runCmd(cmd)
	require.Equal(t, "web", rolledBack)
}

func TestConfirmCancelled_Noop(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.pendingAction = "remove"
	m.confirmDialog.Visible = true
	m.Update(confirmdialog.ResultMsg{Confirmed: false})
	require.False(t, m.confirmDialog.Visible)
}

func TestEmptyStack_ConfirmResult_NavigatesBack(t *testing.T) {
	m := testModel()
	m.pendingAction = "empty-stack"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: false})
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, view.NameStacks, nav.ViewName)
}

func TestScaleDialogResult_Confirmed(t *testing.T) {
	scaled := ""
	var scaledTo uint64
	m := testModel(func(m *Model) {
		m.deps.Services = &mockServiceOps{
			scaleServiceFn: func(_ context.Context, id string, replicas uint64) error {
				scaled = id
				scaledTo = replicas
				return nil
			},
			restartServiceFn:    func(_ context.Context, _ string) error { return nil },
			removeServiceFn:     func(_ context.Context, _ string) error { return nil },
			rollbackServiceFn:   func(_ context.Context, _ string) error { return nil },
			loadNodeServicesFn:  func(_ string) []docker.ServiceEntry { return nil },
			loadStackServicesFn: func(_ string) []docker.ServiceEntry { return nil },
			loadAllServicesFn:   func() []docker.ServiceEntry { return nil },
		}
	})
	loadServices(m, fakeEntries("web"))
	m.scaleDialog.Visible = true
	cmd := m.Update(scaledialog.ResultMsg{Confirmed: true, Replicas: 5})
	require.NotNil(t, cmd)
	runCmd(cmd)
	require.Equal(t, "id-web", scaled)
	require.Equal(t, uint64(5), scaledTo)
}

// --- Error message tests ---

func TestRestartErrorMsg_ShowsDialog(t *testing.T) {
	m := testModel()
	m.Update(RestartErrorMsg{ServiceName: "web", Error: fmt.Errorf("fail")})
	require.True(t, m.confirmDialog.Visible)
	require.True(t, m.confirmDialog.ErrorMode)
	require.Contains(t, m.confirmDialog.Message, "web")
}

func TestScaleErrorMsg_ShowsDialog(t *testing.T) {
	m := testModel()
	m.Update(ScaleErrorMsg{ServiceName: "api", Error: fmt.Errorf("fail")})
	require.True(t, m.confirmDialog.Visible)
	require.Contains(t, m.confirmDialog.Message, "api")
}

func TestRemoveErrorMsg_ShowsDialog(t *testing.T) {
	m := testModel()
	m.Update(RemoveErrorMsg{ServiceName: "db", Error: fmt.Errorf("fail")})
	require.True(t, m.confirmDialog.Visible)
	require.Contains(t, m.confirmDialog.Message, "db")
}

func TestRollbackErrorMsg_ShowsDialog(t *testing.T) {
	m := testModel()
	m.Update(RollbackErrorMsg{ServiceName: "web", Error: fmt.Errorf("fail")})
	require.True(t, m.confirmDialog.Visible)
	require.Contains(t, m.confirmDialog.Message, "web")
}

// --- Sort key tests ---

func TestSortKey_N_SortsByName(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("beta", "alpha"))
	require.Equal(t, SortByName, m.sortField)
	m.Update(key("N"))
	require.False(t, m.sortAscending) // toggles
}

func TestSortKey_S_SortsByStatus(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("S"))
	require.Equal(t, SortByStatus, m.sortField)
}

func TestSortKey_I_SortsByImage(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("I"))
	require.Equal(t, SortByImage, m.sortField)
}

func TestSortKey_P_SortsByPorts(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("P"))
	require.Equal(t, SortByPorts, m.sortField)
}

func TestSortKey_C_SortsByCreated(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("C"))
	require.Equal(t, SortByCreated, m.sortField)
}

func TestSortKey_U_SortsByUpdated(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("U"))
	require.Equal(t, SortByUpdated, m.sortField)
}

func TestSortKey_R_SortsByError(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("R"))
	require.Equal(t, SortByError, m.sortField)
}

// --- View rendering tests ---

func TestView_ShowsServices(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web", "api"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	out := m.View()
	require.Contains(t, out, "web")
	require.Contains(t, out, "api")
}

func TestView_ActiveFilter_TitleReflectsFilteredCount(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web", "api")) // Scope "all"
	m.ApplySearchQuery("api")                  // matches "api" only
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	out := ansi.Strip(m.View())
	require.Contains(t, out, "Services(all)[1]") // count is the filtered row count
	require.Contains(t, out, "</api>")           // active filter appended, k9s-style
}

func TestView_ConfirmDialog(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	m.ready = true
	m.confirmDialog.Visible = true
	m.confirmDialog.Message = "Delete service?"
	out := m.View()
	require.Contains(t, out, "web")
}

func TestGetServicesHelpContent(t *testing.T) {
	cats := GetServicesHelpContent()
	require.True(t, len(cats) >= 3)
	require.Equal(t, "General", cats[0].Title)
}

// --- loadServicesForView tests ---

func TestLoadServicesForView_AllFilter(t *testing.T) {
	mock := noopServiceOps()
	mock.loadStackServicesFn = func(string) []docker.ServiceEntry {
		t.Fatal("AllFilter must not call LoadStackServices")
		return nil
	}
	mock.loadAllServicesFn = func() []docker.ServiceEntry {
		return fakeEntries("web", "db")
	}
	m := testModel(func(m *Model) { m.deps.Services = mock })
	entries, scope := m.loadServicesForView(AllFilter, "", "")
	require.Len(t, entries, 2)
	require.Equal(t, "all", scope)
}

func TestLoadServicesForView_StackFilter(t *testing.T) {
	mock := noopServiceOps()
	mock.loadStackServicesFn = func(stackName string) []docker.ServiceEntry {
		require.Equal(t, "mystack", stackName)
		return fakeEntries("web")
	}
	m := testModel(func(m *Model) { m.deps.Services = mock })
	entries, scope := m.loadServicesForView(StackFilter, "", "mystack")
	require.Len(t, entries, 1)
	require.Equal(t, "mystack", scope)
}

func TestLoadServicesForView_NodeFilter(t *testing.T) {
	mock := noopServiceOps()
	mock.loadNodeServicesFn = func(nodeID string) []docker.ServiceEntry {
		require.Equal(t, "node1", nodeID)
		return fakeEntries("web")
	}
	m := testModel(func(m *Model) { m.deps.Services = mock })
	entries, scope := m.loadServicesForView(NodeFilter, "node1", "")
	require.Len(t, entries, 1)
	require.Equal(t, "node1", scope)
}

func TestFormatRelativeTime_Zero(t *testing.T) {
	require.Equal(t, "-", formatRelativeTime(time.Time{}))
}

func TestFormatRelativeTime_Recent(t *testing.T) {
	result := formatRelativeTime(time.Now().Add(-30 * time.Second))
	require.Equal(t, "just now", result)
}

func TestFormatRelativeTime_Minutes(t *testing.T) {
	result := formatRelativeTime(time.Now().Add(-5 * time.Minute))
	require.Contains(t, result, "m ago")
}

func TestFormatRelativeTime_Hours(t *testing.T) {
	result := formatRelativeTime(time.Now().Add(-3 * time.Hour))
	require.Contains(t, result, "h ago")
}

// --- Business Edition gating tests ---

func TestShortHelpItems_IncludesShellAndPortForward(t *testing.T) {
	m := testModel()
	items := m.ShortHelpItems()
	keys := make(map[string]string)
	for _, item := range items {
		keys[item.Key] = item.Desc
	}
	require.Contains(t, keys, "x")
	require.Contains(t, keys["x"], "(BE)")
	require.Contains(t, keys, "w")
	require.Contains(t, keys["w"], "(BE)")
	require.Contains(t, keys, "t")
	require.Contains(t, keys["t"], "(BE)")
}

func TestKey_X_NoAction_ShowsBEDialog(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("x"))
	require.True(t, m.confirmDialog.Visible)
	require.True(t, m.confirmDialog.ErrorMode)
	require.Contains(t, m.confirmDialog.Message, "Shell")
	require.Contains(t, m.confirmDialog.Message, "swarmcli.io/be")
}

func TestKey_W_NoAction_ShowsBEDialog(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("w"))
	require.True(t, m.confirmDialog.Visible)
	require.True(t, m.confirmDialog.ErrorMode)
	require.Contains(t, m.confirmDialog.Message, "Active Forwards")
	require.Contains(t, m.confirmDialog.Message, "swarmcli.io/be")
}

func TestKey_ShiftW_NoAction_ShowsBEDialog(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("W"))
	require.True(t, m.confirmDialog.Visible)
	require.True(t, m.confirmDialog.ErrorMode)
	require.Contains(t, m.confirmDialog.Message, "Port Forward")
	require.Contains(t, m.confirmDialog.Message, "swarmcli.io/be")
}

func TestKey_X_WithAction_CallsAction(t *testing.T) {
	called := ""
	view.RegisterAction("shell", func(name string) tea.Cmd {
		return func() tea.Msg { called = name; return nil }
	})
	defer view.UnregisterActionForTest("shell")

	m := testModel()
	loadServices(m, fakeEntries("web"))
	cmd := m.Update(key("x"))
	require.NotNil(t, cmd)
	runCmd(cmd)
	require.Equal(t, "web", called)
}

func TestKey_W_WithAction_CallsPortForwardsAction(t *testing.T) {
	called := ""
	view.RegisterAction("port-forwards", func(name string) tea.Cmd {
		return func() tea.Msg { called = name; return nil }
	})
	defer view.UnregisterActionForTest("port-forwards")

	m := testModel()
	loadServices(m, fakeEntries("web"))
	cmd := m.Update(key("w"))
	require.NotNil(t, cmd)
	runCmd(cmd)
	require.Equal(t, "web", called)
}

func TestKey_ShiftW_WithAction_CallsPortForwardAction(t *testing.T) {
	called := ""
	view.RegisterAction("port-forward", func(name string) tea.Cmd {
		return func() tea.Msg { called = name; return nil }
	})
	defer view.UnregisterActionForTest("port-forward")

	m := testModel()
	loadServices(m, fakeEntries("web"))
	cmd := m.Update(key("W"))
	require.NotNil(t, cmd)
	runCmd(cmd)
	require.Equal(t, "web", called)
}

func TestGetServicesHelpContent_IncludesBEActions(t *testing.T) {
	cats := GetServicesHelpContent()
	found := map[string]bool{}
	for _, cat := range cats {
		for _, item := range cat.Items {
			found[item.Keys] = true
		}
	}
	require.True(t, found["<x>"])
	require.True(t, found["<w>"])
	require.True(t, found["<shift+w>"])
	require.True(t, found["<t>"])
}

func TestKey_T_ServiceRow_PassesServiceAndNoTask(t *testing.T) {
	got := ""
	view.RegisterAction("container-stats", func(ref string) tea.Cmd {
		return func() tea.Msg { got = ref; return nil }
	})
	defer view.UnregisterActionForTest("container-stats")

	m := testModel()
	loadServices(m, fakeEntries("web"))
	cmd := m.Update(key("t"))
	require.NotNil(t, cmd)
	runCmd(cmd)
	// Empty task field: the cursor is on the service row, so the extension
	// picks the replica rather than this view guessing one.
	require.Equal(t, []string{"web", ""}, view.DecodeRef(got))
}

func TestKey_T_TaskRow_PassesThatReplicasTaskID(t *testing.T) {
	got := ""
	view.RegisterAction("container-stats", func(ref string) tea.Cmd {
		return func() tea.Msg { got = ref; return nil }
	})
	defer view.UnregisterActionForTest("container-stats")

	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.expandedServices["id-web"] = true
	m.serviceTasks["id-web"] = []docker.TaskEntry{{ID: "task-a"}, {ID: "task-b"}}
	m.selectedTaskIndex = 1

	cmd := m.Update(key("t"))
	require.NotNil(t, cmd)
	runCmd(cmd)
	require.Equal(t, []string{"web", "task-b"}, view.DecodeRef(got))
}

// The task list is refreshed on a 2s poll while a task row stays selected, so
// the index can outlive the row it named. That must read as "no replica
// picked", not index a slice that shrank.
func TestKey_T_StaleTaskIndex_FallsBackToService(t *testing.T) {
	got := ""
	view.RegisterAction("container-stats", func(ref string) tea.Cmd {
		return func() tea.Msg { got = ref; return nil }
	})
	defer view.UnregisterActionForTest("container-stats")

	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.expandedServices["id-web"] = true
	m.serviceTasks["id-web"] = []docker.TaskEntry{{ID: "task-a"}}
	m.selectedTaskIndex = 3

	cmd := m.Update(key("t"))
	require.NotNil(t, cmd)
	runCmd(cmd)
	require.Equal(t, []string{"web", ""}, view.DecodeRef(got))
}

func TestKey_T_NoAction_ShowsBEDialog(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("web"))
	m.Update(key("t"))
	require.True(t, m.confirmDialog.Visible)
	require.True(t, m.confirmDialog.ErrorMode)
	require.Contains(t, m.confirmDialog.Message, "Stats")
	require.Contains(t, m.confirmDialog.Message, "swarmcli.io/be")
}

func TestKey_T_NoSelection_NoOp(t *testing.T) {
	view.RegisterAction("container-stats", func(string) tea.Cmd {
		return func() tea.Msg { return nil }
	})
	defer view.UnregisterActionForTest("container-stats")

	m := testModel() // empty list
	require.Nil(t, m.Update(key("t")))
	require.False(t, m.confirmDialog.Visible)
}
