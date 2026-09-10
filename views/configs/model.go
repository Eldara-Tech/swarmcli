// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package configsview

import (
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/helpbar"
	loading "github.com/Eldara-Tech/swarmcli/v2/views/loading"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type SortField int

const (
	SortByName SortField = iota
	SortByID
	SortByUsed
	SortByCreated
	SortByUpdated
	SortByLabels
)

type Model struct {
	deps          docker.Deps
	configsList   filterlist.FilterableList[configItem]
	width         int
	height        int
	firstResize   bool   // tracks if we've received the first window size
	lastSnapshot  uint64 // hash of last snapshot for change detection
	pollGen       uint64 // generation of the live poll chain; see OnEnter
	polling       atomic.Bool
	visible       bool // tracks if view is currently active
	sortField     SortField
	sortAscending bool // true for ascending, false for descending

	state state
	err   error

	pendingAction      string
	confirmDialog      *confirmdialog.Model
	errorDialogActive  bool
	loadingView        *loading.Model
	configs            []docker.ConfigWithDecodedData
	configToRotateFrom *docker.ConfigWithDecodedData
	configToRotateInto *docker.ConfigWithDecodedData
	configToDelete     *docker.ConfigWithDecodedData

	// Create config dialog
	createDialogActive bool
	createDialogStep   string // "source", "details-file", "details-inline"
	createDialogError  string // error message to display
	createInputFocus   int    // 0 = name, 1 = file path, 2 = labels
	createNameInput    textinput.Model
	createFileInput    textinput.Model // For typing file path
	createLabelsInput  textinput.Model // For typing labels (a=b,c=d)
	createConfigSource string          // "file" or "inline"
	createConfigPath   string          // selected file path from browser
	createConfigData   string
	fileBrowserActive  bool
	fileBrowserPath    string
	fileBrowserFiles   []string
	fileBrowserCursor  int

	// Used By view
	usedByViewActive bool
	usedByList       filterlist.FilterableList[usedByItem]
	usedByConfigName string

	// Spinner for slow-used-status indicator
	spinner int
}

type state int

const (
	stateLoading state = iota
	stateReady
	stateError
)

func New(width, height int) *Model {
	vp := viewport.New(width, height)
	vp.SetContent("")

	m := &Model{
		width:         width,
		height:        height,
		firstResize:   true,
		state:         stateLoading,
		visible:       true,
		sortField:     SortByName,
		sortAscending: true,
	}

	cols := m.buildColumns()
	list := filterlist.FilterableList[configItem]{
		Viewport: vp,
		Match: func(c configItem, query string) bool {
			q := strings.ToLower(query)
			return strings.Contains(strings.ToLower(c.Name), q) ||
				strings.Contains(strings.ToLower(c.ID), q)
		},
		Columns: cols,
		Header: &filterlist.HeaderConfig{
			Columns: filterlist.ColumnDefs(cols),
			SortIndicator: func() (int, bool) {
				return int(m.sortField), m.sortAscending
			},
		},
		Footer: &filterlist.FooterConfig{ItemLabel: "Config"},
	}
	list.SetOuterSize(width, height)

	// Initialize name input for create dialog
	nameInput := textinput.New()
	nameInput.Placeholder = "my-config"
	nameInput.Prompt = "Name: "
	nameInput.CharLimit = 100
	nameInput.Width = 50

	// Initialize file path input for create dialog
	fileInput := textinput.New()
	fileInput.Placeholder = "/path/to/file"
	fileInput.Prompt = "File: "
	fileInput.CharLimit = 512
	fileInput.Width = 50

	// Initialize labels input for create dialog
	labelsInput := textinput.New()
	labelsInput.Placeholder = "key1=value1,key2=value2"
	labelsInput.Prompt = "Labels: "
	labelsInput.CharLimit = 512
	labelsInput.Width = 50

	m.configsList = list
	m.confirmDialog = confirmdialog.New(0, 0)
	m.loadingView = loading.New(width, height, false, "Loading Docker configs...")
	m.createNameInput = nameInput
	m.createFileInput = fileInput
	m.createLabelsInput = labelsInput

	return m
}

func (m *Model) Name() string { return ViewName }

// HasActiveFilter reports whether a filter query is active.
func (m *Model) HasActiveFilter() bool {
	return m.configsList.Query != ""
}

// IsSearching reports whether the configs view is in a sub-view that should
// capture keys (e.g. usedBy sub-list).
func (m *Model) IsSearching() bool {
	if m.usedByViewActive {
		return true
	}
	return false
}

// ApplySearchQuery sets the filter query on the primary configs list.
func (m *Model) ApplySearchQuery(query string) {
	m.configsList.Query = query
	m.configsList.ApplyFilter()
}

// ClearSearchQuery clears the filter on the primary configs list.
func (m *Model) ClearSearchQuery() {
	m.configsList.Query = ""
	m.configsList.ApplyFilter()
	m.configsList.Cursor = 0
	m.configsList.Viewport.GotoTop()
}

func (m *Model) Init() tea.Cmd {
	l().Info("ConfigsView: Init() called - starting ticker and loading configs")
	return tea.Batch(m.spinnerTickCmd(), m.loadConfigsCmd())
}

func (m *Model) spinnerTickCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return SpinnerTickMsg(t)
	})
}

