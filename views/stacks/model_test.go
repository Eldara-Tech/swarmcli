// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// --- mocks ---

type mockStackOps struct {
	removeStackFn             func(ctx context.Context, stackName string) error
	removeStackNetworksFn     func(ctx context.Context, stackName string) error
	deployStackFn             func(stackName string, yamlContent string, opts docker.DeployOptions) error
	validateStackYAMLFn       func(content string) error
	inspectStackFn            func(stackName string) (string, error)
	reconstructStackComposeFn func(stackName string) (string, error)
}

func (m *mockStackOps) RemoveStack(ctx context.Context, stackName string) error {
	return m.removeStackFn(ctx, stackName)
}
func (m *mockStackOps) RemoveStackNetworks(ctx context.Context, stackName string) error {
	return m.removeStackNetworksFn(ctx, stackName)
}
func (m *mockStackOps) DeployStack(stackName string, yamlContent string, opts docker.DeployOptions) error {
	return m.deployStackFn(stackName, yamlContent, opts)
}
func (m *mockStackOps) ValidateStackYAML(content string) error {
	return m.validateStackYAMLFn(content)
}
func (m *mockStackOps) InspectStack(stackName string) (string, error) {
	return m.inspectStackFn(stackName)
}
func (m *mockStackOps) ReconstructStackCompose(stackName string) (string, error) {
	return m.reconstructStackComposeFn(stackName)
}

type mockSnapshotOps struct {
	getSnapshotFn          func() *docker.SwarmSnapshot
	setSnapshotFn          func(s *docker.SwarmSnapshot)
	invalidateSnapshotFn   func()
	refreshSnapshotFn      func() (*docker.SwarmSnapshot, error)
	refreshSnapshotAsyncFn func()
	triggerRefreshFn       func()
	getOrRefreshFn         func() (*docker.SwarmSnapshot, error)
}

func (m *mockSnapshotOps) GetSnapshot() *docker.SwarmSnapshot  { return m.getSnapshotFn() }
func (m *mockSnapshotOps) SetSnapshot(s *docker.SwarmSnapshot) { m.setSnapshotFn(s) }
func (m *mockSnapshotOps) InvalidateSnapshot()                 { m.invalidateSnapshotFn() }
func (m *mockSnapshotOps) RefreshSnapshot() (*docker.SwarmSnapshot, error) {
	return m.refreshSnapshotFn()
}
func (m *mockSnapshotOps) RefreshSnapshotAsync()   { m.refreshSnapshotAsyncFn() }
func (m *mockSnapshotOps) TriggerRefreshIfNeeded() { m.triggerRefreshFn() }
func (m *mockSnapshotOps) GetOrRefreshSnapshot() (*docker.SwarmSnapshot, error) {
	return m.getOrRefreshFn()
}

type mockTaskOps struct {
	getTasksForStackFn   func(stackName string) ([]docker.TaskEntry, error)
	getTasksForServiceFn func(serviceID string) ([]docker.TaskEntry, error)
}

func (m *mockTaskOps) GetTasksForStack(stackName string) ([]docker.TaskEntry, error) {
	return m.getTasksForStackFn(stackName)
}
func (m *mockTaskOps) GetTasksForService(serviceID string) ([]docker.TaskEntry, error) {
	return m.getTasksForServiceFn(serviceID)
}

type mockClusterInfoOps struct {
	getCurrentContextFn func() (string, error)
}

func (m *mockClusterInfoOps) GetCurrentContext() (string, error)             { return m.getCurrentContextFn() }
func (m *mockClusterInfoOps) GetContainerCount() (int, error)                { return 0, nil }
func (m *mockClusterInfoOps) GetServiceCount() (int, error)                  { return 0, nil }
func (m *mockClusterInfoOps) GetSwarmCPUCapacity() (float64, error)          { return 0, nil }
func (m *mockClusterInfoOps) GetSwarmMemCapacity() (int64, error)            { return 0, nil }
func (m *mockClusterInfoOps) GetSwarmResourceUsage() (string, string, error) { return "", "", nil }
func (m *mockClusterInfoOps) GetDockerVersion() (string, error)              { return "", nil }

// --- helpers ---

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case " ":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
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

// fastSpinner shrinks the animation interval for the duration of a test: a
// tea.Tick cmd invoked synchronously blocks for its whole interval, so a batch
// containing one cannot otherwise be drained.
func fastSpinner(t *testing.T) {
	t.Helper()
	prev := spinnerTickInterval
	spinnerTickInterval = time.Millisecond
	t.Cleanup(func() { spinnerTickInterval = prev })
}

