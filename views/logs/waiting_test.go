// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package logsview

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/docker"

	"github.com/charmbracelet/x/ansi"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

type mockTaskOps struct {
	tasks []docker.TaskEntry
	err   error
	calls int
}

func (m *mockTaskOps) GetTasksForStack(string) ([]docker.TaskEntry, error) { return nil, nil }
func (m *mockTaskOps) GetTasksForService(string) ([]docker.TaskEntry, error) {
	m.calls++
	return m.tasks, m.err
}

// refreshCountingSnapshotOps counts the refreshes a read asks for.
type refreshCountingSnapshotOps struct {
	mockSnapshotOps
	refreshes int
}

func (m *refreshCountingSnapshotOps) GetOrRefreshSnapshot() (*docker.SwarmSnapshot, error) {
	m.refreshes++
	return m.snap, nil
}

func pullingTask() docker.TaskEntry {
	return docker.TaskEntry{
		Name: "web.1", Slot: 1, NodeName: "node-01",
		DesiredState: "running", State: "preparing", CurrentState: "preparing 3 seconds ago",
		PullProgress: "pulling · 0/2 layers · 0 B",
	}
}

func fastWaitingPoll(t *testing.T) {
	t.Helper()
	prev := waitingPollInterval
	waitingPollInterval = time.Millisecond
	t.Cleanup(func() { waitingPollInterval = prev })
}

// frame is the content the frame draws, without colour.
func frame(m *Model) string { return ansi.Strip(m.FrameContent()) }

func TestComingUp(t *testing.T) {
	for _, tc := range []struct {
		desired, state string
		want           bool
	}{
		{"running", "new", true},
		{"running", "pending", true},
		{"running", "assigned", true},
		{"running", "preparing", true},
		{"running", "starting", true},
		{"running", "running", false},
		{"running", "failed", false},
		{"shutdown", "preparing", false},
		{"shutdown", "shutdown", false},
	} {
		got := comingUp(docker.TaskEntry{DesiredState: tc.desired, State: tc.state})
		require.Equal(t, tc.want, got, "desired=%s state=%s", tc.desired, tc.state)
	}
}

func TestWaitingContent_ShowsTheTaskThatIsComingUp(t *testing.T) {
	m := testModel()
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{pullingTask()}})

	out := frame(m)
	require.Contains(t, out, "Waiting for web to start — its logs appear here once it does.")
	require.Contains(t, out, "web.1@node-01  pulling · 0/2 layers · 0 B")
	require.Len(t, strings.Split(out, "\n"), m.viewport.Height)
}

// Without a TaskOps decorator there is no pull progress, and the task reads as
// the swarm state it is in.
func TestWaitingContent_FallsBackToTheTaskState(t *testing.T) {
	m := testModel()
	task := pullingTask()
	task.PullProgress = ""
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{task}})

	require.Contains(t, frame(m), "web.1@node-01  preparing 3 seconds ago")
}

func TestWaitingContent_ShowsAPendingTasksError(t *testing.T) {
	m := testModel()
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{{
		Name: "web.1", Slot: 1, DesiredState: "running", State: "pending",
		CurrentState: "pending 2 seconds ago", Error: "no suitable node (insufficient resources)",
	}}})

	// No node is assigned yet, so there is no "@node" to show.
	require.Contains(t, frame(m), "web.1  pending 2 seconds ago · no suitable node (insufficient resources)")
}

func TestWaitingContent_TasksInSlotOrder(t *testing.T) {
	m := testModel()
	two, one := pullingTask(), pullingTask()
	two.Name, two.Slot = "web.2", 2
	m.deps.Tasks = &mockTaskOps{tasks: []docker.TaskEntry{two, one}}
	m.Update(m.loadWaitingCmd()())

	out := frame(m)
	require.Less(t, strings.Index(out, "web.1@"), strings.Index(out, "web.2@"))
}

func TestWaitingContent_GoneOnceALineIsVisible(t *testing.T) {
	m := testModel()
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{pullingTask()}})
	require.Contains(t, frame(m), "Waiting for web")

	m.Update(LineMsg{Line: "node-01\x00task-new\x00hello from the new task"})

	out := frame(m)
	require.Contains(t, out, "hello from the new task")
	require.NotContains(t, out, "Waiting for web")
}

func TestWaitingContent_NothingComingUpDrawsTheViewport(t *testing.T) {
	m := testModel()
	m.Update(waitingTasksMsg{owner: m})
	require.Equal(t, m.viewport.View(), m.FrameContent())
}

// A notice is the view speaking about the stream; an error under the waiting
// message would leave the reader waiting for a stream that has died.
func TestWaitingContent_ANoticeWins(t *testing.T) {
	m := testModel()
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{pullingTask()}})
	m.Update(StreamErrMsg{Err: errors.New("boom")})

	out := frame(m)
	require.Contains(t, out, "Error: boom")
	require.NotContains(t, out, "Waiting for web")
}

