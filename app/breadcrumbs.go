// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	"github.com/charmbracelet/lipgloss"
)

// crumb is one piece of the breadcrumb bar: a view's segment, the "…" prefix
// or a "→" between segments. target is the index into names that a click on it
// goes back to, or -1 where a click goes nowhere: an arrow, or the current view.
type crumb struct {
	text   string
	target int
}

// RenderBreadcrumbs produces the breadcrumb bar string from a list of view names.
func RenderBreadcrumbs(names []string, maxDisplay int) string {
	crumbs := breadcrumbs(names, maxDisplay)
	parts := make([]string, len(crumbs))
	for i, c := range crumbs {
		parts[i] = c.text
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, parts...)
}

// breadcrumbs lays out the bar RenderBreadcrumbs draws, left to right.
// Top-level views show only their own name. Nested views are trimmed to the
// nearest top-level ancestor, then capped at maxDisplay items (with "…" prefix).
// The "…" goes back to the nearest view it hides.
func breadcrumbs(names []string, maxDisplay int) []crumb {
	if len(names) == 0 {
		return nil
	}
	// Current view is top-level → show just that view
	current := len(names) - 1
	if view.IsTopLevel(names[current]) {
		style := ui.Rainbow[0]
		return []crumb{{style.Render(fmt.Sprintf(" %s ", names[current])), -1}}
	}

	// Walk backward to find nearest top-level ancestor; slice from there
	start := 0
	for i := current; i >= 0; i-- {
		if view.IsTopLevel(names[i]) {
			start = i
			break
		}
	}

	// Cap at maxDisplay, prepend ellipsis if trimmed
	first := max(start, len(names)-maxDisplay)
	faint := lipgloss.NewStyle().Faint(true)
	var crumbs []crumb
	if first > start {
		crumbs = append(crumbs, crumb{faint.Render(" … "), first - 1})
	}
	for i := first; i <= current; i++ {
		if len(crumbs) > 0 {
			crumbs = append(crumbs, crumb{faint.Render(" → "), -1})
		}
		target := i
		if i == current {
			target = -1
		}
		style := ui.Rainbow[(i-first)%len(ui.Rainbow)]
		crumbs = append(crumbs, crumb{style.Render(fmt.Sprintf(" %s ", names[i])), target})
	}
	return crumbs
}
