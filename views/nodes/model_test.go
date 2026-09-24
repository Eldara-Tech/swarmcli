// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package nodesview

import (
	"context"
	"fmt"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// --- mocks ---

type mockSnapshotOps struct {
	getSnapshotFn            func() *docker.SwarmSnapshot
	setSnapshotFn            func(s *docker.SwarmSnapshot)
	invalidateSnapshotFn     func()
	refreshSnapshotFn        func() (*docker.SwarmSnapshot, error)
	refreshSnapshotAsyncFn   func()
	triggerRefreshIfNeededFn func()
	getOrRefreshSnapshotFn   func() (*docker.SwarmSnapshot, error)
}

func (m *mockSnapshotOps) GetSnapshot() *docker.SwarmSnapshot  { return m.getSnapshotFn() }
func (m *mockSnapshotOps) SetSnapshot(s *docker.SwarmSnapshot) { m.setSnapshotFn(s) }
func (m *mockSnapshotOps) InvalidateSnapshot()                 { m.invalidateSnapshotFn() }
func (m *mockSnapshotOps) RefreshSnapshot() (*docker.SwarmSnapshot, error) {
	return m.refreshSnapshotFn()
}
func (m *mockSnapshotOps) RefreshSnapshotAsync()   { m.refreshSnapshotAsyncFn() }
func (m *mockSnapshotOps) TriggerRefreshIfNeeded() { m.triggerRefreshIfNeededFn() }
func (m *mockSnapshotOps) GetOrRefreshSnapshot() (*docker.SwarmSnapshot, error) {
	return m.getOrRefreshSnapshotFn()
}

type mockNodeOps struct {
	getNodeIDToHostnameMapFromDockerFn func(ctx context.Context) (map[string]string, error)
	demoteNodeFn                       func(ctx context.Context, nodeID string) error
	promoteNodeFn                      func(ctx context.Context, nodeID string) error
	setNodeAvailabilityFn              func(ctx context.Context, nodeID string, availability swarm.NodeAvailability) error
	addNodeLabelFn                     func(ctx context.Context, nodeID, key, value string) error
	removeNodeLabelFn                  func(ctx context.Context, nodeID, key string) error
	removeNodeFn                       func(ctx context.Context, nodeID string, force bool) error
}

func (m *mockNodeOps) GetNodeIDToHostnameMapFromDocker(ctx context.Context) (map[string]string, error) {
	return m.getNodeIDToHostnameMapFromDockerFn(ctx)
}
func (m *mockNodeOps) DemoteNode(ctx context.Context, nodeID string) error {
	return m.demoteNodeFn(ctx, nodeID)
}
func (m *mockNodeOps) PromoteNode(ctx context.Context, nodeID string) error {
	return m.promoteNodeFn(ctx, nodeID)
}
func (m *mockNodeOps) SetNodeAvailability(ctx context.Context, nodeID string, availability swarm.NodeAvailability) error {
	return m.setNodeAvailabilityFn(ctx, nodeID, availability)
}
func (m *mockNodeOps) AddNodeLabel(ctx context.Context, nodeID, key, value string) error {
	return m.addNodeLabelFn(ctx, nodeID, key, value)
}
func (m *mockNodeOps) RemoveNodeLabel(ctx context.Context, nodeID, key string) error {
	return m.removeNodeLabelFn(ctx, nodeID, key)
}
func (m *mockNodeOps) RemoveNode(ctx context.Context, nodeID string, force bool) error {
	return m.removeNodeFn(ctx, nodeID, force)
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
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case "ctrl+l":
		return tea.KeyMsg{Type: tea.KeyCtrlL}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
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

func noopSnapshotOps() *mockSnapshotOps {
	emptySnap := &docker.SwarmSnapshot{}
	return &mockSnapshotOps{
		getSnapshotFn:            func() *docker.SwarmSnapshot { return emptySnap },
		setSnapshotFn:            func(_ *docker.SwarmSnapshot) {},
		invalidateSnapshotFn:     func() {},
		refreshSnapshotFn:        func() (*docker.SwarmSnapshot, error) { return emptySnap, nil },
		refreshSnapshotAsyncFn:   func() {},
		triggerRefreshIfNeededFn: func() {},
		getOrRefreshSnapshotFn:   func() (*docker.SwarmSnapshot, error) { return emptySnap, nil },
	}
}

func noopNodeOps() *mockNodeOps {
	return &mockNodeOps{
		getNodeIDToHostnameMapFromDockerFn: func(_ context.Context) (map[string]string, error) { return nil, nil },
		demoteNodeFn:                       func(_ context.Context, _ string) error { return nil },
		promoteNodeFn:                      func(_ context.Context, _ string) error { return nil },
		setNodeAvailabilityFn:              func(_ context.Context, _ string, _ swarm.NodeAvailability) error { return nil },
		addNodeLabelFn:                     func(_ context.Context, _, _, _ string) error { return nil },
		removeNodeLabelFn:                  func(_ context.Context, _, _ string) error { return nil },
		removeNodeFn:                       func(_ context.Context, _ string, _ bool) error { return nil },
	}
}

func noopInspectOps() *mockInspectOps {
	return &mockInspectOps{
		inspectFn: func(_ context.Context, _ docker.InspectType, _ string) (string, error) {
			return "{}", nil
		},
	}
}

func testModel(opts ...func(*Model)) *Model {
	m := New(80, 24)
	m.deps = docker.Deps{
		Snapshot: noopSnapshotOps(),
		Nodes:    noopNodeOps(),
		Inspect:  noopInspectOps(),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func fakeNodes(names ...string) []docker.NodeEntry {
	entries := make([]docker.NodeEntry, len(names))
	for i, name := range names {
		entries[i] = docker.NodeEntry{
			ID:           "id-" + name,
			Hostname:     name,
			Role:         "worker",
			State:        "ready",
			Availability: "active",
			Manager:      false,
			Addr:         fmt.Sprintf("10.0.0.%d", i+1),
			Version:      "27.0.0",
		}
	}
	return entries
}

func loadNodes(m *Model, entries []docker.NodeEntry) {
	m.Visible = true
	m.ready = true
	m.Update(Msg{Entries: entries})
}

// --- Tests ---

func TestNew(t *testing.T) {
	m := New(80, 24)
	require.Equal(t, 80, m.width)
	require.Equal(t, 24, m.height)
	require.False(t, m.Visible)
	require.Equal(t, SortByHostname, m.sortField)
	require.True(t, m.sortAscending)
}

func TestName(t *testing.T) {
	m := testModel()
	require.Equal(t, "nodes", m.Name())
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

func TestCapturesInput_AvailabilityDialog(t *testing.T) {
	m := testModel()
	m.availabilityDialog = true
	require.True(t, m.CapturesInput())
}

func TestCapturesInput_LabelInputDialog(t *testing.T) {
	m := testModel()
	m.labelInputDialog = true
	require.True(t, m.CapturesInput())
}

func TestCapturesInput_LabelRemoveDialog(t *testing.T) {
	m := testModel()
	m.labelRemoveDialog = true
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

func TestHasErrors(t *testing.T) {
	m := testModel()
	require.False(t, m.HasErrors())
}

func TestNodeIsUnhealthy(t *testing.T) {
	cases := []struct {
		name  string
		entry docker.NodeEntry
		want  bool
	}{
		{"ready worker", docker.NodeEntry{State: "ready", Availability: "active"}, false},
		{"down", docker.NodeEntry{State: "down", Availability: "active"}, true},
		{"unknown", docker.NodeEntry{State: "unknown", Availability: "active"}, true},
		{"disconnected", docker.NodeEntry{State: "disconnected", Availability: "active"}, true},
		{"empty state", docker.NodeEntry{}, false},
		{"drained is an operator choice", docker.NodeEntry{State: "ready", Availability: "drain"}, false},
		{"paused is an operator choice", docker.NodeEntry{State: "ready", Availability: "pause"}, false},
		{"leader", docker.NodeEntry{State: "ready", Manager: true, ManagerStatus: "Leader"}, false},
		{"reachable manager", docker.NodeEntry{State: "ready", Manager: true, ManagerStatus: "reachable"}, false},
		{"unreachable manager", docker.NodeEntry{State: "ready", Manager: true, ManagerStatus: "unreachable"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, nodeIsUnhealthy(c.entry))
		})
	}
}

func TestHasErrors_DownNode(t *testing.T) {
	m := testModel()
	entries := fakeNodes("n1", "n2")
	entries[1].State = "down"
	loadNodes(m, entries)
	require.True(t, m.HasErrors())
}

func TestHasErrors_DrainedNodeIsNotAnError(t *testing.T) {
	m := testModel()
	entries := fakeNodes("n1", "n2")
	entries[1].Availability = "drain"
	loadNodes(m, entries)
	require.False(t, m.HasErrors())
}

func TestHasErrors_FollowsFilter(t *testing.T) {
	m := testModel()
	entries := fakeNodes("n1", "n2")
	entries[1].State = "down"
	loadNodes(m, entries)
	m.ApplySearchQuery("n1")
	require.False(t, m.HasErrors(), "a down node filtered out of view must not turn the logo red")
}

func TestShortHelpItems(t *testing.T) {
	m := testModel()
	items := m.ShortHelpItems()
	keys := make(map[string]bool)
	for _, item := range items {
		keys[item.Key] = true
	}
	require.True(t, keys["i"])
	require.True(t, keys["a"])
	require.True(t, keys["Ctrl+D"])
}

func TestFormatLabels_Empty(t *testing.T) {
	require.Equal(t, "-", formatLabels(nil))
	require.Equal(t, "-", formatLabels(map[string]string{}))
}

func TestFormatLabels_Sorted(t *testing.T) {
	labels := map[string]string{"b": "2", "a": "1"}
	require.Equal(t, "a=1,b=2", formatLabels(labels))
}

func TestLabelsScroll(t *testing.T) {
	full := formatLabels(map[string]string{"longkey": "longvalue"})
	require.Equal(t, "longkey=longvalue", full)

	require.Equal(t, "key=longvalue", filterlist.ScrollWindow(full, 4, 20))
}

func TestLabelsScroll_Truncation(t *testing.T) {
	full := formatLabels(map[string]string{"a": "verylongvalue"})
	truncated := filterlist.ScrollWindow(full, 0, 5)
	require.Contains(t, truncated, "…")
	require.Equal(t, 5, len([]rune(truncated)))
}

func TestGetNodesHelpContent(t *testing.T) {
	cats := GetNodesHelpContent()
	require.True(t, len(cats) >= 3)
	require.Equal(t, "General", cats[0].Title)
	require.Equal(t, "View", cats[1].Title)
	require.Equal(t, "Navigation", cats[2].Title)
}