// runBatch runs every child of a batched cmd and returns their messages. Call
// fastSpinner first when the batch carries an animation tick.
func runBatch(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		return nil
	}
	var msgs []tea.Msg
	for _, c := range batch {
		if c == nil {
			continue
		}
		msgs = append(msgs, c())
	}
	return msgs
}

// firstOfType returns the first message of type T produced by a batch.
func firstOfType[T tea.Msg](msgs []tea.Msg) (T, bool) {
	for _, msg := range msgs {
		if typed, ok := msg.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}

func noopStackOps() *mockStackOps {
	return &mockStackOps{
		removeStackFn:             func(_ context.Context, _ string) error { return nil },
		removeStackNetworksFn:     func(_ context.Context, _ string) error { return nil },
		deployStackFn:             func(_ string, _ string, _ docker.DeployOptions) error { return nil },
		validateStackYAMLFn:       func(_ string) error { return nil },
		inspectStackFn:            func(_ string) (string, error) { return "", nil },
		reconstructStackComposeFn: func(_ string) (string, error) { return "", nil },
	}
}

func noopSnapshotOps() *mockSnapshotOps {
	snap := &docker.SwarmSnapshot{}
	return &mockSnapshotOps{
		getSnapshotFn:          func() *docker.SwarmSnapshot { return snap },
		setSnapshotFn:          func(_ *docker.SwarmSnapshot) {},
		invalidateSnapshotFn:   func() {},
		refreshSnapshotFn:      func() (*docker.SwarmSnapshot, error) { return snap, nil },
		refreshSnapshotAsyncFn: func() {},
		triggerRefreshFn:       func() {},
		getOrRefreshFn:         func() (*docker.SwarmSnapshot, error) { return snap, nil },
	}
}

func noopTaskOps() *mockTaskOps {
	return &mockTaskOps{
		getTasksForStackFn:   func(_ string) ([]docker.TaskEntry, error) { return nil, nil },
		getTasksForServiceFn: func(_ string) ([]docker.TaskEntry, error) { return nil, nil },
	}
}

func noopClusterInfoOps() *mockClusterInfoOps {
	return &mockClusterInfoOps{
		getCurrentContextFn: func() (string, error) { return "default", nil },
	}
}

func testModel(opts ...func(*Model)) *Model {
	m := New(80, 24)
	m.deps = docker.Deps{
		Stacks:      noopStackOps(),
		Snapshot:    noopSnapshotOps(),
		Tasks:       noopTaskOps(),
		ClusterInfo: noopClusterInfoOps(),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func fakeStacks(names ...string) []docker.StackEntry {
	entries := make([]docker.StackEntry, len(names))
	for i, name := range names {
		entries[i] = docker.StackEntry{
			Name:         name,
			ServiceCount: i + 1,
			NodeCount:    i + 1,
		}
	}
	return entries
}

func loadStacks(m *Model, stacks []docker.StackEntry) {
	m.Update(Msg{NodeID: "node1", Stacks: stacks})
}

// snapshotWithStacks builds a snapshot whose ToStackEntries yields one entry per
// name, so a reload path can be told apart from an empty terminal message.
func snapshotWithStacks(names ...string) *docker.SwarmSnapshot {
	snap := &docker.SwarmSnapshot{}
	for i, name := range names {
		snap.Services = append(snap.Services, swarm.Service{
			ID: fmt.Sprintf("svc%d", i),
			Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{
				Labels: map[string]string{"com.docker.stack.namespace": name},
			}},
		})
	}
	return snap
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
	require.Equal(t, "stacks", m.Name())
}

func TestCapturesInput_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.CapturesInput())
}

func TestCapturesInput_ConfirmVisible(t *testing.T) {
	m := testModel()
	m.confirmDialog.Visible = true
	require.True(t, m.CapturesInput())
}

func TestCapturesInput_CreateDialog(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	require.True(t, m.CapturesInput())
}

func TestCapturesInput_FileBrowser(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	require.True(t, m.CapturesInput())
}

func TestCapturesInput_SaveDialog(t *testing.T) {
	m := testModel()
	m.saveDialogActive = true
	require.True(t, m.CapturesInput())
}

func TestHasActiveFilter_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.HasActiveFilter())
}

func TestIsSearching_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.IsSearching())
}

func TestHasErrors_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.HasErrors())
}

func TestHasErrors_WithError(t *testing.T) {
	m := testModel()
	m.stackHasError["mystack"] = true
	require.True(t, m.HasErrors())
}

func TestShortHelpItems(t *testing.T) {
	m := testModel()
	items := m.ShortHelpItems()
	require.True(t, len(items) >= 9)
	keys := make(map[string]bool)
	for _, item := range items {
		keys[item.Key] = true
	}
	require.True(t, keys["n"])
	require.True(t, keys["e"])
	require.True(t, keys["s"])
	require.True(t, keys["i"])
	require.True(t, keys["ctrl+d"])
}

