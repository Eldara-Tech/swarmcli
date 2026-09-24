// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
)

type NodeEntry struct {
	ID            string
	Version       string
	Hostname      string
	Role          string
	State         string
	Availability  string
	Manager       bool
	Addr          string
	Labels        map[string]string
	ManagerStatus string // Leader, Reachable, Unreachable, or ""
}

// UnhealthyReason says why a node needs the operator's attention, or "" when it
// does not: swarm reports it down, disconnected or unknown (the reason is that
// state), or it is a manager its peers cannot reach ("unreachable").
// Availability is deliberately not consulted — drain and pause are set by the
// operator, not something that went wrong. An empty state is not flagged, so a
// node the snapshot knows nothing about stays uncoloured.
func (n NodeEntry) UnhealthyReason() string {
	switch swarm.NodeState(n.State) {
	case swarm.NodeStateDown, swarm.NodeStateUnknown, swarm.NodeStateDisconnected:
		return n.State
	}
	if swarm.Reachability(n.ManagerStatus) == swarm.ReachabilityUnreachable {
		return string(swarm.ReachabilityUnreachable)
	}
	return ""
}

// StackEntry is a lightweight representation of a Docker stack,
// used for display and cached in SwarmSnapshot.
type StackEntry struct {
	Name         string
	ServiceCount int
	NodeCount    int
}

// SwarmSnapshot contains the in-memory swarm state.
type SwarmSnapshot struct {
	Nodes     []swarm.Node
	Services  []swarm.Service
	Tasks     []swarm.Task
	Fetched   time.Time
	ClusterID string
	// Locked is true when the swarm is reachable but encrypted/locked. In that
	// case the entity lists are empty until the swarm is unlocked.
	Locked bool
}

var (
	snapshotMu sync.RWMutex
	snapshot   *SwarmSnapshot
	// refreshInProgress indicates whether a background refresh goroutine is running.
	refreshInProgress int32
)

// cacheTTL controls how long we reuse the snapshot before refreshing.
const cacheTTL = 3 * time.Second

// GetSnapshot returns the cached snapshot if it's still valid.
func GetSnapshot() *SwarmSnapshot {
	snapshotMu.RLock()
	defer snapshotMu.RUnlock()
	return snapshot
}

// SetSnapshot replaces the cached snapshot (useful for manual refresh).
func SetSnapshot(s *SwarmSnapshot) {
	snapshotMu.Lock()
	defer snapshotMu.Unlock()
	snapshot = s
}

// InvalidateSnapshot clears the cached snapshot, forcing a fresh fetch on next access.
// This should be called after a Docker context switch.
func InvalidateSnapshot() {
	snapshotMu.Lock()
	defer snapshotMu.Unlock()
	snapshot = nil
}

// RefreshSnapshot fetches all swarm data (nodes, services, tasks) at once
// and updates the global cache.
func RefreshSnapshot() (*SwarmSnapshot, error) {
	return RefreshSnapshotCtx(context.Background())
}

// RefreshSnapshotCtx is RefreshSnapshot under a context the caller can cancel.
//
// It exists beside RefreshSnapshot rather than replacing it because the TUI is
// the overwhelming majority of the callers and has no context to give: a Bubble
// Tea command refreshing the cache after a scale or a restart would have to
// invent one. The release engine does have one — a reconcile it must be able to
// abort — and this is how it reaches the four API calls the refresh makes.
func RefreshSnapshotCtx(ctx context.Context) (*SwarmSnapshot, error) {
	c, err := GetClient()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	snap, err := SnapshotWith(ctx, c)
	if err != nil {
		return nil, err
	}
	SetSnapshot(snap)
	return snap, nil
}

