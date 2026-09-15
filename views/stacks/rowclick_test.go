// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// clickModel is a stacks list sized like a real frame and rendered once, the way
// the app has drawn it by the time anybody can click on it.
func clickModel(t *testing.T, height int, names ...string) *Model {
	t.Helper()
	m := testModel()
	m.Update(tea.WindowSizeMsg{Width: 160, Height: height})
	loadStacks(m, fakeStacks(names...))
	_ = m.FrameContent()
	return m
}

func selectedStack(m *Model) string {
	return m.List.Filtered[m.List.Cursor].Name
}

// contentLineOf is the line of the rendered content that shows text.
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

func TestClickRow_SelectsTheStackOnTheLine(t *testing.T) {
	m := clickModel(t, 40, "stk-a", "stk-b", "stk-c")
	m.errorScrollOffset = 10

	require.True(t, m.ClickRow(2))
	require.Equal(t, "stk-c", selectedStack(m))
	require.Zero(t, m.errorScrollOffset, "moving to another stack resets the error scroll")

	m.List.Viewport.YOffset = 1
	require.True(t, m.ClickRow(0))
	require.Equal(t, "stk-b", selectedStack(m))
}

func TestClickRow_Filtered(t *testing.T) {
	m := clickModel(t, 40, "stk-a", "stk-b", "stk-c")
	m.ApplySearchQuery("stk-c")

	require.True(t, m.ClickRow(0))
	require.Equal(t, "stk-c", selectedStack(m))
	require.False(t, m.ClickRow(1))
}

func TestClickRow_PastTheLastRow(t *testing.T) {
	m := clickModel(t, 40, "stk-a", "stk-b")
	m.List.Cursor = 1

	require.False(t, m.ClickRow(2))
	require.Equal(t, 1, m.List.Cursor)
}

// An expanded stack spans its row, a task header and one line per task; a click
// on any of them selects the stack and drops the task selection.
func TestClickRow_ExpandedBlockSelectsTheStack(t *testing.T) {
	m := clickModel(t, 40, "stk-a", "stk-b", "stk-c")
	m.expandedStacks["stk-a"] = true
	m.stackTasks["stk-a"] = []docker.TaskEntry{{Name: "stk-a_web.1"}, {Name: "stk-a_web.2"}}
	_ = m.FrameContent()

	for line := 0; line <= 3; line++ {
		m.List.Cursor = 2
		require.True(t, m.ClickRow(line))
		require.Equal(t, "stk-a", selectedStack(m), "line %d", line)
	}
	require.True(t, m.ClickRow(4))
	require.Equal(t, "stk-b", selectedStack(m))
}

// Clicks are measured against the window on screen, which a task selection deep
// in a tall expanded stack has scrolled.
func TestClickRow_WhileATaskIsSelected(t *testing.T) {
	names := make([]string, 12)
	for i := range names {
		names[i] = fmt.Sprintf("stk-%02d", i)
	}
	m := clickModel(t, 12, names...)
	m.expandedStacks["stk-05"] = true
	m.stackTasks["stk-05"] = []docker.TaskEntry{{Name: "task-1"}, {Name: "task-2"}, {Name: "task-3"}}
	m.List.Cursor = 5
	m.selectedTaskIndex = 2
	_ = m.FrameContent()
	require.Positive(t, m.List.Viewport.YOffset, "the selection scrolled the list")

	require.True(t, m.ClickRow(contentLineOf(t, m, "task-2")))
	require.Equal(t, "stk-05", selectedStack(m))
	require.Equal(t, -1, m.selectedTaskIndex)

	m.selectedTaskIndex = 1
	top := strings.TrimSpace(strings.Split(ansi.Strip(m.FrameContent()), "\n")[0])
	name := strings.Fields(top)[0]
	require.True(t, m.ClickRow(0))
	require.Equal(t, name, selectedStack(m))
	require.Equal(t, -1, m.selectedTaskIndex)
}