func TestWaitingContent_CountsLinesHiddenAsStopped(t *testing.T) {
	m := hideStoppedModel()
	m.mu.Lock()
	m.lines = []string{"old 1", "old 2", "old 3"}
	m.lineTasks = []string{"task-stop", "task-stop", "task-stop"}
	m.lineNodes = []string{"n", "n", "n"}
	m.mu.Unlock()
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{pullingTask()}})

	out := frame(m)
	require.Contains(t, out, "Waiting for web")
	require.Contains(t, out, "3 lines from stopped tasks hidden · t shows them")
	require.NotContains(t, out, "old 1")
}

func TestWaitingContent_OneHiddenLineIsSingular(t *testing.T) {
	m := hideStoppedModel()
	m.mu.Lock()
	m.lines = []string{"old 1"}
	m.lineTasks = []string{"task-stop"}
	m.lineNodes = []string{"n"}
	m.mu.Unlock()
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{pullingTask()}})

	require.Contains(t, frame(m), "1 line from stopped tasks hidden")
}

// The count is of what "t" would bring back: a line another filter hides as
// well stays hidden after "t", so it is not promised.
func TestWaitingContent_HiddenCountRespectsTheOtherFilters(t *testing.T) {
	m := hideStoppedModel()
	m.mu.Lock()
	m.lines = []string{"old on a", "old on b"}
	m.lineTasks = []string{"task-stop", "task-stop"}
	m.lineNodes = []string{"a", "b"}
	m.nodeFilter = "a"
	m.mu.Unlock()
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{pullingTask()}})

	require.Contains(t, frame(m), "1 line from stopped tasks hidden")
}

func TestWaitingContent_NoHintWhenNothingIsHidden(t *testing.T) {
	m := hideStoppedModel()
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{pullingTask()}})
	require.NotContains(t, frame(m), "hidden")

	m.setHideStopped(false)
	m.Update(HideStoppedToggledMsg{})
	require.NotContains(t, frame(m), "hidden")
}

// With hide-stopped off the old lines are on screen, so there is nothing to
// wait for.
func TestWaitingContent_StoppedLinesShownMeansNoWaiting(t *testing.T) {
	m := hideStoppedModel()
	m.setHideStopped(false)
	m.mu.Lock()
	m.lines = []string{"old 1"}
	m.lineTasks = []string{"task-stop"}
	m.lineNodes = []string{"n"}
	m.mu.Unlock()
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{pullingTask()}})

	out := frame(m)
	require.Contains(t, out, "old 1")
	require.NotContains(t, out, "Waiting for web")
}

// The read refreshes the snapshot, and the rebuild after it hides the lines of
// a task that stopped meanwhile — nothing else would, with no line arriving.
func TestWaitingTasks_RebuildsAgainstTheRefreshedSnapshot(t *testing.T) {
	m := hideStoppedModel()
	m.mu.Lock()
	m.lines = []string{"line from the old task"}
	m.lineTasks = []string{"task-run"}
	m.lineNodes = []string{"n"}
	m.mu.Unlock()
	m.syncViewport(0)
	require.Contains(t, frame(m), "line from the old task")

	snap := m.deps.Snapshot.(*mockSnapshotOps).snap
	snap.Tasks[0].Status.State = swarm.TaskStateShutdown
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{pullingTask()}})

	out := frame(m)
	require.Contains(t, out, "Waiting for web")
	require.Contains(t, out, "1 line from stopped tasks hidden")
}

func TestWaitingContent_FitsTheViewport(t *testing.T) {
	m := testModel()
	m.viewport.Width, m.viewport.Height = 30, 4
	tasks := []docker.TaskEntry{pullingTask(), pullingTask(), pullingTask()}
	m.Update(waitingTasksMsg{owner: m, tasks: tasks})

	rows := strings.Split(frame(m), "\n")
	require.Len(t, rows, 4)
	for _, row := range rows {
		require.LessOrEqual(t, ansi.StringWidth(row), 30, row)
	}
	// The sentence wraps rather than being cut.
	require.Contains(t, strings.Join(rows, " "), "Waiting for web to start")
}

func TestLoadWaitingCmd_RefreshesThenKeepsOnlyTasksComingUp(t *testing.T) {
	m := testModel()
	snapshot := &refreshCountingSnapshotOps{}
	running := pullingTask()
	running.Name, running.State, running.PullProgress = "web.2", "running", ""
	tasks := &mockTaskOps{tasks: []docker.TaskEntry{running, pullingTask()}}
	m.deps.Snapshot, m.deps.Tasks = snapshot, tasks

	msg, ok := m.loadWaitingCmd()().(waitingTasksMsg)
	require.True(t, ok)
	require.Same(t, m, msg.owner)
	require.Equal(t, []docker.TaskEntry{pullingTask()}, msg.tasks)
	require.Equal(t, 1, snapshot.refreshes)
	require.Equal(t, 1, tasks.calls)
}

func TestLoadWaitingCmd_AFailedReadWaitsForNothing(t *testing.T) {
	m := testModel()
	m.deps.Tasks = &mockTaskOps{err: errors.New("no snapshot available")}

	msg := m.loadWaitingCmd()().(waitingTasksMsg)
	require.Empty(t, msg.tasks)
}

