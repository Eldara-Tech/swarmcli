// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package taskutil

import (
	"testing"
	"time"

	"github.com/docker/docker/api/types/swarm"
)

func makeTask(serviceID string, slot int, desired, actual swarm.TaskState, ts time.Time, err string) swarm.Task {
	return swarm.Task{
		ServiceID:    serviceID,
		Slot:         slot,
		DesiredState: desired,
		Meta:         swarm.Meta{CreatedAt: ts},
		Status: swarm.TaskStatus{
			State:     actual,
			Timestamp: ts,
			Err:       err,
		},
	}
}

func TestTaskKeyForService_Replicated(t *testing.T) {
	task := swarm.Task{ServiceID: "svc1", Slot: 3}
	if got := TaskKeyForService(task); got != "svc1:3" {
		t.Errorf("got %q, want %q", got, "svc1:3")
	}
}

func TestTaskKeyForService_Global(t *testing.T) {
	task := swarm.Task{ServiceID: "svc1", Slot: 0, NodeID: "node1"}
	if got := TaskKeyForService(task); got != "svc1:node1" {
		t.Errorf("got %q, want %q", got, "svc1:node1")
	}
}

func TestTaskTimestamp_UsesStatusTimestamp(t *testing.T) {
	ts := time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)
	task := swarm.Task{
		Meta:   swarm.Meta{CreatedAt: ts.Add(-time.Hour)},
		Status: swarm.TaskStatus{Timestamp: ts},
	}
	if got := TaskTimestamp(task); !got.Equal(ts) {
		t.Errorf("got %v, want %v", got, ts)
	}
}

func TestTaskTimestamp_FallsBackToCreatedAt(t *testing.T) {
	ts := time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)
	task := swarm.Task{
		Meta: swarm.Meta{CreatedAt: ts},
	}
	if got := TaskTimestamp(task); !got.Equal(ts) {
		t.Errorf("got %v, want %v", got, ts)
	}
}

func TestLatestTasksByServiceKey_PrefersRunning(t *testing.T) {
	now := time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)
	tasks := []swarm.Task{
		makeTask("svc1", 1, swarm.TaskStateShutdown, swarm.TaskStateFailed, now.Add(time.Second), "boom"),
		makeTask("svc1", 1, swarm.TaskStateRunning, swarm.TaskStateRunning, now, ""),
	}
	result := LatestTasksByServiceKey(tasks)
	if len(result) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result))
	}
	if result[0].Status.State != swarm.TaskStateRunning {
		t.Errorf("expected running task to win, got %s", result[0].Status.State)
	}
}

func TestLatestTasksByServiceKey_SameTierNewerWins(t *testing.T) {
	now := time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)
	tasks := []swarm.Task{
		makeTask("svc1", 1, swarm.TaskStateRunning, swarm.TaskStateRunning, now, ""),
		makeTask("svc1", 1, swarm.TaskStateRunning, swarm.TaskStateRunning, now.Add(time.Minute), ""),
	}
	result := LatestTasksByServiceKey(tasks)
	if len(result) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result))
	}
	if !result[0].Status.Timestamp.Equal(now.Add(time.Minute)) {
		t.Error("expected newer task to win")
	}
}

func TestActiveDeploymentErrors_EmptyInput(t *testing.T) {
	result := ActiveDeploymentErrorsByService(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestActiveDeploymentErrors_FailedRollingUpdate(t *testing.T) {
	now := time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)
	tasks := []swarm.Task{
		// Old task still running
		makeTask("svc1", 1, swarm.TaskStateRunning, swarm.TaskStateRunning, now.Add(-time.Minute), ""),
		// New task failed (newer than running)
		makeTask("svc1", 1, swarm.TaskStateRunning, swarm.TaskStateFailed, now, "image not found"),
	}
	result := ActiveDeploymentErrorsByService(tasks)
	if len(result) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result))
	}
	if result["svc1"] != "image not found" {
		t.Errorf("got %q, want %q", result["svc1"], "image not found")
	}
}

func TestActiveDeploymentErrors_RecoveredService(t *testing.T) {
	now := time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)
	tasks := []swarm.Task{
		// Old failed task
		makeTask("svc1", 1, swarm.TaskStateRunning, swarm.TaskStateFailed, now.Add(-time.Minute), "boom"),
		// New running task (newer than error)
		makeTask("svc1", 1, swarm.TaskStateRunning, swarm.TaskStateRunning, now, ""),
	}
	result := ActiveDeploymentErrorsByService(tasks)
	if len(result) != 0 {
		t.Errorf("expected no errors for recovered service, got %v", result)
	}
}

func TestActiveDeploymentErrors_StaleErrorIgnored(t *testing.T) {
	now := time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)
	tasks := []swarm.Task{
		// Running task is newest
		makeTask("svc1", 1, swarm.TaskStateRunning, swarm.TaskStateRunning, now, ""),
		// Old error from > 5 minutes ago
		makeTask("svc1", 2, swarm.TaskStateRunning, swarm.TaskStateFailed, now.Add(-10*time.Minute), "old error"),
	}
	result := ActiveDeploymentErrorsByService(tasks)
	if len(result) != 0 {
		t.Errorf("expected stale error to be ignored, got %v", result)
	}
}

func TestActiveDeploymentErrors_EmptyErrMsg(t *testing.T) {
	now := time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)
	tasks := []swarm.Task{
		makeTask("svc1", 1, swarm.TaskStateRunning, swarm.TaskStateFailed, now, ""),
	}
	result := ActiveDeploymentErrorsByService(tasks)
	if len(result) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result))
	}
	if result["svc1"] == "" {
		t.Error("expected non-empty fallback error message for failed task with no Err text")
	}
}

