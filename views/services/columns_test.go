// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package servicesview

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/features"
	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

// healthCell returns the rendered HEALTH cell for e, or "" when the HEALTH
// column is not in the current column set.
func healthCell(m *Model, e docker.ServiceEntry) string {
	for _, c := range m.serviceColumns() {
		if c.isHealth {
			return c.col.Cell(e)
		}
	}
	return ""
}

const longServiceName = "myproject_some-very-long-service-name"

// loadWithFilter pushes entries into the model under a given scope.
func loadWithFilter(m *Model, ft FilterType, entries []docker.ServiceEntry) {
	m.Update(Msg{Scope: "all", Entries: entries, FilterType: ft})
}

func hasColumn(m *Model, label string) bool {
	return colLabelIndex(m, label) >= 0
}

func colLabelIndex(m *Model, label string) int {
	for i, c := range m.layoutColumns() {
		if c.Label == label {
			return i
		}
	}
	return -1
}

func TestColumns_StackOnlyInAllFilter(t *testing.T) {
	// CE default (feature off): the HEALTH column stays visible via its footnote,
	// so it is present in every scope; only STACK varies with the filter.
	features.Disable(serviceHealthFeature)
	view.ServicesHealthHint = nil
	cases := []struct {
		ft        FilterType
		wantStack bool
		wantLen   int
	}{
		{AllFilter, true, 11},
		{StackFilter, false, 10},
		{NodeFilter, false, 10},
		{NoStackFilter, false, 10},
	}
	for _, tc := range cases {
		m := testModel()
		m.filterType = tc.ft
		require.Len(t, m.layoutColumns(), tc.wantLen, "filter %v", tc.ft)
		require.Equal(t, tc.wantStack, hasColumn(m, "STACK"), "filter %v stack presence", tc.ft)
	}
}

func TestColumns_HealthColumnAndFootnote(t *testing.T) {
	// Feature off (CE / unlicensed): a footnote explains the column, so HEALTH is
	// shown with a "*" placeholder rather than dropped.
	features.Disable(serviceHealthFeature)
	view.ServicesHealthHint = nil
	m := testModel()
	loadWithFilter(m, AllFilter, fakeEntries("web", "api"))
	require.True(t, hasColumn(m, "HEALTH"), "HEALTH stays visible with a footnote")
	require.Equal(t, "*", healthCell(m, docker.ServiceEntry{}), "no-health cell shows the footnote asterisk")

	// Feature on + reachable (empty note), no row has health → HEALTH is dropped.
	features.Enable(serviceHealthFeature)
	t.Cleanup(func() { features.Disable(serviceHealthFeature) })
	view.ServicesHealthHint = func() string { return "" }
	t.Cleanup(func() { view.ServicesHealthHint = nil })
	m2 := testModel()
	loadWithFilter(m2, AllFilter, fakeEntries("web", "api"))
	require.False(t, hasColumn(m2, "HEALTH"), "HEALTH hidden when reachable and no row has health")

	// A populated Health summary shows the real value, right after STATUS.
	entries := fakeEntries("web", "api")
	entries[0].Health = "1/1 healthy"
	m3 := testModel()
	loadWithFilter(m3, AllFilter, entries)
	require.True(t, hasColumn(m3, "HEALTH"), "HEALTH appears when a row has health")
	require.Equal(t, "1/1 healthy", healthCell(m3, docker.ServiceEntry{Health: "1/1 healthy"}))
	require.Equal(t, colLabelIndex(m3, "STATUS")+1, colLabelIndex(m3, "HEALTH"),
		"HEALTH should sit immediately after STATUS")
}

// statusCell returns the rendered STATUS cell for e.
func statusCell(m *Model, e docker.ServiceEntry) string {
	for _, c := range m.serviceColumns() {
		if c.col.Label == "STATUS" {
			return c.col.Cell(e)
		}
	}
	return ""
}

