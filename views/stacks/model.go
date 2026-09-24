// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/core/primitives/hash"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/helpbar"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
)

const defaultStackTemplate = `version: '3.8'

services:
  # Define your services here
  # Example:
  # myservice:
  #   image: myimage:latest
  #   ports:
  #     - "8080:8080"
  #   environment:
  #     - MY_VAR=value

networks:
  # Define custom networks if needed
`

type SortField int

const (
	SortByName SortField = iota
	SortByServices
	SortByTasks
	SortByError
)

type Model struct {
	deps             docker.Deps
	List             filterlist.FilterableList[docker.StackEntry]
	Visible          bool
	nodeID           string
	ready            bool
	firstResize      bool // tracks if we've received the first window size
	width            int
	height           int
	lastSnapshot     uint64 // hash of last snapshot for change detection
	pollGen          uint64 // generation of the live poll chain; see OnEnter
	DelayInitialLoad bool   // when true, delay the first LoadStacksCmd by 3s
	sortField        SortField
	sortAscending    bool // true for ascending, false for descending
	userSetSort      bool // tracks if user manually changed sort (to avoid auto-switching)
	// expandedStacks tracks which stacks are expanded to show tasks
	expandedStacks map[string]bool
	// stackTasks caches loaded tasks per stack
	stackTasks map[string][]docker.TaskEntry
	// stackHasError marks if a stack has at least one task error (checked from snapshot)
	stackHasError map[string]bool
	// stackErrorText stores a representative error text for the stack (first found)
	stackErrorText map[string]string
	// stackConverging marks a stack with a converging service and no error
	stackConverging map[string]bool
	// selectedTaskIndex when navigating tasks within an expanded stack
	selectedTaskIndex int
	// errorScrollOffset for horizontal scrolling of error messages
	errorScrollOffset int
	// confirmDialog for confirming destructive actions
	confirmDialog *confirmdialog.Model
	// pendingAction tracks what action is awaiting confirmation
	pendingAction string

	// Create stack dialog
	createDialogActive  bool
	createDialogStep    string // "source", "details-file", "details-inline"
	createDialogError   string // error message to display
	createInputFocus    int    // 0 = name, 1 = file path/content
	createNameInput     textinput.Model
	createFileInput     textinput.Model // For typing file path
	createStackSource   string          // "file" or "inline"
	createStackPath     string          // selected file path from browser
	createDialogContent string          // YAML content for the stack
	fileBrowserActive   bool
	fileBrowserPath     string
	fileBrowserFiles    []string
	fileBrowserCursor   int

	// Edit stack tracking
	editStackName string // non-empty when editing a stack (vs creating new)

	// Deploy progress
	deploying       bool
	deployingStack  string
	deployStartedAt time.Time
	spinner         int
	toastMessage    string
	toastUntil      time.Time

	// Save stack dialog
	saveDialogActive   bool
	saveDialogError    string          // error message to display in save dialog
	saveFileInput      textinput.Model // For typing file path
	saveStackName      string          // name of stack being saved
	fileBrowserContext string          // "create" or "save" — determines return target from file browser
}

func New(width, height int) *Model {
	vp := viewport.New(width, height)
	vp.SetContent("")
	vp.YOffset = 0

	m := &Model{
		Visible:           false,
		firstResize:       true,
		width:             width,
		height:            height,
		sortField:         SortByName,
		sortAscending:     true,
		expandedStacks:    make(map[string]bool),
		stackTasks:        make(map[string][]docker.TaskEntry),
		stackHasError:     make(map[string]bool),
		stackErrorText:    make(map[string]string),
		stackConverging:   make(map[string]bool),
		selectedTaskIndex: -1,
		createDialogStep:  "source",
		createStackSource: "file",
	}

	list := filterlist.FilterableList[docker.StackEntry]{
		Viewport: vp,
		// Render item will be initialized later after the column with is set
		Match: func(s docker.StackEntry, query string) bool {
			return strings.Contains(strings.ToLower(s.Name), strings.ToLower(query))
		},
		Header: &filterlist.HeaderConfig{
			Columns: []filterlist.ColumnDef{
				{Label: "STACK", Pct: 25},
				{Label: "SERVICES", Pct: 10},
				{Label: "TASKS", Pct: 10},
				{Label: "ERROR", Pct: 55},
			},
			SortIndicator: func() (int, bool) {
				return int(m.sortField), m.sortAscending
			},
			DynamicLabel: func(idx int, base string) string {
				if idx == 3 {
					count := 0
					for _, v := range m.stackHasError {
						if v {
							count++
						}
					}
					return fmt.Sprintf("ERROR: %d", count)
				}
				return ""
			},
		},
		Footer: &filterlist.FooterConfig{ItemLabel: "Stack"},
	}
	list.SetOuterSize(width, height)

	// Initialize name input for create dialog
	nameInput := textinput.New()
	nameInput.Placeholder = "my-stack"
	nameInput.Prompt = "Stack Name: "
	nameInput.CharLimit = 100
	nameInput.Width = 50

	// Initialize file path input for create dialog
	fileInput := textinput.New()
	fileInput.Placeholder = "/path/to/compose.yml"
	fileInput.Prompt = "Compose File: "
	fileInput.CharLimit = 512
	fileInput.Width = 50

	// Initialize save file path input
	saveInput := textinput.New()
	saveInput.Placeholder = "./my-stack.yml"
	saveInput.Prompt = "Save to: "
	saveInput.CharLimit = 512
	saveInput.Width = 50

	m.List = list
	m.confirmDialog = confirmdialog.New(width, height)
	m.createNameInput = nameInput
	m.createFileInput = fileInput
	m.saveFileInput = saveInput
	m.fileBrowserContext = "create"
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

// spinnerTickCmd schedules the next animation frame. Unlike the poll tick it is
// self-terminating: Update only re-arms it while something is animating, so the
// view redraws at rest only on the 5s poll.
func (m *Model) spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(t time.Time) tea.Msg {
		return SpinnerTickMsg(t)
	})
}

