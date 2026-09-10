// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

func TestGetServiceStatus_Active(t *testing.T) {
	svc := swarm.Service{UpdateStatus: nil}
	require.Equal(t, "active", getServiceStatus(svc))
}

func TestGetServiceStatus_Updating(t *testing.T) {
	tests := []struct {
		state    swarm.UpdateState
		expected string
	}{
		{swarm.UpdateStateUpdating, "updating"},
		{swarm.UpdateStatePaused, "paused"},
		{swarm.UpdateStateCompleted, "updated"},
		{swarm.UpdateStateRollbackStarted, "rolling back"},
		{swarm.UpdateStateRollbackPaused, "rollback paused"},
		{swarm.UpdateStateRollbackCompleted, "rolled back"},
	}
	for _, tc := range tests {
		t.Run(string(tc.state), func(t *testing.T) {
			svc := swarm.Service{
				UpdateStatus: &swarm.UpdateStatus{State: tc.state},
			}
			require.Equal(t, tc.expected, getServiceStatus(svc))
		})
	}
}

func TestGetServiceStackAndDesired_Replicated(t *testing.T) {
	replicas := uint64(3)
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{Labels: map[string]string{"com.docker.stack.namespace": "mystack"}},
			Mode:        swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
		},
	}
	snap := &SwarmSnapshot{}
	stack, desired := getServiceStackAndDesired(svc, snap)
	require.Equal(t, "mystack", stack)
	require.Equal(t, 3, desired)
}

func TestGetServiceStackAndDesired_Global(t *testing.T) {
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{Labels: map[string]string{"com.docker.stack.namespace": "mystack"}},
			Mode:        swarm.ServiceMode{Global: &swarm.GlobalService{}},
		},
	}
	snap := &SwarmSnapshot{
		Nodes: []swarm.Node{activeNode("n1"), activeNode("n2"), activeNode("n3")},
	}
	stack, desired := getServiceStackAndDesired(svc, snap)
	require.Equal(t, "mystack", stack)
	require.Equal(t, 3, desired)
}

// A global service targets one task per node that can actually run one. Counting
// a drained or down node in the denominator made a fully healthy service read
// permanently short (issue #480).
func TestGetServiceStackAndDesired_GlobalIgnoresInactiveNodes(t *testing.T) {
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{Labels: map[string]string{"com.docker.stack.namespace": "mystack"}},
			Mode:        swarm.ServiceMode{Global: &swarm.GlobalService{}},
		},
	}
	drained := activeNode("n3")
	drained.Spec.Availability = swarm.NodeAvailabilityDrain
	down := activeNode("n4")
	down.Status.State = swarm.NodeStateDown

	snap := &SwarmSnapshot{Nodes: []swarm.Node{activeNode("n1"), activeNode("n2"), drained, down}}
	_, desired := getServiceStackAndDesired(svc, snap)
	require.Equal(t, 2, desired)
}

// A global service targets the nodes its placement constraints admit, not every
// node in the swarm. A service pinned to the managers of a 3+3 swarm read 3/6
// and never converged in the eyes of the UI (issue #643).
func TestGetServiceStackAndDesired_GlobalHonoursPlacementConstraints(t *testing.T) {
	svc := globalService("node.role == manager")
	snap := &SwarmSnapshot{Nodes: []swarm.Node{
		roleNode("m1", swarm.NodeRoleManager), roleNode("m2", swarm.NodeRoleManager), roleNode("m3", swarm.NodeRoleManager),
		roleNode("w1", swarm.NodeRoleWorker), roleNode("w2", swarm.NodeRoleWorker), roleNode("w3", swarm.NodeRoleWorker),
	}}
	_, desired := getServiceStackAndDesired(svc, snap)
	require.Equal(t, 3, desired)
}

