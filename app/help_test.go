// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"testing"

	helpview "github.com/Eldara-Tech/swarmcli/v2/views/help"
	"github.com/Eldara-Tech/swarmcli/v2/views/helpbar"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// keyedStubView publishes keys to the help bar and nothing else — the state
// every view is in before anyone writes it a help screen.
type keyedStubView struct{ stubView }

func (v *keyedStubView) FrameTitle() string { return "Widgets · prod" }
func (v *keyedStubView) ShortHelpItems() []helpbar.HelpEntry {
	return []helpbar.HelpEntry{
		{Key: "x", Desc: "Do the thing"},
		{Key: "n/N", Desc: "Step", Disabled: true},
	}
}

// authoredStubView carries its own screen.
type authoredStubView struct{ stubView }

func (v *authoredStubView) HelpContent() []helpview.HelpCategory {
	return []helpview.HelpCategory{{
		Title: "Written by hand",
		Items: []helpview.HelpItem{{Keys: "<x>", Description: "What it means"}},
	}}
}

// hidingStubView suppresses the app's global keys and records what reaches it.
type hidingStubView struct {
	stubView
	received []tea.KeyMsg
}

func (v *hidingStubView) HidesGlobalHelp() bool { return true }
func (v *hidingStubView) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		v.received = append(v.received, k)
	}
	return nil
}

func helpCategories(t *testing.T, cmd tea.Cmd) []helpview.HelpCategory {
	t.Helper()
	require.NotNil(t, cmd)
	nav, ok := cmd().(view.NavigateToMsg)
	require.True(t, ok, "? must navigate somewhere")
	require.Equal(t, view.NameHelp, nav.ViewName)
	categories, ok := nav.Payload.([]helpview.HelpCategory)
	require.True(t, ok, "the help view's factory reads this payload by type")
	return categories
}

func allItems(categories []helpview.HelpCategory) []helpview.HelpItem {
	var items []helpview.HelpItem
	for _, c := range categories {
		items = append(items, c.Items...)
	}
	return items
}

// TestHelpKey_DescribesAViewThatSaysNothing is the whole point of the change: a
// view that never implemented "?" still answers it, because the keys it
// publishes to the help bar are enough to build a screen from.
func TestHelpKey_DescribesAViewThatSaysNothing(t *testing.T) {
	m := newTestAppModel(&keyedStubView{})

	_, cmd := m.Update(runeKey('?'))
	items := allItems(helpCategories(t, cmd))

	require.Contains(t, items, helpview.HelpItem{Keys: "<x>", Description: "Do the thing"})
	require.Contains(t, items, helpview.HelpItem{Keys: "<n/N>", Description: "Step"},
		"a key the bar dims is still a key the view has")
	require.Contains(t, items, helpview.HelpItem{Keys: "<?>", Description: "Help"},
		"the screen lists the app's own keys too")
}

func TestHelpKey_PrefersTheViewsOwnScreen(t *testing.T) {
	m := newTestAppModel(&authoredStubView{})

	_, cmd := m.Update(runeKey('?'))
	categories := helpCategories(t, cmd)

	require.Len(t, categories, 2)
	require.Equal(t, "Written by hand", categories[0].Title)
	// A view that writes its own screen still gets the keys that work
	// everywhere: it has no reason to know them, and they used to be missing.
	require.Equal(t, "Everywhere", categories[1].Title)
	require.Contains(t, categories[1].Items, helpview.HelpItem{Keys: "<f>", Description: "Fullscreen"})
}

func TestHelpKey_IsACharacterWhileSearching(t *testing.T) {
	v := &searchingStubView{}
	m := newTestAppModel(v)

	_, cmd := m.Update(runeKey('?'))

	require.Nil(t, cmd)
	require.Len(t, v.received, 1, "a view with an open search box must receive the keystroke")
	require.Equal(t, "?", v.received[0].String())
}

// TestHelpKey_LeftToAViewThatHidesTheGlobalKeys keeps the promise and the
// answer in step: a view that tells the bar not to advertise "?" is a view the
// app must not answer it for.
func TestHelpKey_LeftToAViewThatHidesTheGlobalKeys(t *testing.T) {
	v := &hidingStubView{}
	m := newTestAppModel(v)

	_, cmd := m.Update(runeKey('?'))

	require.Nil(t, cmd)
	require.Len(t, v.received, 1)
}

// TestGlobalHelpEntries_AreTheSameOnBothSurfaces guards the extraction: the bar
// and the help screen must be generated from one list, or they drift.
func TestGlobalHelpEntries_AreTheSameOnBothSurfaces(t *testing.T) {
	m := newTestAppModel(&keyedStubView{})
	entries := m.globalHelpEntries()
	require.NotEmpty(t, entries)

	rendered := m.View()
	items := allItems(helpCategories(t, m.openHelp()))
	for _, e := range entries {
		require.Contains(t, rendered, e.Key, "the help bar advertises %q", e.Key)
		require.Contains(t, items, helpview.HelpItem{Keys: "<" + e.Key + ">", Description: e.Desc},
			"the help screen documents %q", e.Key)
	}

	m.currentView = &hidingStubView{}
	require.Empty(t, m.globalHelpEntries())
}
