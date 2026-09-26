// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package logsview

import (
	"context"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/helpbar"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/docker/docker/api/types/swarm"
)

// Model holds the state for the streaming logs view.
type Model struct {
	deps          docker.Deps
	viewport      viewport.Model
	Visible       bool
	mode          string // "normal" or "search"
	searchTerm    string
	searchIndex   int
	searchMatches []int
	lines         []string    // bounded: only last MaxLines kept
	lineNodes     []string    // node name for each line (parallel to lines)
	lineTasks     []string    // full task ID for each line (parallel to lines), "" if unparseable
	lineKinds     []lineKind  // log line or separator (parallel to lines)
	lineAt        []time.Time // arrival time (parallel to lines), zero when the line is not new
	MaxLines      int
	ready         bool
	// visibleCount is how many lines passed the filters the last time content
	// was built. Cached there rather than counted in FrameTitle, which runs on
	// every render over a buffer of up to MaxLines.
	visibleCount int

	ServiceEntry docker.ServiceEntry

	// streaming control
	StreamCtx    context.Context
	StreamCancel context.CancelFunc // cancel context for streaming goroutine
	streamMu     sync.Mutex         // protects below
	streamActive bool               // whether a stream is active

	// read pump channels (internal to tea)
	linesChan chan string
	errChan   chan error

	// sync for lines slice
	mu sync.Mutex

	// follow behavior
	follow bool
	// wrap behavior
	wrap bool
	// horizontal scroll offset when wrap is off
	horizontalOffset int
	// node filter - if set, only show logs from this node
	nodeFilter string
	// app-level "/" filter — hides non-matching lines
	filterQuery string
	// when true, hide log lines from tasks that are no longer running
	hideStopped bool

	// new-line highlight state
	linesSinceInit int       // lines received since the stream opened
	lastLineAt     time.Time // when the previous line arrived
	highlightArmed bool      // false until the backlog replay is over
	fadeArmed      bool      // a fade tick is already scheduled
	// node selection dialog
	nodeSelectVisible bool
	nodeSelectCursor  int
	nodeSelectNodes   []string
}

// New creates a logs model with sensible defaults.
func New(width, height int, maxLines int, service docker.ServiceEntry) *Model {
	vp := viewport.New(width, height)
	vp.SetContent("")
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{
		viewport:          vp,
		Visible:           false,
		mode:              "normal",
		lines:             make([]string, 0, 1024),
		lineNodes:         make([]string, 0, 1024),
		lineTasks:         make([]string, 0, 1024),
		lineKinds:         make([]lineKind, 0, 1024),
		lineAt:            make([]time.Time, 0, 1024),
		MaxLines:          maxLines,
		StreamCtx:         ctx,
		StreamCancel:      cancel,
		ServiceEntry:      service,
		linesChan:         nil,
		errChan:           nil,
		follow:            true, // auto-follow by default
		wrap:              true, // wrap lines by default
		horizontalOffset:  0,
		nodeFilter:        "",   // empty = show all nodes
		hideStopped:       true, // hide stopped-task logs by default
		nodeSelectVisible: false,
		nodeSelectCursor:  0,
		nodeSelectNodes:   []string{},
	}
}

// lineKind distinguishes what the view itself put in the buffer from what the
// service actually logged. A mark is the separator the reader asked for with
// "enter": it carries no node or task and no arrival time, and is rendered at
// build time rather than stored, so it follows the current width. A notice is
// the view speaking about the stream — an error, or the stream closing. Only a
// lineLog is filterable and counted; see lineVisible and renderRows.
type lineKind uint8

const (
	lineLog lineKind = iota
	lineMark
	lineNotice
)

// appendLine grows the per-line slices in lock-step. They are indexed together
// everywhere, so growing them anywhere but here is how they come apart.
// Callers hold m.mu.
func (m *Model) appendLine(text, node, task string, kind lineKind, at time.Time) {
	m.lines = append(m.lines, text)
	m.lineNodes = append(m.lineNodes, node)
	m.lineTasks = append(m.lineTasks, task)
	m.lineKinds = append(m.lineKinds, kind)
	m.lineAt = append(m.lineAt, at)
}