func tickCmd(gen uint64) tea.Cmd {
	return tea.Tick(PollInterval, func(time.Time) tea.Msg {
		return TickMsg{Gen: gen}
	})
}

func (m *Model) ShortHelpItems() []helpbar.HelpEntry {
	if m.usedByViewActive {
		return []helpbar.HelpEntry{
			{Key: "↑/↓", Desc: "Navigate"},
			{Key: "Enter", Desc: "Go to Stack"},
			{Key: "/", Desc: "Filter"},
			{Key: "Esc", Desc: "Back"},
		}
	}

	return []helpbar.HelpEntry{
		{Key: "↑/↓", Desc: "Navigate"},
		{Key: "n", Desc: "New"},
		{Key: "c", Desc: "Clone"},
		{Key: "i", Desc: "Inspect"},
		{Key: "u", Desc: "Used By"},
		{Key: "Enter", Desc: "Check"},
		{Key: "e", Desc: "Edit & Rotate"},
		{Key: "ctrl+d", Desc: "Delete"},
		{Key: "esc", Desc: "Back"},
	}
}

func (m *Model) selectedConfig() string {
	if len(m.configsList.Filtered) == 0 {
		return ""
	}
	return m.configsList.Filtered[m.configsList.Cursor].Name
}

func (m *Model) findConfigByName(name string) (*docker.ConfigWithDecodedData, error) {
	for i := range m.configs {
		if m.configs[i].Config.Spec.Name == name {
			return &m.configs[i], nil
		}
	}
	return nil, fmt.Errorf("config '%s' not found", name)
}

func (m *Model) addConfig(cfg docker.ConfigWithDecodedData) {
	m.configs = append(m.configs, cfg)
	// No index: this is a config this view has just created, so no service can
	// reference it yet. The rotation that will point services at it is still
	// behind a confirmation dialog.
	m.configsList.Items = append(m.configsList.Items, configItemFromSwarm(cfg.Config, nil))
	m.configsList.ApplyFilter()
}

func (m *Model) OnEnter() tea.Cmd {
	m.visible = true
	l().Info("ConfigsView: OnEnter() - view is now visible")
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
	return tea.Batch(m.loadConfigsCmd(), tickCmd(m.pollGen))
}

func (m *Model) OnExit() tea.Cmd {
	m.visible = false
	l().Info("ConfigsView: OnExit() - view is no longer visible")
	return nil
}

// CapturesInput reports whether the view is currently capturing all keyboard input.
func (m *Model) CapturesInput() bool {
	return m.confirmDialog.Visible || m.errorDialogActive || m.createDialogActive || m.fileBrowserActive
}

// IsInUsedByView returns true if currently viewing the used-by list
func (m *Model) IsInUsedByView() bool {
	return m.usedByViewActive
}

func (m *Model) HasErrors() bool {
	return false
}

// validateConfigName rejects, in the dialog, the names the daemon would refuse.
func validateConfigName(name string) error {
	return docker.ValidateSwarmObjectName("config", name)
}
