// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"time"

	"github.com/docker/docker/api/types/swarm"
)

// ServiceConvergence is what a caller needs to decide whether one service has
// finished rolling out. It is deliberately separate from ServiceEntry: that
// struct backs the services view, where ReplicasOnNode mirrors `docker service
// ls` and so keeps counting a superseded task while its container runs. Running
// here drops the outgoing generation, which is the question --wait asks and the
// services view answers with UpToDate instead (see issue #480).
type ServiceConvergence struct {
	Name string
	Mode string
	// Running counts tasks that are actually running, on an active node.
	Running int
	// Desired is the target count over active nodes.
	Desired int
	// Completed counts tasks that ran to completion on an active node. Only
	// meaningful together with Job: for a long-running service a completed task
	// is one swarm is about to replace, not one that finished its work.
	Completed int
	// Job reports a service swarm will not restart after a clean exit — a
	// restart policy of "none" or "on-failure". Such a service is *supposed* to
	// end with no task running, so Running < Desired is its success state, not
	// a failure to converge (issue #443).
	//
	// A task that exits non-zero and exhausts its restart budget ends Failed,
	// never Complete, so counting only completed tasks keeps the distinction
	// that matters: finished versus broken.
	Job bool
	// UpdateState is the raw swarm UpdateStatus.State, empty when the service
	// has never been updated. Note that a nil UpdateStatus means "no rollout has
	// ever run", NOT "the rollout finished".
	UpdateState string
	// Monitor is UpdateConfig.Monitor: the window after a task is created during
	// which its failure still counts against the rollout. Zero when unset.
	Monitor time.Duration
	// NewestTaskAge is how long the newest running task has been alive, measured
	// from task creation — the same instant swarm measures Monitor from.
	//
	// A caller waiting out the monitor window needs it: a task only reports
	// running once its healthcheck passes, so by then start_period and the
	// checks that followed have already consumed part of the window, and
	// sometimes all of it. Zero when nothing is running.
	NewestTaskAge time.Duration
}

// schedulableNodes returns the nodes that can currently run tasks. A task pinned
// to a node that is down never converges, so counting it in the denominator
// would make a release hang until timeout for a reason no redeploy can fix.
//
// The predicate is swarmkit's own (orchestrator/global.updateNode): drained and
// down, nothing else. A stricter one that also demanded Ready and Active split
// the two halves of the ratio apart — a paused node's running task was counted
// in the numerator while the node itself was dropped from the denominator, so a
// healthy global service read 3/2.
func schedulableNodes(snap *SwarmSnapshot) []swarm.Node {
	out := make([]swarm.Node, 0, len(snap.Nodes))
	for _, n := range snap.Nodes {
		if n.Spec.Availability == swarm.NodeAvailabilityDrain || n.Status.State == swarm.NodeStateDown {
			continue
		}
		out = append(out, n)
	}
	return out
}

// nodeIDSet indexes nodes by ID, for callers filtering tasks by node.
func nodeIDSet(nodes []swarm.Node) map[string]struct{} {
	ids := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		ids[n.ID] = struct{}{}
	}
	return ids
}

// LoadStackConvergence returns per-service convergence facts for a stack.
//
// Running counts tasks whose ACTUAL state is running — not their desired state.
// Up-to-dateness comes free: on a rolling update Swarm marks superseded tasks
// DesiredState=shutdown, so requiring both DesiredState and Status.State to be
// running counts exactly the current generation.
func LoadStackConvergence(stackName string) []ServiceConvergence {
	snap, err := GetOrRefreshSnapshot()
	if err != nil {
		l().Warnf("failed to get snapshot: %v", err)
		return nil
	}
	return snap.StackConvergence(stackName)
}

