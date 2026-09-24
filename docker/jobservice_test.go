// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"testing"
	"time"

	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// jobSvc is svcInStack with a restart policy that declines to restart a task
// after a clean exit — the only way a compose v3 stack can express a one-shot
// step, since `docker stack deploy` cannot render mode: replicated-job.
func jobSvc(id, stack string, cond swarm.RestartPolicyCondition) swarm.Service {
	svc := svcInStack(id, stack)
	svc.Spec.TaskTemplate.RestartPolicy = &swarm.RestartPolicy{Condition: cond}
	return svc
}

func taskInState(svcID, nodeID string, desired, actual swarm.TaskState) swarm.Task {
	return swarm.Task{
		ServiceID:    svcID,
		NodeID:       nodeID,
		DesiredState: desired,
		Status:       swarm.TaskStatus{State: actual},
	}
}

// An omitted restart policy is swarm's "any" default, which restarts a task
// after a clean exit — so it is a long-running service, not a job.
func TestIsJobServiceRecognisesOnlyNonRestartingPolicies(t *testing.T) {
	require.False(t, isJobService(svcInStack("api", "s")), "no restart policy means the 'any' default")
	require.False(t, isJobService(jobSvc("api", "s", swarm.RestartPolicyConditionAny)))
	require.True(t, isJobService(jobSvc("init", "s", swarm.RestartPolicyConditionNone)))
	require.True(t, isJobService(jobSvc("init", "s", swarm.RestartPolicyConditionOnFailure)))
	job := svcInStack("batch", "s")
	job.Spec.Mode = swarm.ServiceMode{ReplicatedJob: &swarm.ReplicatedJob{}}
	require.True(t, isJobService(job), "swarm's native job modes need no restart policy")
	job.Spec.Mode = swarm.ServiceMode{GlobalJob: &swarm.GlobalJob{}}
	require.True(t, isJobService(job))
}

// The bug in #443: swarm sets DesiredState=shutdown once a job's task exits, so
// the running count drops to zero and never comes back, while Desired stays at
// the spec's replica count. The release then reads 0/1 forever and --wait sits
// until it times out.
func TestStackConvergenceCountsACompletedJobTask(t *testing.T) {
	snap := &SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{jobSvc("init", "mystack", swarm.RestartPolicyConditionNone)},
		Tasks: []swarm.Task{
			taskInState("init", "n1", swarm.TaskStateShutdown, swarm.TaskStateComplete),
		},
	}

	conv := snap.StackConvergence("mystack")
	require.Len(t, conv, 1)
	require.True(t, conv[0].Job)
	require.Equal(t, 0, conv[0].Running, "a finished job has nothing running")
	require.Equal(t, 1, conv[0].Completed)
	require.Equal(t, 1, conv[0].Desired)
}

// A job that exited non-zero and exhausted its restart budget ends in state
// Failed, never Complete. Counting it would report a broken migration as a
// finished one, which is the failure this whole change must not introduce.
func TestStackConvergenceDoesNotCountAFailedJobTask(t *testing.T) {
	snap := &SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{jobSvc("init", "mystack", swarm.RestartPolicyConditionOnFailure)},
		Tasks: []swarm.Task{
			taskInState("init", "n1", swarm.TaskStateShutdown, swarm.TaskStateFailed),
		},
	}

	conv := snap.StackConvergence("mystack")
	require.Len(t, conv, 1)
	require.Equal(t, 0, conv[0].Completed, "a failed task is not a completed one")
	require.Equal(t, 0, conv[0].Running)
}

// A completed task belonging to a normal service is one swarm is about to
// replace, not a finished job. Counting it would let --wait return while a
// restart-looping service was between containers.
func TestStackConvergenceIgnoresCompletedTasksOnNonJobServices(t *testing.T) {
	snap := &SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{svcInStack("api", "mystack")},
		Tasks: []swarm.Task{
			taskInState("api", "n1", swarm.TaskStateShutdown, swarm.TaskStateComplete),
		},
	}

	conv := snap.StackConvergence("mystack")
	require.Len(t, conv, 1)
	require.False(t, conv[0].Job)
	require.Equal(t, 0, conv[0].Completed)
}

