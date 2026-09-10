// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package taskutil

import (
	"github.com/Eldara-Tech/swarmcli/v2/docker"

	"github.com/charmbracelet/lipgloss"
	"github.com/docker/docker/api/types/swarm"
)

var (
	taskFailedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	taskStoppedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

// TaskRowStyle tints one task row by what the row is telling the operator, and
// returns base for a task with nothing to say. Red is kept for a task that went
// wrong — it failed, was rejected, orphaned, recorded an error, or its
// healthcheck is failing. A task swarm stopped on purpose gets yellow: that
// happens on every update, scale-down and node drain, and the container's
// docker-ps state is "exited" for it exactly as it is for a crash, so the swarm
// task state is what tells the two apart (issue #601).
//
// base is the caller's normal row style rather than a colour of ours: the three
// views showing tasks each render an ordinary row in their own colour, and only
// the two exceptional tints are shared.
func TaskRowStyle(t docker.TaskEntry, base lipgloss.Style) lipgloss.Style {
	switch {
	case isFailedState(t.State), t.Error != "",
		t.Health == "unhealthy", t.ContainerState == "dead":
		return taskFailedStyle
	// A task that ran to completion is the outcome that was wanted, not
	// something to flag: swarm reaches "complete" only when the container
	// exited 0, and anything else is "failed". It needs an arm of its own ahead
	// of the stopped case because the container's docker-ps state is "exited"
	// here too, exactly as it is for a replica swarm retired (issue #613).
	case swarm.TaskState(t.State) == swarm.TaskStateComplete:
		return base
	case isStoppedState(t.State), t.Health == "starting",
		t.ContainerState == "restarting", t.ContainerState == "exited":
		return taskStoppedStyle
	default:
		return base
	}
}

// isFailedState reports whether a swarm task state means the task went wrong.
// Orphaned counts: the task's node has been unreachable long enough that swarm
// gave up on ever hearing how it ended.
func isFailedState(state string) bool {
	switch swarm.TaskState(state) {
	case swarm.TaskStateFailed, swarm.TaskStateRejected, swarm.TaskStateOrphaned:
		return true
	}
	return false
}

// isStoppedState reports whether a swarm task state means swarm ended the task
// rather than the task ending itself — retired by an update, a scale-down or a
// node drain, or on its way out of the task list. A task that ran to completion
// is not one of these; it is the success TaskRowStyle leaves alone.
func isStoppedState(state string) bool {
	switch swarm.TaskState(state) {
	case swarm.TaskStateShutdown, swarm.TaskStateRemove:
		return true
	}
	return false
}

// IsTerminal reports whether a swarm task state means the task is over: it will
// not run again, and its container has exited if it ever started. The services
// view reads it to stop showing a container state that has nothing left to say —
// on a task that is already over, "exited" is not news, and whether the cell can
// say it at all turns on whether the node has pruned the container yet
// (issue #616).
func IsTerminal(state string) bool {
	return isFailedState(state) || isStoppedState(state) ||
		swarm.TaskState(state) == swarm.TaskStateComplete
}
