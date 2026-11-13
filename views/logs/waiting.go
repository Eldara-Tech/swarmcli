// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package logsview

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/docker/docker/api/types/swarm"
	"github.com/muesli/reflow/wordwrap"
)

// A service whose new task is still pulling its image has logged nothing yet,
// and with the stopped tasks' lines hidden the view would be an empty frame.
// Instead it says what the service is waiting for, task by task. Nothing has to
// reconnect when the task starts: Swarm sends the lines of a task that starts
// later down the stream that is already open, so the first one replaces this.

// waitingPollInterval is how often the view re-reads the service's tasks. A
// var, not a const, so tests can shrink it, for the reason given at
// highlightDuration.
var waitingPollInterval = 2 * time.Second

var waitingHintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

// waitingTickMsg and waitingTasksMsg drive the re-read. Both carry the model
// that started it: a reply still in flight when the view closes is delivered to
// whatever view is current, which can be the next logs view, and must not start
// a second chain there.
type waitingTickMsg struct{ owner *Model }

type waitingTasksMsg struct {
	owner *Model
	tasks []docker.TaskEntry
}

// comingUp reports whether a task is on its way to running: Swarm wants it
// running and it has not started yet. Pulling the image is "preparing".
func comingUp(t docker.TaskEntry) bool {
	if t.DesiredState != string(swarm.TaskStateRunning) {
		return false
	}
	switch swarm.TaskState(t.State) {
	case swarm.TaskStateNew, swarm.TaskStateAllocated, swarm.TaskStatePending,
		swarm.TaskStateAssigned, swarm.TaskStateAccepted, swarm.TaskStatePreparing,
		swarm.TaskStateReady, swarm.TaskStateStarting:
		return true
	}
	return false
}

// loadWaitingCmd reads the tasks that are coming up, off the loop: under a
// TaskOps decorator the read can take seconds, so it is never made from a
// render. It refreshes a stale snapshot first, as the services view does, which
// also keeps the hide-stopped filter current while nothing on screen would
// trigger a rebuild.
func (m *Model) loadWaitingCmd() tea.Cmd {
	snapshotOps, taskOps := m.deps.Snapshot, m.deps.Tasks
	ctx, serviceID := m.StreamCtx, m.ServiceEntry.ServiceID
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		if _, err := snapshotOps.GetOrRefreshSnapshot(); err != nil {
			l().Debugf("[logsview] snapshot refresh failed: %v", err)
		}
		tasks, err := taskOps.GetTasksForService(serviceID)
		if err != nil {
			l().Debugf("[logsview] reading tasks failed: %v", err)
		}
		var waiting []docker.TaskEntry
		for _, t := range tasks {
			if comingUp(t) {
				waiting = append(waiting, t)
			}
		}
		sort.SliceStable(waiting, func(i, j int) bool {
			if waiting[i].Slot != waiting[j].Slot {
				return waiting[i].Slot < waiting[j].Slot
			}
			return waiting[i].NodeName < waiting[j].NodeName
		})
		return waitingTasksMsg{owner: m, tasks: waiting}
	}
}

func (m *Model) waitingTickCmd() tea.Cmd {
	return tea.Tick(waitingPollInterval, func(time.Time) tea.Msg { return waitingTickMsg{owner: m} })
}

// handleWaitingTick runs the next read, or, while lines are on screen and there
// is nothing to wait for, only refreshes the snapshot. A skipped read drops what
// the last one found, so it cannot resurface stale when the screen empties again.
func (m *Model) handleWaitingTick(msg waitingTickMsg) tea.Cmd {
	if msg.owner != m || m.StreamCtx.Err() != nil {
		return nil
	}
	m.mu.Lock()
	onScreen := m.onScreen
	if onScreen {
		m.waiting = nil
	}
	m.mu.Unlock()
	if onScreen {
		return m.refreshSnapshotCmd()
	}
	return m.loadWaitingCmd()
}

// stoppedCheckedMsg answers refreshSnapshotCmd.
type stoppedCheckedMsg struct{ owner *Model }

// refreshSnapshotCmd refreshes a stale snapshot off the loop. The view is open
// on the old task's lines when an update starts, or opens on them while it is
// still shutting down; nothing rebuilds once that task has stopped, so without
// this its lines would stay up for as long as the new task pulls.
func (m *Model) refreshSnapshotCmd() tea.Cmd {
	snapshotOps, ctx := m.deps.Snapshot, m.StreamCtx
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		if _, err := snapshotOps.GetOrRefreshSnapshot(); err != nil {
			l().Debugf("[logsview] snapshot refresh failed: %v", err)
		}
		return stoppedCheckedMsg{owner: m}
	}
}

// handleStoppedChecked rebuilds only when a task of the service has stopped
// since the last build, and reads what is coming up at once if that emptied the
// screen.
func (m *Model) handleStoppedChecked(msg stoppedCheckedMsg) tea.Cmd {
	if msg.owner != m {
		return nil
	}
	m.mu.Lock()
	changed := m.hideStopped && len(m.stoppedTaskIDs()) != m.stoppedAtBuild
	m.mu.Unlock()
	if !changed {
		return m.waitingTickCmd()
	}
	m.syncViewport(0)
	m.mu.Lock()
	onScreen := m.onScreen
	m.mu.Unlock()
	if onScreen {
		return m.waitingTickCmd()
	}
	return m.loadWaitingCmd()
}

// handleWaitingTasks keeps what the read found and rebuilds against the
// snapshot it refreshed, in which a task may just have stopped.
func (m *Model) handleWaitingTasks(msg waitingTasksMsg) tea.Cmd {
	if msg.owner != m {
		return nil
	}
	m.mu.Lock()
	m.waiting = msg.tasks
	m.mu.Unlock()
	m.syncViewport(0)
	return m.waitingTickCmd()
}

// waitingContent is what the frame draws in place of the viewport while nothing
// is on screen and a task is coming up, or "" when the viewport is drawn as
// usual. A TaskOps decorator that knows how far a pull has got shows through
// StatusText; without one a pulling task reads "preparing".
func (m *Model) waitingContent(width, height int) string {
	m.mu.Lock()
	onScreen, waiting, hidden := m.onScreen, m.waiting, m.hiddenStopped
	m.mu.Unlock()
	if onScreen || len(waiting) == 0 {
		return ""
	}

	rows := []string{"Waiting for " + m.ServiceEntry.ServiceName + " to start — its logs appear here once it does.", ""}
	for _, t := range waiting {
		name := t.Name
		if t.NodeName != "" {
			name += "@" + t.NodeName
		}
		row := "  " + name + "  " + t.StatusText()
		if t.Error != "" {
			row += " · " + t.Error
		}
		rows = append(rows, row)
	}
	if hidden > 0 {
		noun := "lines"
		if hidden == 1 {
			noun = "line"
		}
		rows = append(rows, "", waitingHintStyle.Render(fmt.Sprintf("%d %s from stopped tasks hidden · t shows them", hidden, noun)))
	}

	out := make([]string, 0, height)
	for _, row := range rows {
		for _, line := range strings.Split(wordwrap.String(row, width), "\n") {
			out = append(out, ui.TruncateANSI(line, width))
		}
	}
	if len(out) > height {
		out = out[:height]
	}
	for len(out) < height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}
