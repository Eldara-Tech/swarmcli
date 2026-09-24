// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package servicesview

import (
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/helpbar"
	"github.com/Eldara-Tech/swarmcli/v2/views/scaledialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

const ViewName = "services"

type FilterType int

const (
	NodeFilter FilterType = iota
	StackFilter
	NoStackFilter
	AllFilter
)

type SortField int

const (
	SortByName SortField = iota
	SortByStatus
	SortByImage
	SortByPorts
	SortByCreated
	SortByUpdated
	SortByError
)

type Model struct {
	deps         docker.Deps
	List         filterlist.FilterableList[docker.ServiceEntry]
	Visible      bool
	titleScope   string
	ready        bool
	firstResize  bool // tracks if we've received the first window size
	width        int
	height       int
	lastSnapshot uint64 // hash of last snapshot for change detection
	pollGen      uint64 // generation of the live poll chain; see OnEnter

	// Filter
	filterType FilterType
	nodeID     string
	stackName  string

	// Optional one-shot selection after navigation.
	pendingSelectServiceName string

	confirmDialog *confirmdialog.Model
	scaleDialog   *scaledialog.Model

	// Track what action is pending confirmation
	pendingAction string // "restart", "remove", "rollback", or "empty-stack"

	// Track which services have their tasks expanded
	expandedServices map[string]bool               // service ID -> expanded
	serviceTasks     map[string][]docker.TaskEntry // cached tasks per service
	// serviceHasError marks if a service has a running task with an error
	serviceHasError map[string]bool
	// serviceConverging marks a service that is converging and not failing
	serviceConverging map[string]bool
	// serviceErrorText stores a representative error text per service
	serviceErrorText map[string]string

	// Track task navigation: -1 means service row is selected, >= 0 means task at that index
	selectedTaskIndex int

	// Sorting
	sortField     SortField
	sortAscending bool // true for ascending, false for descending
}

func (m *Model) SetPendingSelectServiceName(serviceName string) {
	m.pendingSelectServiceName = serviceName
}

func New(width, height int) *Model {
	vp := viewport.New(width, height)

	m := &Model{
		Visible:           false,
		firstResize:       true,
		width:             width,
		height:            height,
		confirmDialog:     confirmdialog.New(width, height),
		scaleDialog:       scaledialog.New(width, height),
		expandedServices:  make(map[string]bool),
		serviceTasks:      make(map[string][]docker.TaskEntry),
		serviceHasError:   make(map[string]bool),
		serviceConverging: make(map[string]bool),
		serviceErrorText:  make(map[string]string),
		selectedTaskIndex: -1,
		sortField:         SortByName,
		sortAscending:     true,
	}

	list := filterlist.FilterableList[docker.ServiceEntry]{
		Viewport: vp,
		Match: func(s docker.ServiceEntry, query string) bool {
			return strings.Contains(strings.ToLower(s.ServiceName), strings.ToLower(query))
		},
		Columns: m.layoutColumns(),
		Header: &filterlist.HeaderConfig{
			Columns:       filterlist.ColumnDefs(m.layoutColumns()),
			SortIndicator: m.sortIndicator,
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

func (m *Model) Name() string { return ViewName }

func (m *Model) ShortHelpItems() []helpbar.HelpEntry {
	return []helpbar.HelpEntry{
		{Key: "i", Desc: "Inspect"},
		{Key: "↑/↓", Desc: "Navigate"},
		{Key: "p", Desc: "Show/hide tasks"},
		{Key: "s", Desc: "Scale service"},
		{Key: "r", Desc: "Restart service"},
		{Key: "ctrl+r", Desc: "Rollback service"},
		{Key: "ctrl+d", Desc: "Remove service"},
		{Key: "l", Desc: "View logs"},
		{Key: "x", Desc: view.BEHelpDesc("shell", "Shell"), Disabled: !view.HasAction("shell")},
		{Key: "w", Desc: view.BEHelpDesc("port-forwards", "Active Forwards"), Disabled: !view.HasAction("port-forwards")},
		{Key: "shift+w", Desc: view.BEHelpDesc("port-forward", "Port Forward"), Disabled: !view.HasAction("port-forward")},
		{Key: "t", Desc: view.BEHelpDesc("container-stats", "Stats"), Disabled: !view.HasAction("container-stats")},
		{Key: "/", Desc: "Filter"},
		{Key: "esc", Desc: "Back"},
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
	return tickCmd(m.pollGen)
}

func (m *Model) OnExit() tea.Cmd {
	return nil
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

// CapturesInput reports whether the view is currently capturing all keyboard input.
func (m *Model) CapturesInput() bool {
	return m.confirmDialog.Visible || m.scaleDialog.Visible
}

// HasErrors returns true if any service in the current filtered list has errors
func (m *Model) HasErrors() bool {
	// Only check errors for services that are actually in the current view
	for _, svc := range m.List.Filtered {
		if m.serviceHasError[svc.ServiceID] {
			return true
		}
	}
	return false
}

// HasWarnings reports whether any service in the current filtered list is
// converging, which turns the logo amber when nothing is failing.
func (m *Model) HasWarnings() bool {
	for _, svc := range m.List.Filtered {
		if m.serviceConverging[svc.ServiceID] {
			return true
		}
	}
	return false
}
