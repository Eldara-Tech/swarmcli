// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

// warningStubView is a view with converging rows, and optionally failing ones.
type warningStubView struct {
	keyedStubView
	failing bool
}

func (v *warningStubView) HasErrors() bool   { return v.failing }
func (v *warningStubView) HasWarnings() bool { return true }

// The app asks a view that opts into view.Warner for its warnings and hands
// them to the logo; a failing row still wins.
func TestView_LogoFollowsWarnings(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	logo := func(c string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(true).Render(`  ___________      ___________`)
	}

	plain := newTestAppModel(&keyedStubView{}).View()
	require.Contains(t, plain, logo("214"))

	amber := newTestAppModel(&warningStubView{}).View()
	require.Contains(t, amber, logo("3"))
	require.False(t, strings.Contains(amber, logo("214")))

	red := newTestAppModel(&warningStubView{failing: true}).View()
	require.Contains(t, red, logo("9"))
	require.False(t, strings.Contains(red, logo("3")))
}
