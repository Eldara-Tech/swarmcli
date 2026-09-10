// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package taskutil

import (
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

func TestTaskRowStyle(t *testing.T) {
	base := lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	red, yellow := lipgloss.Color("9"), lipgloss.Color("3")

	cases := []struct {
		name string
		task docker.TaskEntry
		want lipgloss.TerminalColor
	}{
		// The bug in #601: swarm stops a replica on purpose and the container
		// reports the same "exited" a crash does, so only the task state can
		// keep red for the crash.
		{"swarm shut it down", docker.TaskEntry{State: "shutdown", ContainerState: "exited"}, yellow},
		{"crashed", docker.TaskEntry{State: "failed", ContainerState: "exited", Error: "task: non-zero exit (1)"}, red},

		{"running", docker.TaskEntry{State: "running", Health: "healthy", ContainerState: "running"}, base.GetForeground()},
		{"running, no healthcheck", docker.TaskEntry{State: "running"}, base.GetForeground()},
		{"still coming up", docker.TaskEntry{State: "preparing"}, base.GetForeground()},
		// #613: "complete" is the one state that proves exit 0 — swarm reaches
		// it only when the container exited cleanly, and anything else is
		// "failed". A job's whole task history is complete, so tinting it at
		// all would make the tint meaningless the way #601's red was.
		{"finished on its own, exit 0", docker.TaskEntry{State: "complete", ContainerState: "exited"}, base.GetForeground()},
		{"finished, container already pruned", docker.TaskEntry{State: "complete"}, base.GetForeground()},
		{"complete but carrying an error", docker.TaskEntry{State: "complete", Error: "boom"}, red},
		{"marked for removal", docker.TaskEntry{State: "remove"}, yellow},
		{"rejected by the node", docker.TaskEntry{State: "rejected", Error: "invalid mount config"}, red},
		{"rejected without a message", docker.TaskEntry{State: "rejected"}, red},
		{"orphaned node", docker.TaskEntry{State: "orphaned"}, red},

		// Health and container state refine a task the swarm state calls
		// ordinary; they never have to carry the terminal cases alone.
		{"failing healthcheck", docker.TaskEntry{State: "running", Health: "unhealthy", ContainerState: "running"}, red},
		{"healthcheck still starting", docker.TaskEntry{State: "running", Health: "starting", ContainerState: "running"}, yellow},
		{"restarting", docker.TaskEntry{State: "running", ContainerState: "restarting"}, yellow},
		{"dead container", docker.TaskEntry{State: "running", ContainerState: "dead"}, red},
		{"exited under a running task", docker.TaskEntry{State: "running", ContainerState: "exited"}, yellow},
		{"error on a running task", docker.TaskEntry{State: "running", Error: "boom"}, red},

		// Community Edition has no health decorator, so those two fields are
		// empty on every row and the state has to be enough on its own.
		{"CE shutdown", docker.TaskEntry{State: "shutdown"}, yellow},
		{"CE failed", docker.TaskEntry{State: "failed", Error: "task: non-zero exit (1)"}, red},
		{"CE running", docker.TaskEntry{State: "running"}, base.GetForeground()},

		{"nothing known", docker.TaskEntry{}, base.GetForeground()},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, TaskRowStyle(c.task, base).GetForeground())
		})
	}
}

// A task with nothing wrong with it must come back with the caller's own style,
// not a colour of ours: the three views showing task rows render an ordinary
// row in three different colours.
func TestTaskRowStyle_ReturnsBaseUntouched(t *testing.T) {
	base := lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true).Italic(true)
	got := TaskRowStyle(docker.TaskEntry{State: "running"}, base)
	require.Equal(t, base.Render("web.1"), got.Render("web.1"))
}

// IsTerminal has to agree with the two classifiers it is built from, and cover
// "complete", which belongs to neither: the services view uses it to decide
// whether a container state is still telling an operator something (issue #616).
func TestIsTerminal(t *testing.T) {
	for _, state := range []string{
		"complete", "shutdown", "remove", "failed", "rejected", "orphaned",
	} {
		require.True(t, IsTerminal(state), "%q is over", state)
	}
	for _, state := range []string{
		"", "new", "pending", "assigned", "preparing", "starting", "running",
	} {
		require.False(t, IsTerminal(state), "%q may still be running", state)
	}
}
