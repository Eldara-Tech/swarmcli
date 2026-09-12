// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/telemetry"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// noticeModel is a model on its first reporting run.
func noticeModel(t *testing.T) *Model {
	t.Helper()
	m := newTestAppModel(&stubView{name: view.NameStacks})
	m.telemetryNoticeActive = true
	return m
}

func TestStackBar_FirstRunNoticeRenders(t *testing.T) {
	setStackBarSuffix(t, "")
	m := noticeModel(t)

	out := ansi.Strip(m.renderStackBar())

	require.Contains(t, out, telemetry.NoticeLine)
	require.Contains(t, out, view.NameStacks, "the breadcrumbs still render beside it")
}

func TestStackBar_NoNoticeOnALaterRun(t *testing.T) {
	setStackBarSuffix(t, "status-suffix")
	m := newTestAppModel(&stubView{name: view.NameStacks})

	out := ansi.Strip(m.renderStackBar())

	require.NotContains(t, out, "Usage reporting")
	require.Contains(t, out, commandHintText, "the ordinary bar is unchanged")
	require.Contains(t, out, "status-suffix")
}

// The disclosure is the last thing dropped: it outranks the discoverability
// hint *and* the status suffix, which is the reverse of their usual order.
func TestStackBar_NoticeSurvivesWhenHintAndSuffixDoNot(t *testing.T) {
	setStackBarSuffix(t, "status-suffix")
	m := noticeModel(t)

	crumbs := m.fitBreadcrumbs([]string{view.NameStacks})
	m.terminalWidth = lipgloss.Width(crumbs) + stackBarGap + lipgloss.Width(telemetry.NoticeLine)

	out := ansi.Strip(m.renderStackBar())

	require.Contains(t, out, telemetry.NoticeLine)
	require.NotContains(t, out, "status-suffix")
	require.NotContains(t, out, commandHintText)
}

func TestStackBar_NoticeFallsBackToTheShortForm(t *testing.T) {
	setStackBarSuffix(t, "")
	m := noticeModel(t)

	crumbs := m.fitBreadcrumbs([]string{view.NameStacks})
	m.terminalWidth = lipgloss.Width(crumbs) + stackBarGap + lipgloss.Width(telemetry.NoticeLineShort)

	out := ansi.Strip(m.renderStackBar())

	require.Contains(t, out, telemetry.NoticeLineShort)
	require.NotContains(t, out, "counts only", "the long form does not fit here")
	require.LessOrEqual(t, lipgloss.Width(ansi.Strip(m.renderStackBar())), m.terminalWidth)
}

func TestTelemetryNotice_ClearedByTheFirstKey(t *testing.T) {
	setStackBarSuffix(t, "")
	m := noticeModel(t)

	// A message that is not a keystroke leaves it up: the notice is retired by
	// somebody looking at the screen, not by the app doing its own work.
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	require.True(t, m.telemetryNoticeActive)
	require.Contains(t, ansi.Strip(m.renderStackBar()), "Usage reporting")

	_, _ = m.Update(runeKey('j'))
	require.False(t, m.telemetryNoticeActive)
	require.NotContains(t, ansi.Strip(m.renderStackBar()), "Usage reporting")
}

// Below the width where anything fits beside the breadcrumbs, the notice takes
// the bar rather than not being shown — the one case where the disclosure
// outranks the navigation state as well.
func TestStackBar_NarrowTerminalKeepsTheNoticeOverTheCrumbs(t *testing.T) {
	setStackBarSuffix(t, "License: Business")
	m := noticeModel(t)
	m.terminalWidth = 46

	out := ansi.Strip(m.renderStackBar())

	require.Contains(t, out, "Usage reporting")
	require.LessOrEqual(t, lipgloss.Width(out), m.terminalWidth)

	// And only for that run: once dismissed the bar is the ordinary one again.
	m.telemetryNoticeActive = false
	require.Contains(t, ansi.Strip(m.renderStackBar()), view.NameStacks)
}
