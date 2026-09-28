// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// An empty context name is rejected rather than quietly falling back to the
// ambient one. Silently resolving it would defeat the whole point of the
// explicit variants: a caller that meant to name a swarm and passed "" would
// get the process default and no indication that it had.
func TestExplicitContextRejectsEmptyName(t *testing.T) {
	_, err := ClientFor("")
	require.ErrorContains(t, err, "docker context name is required")

	require.ErrorContains(t, DeployStackInContext(context.Background(), "", "web", "services: {}\n", ResolveImageDefault, nil, DeployOptions{}),
		"docker context name is required")
	require.ErrorContains(t, RemoveStackCLIInContext(context.Background(), "", "web"),
		"docker context name is required")
}

// StackServices reads the snapshot it is given, not the process-wide cache.
// That is what lets a caller polling one swarm avoid reading another's state:
// with the global cache deliberately holding a different stack, the receiver
// still wins.
func TestStackServicesReadsItsOwnSnapshotNotTheGlobalCache(t *testing.T) {
	t.Cleanup(InvalidateSnapshot)
	SetSnapshot(&SwarmSnapshot{
		Services: []swarm.Service{svcInStack("other-svc", "otherstack")},
	})

	mine := &SwarmSnapshot{Services: []swarm.Service{svcInStack("mine-svc", "mystack")}}

	entries := mine.StackServices("mystack")
	require.Len(t, entries, 1)
	require.Equal(t, "mine-svc", entries[0].ServiceName)

	// And the foreign snapshot's stack is invisible through this one.
	require.Empty(t, mine.StackServices("otherstack"))
}

// The same for convergence, which is the one that would actually hurt: --wait
// polls it, so reading another swarm's tasks would report a rollout finished
// that had never started.
func TestStackConvergenceReadsItsOwnSnapshotNotTheGlobalCache(t *testing.T) {
	t.Cleanup(InvalidateSnapshot)
	SetSnapshot(&SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{svcInStack("other-svc", "otherstack")},
	})

	mine := &SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{svcInStack("mine-svc", "mystack")},
		Tasks: []swarm.Task{{
			ServiceID:    "mine-svc",
			NodeID:       "n1",
			DesiredState: swarm.TaskStateRunning,
			Status:       swarm.TaskStatus{State: swarm.TaskStateRunning},
		}},
	}

	conv := mine.StackConvergence("mystack")
	require.Len(t, conv, 1)
	require.Equal(t, "mine-svc", conv[0].Name)
	require.Equal(t, 1, conv[0].Running)

	require.Empty(t, mine.StackConvergence("otherstack"))
}

func svcInStack(id, stack string) swarm.Service {
	return swarm.Service{
		ID: id,
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{
				Name:   id,
				Labels: map[string]string{"com.docker.stack.namespace": stack},
			},
		},
	}
}

func readyNode(id string) swarm.Node {
	return swarm.Node{
		ID:     id,
		Status: swarm.NodeStatus{State: swarm.NodeStateReady},
		Spec:   swarm.NodeSpec{Availability: swarm.NodeAvailabilityActive},
	}
}
