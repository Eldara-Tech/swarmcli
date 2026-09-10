// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package servicesview

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/swarm"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/scaledialog"

	"github.com/stretchr/testify/require"
)

func TestScaleService_Timeout_ReturnsScaleError(t *testing.T) {
	m := testModel(func(m *Model) {
		m.deps.Services = &mockServiceOps{
			scaleServiceFn: func(_ context.Context, _ string, _ uint64) error {
				return context.DeadlineExceeded
			},
			restartServiceFn:    func(_ context.Context, _ string) error { return nil },
			removeServiceFn:     func(_ context.Context, _ string) error { return nil },
			rollbackServiceFn:   func(_ context.Context, _ string) error { return nil },
			loadNodeServicesFn:  func(_ string) []docker.ServiceEntry { return nil },
			loadStackServicesFn: func(_ string) []docker.ServiceEntry { return nil },
		}
	})
	loadServices(m, fakeEntries("web"))
	m.scaleDialog.Visible = true
	cmd := m.Update(scaledialog.ResultMsg{Confirmed: true, Replicas: 3})
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	_, ok := msg.(ScaleErrorMsg)
	require.True(t, ok, "expected ScaleErrorMsg, got %T", msg)
}

func TestRemoveService_Timeout_ReturnsRemoveError(t *testing.T) {
	m := testModel(func(m *Model) {
		m.deps.Services = &mockServiceOps{
			scaleServiceFn:   func(_ context.Context, _ string, _ uint64) error { return nil },
			restartServiceFn: func(_ context.Context, _ string) error { return nil },
			removeServiceFn: func(_ context.Context, _ string) error {
				return context.DeadlineExceeded
			},
			rollbackServiceFn:   func(_ context.Context, _ string) error { return nil },
			loadNodeServicesFn:  func(_ string) []docker.ServiceEntry { return nil },
			loadStackServicesFn: func(_ string) []docker.ServiceEntry { return nil },
		}
	})
	loadServices(m, fakeEntries("web"))
	m.pendingAction = "remove"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	_, ok := msg.(RemoveErrorMsg)
	require.True(t, ok, "expected RemoveErrorMsg, got %T", msg)
}

func TestRestartService_Timeout_ReturnsRestartError(t *testing.T) {
	m := testModel(func(m *Model) {
		m.deps.Services = &mockServiceOps{
			scaleServiceFn: func(_ context.Context, _ string, _ uint64) error { return nil },
			restartServiceFn: func(_ context.Context, _ string) error {
				return context.DeadlineExceeded
			},
			removeServiceFn:     func(_ context.Context, _ string) error { return nil },
			rollbackServiceFn:   func(_ context.Context, _ string) error { return nil },
			loadNodeServicesFn:  func(_ string) []docker.ServiceEntry { return nil },
			loadStackServicesFn: func(_ string) []docker.ServiceEntry { return nil },
		}
	})
	loadServices(m, fakeEntries("web"))
	m.pendingAction = "restart"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	_, ok := msg.(RestartErrorMsg)
	require.True(t, ok, "expected RestartErrorMsg, got %T", msg)
}

// initTasks is the zammad_init shape from PR #633's review: one slot of a
// run-to-completion service, newest run complete, a failed attempt a minute
// before it, and older history either side.
func initTasks(newest swarm.TaskState, errText string) []swarm.Task {
	now := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	mk := func(state swarm.TaskState, ts time.Time, msg string) swarm.Task {
		return swarm.Task{
			ServiceID: "id-init", Slot: 1, DesiredState: swarm.TaskStateShutdown,
			Status: swarm.TaskStatus{State: state, Timestamp: ts, Err: msg},
		}
	}
	return []swarm.Task{
		mk(newest, now, errText),
		mk(swarm.TaskStateFailed, now.Add(-time.Minute), "task: non-zero exit (1)"),
		mk(swarm.TaskStateComplete, now.Add(-7*24*time.Hour), ""),
		mk(swarm.TaskStateFailed, now.Add(-21*24*time.Hour), "task: non-zero exit (1)"),
	}
}

func modelWithTasks(tasks []swarm.Task) *Model {
	return testModel(func(m *Model) {
		m.deps.Snapshot = &mockSnapshotOps{
			getSnapshotFn: func() *docker.SwarmSnapshot { return &docker.SwarmSnapshot{Tasks: tasks} },
		}
	})
}

// A one-shot service sits at 0/1 forever once it has done its job, so the
// under-replication scan that used to feed this column had its window
// permanently open and reported the failure before the successful run — red row,
// stale ERROR cell, until the service next ran at all (PR #633 review).
func TestServiceErrors_CompletedRunClearsTheEarlierFailure(t *testing.T) {
	m := modelWithTasks(initTasks(swarm.TaskStateComplete, ""))
	m.refreshServiceErrorsFromSnapshot()

	require.False(t, m.serviceHasError["id-init"], "the newest run completed, so the service is not failing")
	require.Empty(t, m.serviceErrorText["id-init"])
}

// The control: nothing has superseded the newest failure, so it is still what
// the column is for.
func TestServiceErrors_NewestFailureIsReported(t *testing.T) {
	m := modelWithTasks(initTasks(swarm.TaskStateFailed, "task: non-zero exit (2)"))
	m.refreshServiceErrorsFromSnapshot()

	require.True(t, m.serviceHasError["id-init"])
	require.Equal(t, "task: non-zero exit (2)", m.serviceErrorText["id-init"])
}

// The eldara-swarmcli_migrate shape from the second PR #633 review, driven
// through the view: the newest attempt completed, but the last failure's status
// was stamped after it — which is what swarm does to the leftovers when the
// service is updated and every task picks up DesiredState=shutdown. The service
// row must agree with the task rows beneath it, and those sort by CreatedAt.
func TestServiceErrors_RestampedFailureDoesNotOutrankANewerRun(t *testing.T) {
	base := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	mk := func(created, stamp time.Time, state swarm.TaskState, msg string) swarm.Task {
		return swarm.Task{
			ServiceID: "id-init", Slot: 1, DesiredState: swarm.TaskStateShutdown,
			Meta:   swarm.Meta{CreatedAt: created},
			Status: swarm.TaskStatus{State: state, Timestamp: stamp, Err: msg},
		}
	}
	m := modelWithTasks([]swarm.Task{
		mk(base, base.Add(20*time.Second), swarm.TaskStateFailed, "task: non-zero exit (3)"),
		mk(base.Add(2*time.Minute), base.Add(7*time.Minute), swarm.TaskStateFailed, "task: non-zero exit (1)"),
		mk(base.Add(3*time.Minute), base.Add(4*time.Minute), swarm.TaskStateComplete, ""),
		mk(base.Add(5*time.Minute), base.Add(6*time.Minute), swarm.TaskStateComplete, ""),
	})
	m.refreshServiceErrorsFromSnapshot()

	require.False(t, m.serviceHasError["id-init"], "the newest attempt completed")
	require.Empty(t, m.serviceErrorText["id-init"])
}
