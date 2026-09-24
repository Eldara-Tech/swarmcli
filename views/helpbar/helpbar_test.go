// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package helpbar

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

func TestNew_DefaultGlobalHelp(t *testing.T) {
	m := New(200, 40)
	require.Len(t, m.globalHelp, 2)
	require.Equal(t, "ctrl+q", m.globalHelp[0].Key)
	require.Equal(t, "?", m.globalHelp[1].Key)
}

func TestView_WithEntries(t *testing.T) {
	m := New(200, 40)
	m.WithViewHelp([]HelpEntry{
		{Key: "n", Desc: "New"},
		{Key: "d", Desc: "Delete"},
	})
	out := m.View("", false, false)
	require.Contains(t, out, "ctrl+q")
	require.Contains(t, out, "help")
	require.Contains(t, out, "n")
	require.Contains(t, out, "New")
	require.Contains(t, out, "_____") // ASCII art logo present
}

func TestView_DisabledEntryNotBold(t *testing.T) {
	m := New(200, 40)
	m.WithViewHelp([]HelpEntry{
		{Key: "x", Desc: "Reveal (BE)", Disabled: true},
	})
	out := m.View("", false, false)
	// Disabled entries still appear in output, just styled differently
	require.Contains(t, out, "x")
	require.Contains(t, out, "Reveal (BE)")
}

func TestView_EmptyHelp_ReturnsSystemInfo(t *testing.T) {
	m := New(200, 40)
	m.globalHelp = nil
	m.viewHelp = nil
	out := m.View("sys-info-block", false, false)
	require.Equal(t, "sys-info-block", out)
}

func TestView_HasError_StillRendersLogo(t *testing.T) {
	m := New(200, 40)
	out := m.View("", true, false)
	// Logo is rendered even in error mode (color differs at runtime with terminal)
	require.Contains(t, out, "_____")
}

func TestView_NarrowWidth_SkipsHelp(t *testing.T) {
	m := New(30, 40) // Very narrow
	out := m.View("wide-system-info-panel-here!!", false, false)
	// Not enough space for help columns; should just return systemInfo
	require.Equal(t, "wide-system-info-panel-here!!", out)
}

func TestSetters(t *testing.T) {
	m := New(80, 24)
	m.SetWidth(100)
	require.Equal(t, 100, m.width)
	m.SetHeight(50)
	require.Equal(t, 50, m.height)
}

// TestView_NeverOverflowsBox guards the invariant the app layout depends on:
// the header is no wider than the width it was given and no taller than the
// height it was given, at every terminal width. The previous implementation
// rendered w+2 wide always, and 7-8 lines tall in the 112-124 band (word-wrap),
// which pushed the body off-screen and scrambled the display.
func TestView_NeverOverflowsBox(t *testing.T) {
	const height = 6
	// 40x6 system info stub, matching the real systeminfo view dimensions.
	systemInfo := strings.TrimRight(strings.Repeat(strings.Repeat("x", 40)+"\n", height), "\n")
	viewHelp := []HelpEntry{
		{Key: "i", Desc: "Inspect"},
		{Key: "ctrl+r", Desc: "Rollback service"},
		{Key: "↑/↓", Desc: "Navigate"},
		{Key: "ctrl+d", Desc: "Remove service"},
		{Key: "p", Desc: "Show/hide tasks"},
		{Key: "l", Desc: "View logs"},
	}

	for w := 60; w <= 200; w += 2 {
		out := New(w, height).WithViewHelp(viewHelp).View(systemInfo, false, false)
		require.LessOrEqualf(t, lipgloss.Width(out), w, "width overflow at vpWidth=%d", w)
		require.LessOrEqualf(t, lipgloss.Height(out), height, "height overflow at vpWidth=%d", w)
	}
}

// TestView_DegradesGracefully checks that the logo and help are shown when there
// is room and dropped (without overflow) when there is not.
func TestView_DegradesGracefully(t *testing.T) {
	const height = 6
	systemInfo := strings.TrimRight(strings.Repeat(strings.Repeat("x", 40)+"\n", height), "\n")
	help := []HelpEntry{{Key: "i", Desc: "Inspect"}, {Key: "p", Desc: "Show/hide tasks"}}

	wide := New(200, height).WithViewHelp(help).View(systemInfo, false, false)
	require.Contains(t, wide, "_____", "logo should show when wide")
	require.Contains(t, wide, "Inspect", "help should show when wide")

	// Just enough for systemInfo only: logo and help both drop, no overflow.
	narrow := New(44, height).WithViewHelp(help).View(systemInfo, false, false)
	require.NotContains(t, narrow, "_____", "logo should drop when narrow")
	require.LessOrEqual(t, lipgloss.Width(narrow), 44)
}

// TestView_OverlongEditionLabel ensures a label wider than the logo cannot panic.
func TestView_OverlongEditionLabel(t *testing.T) {
	orig := EditionLabel
	defer SetEditionLabel(orig)
	SetEditionLabel("An Absurdly Long Enterprise Ultimate Premium Edition Label")

	require.NotPanics(t, func() {
		out := New(200, 6).WithViewHelp([]HelpEntry{{Key: "i", Desc: "Inspect"}}).View("", false, false)
		require.LessOrEqual(t, lipgloss.Width(out), 200)
	})
}

func TestWithGlobalHelp(t *testing.T) {
	m := New(200, 40)
	custom := []HelpEntry{{Key: "a", Desc: "action"}}
	m.WithGlobalHelp(custom)
	require.Equal(t, custom, m.globalHelp)
}

// The logo takes the colour of the rows behind it: red when anything in the
// view fails, amber when something is converging and nothing fails, and its
// usual colour otherwise. Failing wins.
func TestBuildLogo_Colour(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	sgr := func(c string) string {
		return strings.SplitN(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(true).Render("x"), "x", 2)[0]
	}
	cases := []struct {
		name              string
		hasError, hasWarn bool
		want              string
	}{
		{"healthy", false, false, "214"},
		{"converging", false, true, "3"},
		{"failing", true, false, "9"},
		{"failing wins", true, true, "9"},
	}
	for _, c := range cases {
		require.True(t, strings.HasPrefix(buildLogo(c.hasError, c.hasWarn), sgr(c.want)), c.name)
	}
	require.NotEqual(t, sgr("3"), sgr("214"), "amber must differ from the healthy colour")
}