// trimToMaxLines drops the oldest lines once the buffer is over MaxLines and
// returns how many it dropped — which is what a viewport that is not following
// has to take off its offset to keep showing the same lines. Callers hold m.mu.
func (m *Model) trimToMaxLines() int {
	if m.MaxLines <= 0 || len(m.lines) <= m.MaxLines {
		return 0
	}
	start := len(m.lines) - m.MaxLines
	m.lines = append(make([]string, 0, m.MaxLines), m.lines[start:]...)
	m.lineNodes = append(make([]string, 0, m.MaxLines), m.lineNodes[start:]...)
	m.lineTasks = append(make([]string, 0, m.MaxLines), m.lineTasks[start:]...)
	m.lineKinds = append(make([]lineKind, 0, m.MaxLines), m.lineKinds[start:]...)
	m.lineAt = append(make([]time.Time, 0, m.MaxLines), m.lineAt[start:]...)
	return start
}

// kindAt and arrivedAt read the newer per-line slices tolerantly: a caller that
// set m.lines wholesale — SetContent, and every test that builds a buffer by
// hand — leaves them short, and a short slice means "an ordinary log line that
// is not new" rather than an index panic.
func (m *Model) kindAt(i int) lineKind {
	if i < len(m.lineKinds) {
		return m.lineKinds[i]
	}
	return lineLog
}

func (m *Model) arrivedAt(i int) time.Time {
	if i < len(m.lineAt) {
		return m.lineAt[i]
	}
	return time.Time{}
}

// armHighlight opens the highlight once the backlog replay is over. A stream
// opens with the lines Docker replays for the tail request: that is history,
// not news, and highlighting it would flash the whole screen at open. Two
// signals end the replay, and each covers the other's blind spot — a gap
// between arrivals, which a quiet service gives immediately, and more lines
// than the backlog can hold, which is the only signal a service logging
// faster than the gap will ever give. Callers hold m.mu.
func (m *Model) armHighlight(now time.Time) {
	if m.highlightArmed {
		return
	}
	if m.linesSinceInit > backlogTail ||
		(!m.lastLineAt.IsZero() && now.Sub(m.lastLineAt) >= highlightArmDelay) {
		m.highlightArmed = true
	}
}

// stamp is the arrival time to record for a line landing now: the real time
// once highlighting is armed, and the zero time before that, which is what
// makes a line permanently not new. Callers hold m.mu.
func (m *Model) stamp(now time.Time) time.Time {
	if !m.highlightArmed {
		return time.Time{}
	}
	return now
}

// isFresh reports whether the line at i is still inside its highlight window.
// Callers hold m.mu.
func (m *Model) isFresh(i int, now time.Time) bool {
	if m.kindAt(i) == lineMark {
		return false
	}
	at := m.arrivedAt(i)
	return !at.IsZero() && now.Sub(at) < highlightDuration
}

// anyFresh reports whether any line is still highlighted. Arrival times only
// ever grow, so the newest log line settles it — the walk exists to step over
// marks, which carry no arrival time of their own. Callers hold m.mu.
func (m *Model) anyFresh(now time.Time) bool {
	for i := len(m.lines) - 1; i >= 0; i-- {
		if m.kindAt(i) == lineMark {
			continue
		}
		return m.isFresh(i, now)
	}
	return false
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Name() string { return ViewName }

func (m *Model) setFollow(f bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.follow = f
}

func (m *Model) getNodeFilter() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nodeFilter
}

func (m *Model) setNodeFilter(filter string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodeFilter = filter
}

func (m *Model) getNodeSelectVisible() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nodeSelectVisible
}

func (m *Model) setNodeSelectVisible(visible bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodeSelectVisible = visible
}

func (m *Model) GetNodeSelectVisible() bool {
	return m.getNodeSelectVisible()
}

func (m *Model) getFollow() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.follow
}