// Constraints and node state compose: draining an eligible node lowers the
// target, draining an ineligible one changes nothing.
func TestGetServiceStackAndDesired_GlobalConstrainedAndDrained(t *testing.T) {
	drained := roleNode("m2", swarm.NodeRoleManager)
	drained.Spec.Availability = swarm.NodeAvailabilityDrain
	snap := &SwarmSnapshot{Nodes: []swarm.Node{
		roleNode("m1", swarm.NodeRoleManager), drained, roleNode("w1", swarm.NodeRoleWorker),
	}}
	_, desired := getServiceStackAndDesired(globalService("node.role == manager"), snap)
	require.Equal(t, 1, desired)
}

// A constraint no node satisfies means swarm wants zero tasks, and the services
// view renders a zero target as "—" rather than a false shortfall.
func TestGetServiceStackAndDesired_GlobalUnsatisfiableConstraint(t *testing.T) {
	snap := &SwarmSnapshot{Nodes: []swarm.Node{activeNode("n1"), activeNode("n2")}}
	_, desired := getServiceStackAndDesired(globalService("node.labels.absent == true"), snap)
	require.Equal(t, 0, desired)
}

// A replicated service's declared count is its target wherever the replicas can
// land. Filtering it by constraints would report an unschedulable service as
// converged — the opposite of what --wait must do.
func TestGetServiceStackAndDesired_ReplicatedIgnoresConstraints(t *testing.T) {
	three := uint64(3)
	svc := swarm.Service{Spec: swarm.ServiceSpec{
		Mode:         swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &three}},
		TaskTemplate: swarm.TaskSpec{Placement: &swarm.Placement{Constraints: []string{"node.labels.absent == true"}}},
	}}
	snap := &SwarmSnapshot{Nodes: []swarm.Node{activeNode("n1"), activeNode("n2")}}
	_, desired := getServiceStackAndDesired(svc, snap)
	require.Equal(t, 3, desired)
}

// Swarm keeps a paused node's global task running, so the node belongs in the
// denominator its task is counted against. Excluding it read 3/2.
func TestGetServiceStackAndDesired_GlobalCountsPausedNodes(t *testing.T) {
	paused := activeNode("n2")
	paused.Spec.Availability = swarm.NodeAvailabilityPause
	snap := &SwarmSnapshot{Nodes: []swarm.Node{activeNode("n1"), paused}}
	_, desired := getServiceStackAndDesired(globalService(), snap)
	require.Equal(t, 2, desired)
}

func globalService(constraints ...string) swarm.Service {
	svc := swarm.Service{Spec: swarm.ServiceSpec{
		Annotations: swarm.Annotations{Labels: map[string]string{"com.docker.stack.namespace": "mystack"}},
		Mode:        swarm.ServiceMode{Global: &swarm.GlobalService{}},
	}}
	if len(constraints) > 0 {
		svc.Spec.TaskTemplate.Placement = &swarm.Placement{Constraints: constraints}
	}
	return svc
}

func roleNode(id string, role swarm.NodeRole) swarm.Node {
	n := activeNode(id)
	n.Spec.Role = role
	return n
}

func activeNode(id string) swarm.Node {
	return swarm.Node{
		ID:     id,
		Status: swarm.NodeStatus{State: swarm.NodeStateReady},
		Spec:   swarm.NodeSpec{Availability: swarm.NodeAvailabilityActive},
	}
}

func TestGetServiceStackAndDesired_NoStack(t *testing.T) {
	replicas := uint64(1)
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
		},
	}
	snap := &SwarmSnapshot{}
	stack, _ := getServiceStackAndDesired(svc, snap)
	require.Equal(t, "-", stack)
}

// runningTask is a task that is actually up: swarm intends to run it AND the
// container reports running.
func runningTask(svcID, nodeID string) swarm.Task {
	return swarm.Task{
		ServiceID:    svcID,
		NodeID:       nodeID,
		DesiredState: swarm.TaskStateRunning,
		Status:       swarm.TaskStatus{State: swarm.TaskStateRunning},
	}
}

