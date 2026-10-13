// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/settings"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/connectapp"
	"github.com/Eldara-Tech/swarmcli/v2/views/helpbar"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// cloningStubView binds "c" itself, the way configs and contexts do.
type cloningStubView struct {
	stubView
	received []string
}

func (v *cloningStubView) ShortHelpItems() []helpbar.HelpEntry {
	return []helpbar.HelpEntry{{Key: "c", Desc: "Clone"}}
}

func (v *cloningStubView) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		v.received = append(v.received, k.String())
	}
	return nil
}

// recordingPane is a Connect tab that remembers what reached it.
type recordingPane struct{ msgs []tea.Msg }

type paneOnlyMsg struct{}

func (p *recordingPane) Init() tea.Cmd                 { return nil }
func (p *recordingPane) Update(msg tea.Msg) tea.Cmd    { p.msgs = append(p.msgs, msg); return nil }
func (p *recordingPane) View(width, height int) string { return "pane" }
func (p *recordingPane) Unavailable() string           { return "" }

func withConnectPane(t *testing.T, p connectapp.Pane) {
	t.Helper()
	prev := connectapp.NewPane
	connectapp.NewPane = func() connectapp.Pane { return p }
	t.Cleanup(func() { connectapp.NewPane = prev })
}

func TestConnectKey_OpensThePopup(t *testing.T) {
	m := newTestAppModel(&keyedStubView{})
	m.Update(runeKey('c'))
	require.True(t, m.connectApp.Visible())
	require.True(t, m.dialogActive(), "an open popup owns the screen, mouse included")
	require.Contains(t, m.View(), "Connect the SwarmCLI app")
}

func TestConnectKey_IsLeftToAViewThatBindsIt(t *testing.T) {
	v := &cloningStubView{}
	m := newTestAppModel(v)
	m.Update(runeKey('c'))
	require.False(t, m.connectApp.Visible())
	require.Equal(t, []string{"c"}, v.received)
}

func TestConnectKey_IsACharacterWhileSearching(t *testing.T) {
	v := &searchingStubView{}
	m := newTestAppModel(v)
	m.Update(runeKey('c'))
	require.False(t, m.connectApp.Visible())
	require.Len(t, v.received, 1)
}

func TestConnectCommand_OpensThePopupFromAnyView(t *testing.T) {
	m := newTestAppModel(&cloningStubView{})
	m.Update(view.OpenConnectAppMsg{})
	require.True(t, m.connectApp.Visible())
}

func TestPopup_TakesKeysExclusivelyAndEscClosesIt(t *testing.T) {
	m := newTestAppModel(&keyedStubView{})
	m.Update(runeKey('c'))

	m.Update(runeKey('f'))
	require.False(t, m.fullscreen, "f belongs to the popup while it is open")
	m.Update(runeKey(':'))
	require.False(t, m.commandInput.Visible(), "so does :")

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.False(t, m.connectApp.Visible())
	require.NotContains(t, m.View(), "Connect the SwarmCLI app")
}

func TestPopup_PaneHearsMessagesTheAppDoesNotKnow(t *testing.T) {
	p := &recordingPane{}
	withConnectPane(t, p)
	m := newTestAppModel(&keyedStubView{})
	m.Update(runeKey('c'))

	m.Update(paneOnlyMsg{})
	m.Update(runeKey('x'))

	require.Contains(t, p.msgs, tea.Msg(paneOnlyMsg{}))
	require.Contains(t, p.msgs, tea.Msg(runeKey('x')), "a key reaches it once, through the popup")
	require.Len(t, p.msgs, 2)
}

func TestGlobalHelp_AdvertisesConnectUnlessTheViewOwnsC(t *testing.T) {
	m := newTestAppModel(&keyedStubView{})
	require.Contains(t, m.globalHelpEntries(), helpbar.HelpEntry{Key: "c", Desc: "Connect app"})

	m = newTestAppModel(&cloningStubView{})
	for _, e := range m.globalHelpEntries() {
		require.NotEqual(t, "c", e.Key, "one key, one meaning on one bar")
	}
}

func TestConnectNotice_RidesTheStackBarUntilTheFirstKey(t *testing.T) {
	m := newTestAppModel(&keyedStubView{})
	m.connectNoticeActive = true
	line, short := m.stackBarNotice()
	require.Equal(t, connectNotice, line)
	require.Equal(t, connectNoticeShort, short)

	m.Update(runeKey('x'))
	require.False(t, m.connectNoticeActive)
}

func TestConnectNoticeDue_OnceAndRemembered(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, settings.Settings{DismissedUpdateVersion: "v1.0.0"}.Save())

	require.True(t, connectNoticeDueFn())
	require.False(t, connectNoticeDueFn())
	require.Equal(t, "v1.0.0", settings.Load().DismissedUpdateVersion, "recording it keeps the other setting")
}

func TestUpdateDialogDismiss_KeepsTheOtherSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, settings.Settings{ConnectAppNoticeShown: true}.Save())

	m := newTestAppModel(&stubView{name: "test"})
	m.showUpdateNotice("v1.9.0")
	m.Update(confirmdialog.ResultMsg{CheckboxChecked: true})

	s := settings.Load()
	require.Equal(t, "v1.9.0", s.DismissedUpdateVersion)
	require.True(t, s.ConnectAppNoticeShown, "a dismissal used to save a fresh Settings{} over the file")
}