// SnapshotWith fetches a snapshot from an explicit client and returns it
// WITHOUT touching the process-wide cache.
//
// That omission is the point. The cache is a single global with a 3s TTL and a
// background refresh goroutine, sized for a TUI redrawing one swarm; two
// reconciles against different swarms sharing it would not merely read each
// other's data, they would evict each other's. A caller holding an explicit
// client owns its own freshness.
//
// It takes the interface rather than *client.Client because it only ever calls
// NodeList, ServiceList, TaskList and Info, and a consumer that holds an
// APIClient — normally for testability — should not have to write a second
// snapshot builder that misses the locked-swarm handling below.
func SnapshotWith(parent context.Context, c client.APIClient) (*SwarmSnapshot, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	nodes, err := c.NodeList(ctx, swarm.NodeListOptions{})
	if err != nil {
		// A locked swarm rejects every store-backed call. Surface it as a flagged
		// empty snapshot instead of a fatal error so the context switch is not
		// reverted; the app prompts the user to unlock.
		if IsSwarmLockedErr(err) {
			return &SwarmSnapshot{Locked: true, Fetched: time.Now()}, nil
		}
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	services, err := c.ServiceList(ctx, swarm.ServiceListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	tasks, err := c.TaskList(ctx, swarm.TaskListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}

	clusterID := ""
	if info, err := c.Info(ctx); err != nil {
		l().Warnf("snapshot: cli.Info failed (cluster id unavailable): %v", err)
	} else {
		clusterID = clusterIDFromInfo(info)
	}

	return &SwarmSnapshot{
		Nodes:     nodes,
		Services:  services,
		Tasks:     tasks,
		Fetched:   time.Now(),
		ClusterID: clusterID,
	}, nil
}

// clusterIDFromInfo extracts the swarm cluster ID from a Docker Info result.
// Returns empty string on non-swarm daemons (where info.Swarm.Cluster is nil).
func clusterIDFromInfo(info system.Info) string {
	if info.Swarm.Cluster == nil {
		return ""
	}
	return info.Swarm.Cluster.ID
}

// RefreshSnapshotAsync triggers a background refresh if one is not already running.
// It returns immediately.
func RefreshSnapshotAsync() {
	if !atomic.CompareAndSwapInt32(&refreshInProgress, 0, 1) {
		// already refreshing
		return
	}

	go func() {
		defer atomic.StoreInt32(&refreshInProgress, 0)
		if _, err := RefreshSnapshot(); err != nil {
			l().Warnf("background snapshot refresh failed: %v", err)
		}
	}()
}

// TriggerRefreshIfNeeded will check the cache TTL and start a background refresh
// if the snapshot is empty or stale.
// Note: there is a benign TOCTOU race between the staleness check and the async
// refresh start — the worst case is a redundant refresh, which is harmless.
func TriggerRefreshIfNeeded() {
	snapshotMu.RLock()
	s := snapshot
	snapshotMu.RUnlock()

	if s == nil || time.Since(s.Fetched) > cacheTTL {
		RefreshSnapshotAsync()
	}
}

// GetOrRefreshSnapshot returns the current snapshot, refreshing it
// if the cache is empty or too old.
func GetOrRefreshSnapshot() (*SwarmSnapshot, error) {
	if s := freshSnapshot(); s != nil {
		return s, nil
	}
	return RefreshSnapshot()
}

// GetOrRefreshSnapshotCtx is GetOrRefreshSnapshot under a context the caller can
// cancel; see RefreshSnapshotCtx for why both spellings exist. A cache hit never
// consults the context, because it never talks to the daemon.
func GetOrRefreshSnapshotCtx(ctx context.Context) (*SwarmSnapshot, error) {
	if s := freshSnapshot(); s != nil {
		return s, nil
	}
	return RefreshSnapshotCtx(ctx)
}

// freshSnapshot returns the cached snapshot while it is still inside the TTL,
// and nil when it is absent or stale — i.e. when a caller has to go and fetch.
func freshSnapshot() *SwarmSnapshot {
	snapshotMu.RLock()
	defer snapshotMu.RUnlock()

	if snapshot == nil || time.Since(snapshot.Fetched) > cacheTTL {
		return nil
	}
	return snapshot
}

// ToNodeEntries converts the full nodes into display-friendly entries.
func (s SwarmSnapshot) ToNodeEntries() []NodeEntry {
	nodes := make([]NodeEntry, len(s.Nodes))
	for i, n := range s.Nodes {
		ver := "-"
		if n.Description.Engine.EngineVersion != "" {
			ver = n.Description.Engine.EngineVersion
		}
		avail := string(n.Spec.Availability)
		if avail == "" {
			avail = "active"
		}
		// A node is a manager if either ManagerStatus is populated OR the role is explicitly set to manager
		isManager := n.ManagerStatus != nil || n.Spec.Role == swarm.NodeRoleManager
		managerStatus := ""
		if n.ManagerStatus != nil {
			if n.ManagerStatus.Leader {
				managerStatus = "Leader"
			} else {
				// Reachable/Unreachable
				managerStatus = string(n.ManagerStatus.Reachability)
			}
		}
		nodes[i] = NodeEntry{
			ID:            n.ID,
			Version:       ver,
			Hostname:      n.Description.Hostname,
			Role:          string(n.Spec.Role),
			State:         string(n.Status.State),
			Availability:  avail,
			Manager:       isManager,
			Addr:          n.Status.Addr,
			Labels:        n.Spec.Labels,
			ManagerStatus: managerStatus,
		}
	}

	// 🔠 Sort alphabetically by hostname
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Hostname < nodes[j].Hostname
	})

	return nodes
}

// ToStackEntries aggregates services by stack name and produces StackEntry slices.
func (s SwarmSnapshot) ToStackEntries() []StackEntry {
	stackMap := make(map[string]*StackEntry)

	for _, svc := range s.Services {
		// Docker stacks mark services with com.docker.stack.namespace
		if stackName, ok := svc.Spec.Labels["com.docker.stack.namespace"]; ok {
			entry, exists := stackMap[stackName]
			if !exists {
				stackMap[stackName] = &StackEntry{Name: stackName, ServiceCount: 1}
			} else {
				entry.ServiceCount++
			}
		}
	}

	// Optionally count how many nodes run tasks of that stack
	for _, task := range s.Tasks {
		if task.ServiceID == "" {
			continue
		}
		// Find stack name from the service
		var stackName string
		for _, svc := range s.Services {
			if svc.ID == task.ServiceID {
				stackName = svc.Spec.Labels["com.docker.stack.namespace"]
				break
			}
		}
		if stackName == "" {
			continue
		}
		entry := stackMap[stackName]
		if entry != nil {
			entry.NodeCount++ // Each task counts as one node slot, could refine if needed
		}
	}

	stacks := make([]StackEntry, 0, len(stackMap))
	for _, e := range stackMap {
		stacks = append(stacks, *e)
	}

	// ---- 🔠 Sort alphabetically by stack name ----
	sort.Slice(stacks, func(i, j int) bool {
		return stacks[i].Name < stacks[j].Name
	})

	return stacks
}

// FindService looks up a service by ID in the snapshot.
func (s *SwarmSnapshot) FindService(serviceID string) *swarm.Service {
	for i := range s.Services {
		if s.Services[i].ID == serviceID {
			return &s.Services[i]
		}
	}
	return nil
}

// FindServiceByName looks up a service by its name in the snapshot.
func (s *SwarmSnapshot) FindServiceByName(name string) *swarm.Service {
	for i := range s.Services {
		if s.Services[i].Spec.Name == name {
			return &s.Services[i]
		}
	}
	return nil
}