func TestCountTasksForNode_All(t *testing.T) {
	snap := &SwarmSnapshot{
		Tasks: []swarm.Task{
			runningTask("svc1", "n1"),
			runningTask("svc1", "n2"),
			runningTask("svc2", "n1"),
		},
	}
	require.Equal(t, 2, countTasksForNode("svc1", "", snap))
}

func TestCountTasksForNode_Specific(t *testing.T) {
	snap := &SwarmSnapshot{
		Tasks: []swarm.Task{runningTask("svc1", "n1"), runningTask("svc1", "n2")},
	}
	require.Equal(t, 1, countTasksForNode("svc1", "n1", snap))
}

func TestCountTasksForNode_SkipsNonRunning(t *testing.T) {
	shutdown := runningTask("svc1", "n1")
	shutdown.DesiredState = swarm.TaskStateShutdown
	shutdown.Status.State = swarm.TaskStateShutdown

	snap := &SwarmSnapshot{Tasks: []swarm.Task{runningTask("svc1", "n1"), shutdown}}
	require.Equal(t, 1, countTasksForNode("svc1", "", snap))
}

// The bug this fixes: a task swarm INTENDS to run but which has not started —
// pending on an unsatisfiable placement constraint, or still pulling an image —
// was counted as if it were up, so the column read 3/3 where `docker service ls`
// read 1/3 and the service never converged (issue #480).
func TestCountTasksForNode_SkipsScheduledButNotYetRunning(t *testing.T) {
	scheduled := func(state swarm.TaskState) swarm.Task {
		t := runningTask("svc1", "n1")
		t.Status.State = state // still intended to run, not there yet
		return t
	}
	snap := &SwarmSnapshot{
		Tasks: []swarm.Task{
			runningTask("svc1", "n1"),
			scheduled(swarm.TaskStatePending),
			scheduled(swarm.TaskStateStarting),
			scheduled(swarm.TaskStatePreparing),
		},
	}
	require.Equal(t, 1, countTasksForNode("svc1", "", snap))
}

// A superseded task is still counted while it runs, matching `docker service
// ls`. Up-to-dateness is a separate question, and LoadStackConvergence is where
// --wait asks it.
func TestCountTasksForNode_CountsSupersededTasksStillRunning(t *testing.T) {
	outgoing := runningTask("svc1", "n1")
	outgoing.DesiredState = swarm.TaskStateShutdown // superseded, container still up

	snap := &SwarmSnapshot{Tasks: []swarm.Task{runningTask("svc1", "n2"), outgoing}}
	require.Equal(t, 2, countTasksForNode("svc1", "", snap))
}

// A node runs a service's task, or is meant to. Counting only running tasks made
// the node-scoped view drop a service whose task there had not started — the one
// an operator opens that node to find (issue #480).
func TestHasIntendedTaskOnNode(t *testing.T) {
	preparing := runningTask("svc1", "n1")
	preparing.Status.State = swarm.TaskStatePreparing

	terminal := runningTask("svc1", "n2")
	terminal.DesiredState = swarm.TaskStateShutdown
	terminal.Status.State = swarm.TaskStateShutdown

	snap := &SwarmSnapshot{Tasks: []swarm.Task{preparing, terminal, runningTask("svc2", "n3")}}

	require.True(t, hasIntendedTaskOnNode("svc1", "n1", snap), "task preparing on the node")
	require.False(t, hasIntendedTaskOnNode("svc1", "n2", snap), "only a terminal task on the node")
	require.False(t, hasIntendedTaskOnNode("svc1", "n3", snap), "another service's node")
}

// slotTask builds a task carrying the three facts generation counting turns on:
// which replica it is, whether it is up, and when it was created.
func slotTask(svcID, nodeID string, slot int, state swarm.TaskState, ageSeconds int) swarm.Task {
	return swarm.Task{
		ServiceID:    svcID,
		NodeID:       nodeID,
		Slot:         slot,
		DesiredState: swarm.TaskStateRunning,
		Status:       swarm.TaskStatus{State: state},
		Meta:         swarm.Meta{CreatedAt: time.Now().Add(-time.Duration(ageSeconds) * time.Second)},
	}
}

