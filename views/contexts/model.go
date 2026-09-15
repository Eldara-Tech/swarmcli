// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package contexts

import (
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/helpbar"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
)

type SortField int

const (
	SortByName SortField = iota
	SortByStatus
	SortByDescription
	SortByEndpoint
)

type Model struct {
	deps     docker.Deps
	Visible  bool
	viewport viewport.Model
	ready    bool

	List filterlist.FilterableList[docker.ContextInfo]

	sortField     SortField
	sortAscending bool // true for ascending, false for descending

	contexts              []docker.ContextInfo
	cursor                int
	mu                    sync.Mutex
	loading               bool
	pollGen               uint64 // generation of the live poll chain; see OnEnter
	errorMsg              string
	successMsg            string
	switchPending         bool
	confirmDialog         *confirmdialog.Model
	pendingExportContext  string
	pendingDeleteContext  string
	pendingAction         string // "export" or "delete"
	importInput           textinput.Model
	importInputActive     bool
	fileBrowserActive     bool
	fileBrowserPath       string
	fileBrowserFiles      []string
	fileBrowserCursor     int
	errorDialogActive     bool
	createDialogActive    bool
	createNameInput       textinput.Model
	createDescInput       textinput.Model
	createHostInput       textinput.Model
	createInputFocus      int // 0 = name, 1 = description, 2 = host, 3 = tls toggle, 4 = ca, 5 = cert, 6 = key
	createTLSEnabled      bool
	createCAInput         textinput.Model
	createCertInput       textinput.Model
	createKeyInput        textinput.Model
	certFileBrowserActive bool   // true when browsing for cert files (different from import file browser)
	certFileTarget        string // "ca", "cert", or "key" - which field is being browsed
	lastCertBrowserPath   string // Remember last directory used in cert file browser
	editDialogActive      bool
	editContextName       string // Name of context being edited (immutable)
	editContextDesc       string // Description the dialog opened with
	editContextHost       string // Host the dialog opened with
	editContextCurrent    bool   // Whether the context being edited is the active one
	editDescInput         textinput.Model
	editHostInput         textinput.Model
	editInputFocus        int // 0 = description, 1 = host
}

func New() *Model {
	importInput := textinput.New()
	importInput.Placeholder = "/tmp"
	importInput.Prompt = "Directory: "
	importInput.CharLimit = 512
	importInput.Width = 50

	createNameInput := textinput.New()
	createNameInput.Placeholder = "my-context"
	createNameInput.Prompt = "Name: "
	createNameInput.CharLimit = 100
	createNameInput.Width = 50

	createDescInput := textinput.New()
	createDescInput.Placeholder = "Description (optional)"
	createDescInput.Prompt = "Desc: "
	createDescInput.CharLimit = 200
	createDescInput.Width = 50

	createHostInput := textinput.New()
	createHostInput.Placeholder = "tcp://host:2376"
	createHostInput.Prompt = "Host: "
	createHostInput.CharLimit = 256
	createHostInput.Width = 50

	createCAInput := textinput.New()
	createCAInput.Placeholder = "/path/to/ca.pem"
	createCAInput.Prompt = "CA:   "
	createCAInput.CharLimit = 512
	createCAInput.Width = 50

	createCertInput := textinput.New()
	createCertInput.Placeholder = "/path/to/cert.pem"
	createCertInput.Prompt = "Cert: "
	createCertInput.CharLimit = 512
	createCertInput.Width = 50

	createKeyInput := textinput.New()
	createKeyInput.Placeholder = "/path/to/key.pem"
	createKeyInput.Prompt = "Key:  "
	createKeyInput.CharLimit = 512
	createKeyInput.Width = 50

	editDescInput := textinput.New()
	editDescInput.Placeholder = "Description (optional)"
	editDescInput.Prompt = "Desc: "
	editDescInput.CharLimit = 200
	editDescInput.Width = 50

	editHostInput := textinput.New()
	editHostInput.Placeholder = "tcp://host:2376"
	editHostInput.Prompt = "Host: "
	editHostInput.CharLimit = 256
	editHostInput.Width = 50

	// Initialize an internal viewport for the filterable list
	vp := viewport.New(80, 20)
	vp.SetContent("")

	m := &Model{
		Visible:          false,
		contexts:         []docker.ContextInfo{},
		cursor:           0,
		confirmDialog:    confirmdialog.New(0, 0),
		importInput:      importInput,
		fileBrowserPath:  "/tmp",
		fileBrowserFiles: []string{},
		createNameInput:  createNameInput,
		createDescInput:  createDescInput,
		createHostInput:  createHostInput,
		createCAInput:    createCAInput,
		createCertInput:  createCertInput,
		createKeyInput:   createKeyInput,
		editDescInput:    editDescInput,
		editHostInput:    editHostInput,
		sortField:        SortByName,
		sortAscending:    true,
	}

	list := filterlist.FilterableList[docker.ContextInfo]{
		Viewport: vp,
		Match: func(item docker.ContextInfo, query string) bool {
			return strings.Contains(strings.ToLower(item.Name), strings.ToLower(query))
		},
		Header: &filterlist.HeaderConfig{
			Columns: []filterlist.ColumnDef{
				{Label: " NAME"}, {Label: "TLS"}, {Label: "DESCRIPTION"}, {Label: "ENDPOINT"}, {Label: "ERROR"},
			},
			SortIndicator: func() (int, bool) {
				colMap := map[SortField]int{
					SortByName: 0, SortByStatus: 1, SortByDescription: 2, SortByEndpoint: 3,
				}
				col, ok := colMap[m.sortField]
				if !ok {
					return -1, true
				}
				return col, m.sortAscending
			},
		},
	}
	list.SetOuterSize(80, 20)

	m.List = list
	return m
}