// REPLICAS counts every running replica, superseded ones included, so a
// start-first rollout that has not moved a single replica onto the new
// generation still reads 2/2. STATUS is where that gets said (issue #480).
func TestColumns_StatusShowsRolloutProgress(t *testing.T) {
	m := testModel()
	loadWithFilter(m, AllFilter, fakeEntries("web"))

	rollingOut := docker.ServiceEntry{
		Status: "updating", RollingOut: true, ReplicasOnNode: 2, ReplicasTotal: 2, UpToDate: 1,
	}
	require.Equal(t, "updating · 1/2 new", statusCell(m, rollingOut))

	landed := rollingOut
	landed.UpToDate = 2
	require.Equal(t, "updating", statusCell(m, landed), "no counter once every replica is current")

	settled := docker.ServiceEntry{Status: "active", ReplicasOnNode: 1, ReplicasTotal: 2, UpToDate: 1}
	require.Equal(t, "active", statusCell(m, settled), "a restart outside a rollout is not a stale version")

	pulling := rollingOut
	pulling.PullProgress = "pulling · 3/12 layers"
	require.Equal(t, "pulling · 3/12 layers", statusCell(m, pulling), "a pull in flight still owns the cell")

	unknown := docker.ServiceEntry{Status: "updating", RollingOut: true, ReplicasTotal: 0}
	require.Equal(t, "updating", statusCell(m, unknown), "no target to count against")
}

func TestExpandedRow_ContainerStateFallback(t *testing.T) {
	m := testModel()
	loadWithFilter(m, AllFilter, fakeEntries("web"))
	// Expand the service and give it a replica that swarm still calls running
	// while its container has died, with no healthcheck (Health empty). The
	// container's live state must surface as the HEALTH fallback so failures
	// show for images without a HEALTHCHECK.
	m.expandedServices["id-web"] = true
	m.serviceTasks["id-web"] = []docker.TaskEntry{{
		Name: "web.1", NodeName: "node-1", DesiredState: "Running", State: "running",
		CurrentState: "running 8m", ContainerID: "c1", ContainerState: "exited",
	}}
	m.setRenderItem()

	out := m.List.RenderItem(m.List.Filtered[0], false, 0)
	require.Contains(t, out, "HEALTH", "HEALTH column must appear when a replica carries container state")
	require.Contains(t, out, "exited", "the container's live state must render as the HEALTH fallback")
}

// The tint rule itself is tested in views/taskutil; what this asserts is that
// the expanded rows go through it, that a replica swarm stopped on purpose no
// longer reads like the one that crashed next to it (issue #601), and that a
// task which ran to completion reads like an ordinary row whether or not its
// container is still on the node to report "exited" (issue #613).
func TestExpandedRow_TintsByTaskState(t *testing.T) {
	trueColour(t)
	m := testModel()
	loadWithFilter(m, AllFilter, fakeEntries("web"))
	m.expandedServices["id-web"] = true
	m.serviceTasks["id-web"] = []docker.TaskEntry{
		{Name: "web.1", NodeName: "node-up", DesiredState: "Running", State: "running",
			CurrentState: "running 18 minutes ago", Health: "healthy", ContainerState: "running"},
		{Name: "web.2", NodeName: "node-gone", DesiredState: "Shutdown", State: "shutdown",
			CurrentState: "shutdown 11 minutes ago", ContainerState: "exited"},
		{Name: "web.3", NodeName: "node-bad", DesiredState: "Shutdown", State: "failed",
			CurrentState: "failed 19 minutes ago", ContainerState: "exited", Error: "task: non-zero exit (1)"},
		{Name: "web.4", NodeName: "node-done", DesiredState: "Shutdown", State: "complete",
			CurrentState: "complete 44 seconds ago", ContainerState: "exited"},
		{Name: "web.5", NodeName: "node-old", DesiredState: "Shutdown", State: "complete",
			CurrentState: "complete 3 days ago"},
	}
	m.setRenderItem()

	out := m.List.RenderItem(m.List.Filtered[0], false, 0)
	require.Equal(t, fgSeq(lipgloss.Color("7")), rowTint(t, out, "node-up"))
	require.Equal(t, fgSeq(lipgloss.Color("3")), rowTint(t, out, "node-gone"))
	require.Equal(t, fgSeq(lipgloss.Color("9")), rowTint(t, out, "node-bad"))
	// The two rows issue #613 was reported on: same state, and they differ only
	// in whether the node has pruned the container out of the health decorator's
	// inventory. They must not differ in colour.
	require.Equal(t, fgSeq(lipgloss.Color("7")), rowTint(t, out, "node-done"))
	require.Equal(t, fgSeq(lipgloss.Color("7")), rowTint(t, out, "node-old"))
}