// StackConvergence is LoadStackConvergence against an already-fetched snapshot,
// so a caller polling one specific swarm for convergence does not read another
// swarm's tasks out of the process-wide cache.
func (snap *SwarmSnapshot) StackConvergence(stackName string) []ServiceConvergence {
	schedulable := schedulableNodes(snap)
	active := nodeIDSet(schedulable)

	var out []ServiceConvergence
	for _, svc := range snap.Services {
		if svc.Spec.Labels["com.docker.stack.namespace"] != stackName {
			continue
		}

		job := isJobService(svc)

		running, completed := 0, 0
		var newest time.Time
		for _, t := range snap.Tasks {
			if t.ServiceID != svc.ID {
				continue
			}
			// A task not yet assigned has no NodeID; it is by definition not
			// running yet, but guard anyway rather than counting it as active.
			if t.NodeID == "" {
				continue
			}
			if _, ok := active[t.NodeID]; !ok {
				continue
			}
			switch {
			case t.DesiredState == swarm.TaskStateRunning && t.Status.State == swarm.TaskStateRunning:
				running++
			case job && t.Status.State == swarm.TaskStateComplete:
				// Swarm sets DesiredState=shutdown once a job's task exits, so
				// this is not reachable through the running arm above.
				completed++
			default:
				continue
			}
			// The window is outstanding until the LAST task created has survived
			// it, so the newest task governs. A completed job task counts here
			// too, or --wait would measure the window from zero and sit out a
			// full monitor after the job had already finished.
			if t.CreatedAt.After(newest) {
				newest = t.CreatedAt
			}
		}

		out = append(out, ServiceConvergence{
			Name:          svc.Spec.Name,
			Mode:          getServiceMode(svc),
			Running:       running,
			Completed:     completed,
			Job:           job,
			Desired:       desiredOverNodes(svc, schedulable),
			UpdateState:   updateState(svc),
			Monitor:       monitorWindow(svc),
			NewestTaskAge: ageSince(newest),
		})
	}
	return out
}

// ageSince is how long ago t was, clamped at zero. CreatedAt comes off the
// manager's clock and is compared against this host's, so skew can put it in the
// future; reporting a negative age would credit the caller with time that has
// not passed. A zero timestamp (nothing running) is likewise zero age.
func ageSince(t time.Time) time.Duration {
	if t.IsZero() {
		return 0
	}
	if age := time.Since(t); age > 0 {
		return age
	}
	return 0
}

// isJobService reports a service swarm will not restart after a clean exit.
//
// Swarm's native mode: replicated-job is not the shape to look for — the
// compose v3 schema `docker stack deploy` accepts cannot express it, so the
// only way to run a one-shot task in a stack is a normal replicated service
// with a restart policy that declines to restart it. That is what init and
// migration steps in charts use, there being no depends_on in swarm.
//
// An omitted restart policy means "any", swarm's default, which is not a job.
func isJobService(svc swarm.Service) bool {
	rp := svc.Spec.TaskTemplate.RestartPolicy
	if rp == nil {
		return false
	}
	switch rp.Condition {
	case swarm.RestartPolicyConditionNone, swarm.RestartPolicyConditionOnFailure:
		return true
	default:
		return false
	}
}

// DesiredReplicas is the service's target task count against this snapshot: the
// declared replicas, or for a global service one per node that can currently run
// one. Exported for callers outside this package that would otherwise duplicate
// the mode switch and get the global case wrong (issues #480, #643).
func (snap *SwarmSnapshot) DesiredReplicas(svc swarm.Service) int {
	return desiredOverNodes(svc, schedulableNodes(snap))
}

// desiredOverNodes is the target task count. For a global service that is one
// per schedulable node the placement constraints admit, so draining a node
// lowers the target rather than making the service permanently short, and a
// service pinned to the managers is not measured against the workers too.
//
// A replicated service's declared count is its target wherever the replicas can
// land, which is what swarm reports and what --wait must keep waiting for: a
// constraint no node satisfies leaves it pending, not converged.
func desiredOverNodes(svc swarm.Service, nodes []swarm.Node) int {
	switch {
	case svc.Spec.Mode.Replicated != nil && svc.Spec.Mode.Replicated.Replicas != nil:
		return int(*svc.Spec.Mode.Replicated.Replicas)
	case svc.Spec.Mode.Global != nil:
		return eligibleNodeCount(svc, nodes)
	default:
		return 1
	}
}

func updateState(svc swarm.Service) string {
	if svc.UpdateStatus == nil {
		return ""
	}
	return string(svc.UpdateStatus.State)
}

func monitorWindow(svc swarm.Service) time.Duration {
	if svc.Spec.UpdateConfig == nil {
		return 0
	}
	return svc.Spec.UpdateConfig.Monitor
}
