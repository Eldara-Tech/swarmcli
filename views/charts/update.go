// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package chartsview

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/core/primitives/hash"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	servicesview "github.com/Eldara-Tech/swarmcli/v2/views/services"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
)

func formatCreated(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04")
}

// minDetailWidth is the surplus, over what the other columns need, below which
// DETAIL is not worth showing: narrower than this it would win its width by
// truncating NAME and CHART, and a truncated explanation next to a truncated
// name is worse than no explanation.
const minDetailWidth = 28

// minOwnerWidth is the extra surplus, on top of DETAIL's, that buys the OWNER
// column. Owner ids are long ("swarmcli-cd/default/hello"), so a terminal that
// cannot spare this much is better off without it — and the margin over a
// typical id is deliberate, since the column arrives beside DETAIL and taking
// its width back off the explanation is what this gate exists to prevent.
const minOwnerWidth = 32

// buildColumns declares the release table for the shared content-aware layout.
//
// The set is responsive. A terminal with surplus width gets a DETAIL column,
// which carries the one genuinely long, variable-length thing a release has to
// say — so the table fills the width with information first, and only shares out
// what is left over that. A terminal without that surplus gets the compact set
// instead.
func (m *Model) buildColumns() []filterlist.Column[releaseItem] {
	base := m.baseColumns(false)
	width := m.list.Viewport.Width
	if width <= 0 {
		width = m.width
	}
	if width <= 0 {
		return base
	}
	surplus := width - filterlist.NaturalWidth(base, m.list.Items, m.sortColumnIndex())
	if surplus < minDetailWidth {
		return base
	}

	cols := m.baseColumns(true)
	// A second tier: with room for both, the owner says whether a release is
	// managed by `charts apply` or by the controller, or was installed by hand,
	// which is the next most useful thing after why it is not converged.
	if surplus >= minDetailWidth+minOwnerWidth {
		cols = append(cols, filterlist.Column[releaseItem]{
			Label: "OWNER", MinWidth: 5,
			// Elastic, for two things that arrive together: an id wider than
			// its cell can then be read with ←/→, and between OWNER and DETAIL
			// a narrowing terminal eats into the owner and the explanation
			// rather than into the release's identity — a long stamp used to
			// take its width out of NAME and CHART instead.
			Flex: true,
			Cell: func(r releaseItem) string { return ownerCell(r.current().Owner, r.Name) },
		})
	}
	return append(cols, filterlist.Column[releaseItem]{
		Label: "DETAIL", MinWidth: 6,
		// Elastic, so that — with OWNER, the only other column allowed to
		// truncate — a narrowing terminal eats into the explanation rather than
		// into the release's name, and a wide one spends its leftover on the two
		// columns at the end rather than on the identity columns at the front.
		Flex: true,
		Cell: func(r releaseItem) string { return r.detail() },
	})
}

// baseColumns is the set every width shows. withDetail moves the elasticity to
// the DETAIL and OWNER columns that follow: without them, NAME and CHART are
// what gives up width first, scrolls when truncated, and shares any surplus.
func (m *Model) baseColumns(withDetail bool) []filterlist.Column[releaseItem] {
	flex := !withDetail
	return []filterlist.Column[releaseItem]{
		{Label: "NAME", MinWidth: 4, Flex: flex, Cell: func(r releaseItem) string { return r.Name }},
		{Label: "REV", MinWidth: 3, Cell: func(r releaseItem) string { return strconv.Itoa(r.revision()) }},
		{Label: "STATUS", MinWidth: 6, Cell: func(r releaseItem) string { return displayOrDash(r.status()) }},
		{Label: "HEALTH", MinWidth: 6, Cell: func(r releaseItem) string { return r.healthLabel() }},
		{Label: "CHART", MinWidth: 5, Flex: flex, Cell: func(r releaseItem) string { return r.chartRef() }},
		// Next to CHART, because it is the version that chart could be at.
		{Label: "LATEST", MinWidth: 6, Cell: m.updCell},
		{Label: "UPDATED", MinWidth: 16, Cell: func(r releaseItem) string { return formatCreated(r.Created) }},
	}
}

// hasDetailColumn reports whether the current width earned the DETAIL column.
// The footer carries the convergence reason only when it does not, so the same
// sentence is never on screen twice.
func (m *Model) hasDetailColumn() bool {
	cols := m.list.Columns
	return len(cols) > 0 && cols[len(cols)-1].Label == "DETAIL"
}

