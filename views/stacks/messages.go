// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"time"
)

type Msg struct {
	NodeID string
	Stacks []docker.StackEntry
	Err    error
}

type RefreshErrorMsg struct {
	Err error
}

type TickMsg struct{ Gen uint64 }

// PollRetryMsg signals that polling found no changes; the Update handler
// should schedule the next tick.
type PollRetryMsg struct{}

// PollInterval is how often the view re-reads its resource. It is a var, not a
// const, so tests can shrink it: a tea.Tick cmd invoked synchronously blocks
// for the full interval, so a test that runs one to see what it scheduled would
// otherwise sit here for five seconds.
var PollInterval = 5 * time.Second

// SpinnerTickMsg drives the deploy spinner and the toast expiry.
type SpinnerTickMsg time.Time

// spinnerTickInterval is a var, not a const, so tests can shrink it: a tea.Tick
// cmd invoked synchronously blocks for the full interval.
var spinnerTickInterval = 80 * time.Millisecond

// StackTasksLoadedMsg is sent when tasks for a stack are loaded
type StackTasksLoadedMsg struct {
	StackName string
	Tasks     []docker.TaskEntry
	Error     error
}

// RemoveErrorMsg is sent when stack removal fails
type RemoveErrorMsg struct {
	StackName string
	Error     error
}

// StackDeleteIntentMsg carries the result of checking, when a stack delete is
// requested, whether that stack belongs to a chart release. ChartRelease is
// non-empty when it does, so the confirm dialog can warn before removal.
type StackDeleteIntentMsg struct {
	StackName    string
	ChartRelease string
}

// ChartJumpMsg carries the result of checking, when "g" is pressed, whether the
// selected stack belongs to a chart release. ChartRelease is non-empty when it
// does, and the view then navigates to the charts browser on that release.
type ChartJumpMsg struct {
	StackName    string
	ChartRelease string
}

// editorContentMsg is sent when editor returns content
type editorContentMsg struct {
	// StackName is the stack an edit was opened on, bound when its editor
	// opened; empty for a new stack's content.
	StackName       string
	Content         string
	OriginalContent string // populated in edit mode to detect no-change
}

// stackDeployedMsg is sent when a deploy — create or redeploy — returns
// successfully. It is the only signal that clears the deploying state: a plain
// Msg also arrives from the Docker events the deploy itself raises, so clearing
// on that would drop the indicator at the first service creation.
type stackDeployedMsg struct {
	StackName string
	// KeptEmpty names the variables the redeploy of an edit withheld from the
	// docker CLI so that they stayed empty.
	KeptEmpty []string
}

// stackCreateErrorMsg is sent when stack creation has an error
type stackCreateErrorMsg struct {
	Err error
}

// stackUpdateErrorMsg is sent when stack update (edit) has an error
type stackUpdateErrorMsg struct {
	StackName string
	Err       error
}

// filesLoadedMsg is sent when files are loaded from a directory
type filesLoadedMsg struct {
	Path  string
	Files []string
	Error error
}

// stackSavedMsg is sent when stack YAML is successfully saved to file
type stackSavedMsg struct {
	Path string
}

// stackSaveErrorMsg is sent when saving stack YAML fails
type stackSaveErrorMsg struct {
	Err error
}
