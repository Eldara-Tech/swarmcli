// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/commandinput"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/connectapp"
	"github.com/Eldara-Tech/swarmcli/v2/views/helpbar"
	"github.com/Eldara-Tech/swarmcli/v2/views/searchinput"
	systeminfoview "github.com/Eldara-Tech/swarmcli/v2/views/systeminfo"
	"github.com/Eldara-Tech/swarmcli/v2/views/unlockdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"
	"github.com/Eldara-Tech/swarmcli/v2/views/viewstack"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// --- test helpers ---

// stubView is a minimal view.View for testing key routing.
type stubView struct{ name string }

func (v *stubView) Update(tea.Msg) tea.Cmd              { return nil }
func (v *stubView) View() string                        { return "" }
func (v *stubView) Init() tea.Cmd                       { return nil }
func (v *stubView) Name() string                        { return v.name }
func (v *stubView) ShortHelpItems() []helpbar.HelpEntry { return nil }
func (v *stubView) OnEnter() tea.Cmd                    { return nil }
func (v *stubView) OnExit() tea.Cmd                     { return nil }
func (v *stubView) HasErrors() bool                     { return false }
func (v *stubView) FrameTitle() string                  { return "" }
func (v *stubView) FrameHeader() string                 { return "" }
func (v *stubView) FrameFooter() string                 { return "" }
func (v *stubView) FrameContent() string                { return "" }

// searchingStubView simulates a view with active internal search (ctrl+f).
type searchingStubView struct {
	stubView
	received []tea.KeyMsg
}

func (v *searchingStubView) IsSearching() bool { return true }
func (v *searchingStubView) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		v.received = append(v.received, k)
	}
	return nil
}

// newTestAppModel builds a model complete enough to render: View() reaches the
// header and both overlay dialogs, so a fixture missing any of them panics
// rather than failing.
func newTestAppModel(cv view.View) *Model {
	return &Model{
		viewport:           viewport.New(200, 50),
		currentView:        cv,
		viewStack:          viewstack.Stack{},
		commandInput:       commandinput.New(),
		searchInput:        searchinput.New(),
		errorDialog:        confirmdialog.New(200, 50),
		unlockDialog:       unlockdialog.New(200, 50),
		updateDialog:       confirmdialog.New(200, 50),
		contextDriftDialog: confirmdialog.New(200, 50),
		connectApp:         connectapp.New(),
		systemInfo:         systeminfoview.New(docker.DefaultDeps(), "test", "test"),
		terminalWidth:      200,
		terminalHeight:     50,
	}
}

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// findSubmitMsg extracts a commandinput.SubmitMsg from a tea.Cmd,
// handling tea.Batch wrapping.
func findSubmitMsg(cmd tea.Cmd) (commandinput.SubmitMsg, bool) {
	if cmd == nil {
		return commandinput.SubmitMsg{}, false
	}
	msg := cmd()
	if sub, ok := msg.(commandinput.SubmitMsg); ok {
		return sub, true
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if sub, ok := c().(commandinput.SubmitMsg); ok {
				return sub, true
			}
		}
	}
	return commandinput.SubmitMsg{}, false
}

// --- Tests: "/" and ":" forwarded to search input while editing ---

func TestSlashForwardedToSearchInput(t *testing.T) {
	m := newTestAppModel(&stubView{name: "test"})
	m.searchInput.Show()

	m.Update(runeKey('a'))
	m.Update(runeKey('/'))
	m.Update(runeKey('b'))

	require.True(t, m.searchInput.Editing())
	require.Equal(t, "a/b", m.searchInput.Query())
}

func TestColonForwardedToSearchInput(t *testing.T) {
	m := newTestAppModel(&stubView{name: "test"})
	m.searchInput.Show()

	m.Update(runeKey('a'))
	m.Update(runeKey(':'))
	m.Update(runeKey('b'))

	require.True(t, m.searchInput.Editing())
	require.Equal(t, "a:b", m.searchInput.Query())
}

// --- Tests: "/" and ":" forwarded to command input while visible ---

func TestSlashForwardedToCommandInput(t *testing.T) {
	m := newTestAppModel(&stubView{name: "test"})
	m.commandInput.Show()

	m.Update(runeKey('/'))

	require.True(t, m.commandInput.Visible(), "commandInput should remain visible")
	require.False(t, m.searchInput.Visible(), "searchInput should not have opened")

	// Submit and verify "/" was captured as text
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	sub, ok := findSubmitMsg(cmd)
	require.True(t, ok, "expected SubmitMsg")
	require.Equal(t, "/", sub.Command)
}

func TestColonForwardedToCommandInput(t *testing.T) {
	m := newTestAppModel(&stubView{name: "test"})
	m.commandInput.Show()

	m.Update(runeKey(':'))

	require.True(t, m.commandInput.Visible(), "commandInput should remain visible")

	// Submit and verify ":" was captured as text
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	sub, ok := findSubmitMsg(cmd)
	require.True(t, ok, "expected SubmitMsg")
	require.Equal(t, ":", sub.Command)
}

// --- Test: ":" forwarded to view during internal search (ctrl+f) ---

func TestColonForwardedToViewDuringInternalSearch(t *testing.T) {
	sv := &searchingStubView{stubView: stubView{name: "test"}}
	m := newTestAppModel(sv)

	m.Update(runeKey(':'))

	require.False(t, m.commandInput.Visible(), "commandInput should not have opened")
	require.Len(t, sv.received, 1, "view should have received the key")
	require.Equal(t, ":", string(sv.received[0].Runes))
}