func TestCountUpToDateTasks_SteadyState(t *testing.T) {
	snap := &SwarmSnapshot{Tasks: []swarm.Task{
		slotTask("svc1", "n1", 1, swarm.TaskStateRunning, 3600),
		slotTask("svc1", "n2", 2, swarm.TaskStateRunning, 3600),
	}}
	require.Equal(t, 2, countUpToDateTasks("svc1", snap))
}

// The reported case: a start-first rollout where every running replica is still
// the outgoing generation. REPLICAS reads 2/2 — matching `docker service ls` —
// so the count that shows the rollout has not landed has to come from elsewhere.
func TestCountUpToDateTasks_StartFirstRolloutExcludesOutgoing(t *testing.T) {
	outgoing := slotTask("svc1", "n3", 1, swarm.TaskStateRunning, 1209600) // up 2 weeks
	incoming := slotTask("svc1", "n29", 1, swarm.TaskStatePreparing, 26)   // replacing it

	replaced := slotTask("svc1", "n2", 2, swarm.TaskStateShutdown, 1209600)
	replaced.DesiredState = swarm.TaskStateShutdown
	landed := slotTask("svc1", "n2", 2, swarm.TaskStateRunning, 36)

	snap := &SwarmSnapshot{Tasks: []swarm.Task{outgoing, incoming, replaced, landed}}

	require.Equal(t, 2, countTasksForNode("svc1", "", snap), "two containers are up")
	require.Equal(t, 1, countUpToDateTasks("svc1", snap), "only one replica is on the new generation")
}

// The window start-first opens: the incoming task is up before the outgoing one
// is torn down, so a 2-replica service has three containers running. REPLICAS
// reads 3/2 — as `docker service ls` does — while the generation count stays
// bounded by the slots.
func TestCountUpToDateTasks_BoundedBySlotsDuringOverlap(t *testing.T) {
	snap := &SwarmSnapshot{Tasks: []swarm.Task{
		slotTask("svc1", "n3", 1, swarm.TaskStateRunning, 1209600),
		slotTask("svc1", "n29", 1, swarm.TaskStateRunning, 26),
		slotTask("svc1", "n2", 2, swarm.TaskStateRunning, 36),
	}}

	require.Equal(t, 3, countTasksForNode("svc1", "", snap))
	require.Equal(t, 2, countUpToDateTasks("svc1", snap))
}

// A global service's tasks carry no slot, so the node is what identifies the
// replica.
func TestCountUpToDateTasks_GlobalKeysByNode(t *testing.T) {
	snap := &SwarmSnapshot{Tasks: []swarm.Task{
		slotTask("svc1", "n1", 0, swarm.TaskStateRunning, 3600),
		slotTask("svc1", "n2", 0, swarm.TaskStateShutdown, 3600),
		slotTask("svc1", "n2", 0, swarm.TaskStatePreparing, 10),
	}}
	require.Equal(t, 1, countUpToDateTasks("svc1", snap))
}

// A task with neither slot nor node has not been assigned yet. It identifies no
// replica, so it must not collide with the others under a shared empty key.
func TestCountUpToDateTasks_SkipsUnassignedTasks(t *testing.T) {
	snap := &SwarmSnapshot{Tasks: []swarm.Task{
		slotTask("svc1", "", 0, swarm.TaskStatePending, 5),
		slotTask("svc1", "n1", 0, swarm.TaskStateRunning, 3600),
	}}
	require.Equal(t, 1, countUpToDateTasks("svc1", snap))
}

func TestCountUpToDateTasks_IgnoresOtherServices(t *testing.T) {
	snap := &SwarmSnapshot{Tasks: []swarm.Task{
		slotTask("svc1", "n1", 1, swarm.TaskStateRunning, 3600),
		slotTask("svc2", "n1", 1, swarm.TaskStateRunning, 3600),
	}}
	require.Equal(t, 1, countUpToDateTasks("svc1", snap))
}