// refreshColumns rebuilds the column set and the header from it. Both the
// terminal width and the widest cell decide whether DETAIL fits, so this runs
// on a resize and on every load, not once at construction.
func (m *Model) refreshColumns() {
	cols := m.buildColumns()
	m.list.Columns = cols
	if m.list.Header != nil {
		m.list.Header.Columns = filterlist.ColumnDefs(cols)
	}
}

// updCell fills the LATEST column. Four answers, because "no newer version" is
// three different facts and a reader acts on each differently: the release is on
// the newest published version, or its chart is in no index to compare against,
// or this machine has no cached index at all. The last two are "—" and "?"
// rather than footer lines because they are per-row facts, and the footer is
// already carrying the counts, the convergence reason and the read-only hint.
func (m *Model) updCell(r releaseItem) string {
	switch {
	case !m.haveIndexes:
		return "?"
	case r.Latest == "":
		return "—" // the chart is in no cached index: a local chart
	case r.Newer:
		return r.Latest
	default:
		return "✓"
	}
}

// sortColumnIndex maps the active sort field to its column index for the header
// sort arrow. Every column is sortable, so the mapping is 1:1.
func (m *Model) sortColumnIndex() int {
	switch m.sortField {
	case SortByName:
		return 0
	case SortByRevision:
		return 1
	case SortByStatus:
		return 2
	case SortByHealth:
		return 3
	case SortByChart:
		return 4
	case SortByUpdated:
		return 6
	}
	return 0
}

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case SpinnerTickMsg:
		// Only while there is a spinner to animate. An unconditional re-arm
		// keeps an 80ms wakeup chain alive for the life of the process, and
		// the app builds a fresh model on every navigation here, so each
		// visit would leave another one running.
		if m.state != stateLoading {
			return nil
		}
		m.spinner++
		m.list.Viewport.SetContent(m.list.View())
		return spinnerTickCmd()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.Viewport.Width = msg.Width
		m.list.Viewport.Height = msg.Height
		m.list.SetOuterSize(msg.Width, msg.Height)
		m.refreshColumns()
		if m.firstResize {
			m.list.Viewport.YOffset = 0
			m.firstResize = false
		} else if m.list.Cursor == 0 {
			m.list.Viewport.YOffset = 0
		}
		return nil

	case ReleasesLoadedMsg:
		return m.handleReleasesLoaded(msg)

	case TickMsg:
		if msg.Gen != m.pollGen {
			return nil // a leftover from an earlier entry — see OnEnter
		}
		m.tickScheduled = false
		// stateError is deliberately allowed through: one unreadable release
		// record fails the whole listing, so a view that stopped polling on
		// the first failure would stay blank until the operator navigated out
		// and back in. stateLoading is not, because a load is already in
		// flight.
		if m.visible && !m.errorDialogActive && m.state != stateLoading {
			// No re-arm here — the poll's result does it, so two polls can
			// never be in flight at once.
			return m.checkReleasesCmd(m.lastSnapshot)
		}
		return m.armTick()

	case PollRetryMsg:
		return m.armTick()

	case tea.KeyMsg:
		if m.errorDialogActive {
			if msg.String() == "enter" || msg.String() == "esc" {
				m.errorDialogActive = false
			}
			return nil
		}
		if m.confirmDialog.Visible {
			return m.confirmDialog.Update(msg)
		}
		return m.handleNormalKeys(msg)

	case confirmdialog.ResultMsg:
		m.confirmDialog.Visible = false
		m.confirmDialog.InfoMode = false
		return nil
	}
	return nil
}

