// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package servicesview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// clickModel is a visible services list sized like a real frame, sorted by name.
func clickModel(t *testing.T, height int, names ...string) *Model {
	t.Helper()
	m := testModel()
	m.Visible = true
	m.Update(tea.WindowSizeMsg{Width: 120, Height: height})
	loadServices(m, fakeEntries(names...))
	return m
}

func selectedName(m *Model) string {
	return m.List.Filtered[m.List.Cursor].ServiceName
}

// contentLineOf is the line of the rendered content that shows text, which is
// what a click on screen is measured against.
func contentLineOf(t *testing.T, m *Model, text string) int {
	t.Helper()
	for i, l := range strings.Split(ansi.Strip(m.FrameContent()), "\n") {
		if strings.Contains(l, text) {
			return i
		}
	}
	t.Fatalf("%q is not on screen", text)
	return -1
}

func TestClickRow_SelectsTheServiceOnTheLine(t *testing.T) {
	m := clickModel(t, 40, "svc-a", "svc-b", "svc-c")

	require.True(t, m.ClickRow(2))
	require.Equal(t, "svc-c", selectedName(m))

	m.List.Viewport.YOffset = 1
	require.True(t, m.ClickRow(0))
	require.Equal(t, "svc-b", selectedName(m))
}

func TestClickRow_Filtered(t *testing.T) {
	m := clickModel(t, 40, "svc-a", "svc-b", "svc-c")
	m.ApplySearchQuery("svc-c")

	require.True(t, m.ClickRow(0))
	require.Equal(t, "svc-c", selectedName(m))
	require.False(t, m.ClickRow(1))
}

func TestClickRow_PastTheLastRow(t *testing.T) {
	m := clickModel(t, 40, "svc-a", "svc-b")
	m.List.Cursor = 1

	require.False(t, m.ClickRow(2))
	require.Equal(t, 1, m.List.Cursor)
}

func TestClickRow_NotVisible(t *testing.T) {
	m := clickModel(t, 40, "svc-a", "svc-b")
	m.Visible = false

	require.False(t, m.ClickRow(1))
	require.Equal(t, 0, m.List.Cursor)
}

// An expanded service spans its row, a task header and one line per task; a
// click on any of them selects the service, not a task.
func TestClickRow_ExpandedBlockSelectsTheService(t *testing.T) {
	m := clickModel(t, 40, "svc-a", "svc-b", "svc-c")
	m.expandedServices["id-svc-a"] = true
	m.serviceTasks["id-svc-a"] = []docker.TaskEntry{{ID: "t1", NodeName: "n1"}, {ID: "t2", NodeName: "n2"}}
	m.List.Cursor = 2

	for line := 0; line <= 3; line++ {
		require.True(t, m.ClickRow(line))
		require.Equal(t, "svc-a", selectedName(m), "line %d", line)
	}
	require.True(t, m.ClickRow(4))
	require.Equal(t, "svc-b", selectedName(m))
}

// With a task selected the view lays out its own scroll offset. A click has to
// land on the row drawn on screen, and it drops the task selection.
func TestClickRow_WhileATaskIsSelected(t *testing.T) {
	names := make([]string, 12)
	for i := range names {
		names[i] = fmt.Sprintf("svc-%02d", i)
	}
	m := clickModel(t, 12, names...)
	tasks := []docker.TaskEntry{{ID: "t1", NodeName: "n1"}, {ID: "t2", NodeName: "n2"}, {ID: "t3", NodeName: "n3"}}
	m.expandedServices["id-svc-05"] = true
	m.serviceTasks["id-svc-05"] = tasks
	m.List.Cursor = 5
	m.selectedTaskIndex = 2
	_ = m.FrameContent()
	require.Positive(t, m.List.Viewport.YOffset, "the task selection scrolled the list")

	require.True(t, m.ClickRow(contentLineOf(t, m, "svc-05")))
	require.Equal(t, "svc-05", selectedName(m))
	require.Equal(t, -1, m.selectedTaskIndex)

	m.selectedTaskIndex = 2
	_ = m.FrameContent()
	require.True(t, m.ClickRow(contentLineOf(t, m, "svc-03")))
	require.Equal(t, "svc-03", selectedName(m))
	require.Equal(t, -1, m.selectedTaskIndex)

	m.List.Cursor = 5
	m.selectedTaskIndex = 0
	_ = m.FrameContent()
	require.True(t, m.ClickRow(contentLineOf(t, m, "n2")))
	require.Equal(t, "svc-05", selectedName(m))
	require.Equal(t, -1, m.selectedTaskIndex)
}