// Once a task is over, CURRENT STATE has already said so and the container's
// "exited" adds nothing — worse, whether the cell can say it at all turns on
// whether the node has pruned the container out of the health decorator's
// inventory, which is a difference between rows and not between tasks. With no
// healthcheck verdict left to show, the column goes away rather than filling
// with dashes (issue #616).
func TestExpandedRow_HealthDropsWhenEveryTaskIsOver(t *testing.T) {
	m := testModel()
	loadWithFilter(m, AllFilter, fakeEntries("web"))
	m.expandedServices["id-web"] = true
	m.serviceTasks["id-web"] = []docker.TaskEntry{
		{Name: "web.1", NodeName: "node-done", DesiredState: "Shutdown", State: "complete",
			CurrentState: "complete 44 seconds ago", ContainerState: "exited"},
		{Name: "web.2", NodeName: "node-old", DesiredState: "Shutdown", State: "complete",
			CurrentState: "complete 3 days ago"},
	}
	m.setRenderItem()

	out := m.List.RenderItem(m.List.Filtered[0], false, 0)
	require.NotContains(t, out, "HEALTH", "no task can report health, so the column earns no width")
	require.NotContains(t, out, "exited", "a finished task's container state is not a healthcheck verdict")
}

// The fallback keeps earning its place while a task could still be running:
// there the container's state contradicts the task's, and that is the container
// error #616 was careful not to hide.
func TestExpandedRow_HealthKeepsContainerStateWhileTaskCouldRun(t *testing.T) {
	m := testModel()
	loadWithFilter(m, AllFilter, fakeEntries("web"))
	m.expandedServices["id-web"] = true
	m.serviceTasks["id-web"] = []docker.TaskEntry{
		{Name: "web.1", NodeName: "node-loop", DesiredState: "Running", State: "running",
			CurrentState: "running 2 minutes ago", ContainerState: "exited"},
		{Name: "web.2", NodeName: "node-done", DesiredState: "Shutdown", State: "complete",
			CurrentState: "complete 44 seconds ago", ContainerState: "exited"},
	}
	m.setRenderItem()

	out := m.List.RenderItem(m.List.Filtered[0], false, 0)
	require.Contains(t, out, "HEALTH", "one task with something to report brings the column back")
	require.Contains(t, rowFor(t, out, "node-loop"), "exited",
		"a task swarm calls running whose container has died is the case the fallback exists for")
	require.NotContains(t, rowFor(t, out, "node-done"), "exited",
		"the finished task alongside it still says nothing")
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
	return sgrPrefix.FindString(rowFor(t, out, marker))
}

