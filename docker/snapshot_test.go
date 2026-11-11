// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"testing"

	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/api/types/system"
	"github.com/stretchr/testify/require"
)

func TestToNodeEntries_Empty(t *testing.T) {
	snap := SwarmSnapshot{}
	entries := snap.ToNodeEntries()
	require.Empty(t, entries)
}

func TestToNodeEntries_Basic(t *testing.T) {
	snap := SwarmSnapshot{
		Nodes: []swarm.Node{
			{
				ID: "node1",
				Description: swarm.NodeDescription{
					Hostname: "host-a",
					Engine:   swarm.EngineDescription{EngineVersion: "24.0.7"},
				},
				Spec: swarm.NodeSpec{
					Role:         swarm.NodeRoleWorker,
					Availability: swarm.NodeAvailabilityActive,
				},
				Status: swarm.NodeStatus{State: swarm.NodeStateReady, Addr: "10.0.0.1"},
			},
		},
	}
	entries := snap.ToNodeEntries()
	require.Len(t, entries, 1)
	require.Equal(t, "host-a", entries[0].Hostname)
	require.Equal(t, "worker", entries[0].Role)
	require.Equal(t, "ready", entries[0].State)
	require.Equal(t, "active", entries[0].Availability)
	require.Equal(t, "24.0.7", entries[0].Version)
	require.Equal(t, "10.0.0.1", entries[0].Addr)
	require.False(t, entries[0].Manager)
}

func TestToNodeEntries_SortedByHostname(t *testing.T) {
	snap := SwarmSnapshot{
		Nodes: []swarm.Node{
			{Description: swarm.NodeDescription{Hostname: "charlie"}, Spec: swarm.NodeSpec{Availability: "active"}},
			{Description: swarm.NodeDescription{Hostname: "alice"}, Spec: swarm.NodeSpec{Availability: "active"}},
			{Description: swarm.NodeDescription{Hostname: "bob"}, Spec: swarm.NodeSpec{Availability: "active"}},
		},
	}
	entries := snap.ToNodeEntries()
	require.Equal(t, "alice", entries[0].Hostname)
	require.Equal(t, "bob", entries[1].Hostname)
	require.Equal(t, "charlie", entries[2].Hostname)
}

func TestToNodeEntries_ManagerStatus(t *testing.T) {
	snap := SwarmSnapshot{
		Nodes: []swarm.Node{
			{
				Description:   swarm.NodeDescription{Hostname: "leader"},
				Spec:          swarm.NodeSpec{Availability: "active", Role: swarm.NodeRoleManager},
				ManagerStatus: &swarm.ManagerStatus{Leader: true, Reachability: swarm.ReachabilityReachable},
			},
			{
				Description:   swarm.NodeDescription{Hostname: "reachable"},
				Spec:          swarm.NodeSpec{Availability: "active", Role: swarm.NodeRoleManager},
				ManagerStatus: &swarm.ManagerStatus{Leader: false, Reachability: swarm.ReachabilityReachable},
			},
		},
	}
	entries := snap.ToNodeEntries()
	require.Equal(t, "Leader", entries[0].ManagerStatus)
	require.True(t, entries[0].Manager)
	require.Equal(t, "reachable", entries[1].ManagerStatus)
	require.True(t, entries[1].Manager)
}

func TestToNodeEntries_DefaultAvailability(t *testing.T) {
	snap := SwarmSnapshot{
		Nodes: []swarm.Node{
			{Description: swarm.NodeDescription{Hostname: "host"}, Spec: swarm.NodeSpec{Availability: ""}},
		},
	}
	entries := snap.ToNodeEntries()
	require.Equal(t, "active", entries[0].Availability)
}

func TestToStackEntries_Empty(t *testing.T) {
	snap := SwarmSnapshot{}
	entries := snap.ToStackEntries()
	require.Empty(t, entries)
}

func TestToStackEntries_SingleStack(t *testing.T) {
	snap := SwarmSnapshot{
		Services: []swarm.Service{
			{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Labels: map[string]string{"com.docker.stack.namespace": "mystack"}}}},
			{ID: "svc2", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Labels: map[string]string{"com.docker.stack.namespace": "mystack"}}}},
		},
	}
	entries := snap.ToStackEntries()
	require.Len(t, entries, 1)
	require.Equal(t, "mystack", entries[0].Name)
	require.Equal(t, 2, entries[0].ServiceCount)
}