func (m *Model) setWrap(w bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wrap = w
}

func (m *Model) getWrap() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wrap
}

func (m *Model) getVisibleCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.visibleCount
}

func (m *Model) getHideStopped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hideStopped
}

func (m *Model) setHideStopped(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hideStopped = v
}

// GetSearchMode is exported for app to check search mode status
func (m *Model) GetSearchMode() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode == "search"
}

// IsSearching is an alias for GetSearchMode for compatibility with app-level search checks
func (m *Model) IsSearching() bool {
	return m.GetSearchMode()
}

// CapturesInput returns true when the view is capturing all keyboard input.
func (m *Model) CapturesInput() bool {
	return m.getNodeSelectVisible()
}

func (m *Model) getFilterQuery() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.filterQuery
}

// ApplySearchQuery implements view.Filterable — sets the app-level "/" filter.
func (m *Model) ApplySearchQuery(query string) {
	m.mu.Lock()
	m.filterQuery = query
	m.mu.Unlock()
	m.highlightContent()
	if m.ready && m.getFollow() {
		m.viewport.GotoBottom()
	}
}

// ClearSearchQuery implements view.Filterable — clears the app-level "/" filter.
func (m *Model) ClearSearchQuery() {
	m.mu.Lock()
	m.filterQuery = ""
	m.mu.Unlock()
	m.highlightContent()
	if m.ready && m.getFollow() {
		m.viewport.GotoBottom()
	}
}

// HasActiveFilter returns true when the app-level "/" filter is active.
func (m *Model) HasActiveFilter() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.filterQuery != ""
}

// ShortHelpItems stays compatible with your helpbar interface.
func (m *Model) ShortHelpItems() []helpbar.HelpEntry {
	if m.mode == "search" {
		return []helpbar.HelpEntry{
			{Key: "enter", Desc: "Confirm"},
			{Key: "esc", Desc: "Cancel"},
			{Key: "n/N", Desc: "Next/prev"},
		}
	}

	entries := []helpbar.HelpEntry{
		{Key: "ctrl+f", Desc: "Search"},
		{Key: "n/N", Desc: "Next/prev"},
		{Key: "s", Desc: "Toggle AutoScroll"},
		{Key: "w", Desc: "Toggle wrap"},
		{Key: "o", Desc: "Filter node"},
		{Key: "t", Desc: "Show/hide stopped"},
		{Key: "enter", Desc: "Mark"},
		{Key: "/", Desc: "Filter (!x hides)"},
	}

	// Show left/right help only when wrap is off
	if !m.getWrap() {
		entries = append(entries, helpbar.HelpEntry{Key: "←/→", Desc: "Scroll"})
	}

	entries = append(entries, helpbar.HelpEntry{Key: "esc", Desc: "Close"})
	return entries
}

func (m *Model) OnEnter() tea.Cmd {
	// We start streaming with the factory method
	return nil
}

func (m *Model) OnExit() tea.Cmd {
	return m.StopStreamingCmd()
}

func (m *Model) HasErrors() bool {
	return false
}

// isTerminalTaskState reports whether a task's current state means its container
// has exited — i.e. the task is "stopped" and its logs are old history. Starting
// states (new/pending/…/preparing/ready/starting) and running are NOT terminal.
func isTerminalTaskState(state swarm.TaskState) bool {
	switch state {
	case swarm.TaskStateComplete, swarm.TaskStateShutdown, swarm.TaskStateFailed,
		swarm.TaskStateRejected, swarm.TaskStateOrphaned, swarm.TaskStateRemove:
		return true
	default:
		return false
	}
}

