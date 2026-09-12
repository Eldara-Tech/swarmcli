// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"strings"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"
	"github.com/Eldara-Tech/swarmcli/v2/views/commandinput"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	loadingview "github.com/Eldara-Tech/swarmcli/v2/views/loading"
	"github.com/Eldara-Tech/swarmcli/v2/views/searchinput"
	systeminfoview "github.com/Eldara-Tech/swarmcli/v2/views/systeminfo"
	"github.com/Eldara-Tech/swarmcli/v2/views/unlockdialog"

	"github.com/Eldara-Tech/swarmcli/v2/telemetry"
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"
	"github.com/Eldara-Tech/swarmcli/v2/views/viewstack"
	"github.com/charmbracelet/lipgloss"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// Model holds app state
type Model struct {
	deps docker.Deps

	viewport viewport.Model

	systemInfo *systeminfoview.Model

	currentView view.View
	viewStack   viewstack.Stack

	commandInput *commandinput.Model
	searchInput  *searchinput.Model

	// App-level error dialog for loading/navigation errors.
	// Per-view confirmdialog stays for operation errors (scale, restart, etc.).
	errorDialog          *confirmdialog.Model
	appErrorDialogActive bool
	errorFallbackView    string // view to navigate to on dismiss; "" = goBack()

	// App-level swarm unlock-key dialog, shown when the active swarm is locked.
	unlockDialog       *unlockdialog.Model
	unlockDialogActive bool

	// App-level "update available" notice, raised once per new release at
	// startup (and on demand via :dev-update). Driven by the systeminfo
	// version check; dismissal opt-out persists via the settings package.
	updateDialog         *confirmdialog.Model
	updateDialogActive   bool
	pendingUpdateVersion string // version to persist if the opt-out box is ticked

	// App-level first-run usage-reporting disclosure, shown as one line on the
	// stack bar rather than as a dialog. Set once in Init and cleared by the
	// first keystroke the app handles; see renderStackBar and Update.
	telemetryNoticeActive bool

	// App-level "the Docker context changed outside swarmcli" prompt. The
	// session is pinned to one context (docker.SessionContext), so a
	// `docker context use` elsewhere moves nothing until this is answered.
	contextDriftDialog       *confirmdialog.Model
	contextDriftDialogActive bool
	pendingDriftContext      string // context the prompt is offering to switch to
	declinedDriftContext     string // context the user chose not to follow; not offered again

	// Terminal dimensions
	terminalWidth  int
	terminalHeight int

	// Previous context name, set on context switch for revert on failure
	previousContext string
	// revertWritesConfig records whether reverting previousContext should also
	// rewrite ~/.docker/config.json. A switch made inside swarmcli wrote it, so
	// undoing the switch has to undo that too; one made in another terminal and
	// accepted at the drift prompt did not, and swarmcli has no business
	// reaching into the operator's shell context to take it back.
	revertWritesConfig bool

	// Fullscreen mode — hides helpbar/stackbar, uses full terminal
	fullscreen bool
}

var depsTransform func(docker.Deps) docker.Deps

// SetDepsTransform registers a function that transforms the default Deps
// before they are stored in the Model. Must be called before InitialModel().
// Used by extension builds to wrap Deps interfaces with middleware.
func SetDepsTransform(fn func(docker.Deps) docker.Deps) {
	depsTransform = fn
}

func buildDeps() docker.Deps {
	deps := docker.DefaultDeps()
	if depsTransform != nil {
		deps = depsTransform(deps)
	}
	return deps
}

