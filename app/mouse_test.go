// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/telemetry"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// rowView is a list view: content line k reads "row-k", a click is recorded as
// the line it landed on, and every message it is handed is kept.
type rowView struct {
	frameStubView
	rows     int
	clicks   []int
	received []tea.Msg
	captures bool
}

func (v *rowView) Update(msg tea.Msg) tea.Cmd {
	v.received = append(v.received, msg)
	return v.frameStubView.Update(msg)
}

func (v *rowView) FrameContent() string {
	lines := make([]string, v.rows)
	for i := range lines {
		lines[i] = fmt.Sprintf("row-%03d", i)
	}
	return ui.TrimOrPadContentToLines(strings.Join(lines, "\n"),
		ui.ContentRows(v.frameHeight, ui.FramedChromeRows, v.header, v.footer))
}

func (v *rowView) ClickRow(line int) bool {
	if line >= v.rows {
		return false
	}
	v.clicks = append(v.clicks, line)
	return true
}

func (v *rowView) CapturesInput() bool { return v.captures }

func (v *rowView) keys() []tea.KeyType {
	var keys []tea.KeyType
	for _, msg := range v.received {
		if k, ok := msg.(tea.KeyMsg); ok {
			keys = append(keys, k.Type)
		}
	}
	return keys
}

func newMouseModel(t *testing.T, v *rowView) *Model {
	t.Helper()
	m := newLayoutTestModel(v)
	m.mouseOn = true
	m.updateForResize(tea.WindowSizeMsg{Width: 100, Height: 40})
	v.received = nil
	return m
}

func press(button tea.MouseButton, y int) tea.MouseMsg {
	return tea.MouseMsg{Y: y, Action: tea.MouseActionPress, Button: button}
}

// setClock pins the double-click clock for the rest of the test.
func setClock(t *testing.T) *time.Time {
	t.Helper()
	at := time.Unix(1_700_000_000, 0)
	now = func() time.Time { return at }
	t.Cleanup(func() { now = time.Now })
	return &at
}

func TestMouseFromEnv(t *testing.T) {
	for value, want := range map[string]bool{
		"": true, "on": true, "1": true, "true": true, "bogus": true,
		"off": false, "OFF": false, " false ": false, "0": false, "no": false,
	} {
		t.Setenv(MouseEnv, value)
		require.Equal(t, want, mouseFromEnv(), "%s=%q", MouseEnv, value)
	}
}

func TestProgramOptionsCarryTheMouseUnlessSwitchedOff(t *testing.T) {
	t.Setenv(MouseEnv, "")
	require.Len(t, ProgramOptions(), 2, "alt screen and mouse")
	t.Setenv(MouseEnv, "off")
	require.Len(t, ProgramOptions(), 1, "alt screen only")
}

// A click must land on the line it was aimed at in every layout, so the test
// finds each row where View actually draws it rather than trusting the same
// arithmetic the code uses.
func TestClickLandsOnTheRenderedRow(t *testing.T) {
	cases := []struct {
		name       string
		fullscreen bool
		header     string
		openBar    func(*Model)
	}{
		{name: "normal", header: "hdr"},
		{name: "normal with a two-line header", header: "hdr\nsub"},
		{name: "normal without a header"},
		{name: "fullscreen", fullscreen: true, header: "hdr"},
		{name: "normal with the search bar open", header: "hdr", openBar: func(m *Model) {
			m.searchInput.Show()
			m.searchInput.Update(tea.KeyMsg{Type: tea.KeyEnter}) // passive: the list is live
		}},
		{name: "fullscreen with the search bar open", fullscreen: true, header: "hdr", openBar: func(m *Model) {
			m.searchInput.Show()
			m.searchInput.Update(tea.KeyMsg{Type: tea.KeyEnter})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &rowView{rows: 5}
			v.header, v.footer = tc.header, "ftr"
			m := newLayoutTestModel(v)
			m.mouseOn = true
			m.fullscreen = tc.fullscreen
			if tc.openBar != nil {
				tc.openBar(m)
			}
			m.updateForResize(tea.WindowSizeMsg{Width: 100, Height: 40})
			screen := strings.Split(ansi.Strip(m.View()), "\n")

			for line := 0; line < v.rows; line++ {
				y := indexOfRow(screen, fmt.Sprintf("row-%03d", line))
				require.NotEqual(t, -1, y, "row %d not rendered", line)
				m.lastClickAt = time.Time{}
				m.Update(press(tea.MouseButtonLeft, y))
				require.Equal(t, line, v.clicks[len(v.clicks)-1], "click on screen row %d", y)
			}

			// The header row and everything below the content are not rows.
			clicks := len(v.clicks)
			first := indexOfRow(screen, "row-000")
			m.Update(press(tea.MouseButtonLeft, first-1))
			m.Update(press(tea.MouseButtonLeft, len(screen)))
			require.Len(t, v.clicks, clicks)
		})
	}
}

