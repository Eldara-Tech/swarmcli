// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package connectapp

import (
	"strings"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/ui/qr"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// fakePane records what reaches it and says what it is told to.
type fakePane struct {
	unavailable string
	keys        []string
	others      []tea.Msg
	inited      bool
}

type paneReply struct{}

func (p *fakePane) Init() tea.Cmd {
	p.inited = true
	return func() tea.Msg { return paneReply{} }
}

func (p *fakePane) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		p.keys = append(p.keys, k.String())
	} else {
		p.others = append(p.others, msg)
	}
	return nil
}

func (p *fakePane) View(width, height int) string { return "PANE CONTENT" }
func (p *fakePane) Unavailable() string           { return p.unavailable }

func withPane(t *testing.T, p Pane) {
	t.Helper()
	prev := NewPane
	if p == nil {
		NewPane = nil
	} else {
		NewPane = func() Pane { return p }
	}
	t.Cleanup(func() { NewPane = prev })
}

func key(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func plain(s string) string { return ansi.Strip(s) }

// requireFits checks the popup is no larger than the terminal it is drawn on:
// the overlay does not clip, so a line too wide is a line that wraps.
func requireFits(t *testing.T, view string, width, height int) {
	t.Helper()
	require.LessOrEqual(t, lipgloss.Width(view), width, "popup wider than the terminal")
	require.LessOrEqual(t, lipgloss.Height(view), height, "popup taller than the terminal")
}

func TestClosed_DrawsAndDoesNothing(t *testing.T) {
	m := New()
	require.False(t, m.Visible())
	require.Empty(t, m.View())
	require.Nil(t, m.Update(key("x")))

	var nilModel *Model
	require.False(t, nilModel.Visible(), "a fixture without the popup must read as closed")
}

func TestWithoutAPane_OffersTheDownloadAndSaysWhyNotConnect(t *testing.T) {
	withPane(t, nil)
	m := New()
	require.Nil(t, m.Open(80, 24))

	view := m.View()
	text := plain(view)
	require.Contains(t, text, "SwarmCLI for iPhone and Android")
	require.Contains(t, text, "Connect this swarm")
	require.Contains(t, text, "is a Business", "the default reason, wrapped beside the code")
	require.Contains(t, text, "More information: "+AppURL)
	require.NotContains(t, text, "tab switches", "nothing to switch to")
	for _, row := range strings.Split(plain(qr.Render(DownloadURL)), "\n") {
		require.Contains(t, text, row, "the download code is drawn whole")
	}
	requireFits(t, view, 80, 24)
}

func TestTooSmall_FallsBackToTheAddresses(t *testing.T) {
	withPane(t, nil)
	m := New()
	m.Open(60, 20)

	view := m.View()
	text := plain(view)
	require.Contains(t, text, DownloadURL)
	require.Contains(t, text, AppURL)
	require.Contains(t, text, "Enlarge the terminal")
	require.NotContains(t, text, "▀", "a clipped code does not scan, so none is drawn")
	requireFits(t, view, 60, 20)
}

func TestResize_RedecidesTheLayout(t *testing.T) {
	withPane(t, nil)
	m := New()
	m.Open(60, 20)
	require.NotContains(t, plain(m.View()), "▀")

	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	require.Contains(t, plain(m.View()), "▀")
}

func TestEsc_Closes(t *testing.T) {
	withPane(t, &fakePane{})
	m := New()
	m.Open(120, 50)
	m.Update(key("esc"))
	require.False(t, m.Visible())
	require.Empty(t, m.View())
}

func TestConnectable_OpensOnConnectAndSwitchesWithTab(t *testing.T) {
	p := &fakePane{}
	withPane(t, p)
	m := New()

	cmd := m.Open(120, 50)
	require.True(t, p.inited)
	require.IsType(t, paneReply{}, cmd())

	text := plain(m.View())
	require.Contains(t, text, "tab switches")
	require.Contains(t, text, "PANE CONTENT")

	m.Update(key("tab"))
	text = plain(m.View())
	require.NotContains(t, text, "PANE CONTENT")
	require.Contains(t, text, "SwarmCLI for iPhone and Android")
	require.NotContains(t, text, "is a Business", "the download tab carries no unavailable reason")

	m.Update(key("tab"))
	require.Contains(t, plain(m.View()), "PANE CONTENT")
}

func TestConnectable_KeysReachThePaneOnlyOnItsTab(t *testing.T) {
	p := &fakePane{}
	withPane(t, p)
	m := New()
	m.Open(120, 50)

	m.Update(key("x"))
	m.Update(key("tab"))
	m.Update(key("y"))
	m.Update(paneReply{})

	require.Equal(t, []string{"x"}, p.keys, "the pane hears its own tab's keys, not tab or the download tab's")
	require.Equal(t, []tea.Msg{paneReply{}}, p.others, "replies reach the pane whichever tab is showing")
}

func TestUnavailablePane_ShowsItsReasonInsteadOfTheTab(t *testing.T) {
	p := &fakePane{unavailable: "Run :bootstrap first."}
	withPane(t, p)
	m := New()
	m.Open(80, 24)

	text := plain(m.View())
	require.Contains(t, text, "Run :bootstrap first.")
	require.NotContains(t, text, "PANE CONTENT")
	require.NotContains(t, text, "is a Business", "the pane's reason replaces the default one")

	m.Update(key("tab"))
	m.Update(key("x"))
	require.Empty(t, p.keys, "no tab to type into")
	requireFits(t, m.View(), 80, 24)
}
