// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderBreadcrumbs_TopLevelShowsSelf(t *testing.T) {
	// Current view is top-level → shows just that view name
	result := RenderBreadcrumbs([]string{"stacks"}, 3)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "stacks")

	result = RenderBreadcrumbs([]string{"services", "stacks"}, 3)
	assert.Contains(t, result, "stacks")
	// Should not show prior history
	assert.NotContains(t, result, "services")

	result = RenderBreadcrumbs([]string{"configs"}, 3)
	assert.Contains(t, result, "configs")
}

func TestRenderBreadcrumbs_Empty(t *testing.T) {
	assert.Equal(t, "", RenderBreadcrumbs(nil, 3))
	assert.Equal(t, "", RenderBreadcrumbs([]string{}, 3))
}

func TestRenderBreadcrumbs_TrimsToTopLevel(t *testing.T) {
	// [stacks, services, configs, services, logs]
	// nearest top-level from end = configs (index 2)
	// visible = [configs, services, logs] — fits in 3
	result := RenderBreadcrumbs([]string{"stacks", "services", "configs", "services", "logs"}, 3)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "configs")
	assert.Contains(t, result, "services")
	assert.Contains(t, result, "logs")
	// "stacks" should be trimmed away
	assert.NotContains(t, result, "stacks")
}

func TestRenderBreadcrumbs_CapsAt3(t *testing.T) {
	// [stacks, a, b, c, d] — top-level = stacks, visible = [stacks, a, b, c, d] (5 items)
	// capped to last 3: [b, c, d] with ellipsis
	result := RenderBreadcrumbs([]string{"stacks", "a", "b", "c", "d"}, 3)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "…")
	assert.Contains(t, result, "b")
	assert.Contains(t, result, "c")
	assert.Contains(t, result, "d")
	assert.NotContains(t, result, "stacks")
}

func TestRenderBreadcrumbs_NoTopLevelInStack(t *testing.T) {
	// No top-level in history → start from beginning, cap at 3
	result := RenderBreadcrumbs([]string{"services", "tasks", "inspect", "logs"}, 3)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "…")
	assert.Contains(t, result, "tasks")
	assert.Contains(t, result, "inspect")
	assert.Contains(t, result, "logs")
}

// Each piece of the bar goes back to the view it names; "…" to the nearest view
// it hides; arrows and the current view nowhere.
func TestBreadcrumbTargets(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  []int
	}{
		{[]string{"stacks"}, []int{-1}},
		{[]string{"stacks", "services", "tasks"}, []int{0, -1, 1, -1, -1}},
		{[]string{"stacks", "services", "tasks", "inspect", "logs"}, []int{1, -1, 2, -1, 3, -1, -1}},
		{[]string{"stacks", "services", "configs", "services", "logs"}, []int{2, -1, 3, -1, -1}},
		{[]string{"services", "tasks", "inspect", "logs"}, []int{0, -1, 1, -1, 2, -1, -1}},
	} {
		var got []int
		for _, c := range breadcrumbs(tc.names, 3) {
			got = append(got, c.target)
		}
		assert.Equal(t, tc.want, got, "%v", tc.names)
	}
}