func (m *Model) handleReleasesLoaded(msg ReleasesLoadedMsg) tea.Cmd {
	if msg.Err != nil {
		// A single unreadable release record fails the whole listing, so a
		// transient or one-off decode failure must not empty a view that
		// already has good data — keep it and say the refresh failed.
		if m.state == stateReady && len(m.list.Items) > 0 {
			l().Warnf("ChartsView: background refresh failed: %v", msg.Err)
			m.showToast("Refresh failed (will retry)")
			// Cleared here too: a failed load consumes the request just as a
			// successful one does, and leaving it set would snap the cursor to
			// the top on some later, unrelated poll.
			m.resetCursorOnNextLoad = false
			return m.armTick()
		}
		m.state = stateError
		m.err = msg.Err
		m.errorDialogActive = true
		m.resetCursorOnNextLoad = false
		return m.armTick()
	}

	selectedName := ""
	if !m.resetCursorOnNextLoad {
		if sel, ok := m.selected(); ok {
			selectedName = sel.Name
		}
	}

	m.haveIndexes = msg.HaveIndexes

	if h, err := hash.Compute(stableReleases(msg.Releases)); err == nil {
		m.lastSnapshot = h
	} else {
		l().Errorf("ChartsView: Error computing hash: %v", err)
	}

	m.list.Items = msg.Releases
	m.list.ApplyFilter()
	// The widest cell decides whether DETAIL fits, so the column set is
	// reconsidered whenever the data changes, not only on a resize.
	m.refreshColumns()
	m.applySorting()

	if m.resetCursorOnNextLoad {
		m.list.Cursor = 0
		m.list.Viewport.YOffset = 0
		m.resetCursorOnNextLoad = false
	} else if selectedName != "" {
		for i, r := range m.list.Filtered {
			if r.Name == selectedName {
				m.list.Cursor = i
				break
			}
		}
	}

	m.applyPendingSelect()
	m.clampChild()
	m.pruneExpanded()
	m.state = stateReady
	m.err = nil
	m.list.Viewport.SetContent(m.list.View())
	return m.armTick()
}

// pruneExpanded drops expansion state for releases that are no longer listed,
// so an uninstalled release that is later reinstalled does not come back
// already open, and the map does not grow for the life of the view.
func (m *Model) pruneExpanded() {
	if len(m.expanded) == 0 {
		return
	}
	live := make(map[string]struct{}, len(m.list.Items))
	for _, it := range m.list.Items {
		live[it.Name] = struct{}{}
	}
	for name := range m.expanded {
		if _, ok := live[name]; !ok {
			delete(m.expanded, name)
		}
	}
}

// applyPendingSelect honours a cross-link's requested release once there is a
// list to select it in.
//
// It selects and expands rather than filtering: the "/" filter belongs to the
// app's search bar, and setting a query the operator never typed would leave
// the list narrowed with nothing on screen saying why.
func (m *Model) applyPendingSelect() {
	if m.pendingSelect == "" {
		return
	}
	for i, r := range m.list.Filtered {
		if r.Name == m.pendingSelect {
			m.list.Cursor = i
			m.expanded[r.Name] = true
			m.childIndex = noChild
			m.pendingSelect = ""
			return
		}
	}
	// Not found. Give up only once there was a real list to look in — the
	// first read can land empty — because retrying forever would fight the
	// operator's cursor on every poll.
	if len(m.list.Filtered) > 0 {
		m.pendingSelect = ""
	}
}

// clampChild keeps the child selection inside the release under the cursor. A
// reload can land on a release with fewer revisions than the last one had, and
// a filter or sort can move the cursor to a different release entirely.
func (m *Model) clampChild() {
	if m.childIndex == noChild {
		return
	}
	sel, ok := m.selected()
	if !ok || !m.expanded[sel.Name] {
		m.childIndex = noChild
		return
	}
	if n := len(sel.children()); m.childIndex >= n {
		m.childIndex = n - 1
	}
}

func (m *Model) handleNormalKeys(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "N":
		m.toggleSort(SortByName)
	case "R":
		m.toggleSort(SortByRevision)
	case "S":
		m.toggleSort(SortByStatus)
	case "H":
		m.toggleSort(SortByHealth)
	case "C":
		m.toggleSort(SortByChart)
	case "U":
		m.toggleSort(SortByUpdated)
	case "left":
		m.list.ScrollLeft()
		m.list.Viewport.SetContent(m.list.View())
	case "right":
		m.list.ScrollRight()
		m.list.Viewport.SetContent(m.list.View())
	case "up", "k":
		m.moveUp()
	case "down", "j":
		m.moveDown()
	case "pgup":
		m.movePage(-1)
	case "pgdown":
		m.movePage(1)
	case "esc":
		// Only reached because IsRowExpanded told the app to forward it.
		m.collapseOneLevel()
	case "enter":
		return m.handleEnter()
	case "i":
		if rev, ok := m.selectedRevision(); ok {
			sel, _ := m.selected()
			return m.inspectRevisionCmd(sel.Name, rev)
		}
	case "v":
		if rev, ok := m.selectedRevision(); ok {
			sel, _ := m.selected()
			return m.inspectValuesCmd(sel.Name, rev)
		}
	case "d":
		if child, ok := m.selectedChild(); ok && child.kind == childRevision {
			sel, _ := m.selected()
			return m.diffRevisionCmd(sel.Name, child)
		}
	case "s":
		if sel, ok := m.selected(); ok {
			return servicesForStackCmd(sel.Name, "")
		}
	case "u":
		if sel, ok := m.selected(); ok {
			m.showBlocked(upgradeAction(sel.Name))
		}
	case "r":
		if sel, ok := m.selected(); ok {
			m.showBlocked(rollbackAction(sel.Name, sel.revision()))
		}
	case "ctrl+d":
		if sel, ok := m.selected(); ok {
			m.showBlocked(uninstallAction(sel.Name))
		}
	}
	return nil
}