func TestOnEnter_ReturnsLoadCmd(t *testing.T) {
	m := testModel()
	cmd := m.OnEnter()
	require.NotNil(t, cmd)
}

func TestOnExit_ReturnsNil(t *testing.T) {
	m := testModel()
	cmd := m.OnExit()
	require.Nil(t, cmd)
}

func TestTruncateWithEllipsis(t *testing.T) {
	require.Equal(t, "", truncateWithEllipsis("hello", 0))
	require.Equal(t, "…", truncateWithEllipsis("hello", 1))
	require.Equal(t, "hello", truncateWithEllipsis("hello", 10))
}

func TestFormatErrorWithScroll(t *testing.T) {
	require.Equal(t, "", formatErrorWithScroll("", 0, 10))
	require.Equal(t, "hello", formatErrorWithScroll("hello", 0, 10))
	require.Equal(t, "lo", formatErrorWithScroll("hello", 3, 10))
}

func TestWrapText(t *testing.T) {
	require.Equal(t, []string{"short"}, ui.WrapText("short", 20))
	lines := ui.WrapText("this is a longer sentence that wraps", 15)
	require.True(t, len(lines) > 1)
}

func TestGetStacksHelpContent(t *testing.T) {
	cats := GetStacksHelpContent()
	require.True(t, len(cats) >= 3)
	require.Equal(t, "General", cats[0].Title)
	require.Equal(t, "View", cats[1].Title)
	require.Equal(t, "Navigation", cats[2].Title)
}

func TestApplySorting_ByName(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("beta", "alpha", "gamma"))
	m.sortField = SortByName
	m.sortAscending = true
	m.applySorting()
	require.Equal(t, "alpha", m.List.Filtered[0].Name)
	require.Equal(t, "gamma", m.List.Filtered[2].Name)
}

func TestApplySorting_ByServices(t *testing.T) {
	m := testModel()
	stacks := []docker.StackEntry{
		{Name: "a", ServiceCount: 5},
		{Name: "b", ServiceCount: 1},
		{Name: "c", ServiceCount: 3},
	}
	loadStacks(m, stacks)
	m.sortField = SortByServices
	m.sortAscending = true
	m.applySorting()
	require.Equal(t, "b", m.List.Filtered[0].Name)
	require.Equal(t, "a", m.List.Filtered[2].Name)
}

func TestApplySorting_ByError(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("ok-stack", "err-stack"))
	m.stackHasError["err-stack"] = true
	m.stackErrorText["err-stack"] = "task failed"
	m.sortField = SortByError
	m.sortAscending = true
	m.applySorting()
	// Errors first when ascending
	require.Equal(t, "err-stack", m.List.Filtered[0].Name)
}

func TestLoadStacksCmd_CallsSnapshot(t *testing.T) {
	called := false
	snap := &docker.SwarmSnapshot{}
	snapMock := noopSnapshotOps()
	snapMock.getSnapshotFn = func() *docker.SwarmSnapshot {
		called = true
		return snap
	}
	m := testModel(func(m *Model) { m.deps.Snapshot = snapMock })
	cmd := m.LoadStacksCmd("node1")
	msg := runCmd(cmd)
	require.True(t, called)
	stackMsg, ok := msg.(Msg)
	require.True(t, ok)
	require.Equal(t, "node1", stackMsg.NodeID)
}

func TestLoadStacksCmd_RefreshOnNilSnapshot(t *testing.T) {
	refreshed := false
	snap := &docker.SwarmSnapshot{}
	snapMock := noopSnapshotOps()
	snapMock.getSnapshotFn = func() *docker.SwarmSnapshot { return nil }
	snapMock.refreshSnapshotFn = func() (*docker.SwarmSnapshot, error) {
		refreshed = true
		return snap, nil
	}
	m := testModel(func(m *Model) { m.deps.Snapshot = snapMock })
	cmd := m.LoadStacksCmd("node1")
	runCmd(cmd)
	require.True(t, refreshed)
}

func TestLoadStacksCmd_RefreshError(t *testing.T) {
	snapMock := noopSnapshotOps()
	snapMock.getSnapshotFn = func() *docker.SwarmSnapshot { return nil }
	snapMock.refreshSnapshotFn = func() (*docker.SwarmSnapshot, error) {
		return nil, fmt.Errorf("connection refused")
	}
	m := testModel(func(m *Model) { m.deps.Snapshot = snapMock })
	cmd := m.LoadStacksCmd("node1")
	msg := runCmd(cmd)
	stackMsg, ok := msg.(Msg)
	require.True(t, ok)
	require.Empty(t, stackMsg.Stacks)
}