func InitialModel() *Model {
	// Pin the Docker context before anything resolves it lazily, so every part
	// of the session addresses the same swarm even if the operator runs
	// `docker context use` in another terminal a moment later. Resolved here
	// rather than in Init(), which also runs for `swarmcli --version` and has
	// no business spawning a docker subprocess. A failure is only logged: the
	// loading view is where the user gets told a daemon is unreachable.
	if ctxName, err := docker.InitSessionContext(); err != nil {
		swarmlog.L().Infow("could not resolve the Docker context at startup", "error", err)
	} else {
		swarmlog.L().Infow("pinned the Docker context for this session", "context", ctxName)
	}

	// Use larger initial dimensions that will be adjusted by first WindowSizeMsg
	// This avoids the loading view appearing too small on the first render
	terminalWidth, terminalHeight := 200, 50

	vp := viewport.New(terminalWidth, terminalHeight)
	// Start viewport below the system info header (original default)
	vp.YPosition = 5

	loading := loadingview.New(terminalWidth, terminalHeight, true, map[string]string{
		"title":   "Initializing",
		"header":  "Fetching cluster info",
		"message": "Loading Swarm nodes and stacks...",
	})

	deps := buildDeps()

	return &Model{
		deps:               deps,
		viewport:           vp,
		currentView:        loading,
		systemInfo:         systeminfoview.New(deps, version, edition),
		viewStack:          viewstack.Stack{},
		commandInput:       cmdBar(),
		searchInput:        searchinput.New(),
		errorDialog:        confirmdialog.New(terminalWidth, terminalHeight),
		unlockDialog:       unlockdialog.New(terminalWidth, terminalHeight),
		updateDialog:       confirmdialog.New(terminalWidth, terminalHeight),
		contextDriftDialog: confirmdialog.New(terminalWidth, terminalHeight),
		terminalWidth:      terminalWidth,
		terminalHeight:     terminalHeight,
	}
}

// Init  will be automatically called by Bubble Tea if the model implements the Model interface
// and is passed into the tea.NewProgram function.
func (m *Model) Init() tea.Cmd {
	// Read **before** the batch below, not inside a command in it.
	//
	// `systemInfo.Init()` starts the check-in, and the check-in is what creates
	// the install identity. Both would run as goroutines under tea.Batch, so
	// asking "is this the first run" from inside one of them is a race against
	// the other creating the file — lost about as often as won, which is the
	// worst kind. Answering it here, synchronously, makes the ordering a fact
	// rather than a hope.
	m.telemetryNoticeActive = telemetry.ShouldNotice()

	// "" loads all stacks on all nodes
	cmds := []tea.Cmd{
		tick(),
		loadSnapshotAsync(),
		m.systemInfo.LoadStatus(),
		m.systemInfo.Init(), // Initialize systeminfo's tick commands
		watchEventsCmd(),
	}

	return tea.Batch(cmds...)
}

func (m *Model) switchToView(name string, data any) tea.Cmd {
	factory, ok := view.GetFactory(name)
	if !ok {
		return nil
	}

	// Exit hook for current view
	exitCmd := m.currentView.OnExit()

	newView, loadCmd := factory(m.deps, m.viewport.Width, m.viewport.Height, data)
	resizeCmd := handleViewResize(newView, m.viewport.Width, m.viewport.Height, m.fullscreen)

	// Push current view onto stack
	m.viewStack.Push(m.currentView)
	m.currentView = newView

	// Enter hook for new view
	enterCmd := newView.OnEnter()

	return tea.Batch(exitCmd, resizeCmd, loadCmd, enterCmd)
}

func (m *Model) replaceView(name string, data any) tea.Cmd {
	factory, ok := view.GetFactory(name)
	if !ok {
		return nil
	}

	// Run exit hook on current view
	exitCmd := m.currentView.OnExit()

	newView, loadCmd := factory(m.deps, m.viewport.Width, m.viewport.Height, data)
	resizeCmd := handleViewResize(newView, m.viewport.Width, m.viewport.Height, m.fullscreen)

	m.currentView = newView
	m.viewStack.Reset()

	// Run enter hook on new view
	enterCmd := newView.OnEnter()

	return tea.Batch(exitCmd, resizeCmd, loadCmd, enterCmd)
}

// StackBarSuffix is optional right-aligned text on the breadcrumb bar.
// Set from init() to display persistent status (e.g., license mode).
var StackBarSuffix string

// commandHintText advertises the ":" command bar, which is invisible until
// pressed. ":help" opens the command list; "?" opens the keybindings — the
// wording deliberately keeps those two distinct.
const commandHintText = "Type :help for commands"

// stackBarGap is the minimum number of blank columns kept between the
// breadcrumbs and the right-aligned block, and between the segments inside it.
const stackBarGap = 2

// stackBarMaxCrumbs is the most breadcrumb segments shown before the oldest
// are collapsed into a "…" prefix.
const stackBarMaxCrumbs = 3

