// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
)

// A named backend addresses the context it was constructed with, not the one
// the process is pointed at. DOCKER_CONTEXT is set to something else here
// precisely so a fallback would be visible.
func TestNamedBackendIgnoresTheAmbientContext(t *testing.T) {
	t.Setenv("DOCKER_CONTEXT", "ambient")

	b, ok := NewDockerBackend("swarm-b").(*dockerBackend)
	require.True(t, ok)

	name, err := b.contextName()
	require.NoError(t, err)
	require.Equal(t, "swarm-b", name)
}

// The default backend keeps resolving the ambient context, so the CLI behaves
// exactly as it did.
func TestAmbientBackendResolvesTheProcessContext(t *testing.T) {
	t.Setenv("DOCKER_CONTEXT", "ambient")

	name, err := (&dockerBackend{}).contextName()
	require.NoError(t, err)
	require.Equal(t, "ambient", name)
}

// A named backend must not write to the shared snapshot cache. The cache holds
// exactly one swarm, so a reconcile against a second one that refreshed it
// would replace another swarm's state with its own — and every reader of the
// cache, including a TUI in the same process, would silently follow.
func TestNamedBackendRefreshDoesNotTouchTheSharedCache(t *testing.T) {
	t.Cleanup(docker.InvalidateSnapshot)
	mine := &docker.SwarmSnapshot{Services: []swarm.Service{{ID: "sentinel"}}}
	docker.SetSnapshot(mine)

	require.NoError(t, NewDockerBackend("swarm-b").RefreshSnapshot(context.Background()))

	got := docker.GetSnapshot()
	require.NotNil(t, got)
	require.Len(t, got.Services, 1)
	require.Equal(t, "sentinel", got.Services[0].ID, "the shared cache must be untouched")
}

// --- ServiceStatesFrom ---

func readyNode(id string) swarm.Node {
	return swarm.Node{
		ID:     id,
		Status: swarm.NodeStatus{State: swarm.NodeStateReady},
		Spec:   swarm.NodeSpec{Availability: swarm.NodeAvailabilityActive},
	}
}

func stackService(id, stack string, replicas uint64) swarm.Service {
	return swarm.Service{
		ID: id,
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{
				Name:   id,
				Labels: map[string]string{"com.docker.stack.namespace": stack},
			},
			Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
		},
	}
}

// The slot is not decoration: swarm numbers a replicated service's replicas
// from 1, and the convergence count keeps only the newest task in each slot, so
// two tasks sharing slot 0 are two generations of one replica.
func runningTask(svcID, nodeID string, slot int) swarm.Task {
	return swarm.Task{
		ServiceID:    svcID,
		NodeID:       nodeID,
		Slot:         slot,
		DesiredState: swarm.TaskStateRunning,
		Status:       swarm.TaskStatus{State: swarm.TaskStateRunning},
	}
}

// The states a caller gets carry both halves: the display line from the service
// entry, and the convergence facts --wait and a health rollup decide on.
func TestServiceStatesFromCarriesBothHalves(t *testing.T) {
	snap := &docker.SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{stackService("api", "mystack", 2)},
		Tasks:    []swarm.Task{runningTask("api", "n1", 1), runningTask("api", "n1", 2)},
	}

	states := ServiceStatesFrom(snap, "mystack")
	require.Len(t, states, 1)
	require.Equal(t, "api", states[0].Name)
	require.Equal(t, "replicated", states[0].Mode)
	require.Equal(t, "2/2", states[0].Replicas)
	require.Equal(t, 2, states[0].Running, "the running count is by ACTUAL state, not desired (#480)")
	require.Equal(t, 2, states[0].Desired)
}

// The rule this function exists to keep one copy of. A one-shot step ends with
// its task Complete and nothing running, which is its success state — without
// this the release reads 0/1 forever and looks degraded when it is done (#443).
func TestServiceStatesFromReportsAFinishedJobAsCompleted(t *testing.T) {
	svc := stackService("init", "mystack", 1)
	svc.Spec.TaskTemplate.RestartPolicy = &swarm.RestartPolicy{Condition: swarm.RestartPolicyConditionNone}

	snap := &docker.SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{svc},
		Tasks: []swarm.Task{{
			ServiceID: "init",
			NodeID:    "n1",
			// Old enough to have served out the stability window, which applies
			// to a job's task as much as to a long-running one.
			Meta:         swarm.Meta{CreatedAt: time.Now().Add(-time.Hour)},
			DesiredState: swarm.TaskStateShutdown,
			Status:       swarm.TaskStatus{State: swarm.TaskStateComplete},
		}},
	}

	states := ServiceStatesFrom(snap, "mystack")
	require.Len(t, states, 1)
	require.True(t, states[0].Job)
	require.Zero(t, states[0].Running, "a finished job has nothing running")
	require.Equal(t, 1, states[0].Completed)
	require.Equal(t, "1/1", states[0].Replicas, "a completed task counts toward the target")
	require.Equal(t, "completed", states[0].Status)
	require.Equal(t, PhaseConverged, Rollup(states).Phase, "and the release is converged, not degraded")
}

// A native job's ratio is its progress, completions against TotalCompletions,
// and it reads "completed" only once every completion is in (issue #666).
func TestServiceStatesFromReportsNativeJobProgress(t *testing.T) {
	total, maxConc := uint64(3), uint64(1)
	svc := stackService("batch", "mystack", 1)
	svc.Spec.Mode = swarm.ServiceMode{ReplicatedJob: &swarm.ReplicatedJob{MaxConcurrent: &maxConc, TotalCompletions: &total}}
	svc.JobStatus = &swarm.JobStatus{}
	task := func(slot int, state swarm.TaskState) swarm.Task {
		return swarm.Task{
			ServiceID: "batch", NodeID: "n1", Slot: slot,
			Meta:         swarm.Meta{CreatedAt: time.Now().Add(-time.Hour)},
			DesiredState: swarm.TaskStateComplete,
			Status:       swarm.TaskStatus{State: state},
			JobIteration: &swarm.Version{},
		}
	}
	snap := &docker.SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{svc},
		Tasks:    []swarm.Task{task(0, swarm.TaskStateComplete), task(1, swarm.TaskStateRunning)},
	}

	states := ServiceStatesFrom(snap, "mystack")
	require.Len(t, states, 1)
	require.Equal(t, "replicated job", states[0].Mode)
	require.Equal(t, "1/3", states[0].Replicas)
	require.Equal(t, "active", states[0].Status)
	require.Equal(t, PhaseProgressing, Rollup(states).Phase)

	snap.Tasks = []swarm.Task{task(0, swarm.TaskStateComplete), task(1, swarm.TaskStateComplete), task(2, swarm.TaskStateComplete)}
	states = ServiceStatesFrom(snap, "mystack")
	require.Equal(t, "3/3", states[0].Replicas)
	require.Equal(t, "completed", states[0].Status)
	require.Equal(t, PhaseConverged, Rollup(states).Phase)
}

// A snapshot holding several stacks answers about one of them.
func TestServiceStatesFromIsScopedToTheStack(t *testing.T) {
	snap := &docker.SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{stackService("mine", "a", 1), stackService("theirs", "b", 1)},
		Tasks:    []swarm.Task{runningTask("mine", "n1", 1), runningTask("theirs", "n1", 1)},
	}

	states := ServiceStatesFrom(snap, "a")
	require.Len(t, states, 1)
	require.Equal(t, "mine", states[0].Name)
}
