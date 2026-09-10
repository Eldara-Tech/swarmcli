// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package taskutil

import (
	"fmt"
	"time"

	"github.com/docker/docker/api/types/swarm"
)

// TaskKeyForService returns a unique key for a task's slot within its service.
// For replicated services this is serviceID:slot, for global services serviceID:nodeID.
func TaskKeyForService(t swarm.Task) string {
	if t.Slot > 0 {
		return fmt.Sprintf("%s:%d", t.ServiceID, t.Slot)
	}
	return fmt.Sprintf("%s:%s", t.ServiceID, t.NodeID)
}

// TaskTimestamp returns the effective timestamp for a task, falling back to
// CreatedAt when Status.Timestamp is zero.
func TaskTimestamp(t swarm.Task) time.Time {
	if !t.Status.Timestamp.IsZero() {
		return t.Status.Timestamp
	}
	return t.CreatedAt
}

// LatestTasksByServiceKey returns the most relevant task per slot.
// Tasks that want to be running are preferred over terminal tasks;
// within the same tier the most recent task wins.
func LatestTasksByServiceKey(tasks []swarm.Task) []swarm.Task {
	latest := make(map[string]swarm.Task)
	latestAt := make(map[string]time.Time)
	latestWantsRunning := make(map[string]bool)
	for _, t := range tasks {
		key := TaskKeyForService(t)
		at := TaskTimestamp(t)
		wantsRunning := t.DesiredState == swarm.TaskStateRunning
		if _, seen := latest[key]; !seen {
			latest[key] = t
			latestAt[key] = at
			latestWantsRunning[key] = wantsRunning
		} else if wantsRunning && !latestWantsRunning[key] {
			latest[key] = t
			latestAt[key] = at
			latestWantsRunning[key] = true
		} else if wantsRunning == latestWantsRunning[key] && at.After(latestAt[key]) {
			latest[key] = t
			latestAt[key] = at
		}
	}

	res := make([]swarm.Task, 0, len(latest))
	for _, t := range latest {
		res = append(res, t)
	}
	return res
}

// ActiveDeploymentErrorsByService returns serviceID -> error text for every
// service with a slot whose failure nothing has since undone: an error task
// newer than that slot's last sign of life, or with no sign of life at all.
//
// A slot shows a sign of life two ways. A task that both wants to run and is
// running is the rolling-update case — the outgoing generation failed, the old
// task is still up. A task that ran to completion is the run-to-completion case:
// a `..._init` service exits 0, is never restarted, and never has a running task
// for a later attempt to be compared against, so a failure before that success
// would otherwise be reported forever (PR #633 review).
//
// Only errors within 5 minutes of that service's own newest task are considered,
// so a rollout that eventually settled stops being news. The window follows the
// service rather than the cluster: a service that broke weeks ago and has not
// been rescheduled since is still broken, however busy its neighbours are.
// When multiple slots error for the same service, the most recent error wins.
func ActiveDeploymentErrorsByService(tasks []swarm.Task) map[string]string {
	if len(tasks) == 0 {
		return nil
	}

	newestByService := make(map[string]time.Time)
	for _, t := range tasks {
		at := TaskTimestamp(t)
		if newest, seen := newestByService[t.ServiceID]; !seen || at.After(newest) {
			newestByService[t.ServiceID] = at
		}
	}

	type slotInfo struct {
		serviceID string
		aliveAt   time.Time
		errAt     time.Time
		errMsg    string
	}
	bySlot := make(map[string]*slotInfo)
	for _, t := range tasks {
		key := TaskKeyForService(t)
		if bySlot[key] == nil {
			bySlot[key] = &slotInfo{serviceID: t.ServiceID}
		}
		s := bySlot[key]
		at := TaskTimestamp(t)

		if (t.DesiredState == swarm.TaskStateRunning && t.Status.State == swarm.TaskStateRunning) ||
			t.Status.State == swarm.TaskStateComplete {
			if s.aliveAt.IsZero() || at.After(s.aliveAt) {
				s.aliveAt = at
			}
		}
		if t.Status.Err != "" || t.Status.State == swarm.TaskStateFailed || t.Status.State == swarm.TaskStateRejected {
			if at.Before(newestByService[t.ServiceID].Add(-5 * time.Minute)) {
				continue
			}
			if s.errAt.IsZero() || at.After(s.errAt) {
				s.errAt = at
				s.errMsg = t.Status.Err
				if s.errMsg == "" {
					s.errMsg = fmt.Sprintf("task %s", t.Status.State)
				}
			}
		}
	}

	type svcErr struct {
		errAt  time.Time
		errMsg string
	}
	best := make(map[string]*svcErr)
	for _, s := range bySlot {
		if s.errAt.IsZero() {
			continue
		}
		if !s.aliveAt.IsZero() && !s.errAt.After(s.aliveAt) {
			continue
		}
		prev, seen := best[s.serviceID]
		if !seen || s.errAt.After(prev.errAt) {
			best[s.serviceID] = &svcErr{errAt: s.errAt, errMsg: s.errMsg}
		}
	}

	result := make(map[string]string, len(best))
	for svcID, e := range best {
		result[svcID] = e.errMsg
	}
	return result
}