// selectedRevision is the revision `i` and `v` act on: the one under the cursor
// when a revision child is selected, otherwise the release's current revision.
func (m *Model) selectedRevision() (charts.Release, bool) {
	sel, ok := m.selected()
	if !ok {
		return charts.Release{}, false
	}
	if child, isChild := m.selectedChild(); isChild {
		if child.kind != childRevision {
			return charts.Release{}, false
		}
		return child.rev, true
	}
	return sel.current(), true
}

// handleEnter expands or collapses the release, unless a service child is
// selected — from there it drills into that service.
func (m *Model) handleEnter() tea.Cmd {
	sel, ok := m.selected()
	if !ok {
		return nil
	}
	if child, isChild := m.selectedChild(); isChild {
		if child.kind == childService {
			return servicesForStackCmd(sel.Name, child.svc.Name)
		}
		return nil
	}
	m.expanded[sel.Name] = !m.expanded[sel.Name]
	m.childIndex = noChild
	m.list.Viewport.SetContent(m.list.View())
	return nil
}

// servicesForStackCmd opens the services view scoped to the release's stack. A
// release deploys a stack named after itself, so the services view's existing
// stack scope is the right drill-down, and scale, restart and logs stay in the
// view that owns them.
func servicesForStackCmd(release, selectService string) tea.Cmd {
	payload := map[string]any{"stackName": release}
	if selectService != "" {
		payload["selectServiceName"] = selectService
	}
	return func() tea.Msg {
		return view.NavigateToMsg{ViewName: servicesview.ViewName, Payload: payload}
	}
}

// collapseOneLevel walks esc back out: from a child to the release row, then
// from an expanded release to a collapsed one.
func (m *Model) collapseOneLevel() {
	if m.childIndex != noChild {
		m.childIndex = noChild
		m.list.Viewport.SetContent(m.list.View())
		return
	}
	if sel, ok := m.selected(); ok {
		delete(m.expanded, sel.Name)
		m.list.Viewport.SetContent(m.list.View())
	}
}

// moveDown steps into an expanded release's children before moving on to the
// next release.
func (m *Model) moveDown() {
	if sel, ok := m.selected(); ok && m.expanded[sel.Name] {
		if n := len(sel.children()); m.childIndex < n-1 {
			m.childIndex++
			m.list.Viewport.SetContent(m.list.View())
			return
		}
	}
	if m.list.Cursor < len(m.list.Filtered)-1 {
		m.list.Cursor++
		m.childIndex = noChild
		m.list.ResetColumnScroll()
		m.list.Viewport.SetContent(m.list.View())
	}
}

// moveUp is moveDown's mirror: out of the children to the release row, then to
// the previous release — landing on its last child when it is expanded, so the
// two directions traverse the same sequence of rows.
func (m *Model) moveUp() {
	if m.childIndex != noChild {
		m.childIndex--
		m.list.Viewport.SetContent(m.list.View())
		return
	}
	if m.list.Cursor > 0 {
		m.list.Cursor--
		m.childIndex = noChild
		if sel, ok := m.selected(); ok && m.expanded[sel.Name] {
			m.childIndex = len(sel.children()) - 1
		}
		m.list.ResetColumnScroll()
		m.list.Viewport.SetContent(m.list.View())
	}
}

// ClickRow selects the release drawn on a content line (view.RowClicker). A
// click anywhere in an expanded release — a revision, a service or one of their
// header lines — selects the release itself and drops the child selection;
// children are picked with the keys.
func (m *Model) ClickRow(line int) bool {
	oldCursor := m.list.Cursor
	if !m.list.SelectLine(line) {
		return false
	}
	m.childIndex = noChild
	if oldCursor != m.list.Cursor {
		m.list.ResetColumnScroll()
	}
	m.list.Viewport.SetContent(m.list.View())
	return true
}