var (
	stackBarSuffixStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	stackBarHintStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	stackBarNoticeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

func (m *Model) renderStackBar() string {
	names := make([]string, 0, m.viewStack.Len()+1)
	for _, v := range m.viewStack.Views() {
		names = append(names, v.Name())
	}
	names = append(names, m.currentView.Name())
	crumbs := m.fitBreadcrumbs(names)

	// Try the right-aligned renderings widest-first and take the first that
	// fits. The hint is dropped before the suffix: the suffix carries
	// persistent status, the hint is only a discoverability aid.
	for _, right := range m.stackBarRight() {
		gap := m.terminalWidth - lipgloss.Width(crumbs) - lipgloss.Width(right)
		if gap >= stackBarGap {
			return crumbs + strings.Repeat(" ", gap) + right
		}
	}

	// Nothing fit beside the breadcrumbs. On the first reporting run the
	// disclosure takes the whole bar rather than going unshown: a notice that
	// silently disappears below some terminal width is precisely the failure
	// this line exists to avoid, and "swarmcli says so on screen before
	// anything is sent" has to be true on a 46-column terminal too. The
	// breadcrumbs are back on the next frame; the notice is gone on the next
	// keystroke.
	if m.telemetryNoticeActive && m.terminalWidth > 0 {
		return ui.TruncateANSI(stackBarNoticeStyle.Render(telemetry.NoticeLineShort), m.terminalWidth)
	}
	return crumbs
}

// stackBarRight returns the right-aligned stack bar renderings in preference
// order, widest first. Empty when there is nothing to right-align.
func (m *Model) stackBarRight() []string {
	suffix := ""
	if StackBarSuffix != "" {
		suffix = stackBarSuffixStyle.Render(StackBarSuffix)
	}
	hint := ""
	if !m.hidesGlobalKeys() {
		hint = stackBarHintStyle.Render(commandHintText)
	}

	var candidates []string
	switch {
	case hint != "" && suffix != "":
		candidates = []string{hint + strings.Repeat(" ", stackBarGap) + suffix, suffix}
	case suffix != "":
		candidates = []string{suffix}
	case hint != "":
		candidates = []string{hint}
	}

	// The first-run disclosure is appended to every candidate and then tried
	// alone, so it is the last thing standing when the bar runs out of room —
	// ahead of both the hint and the suffix, which is the opposite of their
	// usual order and deliberate. Losing the hint costs discoverability and
	// losing the suffix costs a status somebody can read on the next frame;
	// losing the disclosure costs the reason reporting-by-default is
	// defensible at all. The short form is the last rung: on a terminal too
	// narrow for the full line, "reporting is on, type this" is still the
	// message.
	if !m.telemetryNoticeActive {
		return candidates
	}

	notice := stackBarNoticeStyle.Render(telemetry.NoticeLine)
	short := stackBarNoticeStyle.Render(telemetry.NoticeLineShort)

	withNotice := make([]string, 0, len(candidates)+2)
	for _, c := range candidates {
		withNotice = append(withNotice, c+strings.Repeat(" ", stackBarGap)+notice)
	}
	return append(withNotice, notice, short)
}

// fitBreadcrumbs renders the breadcrumb trail for names, shedding the oldest
// segments until the line fits terminalWidth. RenderBreadcrumbs already
// collapses what it drops into a "…" prefix, so narrowing maxDisplay degrades
// gracefully and always keeps the current view — the rightmost segment, and
// the one worth keeping. Truncates only when even a single segment overflows.
func (m *Model) fitBreadcrumbs(names []string) string {
	if m.terminalWidth <= 0 {
		return RenderBreadcrumbs(names, stackBarMaxCrumbs)
	}
	crumbs := ""
	for display := stackBarMaxCrumbs; display >= 1; display-- {
		crumbs = RenderBreadcrumbs(names, display)
		if lipgloss.Width(crumbs) <= m.terminalWidth {
			return crumbs
		}
	}
	return lipgloss.NewStyle().MaxWidth(m.terminalWidth).Render(crumbs)
}

func cmdBar() *commandinput.Model {
	cmdBar := commandinput.New()
	return cmdBar
}

func (m *Model) showAppError(errMsg string, fallbackView string) {
	m.errorDialog.Visible = true
	m.errorDialog.InfoMode = false
	m.errorDialog.ErrorMode = true
	m.errorDialog.Message = errMsg
	m.appErrorDialogActive = true
	m.errorFallbackView = fallbackView
}

// showAppInfo shows the app-level modal styled as a neutral notice (not an error).
func (m *Model) showAppInfo(infoMsg string, fallbackView string) {
	m.errorDialog.Visible = true
	m.errorDialog.ErrorMode = false
	m.errorDialog.InfoMode = true
	m.errorDialog.Message = infoMsg
	m.appErrorDialogActive = true
	m.errorFallbackView = fallbackView
}
