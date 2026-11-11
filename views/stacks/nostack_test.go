// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	"github.com/charmbracelet/x/ansi"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// Services created outside a stack get a row of their own (#709), tinted by
// their health like any stack, so the stacks view reaches every service.
func TestNoStackRow_ListsAndTintsServicesWithoutAStack(t *testing.T) {
	trueColour(t)
	one := uint64(1)
	snap := &docker.SwarmSnapshot{
		Nodes: []swarm.Node{{ID: "n1", Status: swarm.NodeStatus{State: swarm.NodeStateReady}}},
		Services: []swarm.Service{
			{ID: "web_app", Spec: swarm.ServiceSpec{
				Annotations: swarm.Annotations{Name: "web_app", Labels: map[string]string{"com.docker.stack.namespace": "web"}},
				Mode:        swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &one}},
			}},
			{ID: "loose", Spec: swarm.ServiceSpec{
				Annotations: swarm.Annotations{Name: "loose"},
				Mode:        swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &one}},
			}},
		},
		Tasks: []swarm.Task{
			{ServiceID: "web_app", Slot: 1, NodeID: "n1", DesiredState: swarm.TaskStateRunning,
				Status: swarm.TaskStatus{State: swarm.TaskStateRunning}},
			{ServiceID: "loose", Slot: 1, NodeID: "n1", DesiredState: swarm.TaskStateRunning,
				Meta:   swarm.Meta{CreatedAt: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)},
				Status: swarm.TaskStatus{State: swarm.TaskStateFailed, Err: "task: non-zero exit (1)"}},
		},
	}
	m := stacksModelWithSnapshot(snap)
	m.Update(Msg{Stacks: snap.ToStackEntries()})
	m.List.Viewport.Width = 120
	m.List.Viewport.Height = 20

	require.Contains(t, ansi.Strip(m.View()), noStackLabel)
	noStack := docker.StackEntry{Name: ""}
	require.Equal(t, fgSeq("9"), rowTint(t, m.List.RenderItem(noStack, false, 0), noStackLabel))
	require.True(t, m.HasErrors())
	require.False(t, m.stackHasError["web"])
}

func TestNoStackRow_ConvergingLooseServiceIsAmber(t *testing.T) {
	trueColour(t)
	two := uint64(2)
	task := func(slot int, state swarm.TaskState) swarm.Task {
		return swarm.Task{ServiceID: "loose", Slot: slot, NodeID: "n1", DesiredState: swarm.TaskStateRunning,
			Status: swarm.TaskStatus{State: state}}
	}
	snap := &docker.SwarmSnapshot{
		Nodes: []swarm.Node{{ID: "n1", Status: swarm.NodeStatus{State: swarm.NodeStateReady}}},
		Services: []swarm.Service{{ID: "loose", Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{Name: "loose"},
			Mode:        swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &two}},
		}}},
		Tasks: []swarm.Task{task(1, swarm.TaskStateRunning), task(2, swarm.TaskStateStarting)},
	}
	m := stacksModelWithSnapshot(snap)
	m.Update(Msg{Stacks: snap.ToStackEntries()})
	m.List.Viewport.Width = 120

	require.Equal(t, fgSeq("3"), rowTint(t, m.List.RenderItem(docker.StackEntry{}, false, 0), noStackLabel))
	require.True(t, m.HasWarnings())
}

func TestNoStackRow_EnterOpensServicesWithoutAStack(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("", "web"))

	for _, want := range []map[string]any{{"noStack": true}, {"stackName": "web"}} {
		nav, ok := runCmd(m.Update(key("enter"))).(view.NavigateToMsg)
		require.True(t, ok)
		require.Equal(t, "services", nav.ViewName)
		require.Equal(t, want, nav.Payload)
		m.Update(key("down"))
	}
}

func TestNoStackRow_PExpandsItsTasks(t *testing.T) {
	asked := "unset"
	tasks := noopTaskOps()
	tasks.getTasksForStackFn = func(name string) ([]docker.TaskEntry, error) {
		asked = name
		return nil, nil
	}
	m := testModel(func(m *Model) { m.deps.Tasks = tasks })
	loadStacks(m, fakeStacks(""))
	asked = "unset" // the load itself prefetches every stack's tasks

	runCmd(m.Update(key("p")))
	require.True(t, m.expandedStacks[""])
	require.Equal(t, "", asked)
}

// The stack-level actions have no stack to act on, so each says why instead of
// running against an empty stack name.
func TestNoStackRow_RefusesStackActions(t *testing.T) {
	for _, k := range []string{"i", "e", "s", "ctrl+d", "g"} {
		t.Run(k, func(t *testing.T) {
			stacks := noopStackOps()
			stacks.inspectStackFn = func(string) (string, error) { t.Fatal("inspected"); return "", nil }
			stacks.reconstructStackComposeFn = func(string) (string, error) { t.Fatal("reconstructed"); return "", nil }
			m := testModel(func(m *Model) { m.deps.Stacks = stacks })
			loadStacks(m, fakeStacks(""))

			require.Nil(t, m.Update(key(k)))
			require.Equal(t, noStackLabel+" is not a stack — enter lists its services", m.toastMessage)
			require.False(t, m.saveDialogActive)
			require.False(t, m.confirmDialog.Visible)
		})
	}
}

func TestNoStackRow_FilterMatchesItsLabel(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("", "web"))
	m.ApplySearchQuery("no stack")
	require.Equal(t, []docker.StackEntry{{Name: "", ServiceCount: 1, NodeCount: 1}}, m.List.Filtered)
}

// The cursor stays on the row through a re-sort: its empty name is a real
// row, not "no cursor".
func TestNoStackRow_KeepsTheCursorThroughASort(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("", "a", "b"))
	require.Equal(t, "", m.List.Filtered[m.List.Cursor].Name)

	m.Update(key("S")) // name, descending: b, a, ""
	require.Equal(t, "", m.List.Filtered[m.List.Cursor].Name)
}