// beginDeploy marks a deploy in flight and returns the animation tick that
// drives the footer indicator.
func (m *Model) beginDeploy(stackName string) tea.Cmd {
	m.deploying = true
	m.deployingStack = stackName
	m.deployStartedAt = time.Now()
	return m.spinnerTickCmd()
}

func (m *Model) endDeploy() {
	m.deploying = false
	m.deployingStack = ""
}

func (m *Model) showToast(msg string) {
	if msg == "" {
		return
	}
	m.toastMessage = msg
	// Give multi-line toasts a bit more time so users can read them.
	d := 2 * time.Second
	if strings.Contains(msg, "\n") {
		d = 5 * time.Second
	}
	m.toastUntil = time.Now().Add(d)
}

// animating reports whether the footer still has something to redraw.
func (m *Model) animating() bool {
	return m.deploying || (m.toastMessage != "" && time.Now().Before(m.toastUntil))
}

func (m *Model) Name() string { return ViewName }

func (m *Model) ShortHelpItems() []helpbar.HelpEntry {
	return []helpbar.HelpEntry{
		{Key: "n", Desc: "New stack"},
		{Key: "e", Desc: "Edit"},
		{Key: "s", Desc: "Save YAML"},
		{Key: "enter", Desc: "Services"},
		{Key: "i", Desc: "Inspect"},
		{Key: "p", Desc: "Tasks"},
		{Key: "ctrl+d", Desc: "Delete"},
		{Key: "↑/↓", Desc: "Navigate"},
		{Key: "/", Desc: "Filter"},
	}
}

func (m *Model) LoadStacksCmd(nodeID string) tea.Cmd {
	snapOps := m.deps.Snapshot
	return func() tea.Msg {
		snapOps.TriggerRefreshIfNeeded()

		snap := snapOps.GetSnapshot()
		if snap == nil {
			s, err := snapOps.RefreshSnapshot()
			if err != nil {
				l().Errorf("LoadStacksCmd: RefreshSnapshot failed: %v", err)
				return Msg{NodeID: nodeID, Stacks: []docker.StackEntry{}}
			}
			snap = s
		}
		stacks := snap.ToStackEntries()

		l().Debugf("LoadStacksCmd: Loaded %v stacks", stacks)

		return Msg{NodeID: nodeID, Stacks: stacks}
	}
}

// checkStacksCmd checks if stacks have changed and returns update message if so
func (m *Model) checkStacksCmd(lastHash uint64, nodeID string) tea.Cmd {
	snapOps := m.deps.Snapshot
	clusterOps := m.deps.ClusterInfo
	return func() tea.Msg {
		l().Info("checkStacksCmd: Polling for stack changes")

		snapOps.TriggerRefreshIfNeeded()

		snap := snapOps.GetSnapshot()
		if snap == nil {
			s, err := snapOps.RefreshSnapshot()
			if err != nil {
				l().Errorf("checkStacksCmd: RefreshSnapshot failed: %v", err)
				return PollRetryMsg{}
			}
			snap = s
		}
		stacks := snap.ToStackEntries()

		newHash, err := hash.Compute(stacks)
		if err != nil {
			l().Errorf("checkStacksCmd: Error computing hash: %v", err)
			// Keep polling on error instead of returning nil which would stop the tick loop
			return PollRetryMsg{}
		}

		l().Infof("checkStacksCmd: lastHash=%s, newHash=%s, stackCount=%d",
			hash.Fmt(lastHash), hash.Fmt(newHash), len(stacks))

		l().Debugf("checkStacksCmd: Stacks: %+v", stacks)

		ctxName, _ := clusterOps.GetCurrentContext()
		l().Debugf("checkStacksCmd: docker context: %s", ctxName)

		// Only return update message if something changed
		if newHash != lastHash {
			l().Info("checkStacksCmd: Change detected! Refreshing stack list")
			return Msg{NodeID: nodeID, Stacks: stacks}
		}

		l().Info("checkStacksCmd: No changes detected, scheduling next poll")
		return PollRetryMsg{}
	}
}

// OnEnter re-arms the animation tick when a deploy is still running, so
// navigating away and back does not leave a frozen spinner in the footer.
func (m *Model) OnEnter() tea.Cmd {
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
	if m.deploying {
		return tea.Batch(m.LoadStacksCmd(m.nodeID), m.spinnerTickCmd(), tickCmd(m.pollGen))
	}
	return tea.Batch(m.LoadStacksCmd(m.nodeID), tickCmd(m.pollGen))
}
func (m *Model) OnExit() tea.Cmd { return nil }

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
	return (m.confirmDialog != nil && m.confirmDialog.Visible) || m.createDialogActive || m.saveDialogActive || m.fileBrowserActive
}

// HasErrors returns true if any stack has errors
func (m *Model) HasErrors() bool {
	for _, hasErr := range m.stackHasError {
		if hasErr {
			return true
		}
	}
	return false
}

// HasWarnings returns true if any stack is converging, which turns the logo
// amber when nothing is failing.
func (m *Model) HasWarnings() bool {
	for _, converging := range m.stackConverging {
		if converging {
			return true
		}
	}
	return false
}
