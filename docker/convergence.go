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
	// NativeJob reports swarm's own job modes, replicated-job and global-job.
	// Their target is a number of completions, so a running task is progress
	// towards it rather than arrival, and Completed counts only the current
	// run (JobIteration) of the job (issue #666).
	NativeJob bool
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
	// DeadTask reports a one-shot whose current task ended without completing
	// and that swarm will not try again: a restart condition of "none" leaves
	// the slot exactly as it is. Nothing about such a release will ever change,
	// so waiting on it only delays the report.
	//
	// Restricted to condition "none" on purpose. Under "on-failure" swarm
	// replaces the task — which is what makes a rejected task survivable there —
	// so a failure is not yet terminal and this stays false.
	DeadTask bool
	// DeadTaskReason is what swarm said about that task: the container's exit
	// status, or why a node would not run it at all ("invalid pool request: Pool
	// overlaps with other one on this address space"). Empty when swarm recorded
	// no message.
	DeadTaskReason string
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
		native := isNativeJob(svc)

		running, completed, dead := 0, 0, 0
		deadReason := ""
		// Only a restart condition of "none" makes a failed task terminal;
		// "on-failure" is a job too, but swarm replaces the task.
		terminal := job && neverRestarts(svc)
		var newest time.Time
		// Only the newest task in each slot is judged. Swarm keeps terminal
		// tasks in the list up to --task-history-limit, so a one-shot that has
		// run before still lists every earlier Complete task, and counting those
		// let three old successes meet a target of one: a migration whose latest
		// run exited non-zero read converged, and the release detail said "5/1
		// tasks running". An unassigned task has no slot to be the newest of and
		// is dropped here rather than by the NodeID guard this replaces.
		for _, t := range newestTaskPerSlot(svc.ID, snap.Tasks) {
			if _, ok := active[t.NodeID]; !ok {
				continue
			}
			// A job's earlier runs stay in the task list, and a rerun reuses
			// their slots, so a slot not yet reached this run would otherwise
			// still offer last run's Complete task. swarmkit's own count
			// (ListServiceStatuses) filters the same way.
			if native && !inCurrentJobIteration(svc, t) {
				continue
			}
			switch {
			// A native job's tasks carry DesiredState=complete from creation,
			// including while they run.
			case (t.DesiredState == swarm.TaskStateRunning || (native && t.DesiredState == swarm.TaskStateComplete)) &&
				t.Status.State == swarm.TaskStateRunning:
				running++
			case job && t.Status.State == swarm.TaskStateComplete:
				// Swarm sets DesiredState=shutdown once a job's task exits, so
				// this is not reachable through the running arm above.
				completed++
			case terminal && isDeadTaskState(t.Status.State):
				// A one-shot swarm will not retry, whose task did not complete:
				// it exited non-zero, or a node refused it outright. The slot
				// keeps this task forever, so the release is as finished as it
				// is ever going to get (issue #651).
				dead++
				if deadReason == "" {
					deadReason = t.Status.Err
				}
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
			Name:           svc.Spec.Name,
			Mode:           getServiceMode(svc),
			Running:        running,
			Completed:      completed,
			Job:            job,
			NativeJob:      native,
			Desired:        desiredOverNodes(svc, schedulable),
			UpdateState:    updateState(svc),
			Monitor:        monitorWindow(svc),
			NewestTaskAge:  ageSince(newest),
			DeadTask:       dead > 0,
			DeadTaskReason: deadReason,
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

// isJobService reports a service swarm will not restart after a clean exit:
// one of swarm's native job modes, or a normal service with a restart policy
// that declines to restart it — the shape init and migration steps in charts
// have long used, there being no depends_on in swarm.
//
// An omitted restart policy means "any", swarm's default, which is not a job.
func isJobService(svc swarm.Service) bool {
	if isNativeJob(svc) {
		return true
	}
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

// isNativeJob reports swarm's own job modes, which `docker stack deploy`
// renders from `deploy.mode: replicated-job` and `global-job`.
func isNativeJob(svc swarm.Service) bool {
	return svc.Spec.Mode.ReplicatedJob != nil || svc.Spec.Mode.GlobalJob != nil
}

// inCurrentJobIteration reports a task belonging to the job's current run.
func inCurrentJobIteration(svc swarm.Service, t swarm.Task) bool {
	return svc.JobStatus != nil && t.JobIteration != nil && t.JobIteration.Index == svc.JobStatus.JobIteration.Index
}

// neverRestarts reports a restart policy of "none": swarm leaves a task that
// ended exactly where it is, whether it exited 0 or was rejected by every node.
// "on-failure" is a job by isJobService's reckoning but not this one, because
// swarm does replace its failed tasks.
func neverRestarts(svc swarm.Service) bool {
	rp := svc.Spec.TaskTemplate.RestartPolicy
	return rp != nil && rp.Condition == swarm.RestartPolicyConditionNone
}

// isDeadTaskState reports the terminal states a task reaches without having done
// its work. Complete is deliberately absent — that is success for a one-shot.
// Shutdown is absent too: swarm stops a task deliberately, which is not a
// failure of the task.
func isDeadTaskState(state swarm.TaskState) bool {
	switch state {
	case swarm.TaskStateFailed, swarm.TaskStateRejected, swarm.TaskStateOrphaned:
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
//
// A job's target is completions: TotalCompletions for a replicated job, and for
// a global job one per eligible node, which is where swarmkit's global job
// reconciler creates a task each run.
func desiredOverNodes(svc swarm.Service, nodes []swarm.Node) int {
	switch {
	case svc.Spec.Mode.Replicated != nil && svc.Spec.Mode.Replicated.Replicas != nil:
		return int(*svc.Spec.Mode.Replicated.Replicas)
	case svc.Spec.Mode.ReplicatedJob != nil && svc.Spec.Mode.ReplicatedJob.TotalCompletions != nil:
		return int(*svc.Spec.Mode.ReplicatedJob.TotalCompletions)
	case svc.Spec.Mode.Global != nil, svc.Spec.Mode.GlobalJob != nil:
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
