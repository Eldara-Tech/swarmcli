// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/ui/dialog"

	"github.com/charmbracelet/lipgloss"

	"github.com/charmbracelet/x/ansi"
	"github.com/docker/docker/api/types/swarm"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

func TestView_NotVisible_Empty(t *testing.T) {
	m := testModel()
	m.Visible = false
	require.Equal(t, "", m.View())
}

func TestView_WithStacks_ShowsTitle(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("alpha", "beta"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	out := ansi.Strip(m.View())
	require.Contains(t, out, "Stacks(all)[2]")
}

func TestView_ActiveFilter_TitleReflectsFilteredCount(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("alpha", "beta"))
	m.ApplySearchQuery("alp") // matches "alpha" only
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	out := ansi.Strip(m.View())
	require.Contains(t, out, "Stacks(all)[1]") // count is the filtered row count
	require.Contains(t, out, "</alp>")         // active filter appended, k9s-style
}

func TestView_ShowsStackNames(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("webstack", "apistack"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	out := m.View()
	require.Contains(t, out, "apistack")
	require.Contains(t, out, "webstack")
}

func TestView_CreateDialog_Source(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	m.createDialogActive = true
	m.createDialogStep = "source"
	out := m.View()
	require.Contains(t, out, "Create Stack")
	require.Contains(t, out, "compose file")
}

func TestView_CreateDialog_DetailsFile(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	out := m.View()
	require.Contains(t, out, "Compose File")
}

func TestView_CreateDialog_DetailsInline(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	out := m.View()
	require.Contains(t, out, "Inline Editor")
}

func TestView_ConfirmDialog(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	m.confirmDialog.Visible = true
	m.confirmDialog.Message = "Remove stack?"
	out := m.View()
	require.Contains(t, out, "Remove stack?")
}

func TestView_ExpandedStackShowsTasks(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("mystack"))
	m.expandedStacks["mystack"] = true
	m.stackTasks["mystack"] = []docker.TaskEntry{
		{Name: "mystack_web.1", NodeName: "node1", DesiredState: "running", CurrentState: "running"},
	}
	m.setRenderItem()
	m.List.Viewport.Width = 120
	m.List.Viewport.Height = 20
	out := m.View()
	require.Contains(t, out, "mystack_web.1")
	require.Contains(t, out, "node1")
}

// The tint rule itself is tested in views/taskutil; what this asserts is that
// both task-row branches here — the expanded stack under the cursor and one
// merely expanded — go through it, so a replica swarm stopped on purpose does
// not read like a crash (issue #601).
func TestView_ExpandedStackTintsTasksByState(t *testing.T) {
	trueColour(t)
	m := testModel()
	loadStacks(m, fakeStacks("mystack"))
	m.expandedStacks["mystack"] = true
	m.stackTasks["mystack"] = []docker.TaskEntry{
		{Name: "mystack_web.1", NodeName: "node-up", DesiredState: "running", State: "running",
			CurrentState: "running 18 minutes ago"},
		{Name: "mystack_web.2", NodeName: "node-gone", DesiredState: "shutdown", State: "shutdown",
			CurrentState: "shutdown 11 minutes ago"},
		{Name: "mystack_web.3", NodeName: "node-bad", DesiredState: "shutdown", State: "failed",
			CurrentState: "failed 19 minutes ago", Error: "task: non-zero exit (1)"},
	}
	m.setRenderItem()
	m.List.Viewport.Width = 120

	for _, underCursor := range []bool{false, true} {
		out := m.List.RenderItem(m.List.Filtered[0], underCursor, 0)
		require.Equal(t, fgSeq(lipgloss.Color("7")), rowTint(t, out, "node-up"), "under cursor: %v", underCursor)
		require.Equal(t, fgSeq(lipgloss.Color("3")), rowTint(t, out, "node-gone"), "under cursor: %v", underCursor)
		require.Equal(t, fgSeq(lipgloss.Color("9")), rowTint(t, out, "node-bad"), "under cursor: %v", underCursor)
	}
}

// trueColour makes a test's assertions run against the tinted rows: the default
// profile under `go test` is Ascii, where every style renders as plain text.
func trueColour(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// rowTint returns the opening SGR sequence of the rendered row carrying marker.
func rowTint(t *testing.T, out, marker string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(ansi.Strip(line), marker) {
			return sgrPrefix.FindString(line)
		}
	}
	require.FailNowf(t, "no rendered row", "no row containing %q", marker)
	return ""
}

var sgrPrefix = regexp.MustCompile(`^\x1b\[[0-9;]*m`)

// fgSeq is the SGR sequence a foreground colour renders as, so a test names the
// colour it expects rather than an escape sequence.
func fgSeq(c lipgloss.Color) string {
	return strings.SplitN(lipgloss.NewStyle().Foreground(c).Render("x"), "x", 2)[0]
}

func TestView_SaveDialog(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	m.saveDialogActive = true
	m.saveStackName = "mystack"
	m.saveFileInput.SetValue("mystack.yml")
	out := m.View()
	require.Contains(t, out, "Save Stack")
}

func TestView_ErrorColumnHeader(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.stackHasError["s1"] = true
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	out := m.View()
	require.Contains(t, out, "ERROR: 1")
}

// --- #525: the browse hint advertises the chord, from any focus ---