// stoppedTaskIDs returns the set of full task IDs for this service whose current
// state is terminal (container has exited). Returns nil when no snapshot is
// available (callers treat nil as "hide nothing" — fail open). The hide-stopped
// filter is a denylist over this set: lines from running, starting, and unknown
// (not-yet-in-snapshot) tasks stay visible. Does not take m.mu; callers already
// hold it.
func (m *Model) stoppedTaskIDs() map[string]bool {
	snap := m.deps.Snapshot.GetSnapshot()
	if snap == nil {
		return nil
	}
	stopped := make(map[string]bool)
	for _, task := range snap.Tasks {
		if task.ServiceID == m.ServiceEntry.ServiceID && isTerminalTaskState(task.Status.State) {
			stopped[task.ID] = true
		}
	}
	return stopped
}

// lineVisible reports whether the line at index i passes all active filters
// (node filter, "/" text filter, hide-stopped). Callers must hold m.mu and pass
// the precomputed stopped-task set (nil = don't apply the hide-stopped filter).
func (m *Model) lineVisible(i int, stopped map[string]bool) bool {
	// Anything the view itself put in the buffer is exempt from every filter.
	// A separator is the reader's mark of where they had read to, and a filter
	// that swallowed it would answer a keypress with nothing at all. A notice
	// is worse: the filters key off a node and a task a notice does not have,
	// so a reader filtered to one node would watch the stream die and be told
	// nothing. A notice is still matched by the "ctrl+f" search — that seeks
	// rather than hides, and finding one is no reason to hide the rest.
	if m.kindAt(i) != lineLog {
		return true
	}
	// node filter
	if m.nodeFilter != "" && (i >= len(m.lineNodes) || m.lineNodes[i] != m.nodeFilter) {
		return false
	}
	// app-level "/" text filter
	if m.filterQuery != "" && !matchesFilter(m.lines[i], m.filterQuery) {
		return false
	}
	// hide-stopped filter: hide only lines from tasks known to be terminal.
	// Unknown task IDs (not yet in the snapshot — e.g. a starting container)
	// fail open and stay visible.
	if m.hideStopped && stopped != nil {
		taskID := ""
		if i < len(m.lineTasks) {
			taskID = m.lineTasks[i]
		}
		// empty task ID => always visible (can't determine task state)
		if taskID != "" && stopped[taskID] {
			return false
		}
	}
	return true
}

// matchesFilter reports whether line passes the app-level "/" query. The query
// is one or more "|"-separated terms, and a line matches when it contains any
// of them, ignoring case. A leading "!" inverts that: it is the `grep -v` a
// reader reaches for to drop a health check's lines (#641). Matching runs on
// the text as read, not on the colour codes around the task prefix, which every
// line carries and which would otherwise match a "5" on every line. A query
// with no terms — a lone "!" while the rest is still being typed — hides
// nothing.
func matchesFilter(line, query string) bool {
	query, exclude := strings.CutPrefix(query, "!")
	text := strings.ToLower(ansi.Strip(line))
	hasTerm := false
	for _, term := range strings.Split(strings.ToLower(query), "|") {
		if term == "" {
			continue
		}
		if strings.Contains(text, term) {
			return !exclude
		}
		hasTerm = true
	}
	return !hasTerm || exclude
}

// extractUniqueNodes returns a sorted list of nodes where the service has running tasks
func (m *Model) extractUniqueNodes() []string {
	snapshotOps := m.deps.Snapshot
	snap := snapshotOps.GetSnapshot()
	if snap == nil {
		return []string{"All nodes"}
	}

	nodeMap := make(map[string]string) // nodeID -> hostname

	// Find all tasks for this service
	for _, task := range snap.Tasks {
		if task.ServiceID == m.ServiceEntry.ServiceID && task.DesiredState == swarm.TaskStateRunning {
			// Get the node hostname for this task
			for _, node := range snap.Nodes {
				if node.ID == task.NodeID {
					if node.Description.Hostname != "" {
						nodeMap[node.ID] = node.Description.Hostname
					}
					break
				}
			}
		}
	}

	// Convert to sorted slice of hostnames
	nodes := make([]string, 0, len(nodeMap))
	for _, hostname := range nodeMap {
		nodes = append(nodes, hostname)
	}
	sort.Strings(nodes)

	// Add "All nodes" option at the beginning
	return append([]string{"All nodes"}, nodes...)
}