// The monitor window is measured from task creation. A finished job has no
// running task, so without counting the completed one the newest timestamp
// stays zero, NewestTaskAge reads zero, and --wait sits out a full monitor
// after the job has already done its work.
func TestStackConvergenceAgesACompletedJobTask(t *testing.T) {
	task := taskInState("init", "n1", swarm.TaskStateShutdown, swarm.TaskStateComplete)
	task.CreatedAt = time.Now().Add(-time.Hour)

	snap := &SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{jobSvc("init", "mystack", swarm.RestartPolicyConditionNone)},
		Tasks:    []swarm.Task{task},
	}

	conv := snap.StackConvergence("mystack")
	require.Len(t, conv, 1)
	require.Greater(t, conv[0].NewestTaskAge, 30*time.Minute)
}

// jobTaskAt is one run of a one-shot: a task in slot 1, created at a known time
// so the generation rule has something to sort on.
func jobTaskAt(svcID string, created time.Time, actual swarm.TaskState) swarm.Task {
	t := taskInState(svcID, "n1", swarm.TaskStateShutdown, actual)
	t.Slot = 1
	t.Meta = swarm.Meta{CreatedAt: created}
	return t
}

// Swarm keeps terminal tasks up to --task-history-limit, so a migration that
// has run four times lists four tasks in the same slot. Counting all of them
// meant a job that had ever succeeded could not report a later failure: three
// old Completes met a target of one, so a run that exited non-zero read as
// converged and --wait went green on it. Only the newest run is the answer.
func TestStackConvergenceJudgesOnlyTheLatestJobRun(t *testing.T) {
	now := time.Now()
	snap := &SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{jobSvc("migrate", "mystack", swarm.RestartPolicyConditionNone)},
		Tasks: []swarm.Task{
			jobTaskAt("migrate", now.Add(-3*time.Hour), swarm.TaskStateComplete),
			jobTaskAt("migrate", now.Add(-2*time.Hour), swarm.TaskStateComplete),
			jobTaskAt("migrate", now.Add(-time.Hour), swarm.TaskStateComplete),
			jobTaskAt("migrate", now, swarm.TaskStateFailed),
		},
	}

	conv := snap.StackConvergence("mystack")
	require.Len(t, conv, 1)
	require.Equal(t, 0, conv[0].Completed, "the latest run failed; its predecessors are history")
	require.Equal(t, 1, conv[0].Desired)

	// The same four tasks with the newest one Complete: one run, not four.
	snap.Tasks[3] = jobTaskAt("migrate", now, swarm.TaskStateComplete)
	conv = snap.StackConvergence("mystack")
	require.Equal(t, 1, conv[0].Completed, "a job that ran four times has still done its work once")
}

// A one-shot whose task the swarm refused is finished, not slow: a restart
// condition of "none" means the slot keeps that task and no later poll will read
// differently. Reporting it as merely short of its target spent a caller's whole
// timeout on a release that had already failed (issue #651).
func TestStackConvergenceReportsATaskSwarmWillNotRetry(t *testing.T) {
	const refusal = "invalid pool request: Pool overlaps with other one on this address space"

	rejected := jobTaskAt("migrate", time.Now(), swarm.TaskStateRejected)
	rejected.Status.Err = refusal

	snap := &SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{jobSvc("migrate", "mystack", swarm.RestartPolicyConditionNone)},
		Tasks:    []swarm.Task{rejected},
	}

	conv := snap.StackConvergence("mystack")
	require.Len(t, conv, 1)
	require.True(t, conv[0].DeadTask)
	require.Equal(t, refusal, conv[0].DeadTaskReason, "swarm's own words, so the reader is not left guessing")
	require.Equal(t, 0, conv[0].Completed)
}

// "on-failure" is a job too, but swarm replaces its failed tasks — which is what
// makes a rejected task survivable there. Calling that terminal would report a
// release as dead while swarm was still working on it.
func TestStackConvergenceDoesNotBuryARetryableFailure(t *testing.T) {
	failed := jobTaskAt("migrate", time.Now(), swarm.TaskStateFailed)
	failed.Status.Err = "task: non-zero exit (3)"

	snap := &SwarmSnapshot{
		Nodes:    []swarm.Node{readyNode("n1")},
		Services: []swarm.Service{jobSvc("migrate", "mystack", swarm.RestartPolicyConditionOnFailure)},
		Tasks:    []swarm.Task{failed},
	}

	conv := snap.StackConvergence("mystack")
	require.Len(t, conv, 1)
	require.True(t, conv[0].Job, "on-failure is still a job")
	require.False(t, conv[0].DeadTask, "swarm will create a replacement, so nothing is settled yet")
}