func TestIsRollingOut(t *testing.T) {
	require.False(t, isRollingOut(swarm.Service{}), "never updated")

	cases := map[swarm.UpdateState]bool{
		swarm.UpdateStateUpdating:          true,
		swarm.UpdateStatePaused:            true,
		swarm.UpdateStateRollbackStarted:   true,
		swarm.UpdateStateRollbackPaused:    true,
		swarm.UpdateStateCompleted:         false,
		swarm.UpdateStateRollbackCompleted: false,
	}
	for state, want := range cases {
		t.Run(string(state), func(t *testing.T) {
			svc := swarm.Service{UpdateStatus: &swarm.UpdateStatus{State: state}}
			require.Equal(t, want, isRollingOut(svc))
		})
	}
}

func TestSortEntries(t *testing.T) {
	entries := []ServiceEntry{
		{StackName: "beta", ServiceName: "web"},
		{StackName: "alpha", ServiceName: "api"},
		{StackName: "alpha", ServiceName: "db"},
	}
	sortEntries(entries)
	require.Equal(t, "alpha", entries[0].StackName)
	require.Equal(t, "api", entries[0].ServiceName)
	require.Equal(t, "alpha", entries[1].StackName)
	require.Equal(t, "db", entries[1].ServiceName)
	require.Equal(t, "beta", entries[2].StackName)
}

func TestStripDockerLogHeaders_Empty(t *testing.T) {
	require.Equal(t, "", stripDockerLogHeaders(nil))
}

func TestStripDockerLogHeaders_Short(t *testing.T) {
	require.Equal(t, "hello", stripDockerLogHeaders([]byte("hello")))
}

func TestStripDockerLogHeaders_NotMultiplexed(t *testing.T) {
	data := []byte("plain text log line that is longer than 8 bytes")
	require.Equal(t, string(data), stripDockerLogHeaders(data))
}

func TestStripDockerLogHeaders_SingleFrame(t *testing.T) {
	payload := []byte("hello world")
	header := make([]byte, 8)
	header[0] = 1 // stdout
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	frame := append(header, payload...)
	require.Equal(t, "hello world", stripDockerLogHeaders(frame))
}

func TestStripDockerLogHeaders_MultipleFrames(t *testing.T) {
	var frames []byte
	for _, msg := range []string{"hello ", "world"} {
		header := make([]byte, 8)
		header[0] = 1
		binary.BigEndian.PutUint32(header[4:], uint32(len(msg)))
		frames = append(frames, header...)
		frames = append(frames, []byte(msg)...)
	}
	require.Equal(t, "hello world", stripDockerLogHeaders(frames))
}

func TestStripDockerLogHeaders_InvalidSize(t *testing.T) {
	header := make([]byte, 8)
	header[0] = 1
	binary.BigEndian.PutUint32(header[4:], 9999) // size exceeds available data
	frame := append(header, []byte("short")...)
	result := stripDockerLogHeaders(frame)
	require.Equal(t, string(frame), result, "should fallback to raw on invalid frame")
}

func TestTrySendProgress_BufferedChannel(t *testing.T) {
	ch := make(chan ProgressUpdate, 1)
	trySendProgress(ch, ProgressUpdate{Replaced: 1, Running: 2, Total: 3})
	got := <-ch
	require.Equal(t, 1, got.Replaced)
	require.Equal(t, 2, got.Running)
	require.Equal(t, 3, got.Total)
}

func TestTrySendProgress_FullChannel(t *testing.T) {
	ch := make(chan ProgressUpdate) // unbuffered
	done := make(chan struct{})
	go func() {
		trySendProgress(ch, ProgressUpdate{Replaced: 1, Total: 1})
		close(done)
	}()
	select {
	case <-done:
		// good - did not block
	case <-time.After(time.Second):
		t.Fatal("trySendProgress blocked on full channel")
	}
}