// rowFor returns the rendered line carrying marker, styling included.
func rowFor(t *testing.T, out, marker string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(ansi.Strip(line), marker) {
			return line
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

func TestFirstNonEmpty(t *testing.T) {
	require.Equal(t, "healthy", firstNonEmpty("healthy", "running"))
	require.Equal(t, "exited", firstNonEmpty("", "exited"))
	require.Equal(t, "", firstNonEmpty("", ""))
}

func TestFormatTaskRow_ConditionalColumns(t *testing.T) {
	cells := taskRow{
		replica: "1", image: "web:v2", node: "node-1", desired: "Running",
		current: "running 8m", health: "healthy", ports: "8000/tcp", errText: "boom",
	}

	// No flags set → the mandatory REPLICA…ERROR row only.
	plain := formatTaskRow(cells, taskRowColumns{})
	require.NotContains(t, plain, "web:v2")
	require.NotContains(t, plain, "healthy")
	require.NotContains(t, plain, "8000/tcp")
	require.Contains(t, plain, "boom")

	// All set → IMAGE sits before NODE, HEALTH and PORTS before ERROR.
	full := formatTaskRow(cells, taskRowColumns{image: true, health: true, ports: true})
	require.Less(t, indexOf(full, "web:v2"), indexOf(full, "node-1"))
	require.Less(t, indexOf(full, "healthy"), indexOf(full, "boom"))
	require.Less(t, indexOf(full, "8000/tcp"), indexOf(full, "boom"))
}

// The reported confusion: three task rows against a target of 2 read as a
// contradiction, because the slot — the one field that says two of them are one
// replica being replaced — was truncated out of the NAME column (issue #480).
func TestExpandedRow_ReplicaAndImageIdentifyGenerations(t *testing.T) {
	m := testModel()
	loadWithFilter(m, AllFilter, fakeEntries("web"))
	m.expandedServices["id-web"] = true
	m.serviceTasks["id-web"] = []docker.TaskEntry{
		{Slot: 1, Image: "registry.example.com/acme/web:v2", NodeName: "node-29",
			DesiredState: "Running", CurrentState: "preparing 26s"},
		{Slot: 2, Image: "registry.example.com/acme/web:v2", NodeName: "node-02",
			DesiredState: "Running", CurrentState: "running 36s"},
		{Slot: 1, Image: "registry.example.com/acme/web:v1", NodeName: "node-03",
			DesiredState: "Running", CurrentState: "running 2 weeks"},
	}
	m.setRenderItem()

	out := m.List.RenderItem(m.List.Filtered[0], false, 0)
	require.Contains(t, out, "REPLICA", "the slot must be a column of its own")
	require.NotContains(t, out, "web.1", "the service name is on the row above, not repeated per task")
	require.Contains(t, out, "IMAGE", "generations differ, so IMAGE earns its width")
	require.Contains(t, out, "web:v1", "the outgoing generation is identifiable")
	require.Contains(t, out, "web:v2")
	require.NotContains(t, out, "registry.example.com", "the registry path is dropped, the tag is not")
}

// A settled service renders no wider than before: every replica agrees on the
// image, so the column would only repeat the service row above it.
func TestExpandedRow_ImageColumnHiddenWhenGenerationsAgree(t *testing.T) {
	m := testModel()
	loadWithFilter(m, AllFilter, fakeEntries("web"))
	m.expandedServices["id-web"] = true
	m.serviceTasks["id-web"] = []docker.TaskEntry{
		{Slot: 1, Image: "acme/web:v2", NodeName: "node-1", DesiredState: "Running", CurrentState: "running 8m"},
		{Slot: 2, Image: "acme/web:v2", NodeName: "node-2", DesiredState: "Running", CurrentState: "running 8m"},
	}
	m.setRenderItem()

	out := m.List.RenderItem(m.List.Filtered[0], false, 0)
	require.NotContains(t, out, "IMAGE")
	require.NotContains(t, out, "web:v2")
}

// A global service's tasks carry no slot; NODE is what tells them apart.
func TestExpandedRow_GlobalServiceHasNoSlot(t *testing.T) {
	require.Equal(t, "—", replicaCell(docker.TaskEntry{Slot: 0}))
	require.Equal(t, "3", replicaCell(docker.TaskEntry{Slot: 3}))
}

func TestShortImage(t *testing.T) {
	require.Equal(t, "frontend:v1.3.23", shortImage("dockerhub.esix.hu/eldara-tech/swarmcli-website/frontend:v1.3.23"))
	require.Equal(t, "repo:tag", shortImage("host:5000/repo:tag"), "a registry port is not a path separator")
	require.Equal(t, "nginx:latest", shortImage("nginx:latest"))
	require.Equal(t, "", shortImage(""))
}

func indexOf(s, sub string) int {
	return strings.Index(s, sub)
}

func TestColWidths_LengthMatchesHeader(t *testing.T) {
	for _, ft := range []FilterType{AllFilter, StackFilter, NodeFilter, NoStackFilter} {
		m := testModel()
		loadWithFilter(m, ft, fakeEntries("web", "api"))
		require.Len(t, m.List.ColWidths(), len(m.List.Header.Columns), "filter %v header sync", ft)
	}
}

func TestColWidths_WideTerminal_ServiceFits(t *testing.T) {
	m := testModel()
	loadWithFilter(m, AllFilter, fakeEntries(longServiceName, "api"))
	m.List.Viewport.Width = 200

	widths := m.List.ColWidths()
	svc := colLabelIndex(m, "SERVICE")
	// Column 0 renders with a leading space and a trailing gap, so its usable
	// content width is width-ColGap-1; it must fit the full name on a wide term.
	require.GreaterOrEqual(t, widths[svc]-filterlist.ColGap-1, len([]rune(longServiceName)),
		"SERVICE column must show the full name on a wide terminal")
}

func TestSortIndicator_ResolvesActiveIndex(t *testing.T) {
	// AllFilter: SERVICE,STACK,REPLICAS,STATUS,... -> STATUS at index 3.
	mAll := testModel()
	mAll.filterType = AllFilter
	mAll.sortField = SortByStatus
	idx, asc := mAll.sortIndicator()
	require.Equal(t, 3, idx)
	require.True(t, asc)

	// StackFilter drops STACK: SERVICE,REPLICAS,STATUS,... -> STATUS at index 2.
	mStack := testModel()
	mStack.filterType = StackFilter
	mStack.sortField = SortByStatus
	mStack.sortAscending = false
	idx, asc = mStack.sortIndicator()
	require.Equal(t, 2, idx)
	require.False(t, asc)
}

func TestView_FullServiceNameOnWideTerminal(t *testing.T) {
	m := testModel()
	loadWithFilter(m, AllFilter, fakeEntries(longServiceName, "api"))
	m.List.Viewport.Width = 200
	m.List.Viewport.Height = 20

	out := m.View()
	require.Contains(t, out, longServiceName, "full name must be visible, not truncated (#392)")
}

func TestView_FullServiceImageOnWideTerminal(t *testing.T) {
	const longImage = "registry.example.com/myproject/some-very-long-image-name:v1.2.3"
	m := testModel()
	entries := fakeEntries("web")
	entries[0].Image = longImage
	loadWithFilter(m, AllFilter, entries)
	m.List.Viewport.Width = 200
	m.List.Viewport.Height = 20

	out := m.View()
	require.Contains(t, out, longImage, "full image must be visible, not truncated")
}

func TestHeaderRowAlignment(t *testing.T) {
	// Header and rows must share the same total width across scopes and sort
	// fields, including the sorted column's " ▲" indicator (the #392 regression).
	for _, ft := range []FilterType{AllFilter, StackFilter} {
		for _, sf := range []SortField{SortByName, SortByStatus, SortByCreated, SortByError} {
			m := testModel()
			loadWithFilter(m, ft, fakeEntries(longServiceName, "api"))
			m.sortField = sf
			m.setRenderItem()
			for _, w := range []int{40, 120, 200} {
				m.List.Viewport.Width = w
				header := m.List.RenderHeader()
				row := m.List.RenderItem(m.List.Filtered[0], false, 0)
				require.Equal(t, lipgloss.Width(header), lipgloss.Width(row),
					"filter=%v sort=%v width=%d header/row width mismatch", ft, sf, w)
			}
		}
	}
}

// colWidth reports the laid-out width of the named column at totalWidth, and
// the width at which the table is neither stretched nor squeezed — the baseline
// a wider terminal has to be compared against.
func colWidth(t *testing.T, m *Model, label string, totalWidth int) (width, natural int) {
	t.Helper()
	cols := m.layoutColumns()
	sortCol, _ := m.sortIndicator()
	natural = filterlist.NaturalWidth(cols, m.List.Items, sortCol)
	widths := filterlist.LayoutWidths(cols, m.List.Items, totalWidth, sortCol)
	for i, c := range cols {
		if c.Label == label {
			return widths[i], natural
		}
	}
	t.Fatalf("no %s column", label)
	return 0, 0
}

// A wide terminal must be filled, not left half dead.
//
// Confining the leftover to the trailing column was tried first, so the columns
// before it would stop drifting apart. ERROR is that column here, and it is
// empty on a swarm with nothing wrong: on a 360-column terminal the table needs
// 177 and the remaining 183 cells went into a cell with nothing in them, so
// every service packed into the left half. The elastic columns share it instead.
func TestWideTerminalFillsTheColumnsAnOperatorReads(t *testing.T) {
	m := testModel()
	loadServices(m, fakeEntries("alpha", "beta"))

	for _, label := range []string{"SERVICE", "IMAGE", "PORTS"} {
		wide, natural := colWidth(t, m, label, 300)
		require.Less(t, natural, 300, "the fixture must leave a wide terminal something to spend")
		at, _ := colWidth(t, m, label, natural)
		require.Greater(t, wide, at, "%s must take a share of the leftover", label)
	}
}
