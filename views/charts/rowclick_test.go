// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package chartsview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/charts"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func selectedRelease(m *Model) string {
	return m.list.Filtered[m.list.Cursor].Name
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

func threeReleases(t *testing.T, m *Model) {
	t.Helper()
	loadReleases(t, m, map[string][]charts.Release{
		"rel-a": deployed("rel-a", "c", "1.0.0"),
		"rel-b": deployed("rel-b", "c", "1.0.0"),
		"rel-c": deployed("rel-c", "c", "1.0.0"),
	}, nil)
	_ = m.FrameContent()
}

func TestClickRow_SelectsTheReleaseOnTheLine(t *testing.T) {
	m := sized(testModel(), 120, 24)
	threeReleases(t, m)

	require.True(t, m.ClickRow(2))
	require.Equal(t, "rel-c", selectedRelease(m))

	m.list.Viewport.YOffset = 1
	require.True(t, m.ClickRow(0))
	require.Equal(t, "rel-b", selectedRelease(m))
}

func TestClickRow_Filtered(t *testing.T) {
	m := sized(testModel(), 120, 24)
	threeReleases(t, m)
	m.ApplySearchQuery("rel-c")

	require.True(t, m.ClickRow(0))
	require.Equal(t, "rel-c", selectedRelease(m))
	require.False(t, m.ClickRow(1))
}

func TestClickRow_PastTheLastRow(t *testing.T) {
	m := sized(testModel(), 120, 24)
	threeReleases(t, m)
	m.list.Cursor = 1

	require.False(t, m.ClickRow(3))
	require.Equal(t, 1, m.list.Cursor)
}

// "Loading..." sits on line 0 before any release has arrived; it is not a row.
func TestClickRow_LoadingPlaceholder(t *testing.T) {
	m := sized(testModel(), 120, 24)
	require.Contains(t, m.FrameContent(), "Loading")

	require.False(t, m.ClickRow(0))
}

// An expanded release spans its row and the whole expansion block — revision
// and service headers included; a click on any of those lines selects the
// release and drops the child selection.
func TestClickRow_ExpandedBlockSelectsTheRelease(t *testing.T) {
	m := sized(testModel(), 120, 40)
	loadReleases(t, m,
		map[string][]charts.Release{
			"app": {
				rev("app", 1, charts.StatusSuperseded, "c", "1.0.0"),
				rev("app", 2, charts.StatusDeployed, "c", "2.0.0"),
			},
			"zeta": deployed("zeta", "c", "1.0.0"),
		},
		map[string][]charts.ServiceState{"app": {converged("app_web")}})
	m.Update(key("enter"))
	_ = m.FrameContent()
	block := m.itemLineCount(m.list.Filtered[0])
	require.Greater(t, block, 1, "app is expanded")

	for line := 0; line < block; line++ {
		m.list.Cursor, m.childIndex = 1, noChild
		require.True(t, m.ClickRow(line))
		require.Equal(t, "app", selectedRelease(m), "line %d", line)
		require.Equal(t, noChild, m.childIndex)
	}
	require.True(t, m.ClickRow(block))
	require.Equal(t, "zeta", selectedRelease(m))
}

// With a child selected the view takes the scroll offset over. A click has to
// land on the row drawn on screen, and it drops the child selection.
func TestClickRow_WhileAChildIsSelected(t *testing.T) {
	all := map[string][]charts.Release{}
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("rel-%02d", i)
		all[name] = deployed(name, "c", "1.0.0")
	}
	all["rel-05"] = []charts.Release{
		rev("rel-05", 1, charts.StatusSuperseded, "c", "1.0.0"),
		rev("rel-05", 2, charts.StatusSuperseded, "c", "1.1.0"),
		rev("rel-05", 3, charts.StatusDeployed, "c", "2.0.0"),
	}
	m := sized(testModel(), 120, 14)
	loadReleases(t, m, all, map[string][]charts.ServiceState{"rel-05": {converged("svc-web")}})
	for m.list.Cursor < 5 {
		m.Update(key("down"))
	}
	require.Equal(t, "rel-05", selectedRelease(m))
	m.Update(key("enter"))
	for i := 0; i < 3; i++ {
		m.Update(key("down"))
	}
	require.NotEqual(t, noChild, m.childIndex)
	_ = m.FrameContent()
	require.Positive(t, m.list.Viewport.YOffset, "the child selection scrolled the list")

	require.True(t, m.ClickRow(contentLineOf(t, m, "c-1.1.0")))
	require.Equal(t, "rel-05", selectedRelease(m))
	require.Equal(t, noChild, m.childIndex)

	for i := 0; i < 3; i++ {
		m.Update(key("down"))
	}
	require.NotEqual(t, noChild, m.childIndex)
	top := strings.Fields(ansi.Strip(strings.Split(m.FrameContent(), "\n")[0]))[0]
	require.True(t, strings.HasPrefix(top, "rel-"), "line 0 is a release row, got %q", top)
	require.True(t, m.ClickRow(0))
	require.Equal(t, top, selectedRelease(m))
	require.Equal(t, noChild, m.childIndex)
}
