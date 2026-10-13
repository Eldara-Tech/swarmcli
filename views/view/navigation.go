// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package view

type NavigateToMsg struct {
	ViewName string
	Payload  any // Can be service ID, stack ID, etc.
	// Replace indicates whether the target view should replace the current
	// view (i.e., not be pushed onto the navigation stack). When false,
	// the view manager should push the new view onto the history stack.
	Replace bool
}

// AppErrorMsg signals that a view (or the app layer) encountered a loading
// error. The app shows a modal error dialog. On dismiss it navigates to
// FallbackView (if non-empty) or calls goBack().
type AppErrorMsg struct {
	Error        string
	FallbackView string
}

// AppInfoMsg signals that a view (or the app layer) wants to show an
// informational notice — the AppErrorMsg counterpart for non-error content.
// The app shows the same modal styled as a neutral "Info" notice (not an
// error). On dismiss it navigates to FallbackView (if non-empty) or calls
// goBack(). Generic extension point: senders decide what warrants a notice.
type AppInfoMsg struct {
	Message      string
	FallbackView string
}

// GoBackMsg requests the app to pop the current view and return to the previous one.
type GoBackMsg struct{}

// OpenUnlockDialogMsg requests the app to open the swarm unlock-key dialog.
type OpenUnlockDialogMsg struct{}

// OpenConnectAppMsg requests the app to open the popup that connects the
// SwarmCLI phone app (the "c" key and :connect).
type OpenConnectAppMsg struct{}

// OpenUpdateDialogMsg requests the app to force-show the "update available"
// notice on demand (the dev-only :dev-update command), bypassing the
// per-version dismissal. Version overrides the displayed latest version when
// set; otherwise the app falls back to the last checked version or a stub.
type OpenUpdateDialogMsg struct {
	Version string
}
