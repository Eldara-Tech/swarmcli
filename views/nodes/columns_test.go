// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package nodesview

import (
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

const longHostname = "worker-with-a-very-long-hostname-that-overflows"

func readyNodes(m *Model, width int, names ...string) {
	loadNodes(m, fakeNodes(names...))
	m.setRenderItem()
	m.List.Viewport.Width = width
	m.List.Viewport.Height = 20
}

func longNode(m *Model) docker.NodeEntry {
	for _, n := range m.List.Filtered {
		if n.Hostname == longHostname {
			return n
		}
	}
	return docker.NodeEntry{}
}

func TestContentAwareHostname_WideTerminal(t *testing.T) {
	m := testModel()
	readyNodes(m, 220, longHostname, "w2")
	row := m.List.RenderItem(longNode(m), false, 0)
	require.Contains(t, row, longHostname, "full hostname must be visible on a wide terminal")
}

func TestHeaderRowAlignment(t *testing.T) {
	m := testModel()
	loadNodes(m, fakeNodes(longHostname, "w2"))
	m.setRenderItem()
	for _, w := range []int{60, 120, 220} {
		m.List.Viewport.Width = w
		header := m.List.RenderHeader()
		row := m.List.RenderItem(longNode(m), false, 0)
		require.Equal(t, lipgloss.Width(header), lipgloss.Width(row), "width=%d header/row mismatch", w)
	}
}

func TestArrowScrollMovesWindow(t *testing.T) {
	m := testModel()
	readyNodes(m, 70, longHostname, "w2") // narrow → flex columns overflow
	before := m.List.RenderItem(longNode(m), true, 0)
	m.Update(key("right"))
	after := m.List.RenderItem(longNode(m), true, 0)
	require.NotEqual(t, before, after, "right arrow should shift the truncated cell window")
}

func TestResetScrollOnCursorMove(t *testing.T) {
	m := testModel()
	readyNodes(m, 70, longHostname, "w2")
	m.Update(key("right"))
	m.Update(key("right"))
	scrolled := m.List.RenderItem(longNode(m), true, 0)

	m.Update(key("down"))
	m.Update(key("up"))

	require.NotEqual(t, scrolled, m.List.RenderItem(longNode(m), true, 0),
		"scroll offset must reset when the cursor moves")
}

// --- Mouse ---

func TestClickRow_SelectsTheLineInTheScrolledWindow(t *testing.T) {
	m := testModel()
	loadNodes(m, fakeNodes("n1", "n2", "n3", "n4"))
	m.List.Viewport.YOffset = 1

	require.True(t, m.ClickRow(1))
	require.Equal(t, "n3", m.List.Filtered[m.List.Cursor].Hostname)
}

func TestClickRow_SelectsTheFilteredRow(t *testing.T) {
	m := testModel()
	loadNodes(m, fakeNodes("n1", "n2", "n3"))
	m.ApplySearchQuery("n3")

	require.True(t, m.ClickRow(0))
	require.Equal(t, "n3", m.List.Filtered[m.List.Cursor].Hostname)
}

func TestClickRow_BelowTheLastRowSelectsNothing(t *testing.T) {
	m := testModel()
	loadNodes(m, fakeNodes("n1", "n2", "n3"))
	m.List.Cursor = 1

	require.False(t, m.ClickRow(3))
	require.Equal(t, 1, m.List.Cursor)
}

func TestClickRow_ResetsScrollLikeTheArrowKeys(t *testing.T) {
	m := testModel()
	readyNodes(m, 70, longHostname, "w2")
	require.True(t, m.ClickRow(1))
	before := m.List.RenderItem(longNode(m), true, 0)
	m.Update(key("right"))
	m.Update(key("right"))
	scrolled := m.List.RenderItem(longNode(m), true, 0)
	require.NotEqual(t, before, scrolled, "precondition: the long row scrolled")

	require.True(t, m.ClickRow(0))

	require.NotEqual(t, scrolled, m.List.RenderItem(longNode(m), true, 0),
		"scroll offset must reset when a click moves the cursor")
}

// An unhealthy node renders red unless the cursor is on it, where the selection
// highlight wins as it does in the services view; a drained node stays plain.
func TestRowStyle_UnhealthyNodeIsRed(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := testModel()
	entries := fakeNodes("up", "gone", "drained")
	entries[1].State = "down"
	entries[2].Availability = "drain"
	loadNodes(m, entries)
	m.List.Viewport.Width = 220

	red := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	for _, n := range m.List.Filtered {
		row := m.List.RenderRow(n, false)
		got := m.List.RenderItem(n, false, 0)
		if n.Hostname == "gone" {
			require.Equal(t, red.Render(row), got)
		} else {
			require.Equal(t, ui.ListItemStyle.Render(row), got, n.Hostname)
		}
		require.Equal(t, ui.ListSelectedStyle.Render(m.List.RenderRow(n, true)),
			m.List.RenderItem(n, true, 0), n.Hostname)
	}
}