func indexOfRow(screen []string, text string) int {
	for i, l := range screen {
		if strings.Contains(l, text) {
			return i
		}
	}
	return -1
}

func TestWheelBecomesUpAndDownKeys(t *testing.T) {
	v := &rowView{rows: 5}
	m := newMouseModel(t, v)

	m.Update(press(tea.MouseButtonWheelDown, 10))
	m.Update(press(tea.MouseButtonWheelUp, 10))

	require.Equal(t, []tea.KeyType{tea.KeyDown, tea.KeyUp}, v.keys())
}

// No mouse event reaches a view as a mouse event: the list views would hand it
// to a viewport that scrolls under the cursor.
func TestMouseEventsAreNeverDelegated(t *testing.T) {
	v := &rowView{rows: 5}
	m := newMouseModel(t, v)

	for _, msg := range []tea.MouseMsg{
		press(tea.MouseButtonWheelDown, 10),
		press(tea.MouseButtonLeft, 10),
		press(tea.MouseButtonRight, 10),
		{Y: 10, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft},
		{Y: 10, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft},
	} {
		m.Update(msg)
	}

	for _, msg := range v.received {
		_, isMouse := msg.(tea.MouseMsg)
		require.False(t, isMouse, "view received %#v", msg)
	}
}

func TestReleaseMotionAndOtherButtonsDoNothing(t *testing.T) {
	v := &rowView{rows: 5}
	m := newMouseModel(t, v)
	y := indexOfRow(strings.Split(ansi.Strip(m.View()), "\n"), "row-001")

	m.Update(tea.MouseMsg{Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	m.Update(tea.MouseMsg{Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m.Update(press(tea.MouseButtonRight, y))
	m.Update(press(tea.MouseButtonMiddle, y))

	require.Empty(t, v.clicks)
	require.Empty(t, v.received)
}

func TestDoubleClickIsEnter(t *testing.T) {
	v := &rowView{rows: 5}
	m := newMouseModel(t, v)
	clock := setClock(t)
	y := indexOfRow(strings.Split(ansi.Strip(m.View()), "\n"), "row-002")

	m.Update(press(tea.MouseButtonLeft, y))
	*clock = clock.Add(doubleClickWindow)
	m.Update(press(tea.MouseButtonLeft, y))
	require.Equal(t, []tea.KeyType{tea.KeyEnter}, v.keys())

	// A third click in the same window starts over.
	m.Update(press(tea.MouseButtonLeft, y))
	require.Equal(t, []tea.KeyType{tea.KeyEnter}, v.keys())
	require.Equal(t, []int{2, 2, 2}, v.clicks, "every click still selects")
}

func TestSlowSecondClickOnlySelects(t *testing.T) {
	v := &rowView{rows: 5}
	m := newMouseModel(t, v)
	clock := setClock(t)
	y := indexOfRow(strings.Split(ansi.Strip(m.View()), "\n"), "row-002")

	m.Update(press(tea.MouseButtonLeft, y))
	*clock = clock.Add(doubleClickWindow + time.Millisecond)
	m.Update(press(tea.MouseButtonLeft, y))

	require.Empty(t, v.keys())
}

func TestClicksOnDifferentRowsAreNotADoubleClick(t *testing.T) {
	v := &rowView{rows: 5}
	m := newMouseModel(t, v)
	setClock(t)
	screen := strings.Split(ansi.Strip(m.View()), "\n")

	m.Update(press(tea.MouseButtonLeft, indexOfRow(screen, "row-001")))
	m.Update(press(tea.MouseButtonLeft, indexOfRow(screen, "row-002")))

	require.Empty(t, v.keys())
}

// A click that lands on no row forgets the one before it, so row, gap, row is
// not a double click.
func TestClickOnNoRowResetsTheDoubleClick(t *testing.T) {
	v := &rowView{rows: 2}
	m := newMouseModel(t, v)
	setClock(t)
	screen := strings.Split(ansi.Strip(m.View()), "\n")
	y := indexOfRow(screen, "row-001")

	m.Update(press(tea.MouseButtonLeft, y))
	m.Update(press(tea.MouseButtonLeft, y+1)) // padding below the last row
	m.Update(press(tea.MouseButtonLeft, y))

	require.Empty(t, v.keys())
}

type fakeOverlay struct{ active bool }

func (o *fakeOverlay) Active() bool           { return o.active }
func (o *fakeOverlay) Update(tea.Msg) tea.Cmd { return nil }
func (o *fakeOverlay) View() string           { return "" }

func TestMouseIsIgnoredWhileSomethingHoldsTheKeyboard(t *testing.T) {
	cases := map[string]func(*Model, *rowView){
		"mouse switched off":   func(m *Model, _ *rowView) { m.mouseOn = false },
		"app dialog":           func(m *Model, _ *rowView) { m.appErrorDialogActive = true },
		"context drift prompt": func(m *Model, _ *rowView) { m.contextDriftDialogActive = true },
		"command bar":          func(m *Model, _ *rowView) { m.commandInput.Show() },
		"search bar typing":    func(m *Model, _ *rowView) { m.searchInput.Show() },
		"view dialog":          func(_ *Model, v *rowView) { v.captures = true },
		"startup overlay": func(*Model, *rowView) {
			startupOverlay = &fakeOverlay{active: true}
		},
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(func() { startupOverlay = nil })
			v := &rowView{rows: 5}
			m := newMouseModel(t, v)
			y := indexOfRow(strings.Split(ansi.Strip(m.View()), "\n"), "row-001")
			block(m, v)
			v.received = nil

			m.Update(press(tea.MouseButtonLeft, y))
			m.Update(press(tea.MouseButtonWheelDown, y))

			require.Empty(t, v.clicks)
			require.Empty(t, v.keys())
		})
	}
}

// A view that is not a list still scrolls with the wheel, and a click on it is
// harmless.
func TestViewWithoutRowsGetsOnlyTheWheel(t *testing.T) {
	v := &keyRecordingView{}
	m := newLayoutTestModel(v)
	m.mouseOn = true
	m.updateForResize(tea.WindowSizeMsg{Width: 100, Height: 40})
	v.keys = nil

	m.Update(press(tea.MouseButtonLeft, 10))
	m.Update(press(tea.MouseButtonLeft, 10))
	m.Update(press(tea.MouseButtonWheelDown, 10))

	require.Equal(t, []tea.KeyType{tea.KeyDown}, v.keys)
}

type keyRecordingView struct {
	frameStubView
	keys []tea.KeyType
}

func (v *keyRecordingView) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		v.keys = append(v.keys, k.Type)
	}
	return v.frameStubView.Update(msg)
}