func (m *Model) SetSize(width, height int) {
	m.viewport.Width = width
	m.viewport.Height = height
	m.confirmDialog.Width = width
	m.confirmDialog.Height = height
	// Keep the internal list viewport in sync so it doesn't stay at its
	// initial 80x20 size when the view receives data.
	if width > 0 {
		m.List.Viewport.Width = width
	}
	if height > 0 {
		// Reserve 2 lines for stackbar/bottom status like other views
		h := height - 2
		if h <= 0 {
			h = 20
		}
		m.List.Viewport.Height = h
	}
	if !m.ready {
		m.ready = true
	}
}

func (m *Model) GetContexts() []docker.ContextInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	contexts := make([]docker.ContextInfo, len(m.contexts))
	copy(contexts, m.contexts)
	return contexts
}

func (m *Model) SetContexts(contexts []docker.ContextInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Remember the name of the currently selected context (if any)
	var cursorName string
	if m.List.Cursor >= 0 && m.List.Cursor < len(m.List.Filtered) {
		cursorName = m.List.Filtered[m.List.Cursor].Name
	}

	m.contexts = contexts

	// Update the FilterableList backing items and apply filter
	m.List.Items = m.contexts
	// Ensure the list viewport matches the current view size so the
	// content fills the frame immediately when contexts arrive.
	if m.viewport.Width > 0 {
		m.List.Viewport.Width = m.viewport.Width
	}
	if m.viewport.Height > 0 {
		h := m.viewport.Height
		if h <= 0 {
			h = 20
		}
		m.List.Viewport.Height = h
	}
	m.List.ApplyFilter()
	m.applySorting()
	// Update the internal viewport content immediately so parent view
	// that uses the viewport's content (e.g., during initial render)
	// doesn't keep showing the loading placeholder.
	m.List.Viewport.SetContent(m.List.View())

	// Restore cursor to the context with the same name, if possible
	found := false
	if cursorName != "" {
		for i, ctx := range m.List.Filtered {
			if ctx.Name == cursorName {
				m.List.Cursor = i
				found = true
				break
			}
		}
	}
	if !found {
		if len(m.List.Filtered) > 0 {
			m.List.Cursor = 0
		} else {
			m.List.Cursor = 0
		}
	}
	// Synchronize m.cursor from m.List.Cursor for legacy accessors
	m.cursor = m.List.Cursor
}

func (m *Model) GetCursor() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.List.Cursor
}

func (m *Model) MoveCursor(delta int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.List.Cursor += delta
	if m.List.Cursor < 0 {
		m.List.Cursor = 0
	}
	if m.List.Cursor >= len(m.List.Filtered) {
		m.List.Cursor = len(m.List.Filtered) - 1
	}
	// ApplyFilter will keep the cursor in-bounds and update viewport offset
	m.List.ApplyFilter()
	// Synchronize m.cursor from m.List.Cursor for legacy accessors
	m.cursor = m.List.Cursor
}

func (m *Model) GetSelectedContext() (docker.ContextInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.List.Cursor >= 0 && m.List.Cursor < len(m.List.Filtered) {
		return m.List.Filtered[m.List.Cursor], true
	}
	return docker.ContextInfo{}, false
}

func (m *Model) SetLoading(loading bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loading = loading
	// Capture values under lock for race-free debug logging.
	l, c := m.loading, len(m.contexts)
	go debugWriteLoadingState(l, c)
}

