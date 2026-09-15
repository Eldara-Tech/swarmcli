// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package volumesview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

// longVolName is wide enough to overflow the NAME column on a narrow terminal
// and to verify full visibility on a wide one.
const longVolName = "volume_with_a_fairly_long_descriptive_name"

// longVolume returns the loaded item whose name overflows, for scroll tests.
func longVolume(m *Model) volumeItem {
	for _, v := range m.volumesList.Filtered {
		if v.Name == longVolName {
			return v
		}
	}
	return volumeItem{}
}

func TestHeaderAndRow_Aligned(t *testing.T) {
	for _, w := range []int{60, 120, 220} {
		m := testModel()
		m.volumesList.Viewport.Width = w
		loadVolumes(m, fakeVolumes(longVolName, "z2"))

		header := m.volumesList.RenderHeader()
		row := m.volumesList.RenderItem(m.volumesList.Filtered[0], false, 0)
		require.Equal(t, lipgloss.Width(header), lipgloss.Width(row),
			"header and row widths must match at width %d", w)
	}
}

func TestContentAwareName_WideTerminal(t *testing.T) {
	m := testModel()
	m.volumesList.Viewport.Width = 220
	loadVolumes(m, fakeVolumes(longVolName, "z2"))
	row := m.volumesList.RenderItem(longVolume(m), false, 0)
	require.Contains(t, row, longVolName, "full name must be visible on a wide terminal")
}

func TestArrowScrollMovesWindow(t *testing.T) {
	m := testModel()
	m.volumesList.Viewport.Width = 70 // narrow → flex columns overflow
	loadVolumes(m, fakeVolumes(longVolName, "z2"))
	before := m.volumesList.RenderItem(longVolume(m), true, 0)
	m.Update(key("right"))
	after := m.volumesList.RenderItem(longVolume(m), true, 0)
	require.NotEqual(t, before, after, "right arrow should shift the truncated cell window")
}

func TestResetScrollOnCursorMove(t *testing.T) {
	m := testModel()
	m.volumesList.Viewport.Width = 70
	loadVolumes(m, fakeVolumes(longVolName, "z2"))
	m.Update(key("right"))
	m.Update(key("right"))
	scrolled := m.volumesList.RenderItem(longVolume(m), true, 0)

	m.Update(key("down"))
	m.Update(key("up"))

	require.NotEqual(t, scrolled, m.volumesList.RenderItem(longVolume(m), true, 0),
		"scroll offset must reset when the cursor moves")
}

// A wide terminal must be filled, not left half dead.
//
// Confining the leftover to the trailing column was tried first, so the columns
// before it would stop drifting apart. It stops the drift by spending the whole
// surplus on one cell: on a 360-column terminal the volumes table needs 108 and
// MOUNT POINT got a 300-cell column to hold a 43-character path, so everything
// packed into the left third. Every column takes an equal share instead.
func TestWideTerminalWidensEveryGapEqually(t *testing.T) {
	positions := func(width int) map[string]int {
		m := testModel()
		m.volumesList.Viewport.Width = width
		loadVolumes(m, fakeVolumes("openclaw_data", "pg_data"))
		header := m.volumesList.RenderHeader()
		out := map[string]int{}
		for _, col := range []string{"STACK", "DRIVER", "CREATED", "HOST", "MOUNT POINT"} {
			i := strings.Index(header, col)
			require.GreaterOrEqual(t, i, 0, "%q not in %q", col, header)
			out[col] = lipgloss.Width(header[:i])
		}
		return out
	}

	narrow, wide := positions(140), positions(240)
	gap := wide["STACK"] - narrow["STACK"]
	require.Positive(t, gap, "a wider terminal must be filled, not left dead")
	// Each column starts one more gap further right than the one before it, so
	// the extra width is spacing rather than a hole after any single column.
	for i, col := range []string{"DRIVER", "CREATED", "HOST", "MOUNT POINT"} {
		require.Equal(t, gap*(i+2), wide[col]-narrow[col],
			"%s must move by its position's share of equal gaps", col)
	}

	m := testModel()
	m.volumesList.Viewport.Width = 240
	loadVolumes(m, fakeVolumes("openclaw_data"))
	require.Equal(t, 240, lipgloss.Width(m.volumesList.RenderRow(m.volumesList.Filtered[0], true)),
		"the row still spans the frame, so the selection highlight does too")
}

// --- Mouse ---

func TestClickRow_SelectsTheLineInTheScrolledWindow(t *testing.T) {
	m := testModel()
	loadVolumes(m, fakeVolumes("v1", "v2", "v3", "v4"))
	m.volumesList.Viewport.YOffset = 1

	require.True(t, m.ClickRow(1))
	require.Equal(t, "v3", m.volumesList.Filtered[m.volumesList.Cursor].Name)
}

func TestClickRow_SelectsTheFilteredRow(t *testing.T) {
	m := testModel()
	loadVolumes(m, fakeVolumes("v1", "v2", "v3"))
	m.ApplySearchQuery("v3")

	require.True(t, m.ClickRow(0))
	require.Equal(t, "v3", m.volumesList.Filtered[m.volumesList.Cursor].Name)
}

func TestClickRow_BelowTheLastRowSelectsNothing(t *testing.T) {
	m := testModel()
	loadVolumes(m, fakeVolumes("v1", "v2", "v3"))
	m.volumesList.Cursor = 1

	require.False(t, m.ClickRow(3))
	require.Equal(t, 1, m.volumesList.Cursor)
}

// Before the first load the content is the loading placeholder, not a row.
func TestClickRow_WhileLoadingSelectsNothing(t *testing.T) {
	require.False(t, testModel().ClickRow(0))
}

func TestClickRow_ResetsScrollLikeTheArrowKeys(t *testing.T) {
	m := testModel()
	m.volumesList.Viewport.Width = 70
	loadVolumes(m, fakeVolumes(longVolName, "z2"))
	before := m.volumesList.RenderItem(longVolume(m), true, 0)
	m.Update(key("right"))
	m.Update(key("right"))
	scrolled := m.volumesList.RenderItem(longVolume(m), true, 0)
	require.NotEqual(t, before, scrolled, "precondition: the long row scrolled")

	require.True(t, m.ClickRow(1))

	require.NotEqual(t, scrolled, m.volumesList.RenderItem(longVolume(m), true, 0),
		"scroll offset must reset when a click moves the cursor")
}