func TestToggleMouse(t *testing.T) {
	setStackBarSuffix(t, "")
	m := newTestAppModel(&stubView{name: view.NameStacks})
	m.mouseOn = true

	_, cmd := m.Update(view.ToggleMouseMsg{})
	require.False(t, m.mouseOn)
	require.Equal(t, tea.DisableMouse(), cmd())
	require.Contains(t, ansi.Strip(m.renderStackBar()), mouseOffNotice)

	_, cmd = m.Update(view.ToggleMouseMsg{})
	require.True(t, m.mouseOn)
	require.Equal(t, tea.EnableMouseCellMotion(), cmd())
	require.Contains(t, ansi.Strip(m.renderStackBar()), mouseOnNotice)

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	require.NotContains(t, ansi.Strip(m.renderStackBar()), "Mouse", "the next key clears it")
}

// The first-run disclosure keeps the bar when a toggle happens on the same run.
func TestTelemetryNoticeOutranksTheMouseNotice(t *testing.T) {
	setStackBarSuffix(t, "")
	m := newTestAppModel(&stubView{name: view.NameStacks})
	m.telemetryNoticeActive = true
	m.mouseNoticeActive = true

	out := ansi.Strip(m.renderStackBar())

	require.Contains(t, out, telemetry.NoticeLine)
	require.NotContains(t, out, "Mouse")
}

// The terminal forgets mouse reporting when an editor takes it over, so the
// app switches it back on afterwards — but only if the session has it on.
func TestRestoreMouse(t *testing.T) {
	m := newTestAppModel(&stubView{name: view.NameStacks})

	m.mouseOn = true
	_, cmd := m.Update(view.RestoreMouseMsg{})
	require.Equal(t, tea.EnableMouseCellMotion(), cmd())

	m.mouseOn = false
	_, cmd = m.Update(view.RestoreMouseMsg{})
	require.Nil(t, cmd)
}