func TestActiveDeploymentErrors_NoRunningTask(t *testing.T) {
	now := time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)
	tasks := []swarm.Task{
		makeTask("svc1", 1, swarm.TaskStateRunning, swarm.TaskStateRejected, now, "no suitable node"),
	}
	result := ActiveDeploymentErrorsByService(tasks)
	if len(result) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result))
	}
	if result["svc1"] != "no suitable node" {
		t.Errorf("got %q, want %q", result["svc1"], "no suitable node")
	}
}

// A run-to-completion service (`..._init`) exits 0 and is never restarted, so it
// never has a running task for a later attempt to be measured against. Its
// successful run still has to clear the failure before it, or the service row
// carries that error until the next time the service runs at all — which for a
// stack deployed once a month is forever (PR #633 review).
func TestActiveDeploymentErrors_CompletedRunSupersedesFailure(t *testing.T) {
	now := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	tasks := []swarm.Task{
		makeTask("svc1", 1, swarm.TaskStateShutdown, swarm.TaskStateComplete, now, ""),
		makeTask("svc1", 1, swarm.TaskStateShutdown, swarm.TaskStateFailed, now.Add(-time.Minute), "task: non-zero exit (1)"),
		makeTask("svc1", 1, swarm.TaskStateShutdown, swarm.TaskStateComplete, now.Add(-7*24*time.Hour), ""),
	}
	if result := ActiveDeploymentErrorsByService(tasks); len(result) != 0 {
		t.Errorf("expected the completed run to clear the earlier failure, got %v", result)
	}
}

// The other direction: a completed run does not immunize the slot, it only
// speaks for the moment it finished.
func TestActiveDeploymentErrors_FailureAfterCompletedRun(t *testing.T) {
	now := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	tasks := []swarm.Task{
		makeTask("svc1", 1, swarm.TaskStateShutdown, swarm.TaskStateComplete, now.Add(-time.Minute), ""),
		makeTask("svc1", 1, swarm.TaskStateShutdown, swarm.TaskStateFailed, now, "task: non-zero exit (1)"),
	}
	result := ActiveDeploymentErrorsByService(tasks)
	if result["svc1"] != "task: non-zero exit (1)" {
		t.Errorf("got %q, want %q", result["svc1"], "task: non-zero exit (1)")
	}
}

// The window follows the service rather than the cluster. A service that broke a
// week ago and has not been rescheduled since is still broken, and a neighbour
// that deployed a minute ago must not push it out of its own window.
func TestActiveDeploymentErrors_WindowFollowsTheService(t *testing.T) {
	now := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	tasks := []swarm.Task{
		makeTask("stale", 1, swarm.TaskStateRunning, swarm.TaskStateRejected, now.Add(-7*24*time.Hour), "no suitable node"),
		makeTask("busy", 1, swarm.TaskStateRunning, swarm.TaskStateRunning, now, ""),
	}
	result := ActiveDeploymentErrorsByService(tasks)
	if result["stale"] != "no suitable node" {
		t.Errorf("got %q, want %q", result["stale"], "no suitable node")
	}
	if _, ok := result["busy"]; ok {
		t.Errorf("the healthy neighbour reported an error: %v", result)
	}
}

func TestAttemptOrder_PrefersCreatedAt(t *testing.T) {
	created := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	task := swarm.Task{
		Meta:   swarm.Meta{CreatedAt: created},
		Status: swarm.TaskStatus{Timestamp: created.Add(time.Hour)},
	}
	if got := AttemptOrder(task); !got.Equal(created) {
		t.Errorf("got %v, want %v", got, created)
	}
}

func TestAttemptOrder_FallsBackToStatusTimestamp(t *testing.T) {
	ts := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	task := swarm.Task{Status: swarm.TaskStatus{Timestamp: ts}}
	if got := AttemptOrder(task); !got.Equal(ts) {
		t.Errorf("got %v, want %v", got, ts)
	}
}

// The second shape from PR #633's review: eldara-swarmcli_migrate, three failed
// attempts, a run that completed, then a completed run of the next image. Every
// task carries DesiredState=shutdown, which swarm stamps onto the leftovers when
// the service is updated — so the last failure's Status.Timestamp lands after
// both completed runs even though it was created before them. The task list
// orders attempts by CreatedAt, so the row above it has to as well, or the row
// contradicts the rows beneath it.
func TestActiveDeploymentErrors_OrdersAttemptsByCreation(t *testing.T) {
	base := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	mk := func(created, stamp time.Time, state swarm.TaskState, msg string) swarm.Task {
		return swarm.Task{
			ServiceID: "migrate", Slot: 1, DesiredState: swarm.TaskStateShutdown,
			Meta:   swarm.Meta{CreatedAt: created},
			Status: swarm.TaskStatus{State: state, Timestamp: stamp, Err: msg},
		}
	}
	tasks := []swarm.Task{
		mk(base, base.Add(20*time.Second), swarm.TaskStateFailed, "task: non-zero exit (3)"),
		mk(base.Add(time.Minute), base.Add(80*time.Second), swarm.TaskStateFailed, "task: non-zero exit (3)"),
		// Created third, restamped last.
		mk(base.Add(2*time.Minute), base.Add(7*time.Minute), swarm.TaskStateFailed, "task: non-zero exit (1)"),
		mk(base.Add(3*time.Minute), base.Add(4*time.Minute), swarm.TaskStateComplete, ""),
		mk(base.Add(5*time.Minute), base.Add(6*time.Minute), swarm.TaskStateComplete, ""),
	}
	if result := ActiveDeploymentErrorsByService(tasks); len(result) != 0 {
		t.Errorf("the newest attempt completed, so the service is not failing; got %v", result)
	}
}