func TestToStackEntries_MultipleStacks(t *testing.T) {
	snap := SwarmSnapshot{
		Services: []swarm.Service{
			{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Labels: map[string]string{"com.docker.stack.namespace": "beta"}}}},
			{ID: "svc2", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Labels: map[string]string{"com.docker.stack.namespace": "alpha"}}}},
		},
	}
	entries := snap.ToStackEntries()
	require.Len(t, entries, 2)
	require.Equal(t, "alpha", entries[0].Name)
	require.Equal(t, "beta", entries[1].Name)
}

func TestToStackEntries_NodeCountFromTasks(t *testing.T) {
	snap := SwarmSnapshot{
		Services: []swarm.Service{
			{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Labels: map[string]string{"com.docker.stack.namespace": "mystack"}}}},
		},
		Tasks: []swarm.Task{
			{ServiceID: "svc1", NodeID: "node1"},
			{ServiceID: "svc1", NodeID: "node2"},
		},
	}
	entries := snap.ToStackEntries()
	require.Len(t, entries, 1)
	require.Equal(t, 2, entries[0].NodeCount)
}

func TestToStackEntries_GroupsServicesWithoutAStack(t *testing.T) {
	snap := SwarmSnapshot{
		Services: []swarm.Service{
			{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Labels: map[string]string{"com.docker.stack.namespace": "mystack"}}}},
			{ID: "loose1"},
			{ID: "loose2", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Labels: map[string]string{"app": "x"}}}},
		},
		Tasks: []swarm.Task{
			{ServiceID: "svc1"},
			{ServiceID: "loose1"},
			{ServiceID: "loose2"},
			{ServiceID: "loose2"},
			// A task whose service is gone belongs to no entry, not to the
			// services without a stack.
			{ServiceID: "removed"},
			{},
		},
	}
	entries := snap.ToStackEntries()
	require.Equal(t, []StackEntry{
		{Name: "", ServiceCount: 2, NodeCount: 3},
		{Name: "mystack", ServiceCount: 1, NodeCount: 1},
	}, entries)
}

func TestFindService_Found(t *testing.T) {
	snap := &SwarmSnapshot{
		Services: []swarm.Service{
			{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "web"}}},
		},
	}
	svc := snap.FindService("svc1")
	require.NotNil(t, svc)
	require.Equal(t, "svc1", svc.ID)
}

func TestFindService_NotFound(t *testing.T) {
	snap := &SwarmSnapshot{
		Services: []swarm.Service{
			{ID: "svc1"},
		},
	}
	svc := snap.FindService("nonexistent")
	require.Nil(t, svc)
}

func TestFindServiceByName_Found(t *testing.T) {
	snap := &SwarmSnapshot{
		Services: []swarm.Service{
			{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "web"}}},
		},
	}
	svc := snap.FindServiceByName("web")
	require.NotNil(t, svc)
	require.Equal(t, "svc1", svc.ID)
}

func TestFindServiceByName_NotFound(t *testing.T) {
	snap := &SwarmSnapshot{
		Services: []swarm.Service{
			{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "web"}}},
		},
	}
	svc := snap.FindServiceByName("nonexistent")
	require.Nil(t, svc)
}

func TestClusterIDFromInfo_Populated(t *testing.T) {
	info := system.Info{
		Swarm: swarm.Info{
			Cluster: &swarm.ClusterInfo{ID: "abc123"},
		},
	}
	require.Equal(t, "abc123", clusterIDFromInfo(info))
}

func TestClusterIDFromInfo_NilCluster(t *testing.T) {
	// Non-swarm daemon: info.Swarm.Cluster is nil.
	info := system.Info{Swarm: swarm.Info{Cluster: nil}}
	require.Equal(t, "", clusterIDFromInfo(info))
}

func TestClusterIDFromInfo_EmptyID(t *testing.T) {
	info := system.Info{
		Swarm: swarm.Info{Cluster: &swarm.ClusterInfo{ID: ""}},
	}
	require.Equal(t, "", clusterIDFromInfo(info))
}