// movePage advances the cursor by one screenful of RENDERED LINES.
//
// Counting items instead would page past several screens whenever a release is
// expanded — one item can be twenty lines — and counting the viewport height
// instead of the content region would overshoot by the frame, header and
// footer rows on every press.
func (m *Model) movePage(dir int) {
	if len(m.list.Filtered) == 0 {
		return
	}
	budget := m.contentLines()
	i := m.list.Cursor
	// The row the cursor is on occupies the screen too, so an expanded release
	// under the cursor shortens the page rather than being travelled over for
	// free.
	used := m.itemLineCount(m.list.Filtered[i])
	for {
		next := i + dir
		if next < 0 || next >= len(m.list.Filtered) {
			break
		}
		used += m.itemLineCount(m.list.Filtered[next])
		if used > budget {
			break
		}
		i = next
	}
	// Always move at least one row, so a release taller than the screen does
	// not make the key do nothing at all.
	if i == m.list.Cursor {
		i = m.list.Cursor + dir
	}
	if i < 0 {
		i = 0
	}
	if i >= len(m.list.Filtered) {
		i = len(m.list.Filtered) - 1
	}
	if i < 0 {
		i = 0
	}
	m.list.Cursor = i
	m.childIndex = noChild
	m.list.ResetColumnScroll()
	m.list.Viewport.SetContent(m.list.View())
}

func (m *Model) toggleSort(field SortField) {
	if m.sortField == field {
		m.sortAscending = !m.sortAscending
	} else {
		m.sortField = field
		m.sortAscending = true
	}
	m.applySorting()
}

func cmpStr(a, b string, asc bool) bool {
	if asc {
		return a < b
	}
	return a > b
}

func cmpInt(a, b int, asc bool) bool {
	if asc {
		return a < b
	}
	return a > b
}

func (m *Model) applySorting() {
	if len(m.list.Filtered) == 0 {
		return
	}
	cursorName := ""
	if sel, ok := m.selected(); ok {
		cursorName = sel.Name
	}

	f := m.list.Filtered
	asc := m.sortAscending
	byName := func(i, j int) bool { return strings.ToLower(f[i].Name) < strings.ToLower(f[j].Name) }
	switch m.sortField {
	case SortByName:
		sort.SliceStable(f, func(i, j int) bool {
			return cmpStr(strings.ToLower(f[i].Name), strings.ToLower(f[j].Name), asc)
		})
	case SortByRevision:
		sort.SliceStable(f, func(i, j int) bool {
			if f[i].revision() == f[j].revision() {
				return byName(i, j)
			}
			return cmpInt(f[i].revision(), f[j].revision(), asc)
		})
	case SortByStatus:
		sort.SliceStable(f, func(i, j int) bool {
			if f[i].status() == f[j].status() {
				return byName(i, j)
			}
			return cmpStr(f[i].status(), f[j].status(), asc)
		})
	case SortByHealth:
		sort.SliceStable(f, func(i, j int) bool {
			if f[i].healthRank() == f[j].healthRank() {
				return byName(i, j)
			}
			return cmpInt(f[i].healthRank(), f[j].healthRank(), asc)
		})
	case SortByChart:
		sort.SliceStable(f, func(i, j int) bool {
			if f[i].chartRef() == f[j].chartRef() {
				return byName(i, j)
			}
			return cmpStr(strings.ToLower(f[i].chartRef()), strings.ToLower(f[j].chartRef()), asc)
		})
	case SortByUpdated:
		sort.SliceStable(f, func(i, j int) bool {
			if f[i].Created.Equal(f[j].Created) {
				return byName(i, j)
			}
			if asc {
				return f[i].Created.Before(f[j].Created)
			}
			return f[i].Created.After(f[j].Created)
		})
	}

	if cursorName != "" {
		for i, r := range f {
			if r.Name == cursorName {
				m.list.Cursor = i
				break
			}
		}
	}
	m.clampChild()
	m.list.Viewport.SetContent(m.list.View())
}

func (m *Model) setRenderItem() {
	m.list.RenderItem = func(item releaseItem, selected bool, _ int) string {
		row := m.list.RenderRow(item, selected)
		// The release row loses its highlight while a child is selected, so
		// exactly one line on screen reads as the cursor.
		if selected && m.childIndex == noChild {
			row = ui.ListSelectedStyle.Render(row)
		} else {
			row = ui.ListItemStyle.Render(row)
		}
		if !m.expanded[item.Name] {
			return row
		}
		child := noChild
		if selected {
			child = m.childIndex
		}
		lines, _ := expansionBlock(item, child, m.contentWidth())
		return row + "\n" + strings.Join(lines, "\n")
	}
	m.list.Viewport.SetContent(m.list.View())
}