func TestWaitingTick_ReadsWhileNothingIsOnScreen(t *testing.T) {
	m := testModel()
	tasks := &mockTaskOps{tasks: []docker.TaskEntry{pullingTask()}}
	m.deps.Tasks = tasks
	m.syncViewport(0)

	cmd := m.Update(waitingTickMsg{owner: m})
	require.NotNil(t, cmd)
	_, isReply := cmd().(waitingTasksMsg)
	require.True(t, isReply)
	require.Equal(t, 1, tasks.calls)
}

func TestWaitingTick_SkipsTheReadWhileLinesAreOnScreen(t *testing.T) {
	fastWaitingPoll(t)
	m := testModel()
	tasks := &mockTaskOps{}
	m.deps.Tasks = tasks
	m.Update(waitingTasksMsg{owner: m, tasks: []docker.TaskEntry{pullingTask()}})
	m.Update(LineMsg{Line: "node-01\x00task-new\x00hello"})

	snap := &refreshCountingSnapshotOps{mockSnapshotOps: *m.deps.Snapshot.(*mockSnapshotOps)}
	m.deps.Snapshot = snap

	cmd := m.Update(waitingTickMsg{owner: m})
	checked, isCheck := cmd().(stoppedCheckedMsg)
	require.True(t, isCheck, "only the snapshot is refreshed")
	require.Equal(t, 1, snap.refreshes)
	require.Zero(t, tasks.calls)
	m.mu.Lock()
	require.Nil(t, m.waiting, "what the last read found is dropped")
	m.mu.Unlock()

	_, isTick := m.Update(checked)().(waitingTickMsg)
	require.True(t, isTick, "nothing stopped, so the chain just ticks on")
	require.Zero(t, tasks.calls)
}

// The view opened on the old task's lines while it was still shutting down. Once
// the refreshed snapshot says it stopped, its lines go, and what the service is
// waiting for is read straight away rather than a tick later.
func TestStoppedChecked_HidesATaskThatStoppedSinceTheBuild(t *testing.T) {
	m := hideStoppedModel()
	tasks := &mockTaskOps{tasks: []docker.TaskEntry{pullingTask()}}
	m.deps.Tasks = tasks
	m.mu.Lock()
	m.lines = []string{"line from the old task"}
	m.lineTasks = []string{"task-run"}
	m.lineNodes = []string{"n"}
	m.mu.Unlock()
	m.syncViewport(0)
	require.Contains(t, frame(m), "line from the old task")

	m.deps.Snapshot.(*mockSnapshotOps).snap.Tasks[0].Status.State = swarm.TaskStateShutdown
	reply, isRead := m.Update(stoppedCheckedMsg{owner: m})().(waitingTasksMsg)
	require.True(t, isRead, "the emptied screen reads what is coming up at once")
	require.NotContains(t, frame(m), "line from the old task")

	m.Update(reply)
	out := frame(m)
	require.Contains(t, out, "Waiting for web")
	require.Contains(t, out, "1 line from stopped tasks hidden")
}

func TestStoppedChecked_IgnoresAnotherViewsReply(t *testing.T) {
	m := testModel()
	require.Nil(t, m.Update(stoppedCheckedMsg{owner: testModel()}))
}

func TestRefreshSnapshotCmd_SendsNothingAfterExit(t *testing.T) {
	m := testModel()
	refresh := m.refreshSnapshotCmd()
	m.OnExit()()
	require.Nil(t, refresh())
}

// The chain is one read at a time: a reply schedules the next tick, never a
// second read.
func TestWaitingTasks_ScheduleATickNotARead(t *testing.T) {
	fastWaitingPoll(t)
	m := testModel()
	tasks := &mockTaskOps{}
	m.deps.Tasks = tasks

	cmd := m.Update(waitingTasksMsg{owner: m})
	_, isTick := cmd().(waitingTickMsg)
	require.True(t, isTick)
	require.Zero(t, tasks.calls)
}

func TestWaitingTick_StopsOnceTheViewHasExited(t *testing.T) {
	m := testModel()
	tasks := &mockTaskOps{tasks: []docker.TaskEntry{pullingTask()}}
	m.deps.Tasks = tasks
	read := m.loadWaitingCmd()

	m.OnExit()()

	require.Nil(t, m.Update(waitingTickMsg{owner: m}))
	require.Nil(t, read(), "a read issued before the exit sends nothing")
	require.Zero(t, tasks.calls)
}

// A reply addressed to an earlier logs view lands on whatever view is current,
// which can be a new logs view; it must neither start a chain there nor
// overwrite what that view found.
func TestWaiting_IgnoresAnotherViewsMessages(t *testing.T) {
	m := testModel()
	m.deps.Tasks = &mockTaskOps{}
	other := testModel()

	require.Nil(t, m.Update(waitingTickMsg{owner: other}))
	require.Nil(t, m.Update(waitingTasksMsg{owner: other, tasks: []docker.TaskEntry{pullingTask()}}))
	require.NotContains(t, frame(m), "Waiting for web")
}