func TestRenderCreateDialog_ShowsBrowseHintAtEveryFocus(t *testing.T) {
	for _, focus := range []int{0, 1} {
		m := testModel()
		m.createDialogActive = true
		m.createDialogStep = "details-file"
		m.createInputFocus = focus
		out := ansi.Strip(m.renderCreateDialog())
		require.Contains(t, out, dialog.BrowseHint, "focus %d must show the browse hint", focus)
		require.Contains(t, out, dialog.BrowseHelpKey)
		require.NotContains(t, out, "[f: Browse]")
		require.NotContains(t, out, "<f>")
	}
}

func TestRenderSaveDialog_ShowsBrowseHint(t *testing.T) {
	m := testModel()
	m.saveDialogActive = true
	m.saveStackName = "mystack"
	out := ansi.Strip(m.renderSaveDialog())
	require.Contains(t, out, dialog.BrowseHint)
	require.Contains(t, out, dialog.BrowseHelpKey)
	require.NotContains(t, out, "[f: Browse]")
}

// The hint used to be padded to a fixed width so the dialog would not resize
// between focuses. It is unconditional now, so the width must hold on its own.
func TestRenderCreateDialog_WidthIsStableAcrossFocus(t *testing.T) {
	width := func(focus int) int {
		m := testModel()
		m.createDialogActive = true
		m.createDialogStep = "details-file"
		m.createInputFocus = focus
		return widestLine(m.renderCreateDialog())
	}
	require.Equal(t, width(0), width(1))
}

// widestLine returns the widest rendered line, in cells.
func widestLine(s string) int {
	max := 0
	for _, line := range strings.Split(ansi.Strip(s), "\n") {
		if w := lipgloss.Width(line); w > max {
			max = w
		}
	}
	return max
}

func TestView_Deploying_ShowsFooterStatus(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("alpha", "beta"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	m.beginDeploy("alpha")

	out := ansi.Strip(m.View())
	require.Contains(t, out, `Deploying stack "alpha"`)
	require.Contains(t, out, "0s")
	require.NotContains(t, out, "Stack 1 of", "the deploy status replaces the row counter")
}

func TestView_Deploying_ShowsElapsed(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("alpha"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	m.beginDeploy("alpha")
	m.deployStartedAt = time.Now().Add(-90 * time.Second)

	require.Contains(t, ansi.Strip(m.View()), "1m30s")
}

func TestView_Toast_ShownThenExpires(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("alpha"))
	m.setRenderItem()
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20

	m.showToast("✓ Stack \"alpha\" deployed")
	require.Contains(t, ansi.Strip(m.View()), "deployed")

	m.toastUntil = time.Now().Add(-time.Second)
	out := ansi.Strip(m.View())
	require.NotContains(t, out, "deployed")
	require.Contains(t, out, "Stack 1 of")
	require.Equal(t, "", m.toastMessage, "an expired toast is cleared")
}

// A stack with a converging service and none failing reads amber; one with a
// failing service reads red even though another of its services converges.
func TestView_ConvergingStackIsAmber(t *testing.T) {
	trueColour(t)
	one, two := uint64(1), uint64(2)
	svc := func(id, stack string, replicas *uint64) swarm.Service {
		return swarm.Service{ID: id, Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{Name: id, Labels: map[string]string{"com.docker.stack.namespace": stack}},
			Mode:        swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: replicas}},
		}}
	}
	task := func(svcID string, slot int, state swarm.TaskState, errText string) swarm.Task {
		return swarm.Task{ServiceID: svcID, Slot: slot, NodeID: "n1", DesiredState: swarm.TaskStateRunning,
			Meta:   swarm.Meta{CreatedAt: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)},
			Status: swarm.TaskStatus{State: state, Err: errText}}
	}
	snap := &docker.SwarmSnapshot{
		Nodes: []swarm.Node{{ID: "n1", Status: swarm.NodeStatus{State: swarm.NodeStateReady}}},
		Services: []swarm.Service{
			svc("calm_web", "calm", &one),
			svc("rolling_api", "rolling", &two),
			svc("broken_api", "broken", &two), svc("broken_bad", "broken", &one),
		},
		Tasks: []swarm.Task{
			task("calm_web", 1, swarm.TaskStateRunning, ""),
			task("rolling_api", 1, swarm.TaskStateRunning, ""),
			task("rolling_api", 2, swarm.TaskStateStarting, ""),
			task("broken_api", 1, swarm.TaskStateStarting, ""),
			task("broken_bad", 1, swarm.TaskStateFailed, "task: non-zero exit (1)"),
		},
	}
	m := stacksModelWithSnapshot(snap)
	loadStacks(m, fakeStacks("calm", "rolling", "broken"))
	m.List.Viewport.Width = 120

	want := map[string]lipgloss.Color{"calm": "117", "rolling": "3", "broken": "9"}
	for _, s := range m.List.Filtered {
		require.Equal(t, fgSeq(want[s.Name]), rowTint(t, m.List.RenderItem(s, false, 0), s.Name), s.Name)
	}
	require.True(t, m.stackConverging["rolling"])
	require.False(t, m.stackConverging["broken"], "failing wins over converging")
	require.True(t, m.HasWarnings())
}

func TestHasWarnings_Default(t *testing.T) {
	require.False(t, testModel().HasWarnings())
}
