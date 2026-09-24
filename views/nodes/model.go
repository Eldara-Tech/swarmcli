// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package nodesview

import (
	"github.com/Eldara-Tech/swarmcli/v2/core/primitives/hash"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/helpbar"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type SortField int

const (
	SortByHostname SortField = iota
	SortByState
	SortByAvailability
	SortByRole
	SortByVersion
	SortByAddress
	SortByLabels
)

type Model struct {
	deps                  docker.Deps
	List                  filterlist.FilterableList[docker.NodeEntry]
	Visible               bool
	ready                 bool
	firstResize           bool // tracks if we've received the first window size
	width                 int
	height                int
	sortField             SortField
	sortAscending         bool   // true for ascending, false for descending
	lastSnapshot          uint64 // Hash of last node state for change detection
	pollGen               uint64 // Generation of the live poll chain; see OnEnter
	confirmDialog         *confirmdialog.Model
	errorDialogActive     bool
	availabilityDialog    bool     // Whether availability selection dialog is visible
	availabilityNodeID    string   // Node ID for availability change
	availabilitySelection int      // Currently selected option (0=active, 1=pause, 2=drain)
	labelInputDialog      bool     // Whether label input dialog is visible
	labelInputNodeID      string   // Node ID for label add
	labelInputValue       string   // Current input value for label (key=value format)
	labelRemoveDialog     bool     // Whether label remove dialog is visible
	labelRemoveNodeID     string   // Node ID for label remove
	labelRemoveSelection  int      // Currently selected label to remove
	labelRemoveLabels     []string // List of "key=value" strings
}

func New(width, height int) *Model {
	vp := viewport.New(width, height)
	vp.SetContent("")

	m := &Model{
		Visible:       false,
		firstResize:   true,
		width:         width,
		height:        height,
		confirmDialog: confirmdialog.New(width, height),
		sortField:     SortByHostname,
		sortAscending: true,
	}

	cols := m.buildColumns()
	list := filterlist.FilterableList[docker.NodeEntry]{
		Viewport: vp,
		Match: func(n docker.NodeEntry, query string) bool {
			return strings.Contains(strings.ToLower(n.Hostname), strings.ToLower(query))
		},
		Columns: cols,
		Header: &filterlist.HeaderConfig{
			Columns: filterlist.ColumnDefs(cols),
			SortIndicator: func() (int, bool) {
				sortColMap := map[SortField]int{
					SortByHostname:     1,
					SortByRole:         2,
					SortByState:        3,
					SortByAvailability: 4,
					SortByVersion:      7,
					SortByAddress:      8,
					SortByLabels:       9,
				}
				col, ok := sortColMap[m.sortField]
				if !ok {
					return -1, true
				}
				return col, m.sortAscending
			},
		},
		Footer: &filterlist.FooterConfig{ItemLabel: "Node"},
	}
	list.SetOuterSize(width, height)
	m.List = list
	return m
}

func (m *Model) Init() tea.Cmd {
	return nil
}

func tickCmd(gen uint64) tea.Cmd {
	return tea.Tick(PollInterval, func(time.Time) tea.Msg {
		return TickMsg{Gen: gen}
	})
}

func (m *Model) Name() string {
	return ViewName
}

func (m *Model) ShortHelpItems() []helpbar.HelpEntry {
	return []helpbar.HelpEntry{
		{Key: "i", Desc: "Inspect"},
		{Key: "p", Desc: "ps"},
		{Key: "a", Desc: "Availability"},
		{Key: "Ctrl+L", Desc: "Add label"},
		{Key: "Ctrl+R", Desc: "Remove label"},
		{Key: "Ctrl+T", Desc: "Demote node"},
		{Key: "Ctrl+O", Desc: "Promote node"},
		{Key: "Ctrl+D", Desc: "Remove node"},
		{Key: "↑/↓", Desc: "Navigate"},
		{Key: "/", Desc: "Filter"},
		{Key: "esc", Desc: "Back"},
	}
}

// CapturesInput reports whether the view is currently capturing all keyboard input.
func (m *Model) CapturesInput() bool {
	return m.confirmDialog.Visible || m.errorDialogActive || m.availabilityDialog || m.labelInputDialog || m.labelRemoveDialog
}

func (m *Model) loadNodes() []docker.NodeEntry {
	snapshotOps := m.deps.Snapshot
	// Prefer cached snapshot to avoid blocking the UI. Trigger an async refresh if needed.
	snapshotOps.TriggerRefreshIfNeeded()

	snap := snapshotOps.GetSnapshot()
	if snap == nil {
		// Try synchronous refresh as a last resort
		s, err := snapshotOps.RefreshSnapshot()
		if err != nil {
			l().Errorf("LoadNodes: RefreshSnapshot failed: %v", err)
			return []docker.NodeEntry{}
		}
		snap = s
	}
	return snap.ToNodeEntries()
}

func (m *Model) LoadNodesCmd() tea.Cmd {
	return func() tea.Msg {
		entries := m.loadNodes()
		return Msg{Entries: entries}
	}
}

// checkNodesCmd checks if nodes have changed and returns update message if so
func (m *Model) checkNodesCmd(lastHash uint64) tea.Cmd {
	return func() tea.Msg {
		l().Info("CheckNodesCmd: Polling for node changes")

		entries := m.loadNodes()
		newHash, err := hash.Compute(entries)
		if err != nil {
			l().Errorf("CheckNodesCmd: Compute hash failed: %v", err)
			return PollRetryMsg{}
		}

		l().Infof("CheckNodesCmd: lastHash=%s, newHash=%s, nodeCount=%d",
			hash.Fmt(lastHash), hash.Fmt(newHash), len(entries))

		l().Debugf("CheckNodesCmd: Node entries: %+v", entries)

		// Only return update message if something changed
		if newHash != lastHash {
			l().Info("CheckNodesCmd: Change detected! Refreshing node list")
			return Msg{Entries: entries}
		}

		l().Info("CheckNodesCmd: No changes detected, scheduling next poll")
		return PollRetryMsg{}
	}
}

func (m *Model) OnEnter() tea.Cmd {
	m.Visible = true
	// The tick is armed here, not in Init or the factory: OnEnter is the only
	// hook that runs both on first entry and on every return from a drill-down,
	// and a chain does not survive a navigation — its tick is delivered to
	// whichever view is current by then, and dropped.
	//
	// Each entry gets its own generation. "Does not survive" holds only once
	// the leftover tick has fired: one armed just before a drill-down can
	// still be in flight when the operator returns, and would find this view
	// current again and re-arm, leaving two chains for the rest of the view's
	// life. The generation makes it recognisable as a leftover.
	m.pollGen++
	return tea.Batch(m.LoadNodesCmd(), tickCmd(m.pollGen))
}

func (m *Model) OnExit() tea.Cmd {
	return nil
}

// HasErrors reports whether any node in the current filtered list is unhealthy,
// which turns the logo red the same way a failing service or stack does.
func (m *Model) HasErrors() bool {
	for _, n := range m.List.Filtered {
		if nodeIsUnhealthy(n) {
			return true
		}
	}
	return false
}

// nodeIsUnhealthy reports whether a node needs the operator's attention, by
// the rule docker.NodeEntry.UnhealthyReason documents and the swarm health
// verdict shares.
func nodeIsUnhealthy(n docker.NodeEntry) bool {
	return n.UnhealthyReason() != ""
}

// HasActiveFilter reports whether a filter query is active.
func (m *Model) HasActiveFilter() bool {
	return m.List.Query != ""
}

// IsSearching reports whether the list is currently in search mode.
func (m *Model) IsSearching() bool {
	return false
}

// ApplySearchQuery sets the filter query and applies it.
func (m *Model) ApplySearchQuery(query string) {
	m.List.Query = query
	m.List.ApplyFilter()
}

// ClearSearchQuery clears the filter query and resets the view.
func (m *Model) ClearSearchQuery() {
	m.List.Query = ""
	m.List.ApplyFilter()
	m.List.Cursor = 0
	m.List.Viewport.GotoTop()
}
