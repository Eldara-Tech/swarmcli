// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"testing"

	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

func globalSvcInStack(id, stack string, constraints ...string) swarm.Service {
	svc := svcInStack(id, stack)
	svc.Spec.Mode = swarm.ServiceMode{Global: &swarm.GlobalService{}}
	if len(constraints) > 0 {
		svc.Spec.TaskTemplate.Placement = &swarm.Placement{Constraints: constraints}
	}
	return svc
}

// --wait must not sit out its timeout on a global service that is already fully
// deployed. Measuring it against the workers it is forbidden to run on made
// every manager-pinned agent look permanently half-rolled-out (issue #643).
func TestStackConvergenceGlobalHonoursPlacementConstraints(t *testing.T) {
	mgr, wrk := readyNode("m1"), readyNode("w1")
	mgr.Spec.Role, wrk.Spec.Role = swarm.NodeRoleManager, swarm.NodeRoleWorker

	snap := &SwarmSnapshot{
		Nodes:    []swarm.Node{mgr, wrk},
		Services: []swarm.Service{globalSvcInStack("agent", "mystack", "node.role == manager")},
		Tasks: []swarm.Task{
			taskInState("agent", "m1", swarm.TaskStateRunning, swarm.TaskStateRunning),
		},
	}

	conv := snap.StackConvergence("mystack")
	require.Len(t, conv, 1)
	require.Equal(t, 1, conv[0].Running)
	require.Equal(t, 1, conv[0].Desired)
}

// Swarm keeps a paused node's tasks running, so both halves of the ratio must
// count it. Dropping the node from the denominator while its task still counted
// in the numerator reported 2/1.
func TestStackConvergenceCountsPausedNodes(t *testing.T) {
	paused := readyNode("n2")
	paused.Spec.Availability = swarm.NodeAvailabilityPause

	snap := &SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1"), paused},
		Services: []swarm.Service{globalSvcInStack("agent", "mystack")},
		Tasks: []swarm.Task{
			taskInState("agent", "n1", swarm.TaskStateRunning, swarm.TaskStateRunning),
			taskInState("agent", "n2", swarm.TaskStateRunning, swarm.TaskStateRunning),
		},
	}

	conv := snap.StackConvergence("mystack")
	require.Len(t, conv, 1)
	require.Equal(t, 2, conv[0].Running)
	require.Equal(t, 2, conv[0].Desired)
}

// A drained node runs nothing and is not waited for.
func TestStackConvergenceSkipsDrainedNodes(t *testing.T) {
	drained := readyNode("n2")
	drained.Spec.Availability = swarm.NodeAvailabilityDrain

	snap := &SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1"), drained},
		Services: []swarm.Service{globalSvcInStack("agent", "mystack")},
		Tasks: []swarm.Task{
			taskInState("agent", "n1", swarm.TaskStateRunning, swarm.TaskStateRunning),
			taskInState("agent", "n2", swarm.TaskStateRunning, swarm.TaskStateRunning),
		},
	}

	conv := snap.StackConvergence("mystack")
	require.Len(t, conv, 1)
	require.Equal(t, 1, conv[0].Running, "a drained node's task is not waited for")
	require.Equal(t, 1, conv[0].Desired)
}