// debugWriteLoadingState logs loading state for diagnostics.
func debugWriteLoadingState(loading bool, count int) {
	swarmlog.L().Debugf("[contexts] loading=%v count=%d", loading, count)
}

func (m *Model) IsLoading() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loading
}

func (m *Model) SetError(err string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errorMsg = err
}

func (m *Model) GetError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.errorMsg
}

func (m *Model) SetSuccess(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.successMsg = msg
}

func (m *Model) GetSuccess() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.successMsg
}

func (m *Model) SetSwitchPending(pending bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.switchPending = pending
}

func (m *Model) IsSwitchPending() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.switchPending
}

// CapturesInput reports whether the view is currently capturing all keyboard input.
func (m *Model) CapturesInput() bool {
	return m.confirmDialog.Visible || m.importInputActive || m.fileBrowserActive || m.errorDialogActive || m.createDialogActive || m.certFileBrowserActive || m.editDialogActive
}

// Init initializes the model (part of View interface)
func (m *Model) Init() tea.Cmd {
	return nil
}
func (m *Model) HasErrors() bool {
	return false
}

// Name returns the view name (part of View interface)
func (m *Model) Name() string {
	return ViewName
}

// OnEnter is called when the view becomes active
func (m *Model) OnEnter() tea.Cmd {
	m.Visible = true
	// The tick is armed here, not in the factory: OnEnter is the only hook
	// that runs both on first entry and on every return from a drill-down,
	// and a chain does not survive a navigation — its tick is delivered to
	// whichever view is current by then, and dropped.
	//
	// Each entry gets its own generation. "Does not survive" holds only once
	// the leftover tick has fired: one armed just before a drill-down can
	// still be in flight when the operator returns, and would find this view
	// current again and re-arm, leaving two chains for the rest of the view's
	// life. The generation makes it recognisable as a leftover.
	m.pollGen++
	tick := tickCmd(m.pollGen)
	// If we have no contexts loaded, trigger a load on enter so the
	// view shows progress and fills itself. Also allow explicit reloads
	// from other code paths by calling SetLoading(true) before navigating.
	if len(m.contexts) == 0 {
		m.SetLoading(true)
		return tea.Batch(m.loadContextsCmd(), tick)
	}
	return tick
}

// OnExit is called when the view is exited
func (m *Model) OnExit() tea.Cmd {
	m.Visible = false
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

// updateCreateFocus updates focus state for create dialog inputs
func (m *Model) updateCreateFocus() {
	m.createNameInput.Blur()
	m.createDescInput.Blur()
	m.createHostInput.Blur()
	m.createCAInput.Blur()
	m.createCertInput.Blur()
	m.createKeyInput.Blur()

	switch m.createInputFocus {
	case 0:
		m.createNameInput.Focus()
	case 1:
		m.createDescInput.Focus()
	case 2:
		m.createHostInput.Focus()
	case 4:
		m.createCAInput.Focus()
	case 5:
		m.createCertInput.Focus()
	case 6:
		m.createKeyInput.Focus()
		// case 3 is the TLS checkbox, no focus needed
	}
}

// updateEditFocus updates focus state for edit dialog inputs
func (m *Model) updateEditFocus() {
	m.editDescInput.Blur()
	m.editHostInput.Blur()

	switch m.editInputFocus {
	case 0:
		m.editDescInput.Focus()
	case 1:
		m.editHostInput.Focus()
	}
}

// closeEditDialog clears the edit dialog's state.
func (m *Model) closeEditDialog() {
	m.editDialogActive = false
	m.editContextName = ""
	m.editContextDesc = ""
	m.editContextHost = ""
	m.editContextCurrent = false
	m.editInputFocus = 0
	m.editDescInput.Blur()
	m.editDescInput.SetValue("")
	m.editHostInput.Blur()
	m.editHostInput.SetValue("")
}

// ShortHelpItems returns the help items for the view
func (m *Model) ShortHelpItems() []helpbar.HelpEntry {
	return []helpbar.HelpEntry{
		{Key: "↑/↓", Desc: "Navigate"},
		{Key: "Enter", Desc: "Switch"},
		{Key: "i", Desc: "Inspect"},
		{Key: "e", Desc: "Edit"},
		{Key: "x", Desc: "Export"},
		{Key: "m", Desc: "Import"},
		{Key: "c", Desc: "Create"},
		{Key: "ctrl+d", Desc: "Delete"},
		{Key: "/", Desc: "Filter"},
		{Key: "Esc", Desc: "Back"},
	}
}
