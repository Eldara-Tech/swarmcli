// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"fmt"
	"strings"
)

const (
	installDocsURL         = "https://swarmcli.io/docs/cli#installation"
	installDocsURLBusiness = "https://swarmcli.io/docs/cli#installation-business"
	updateNoticeCheckbox   = "Do not show this again for this version"
)

// BusinessEditionActive reports whether Business Edition is effectively active.
// The update notice uses it to decide whether to show the "try Business
// Edition" hint. It defaults to the static build edition flag, so the CE binary
// always reports false (and shows the hint). BE overrides it from its live
// license state — so an unlicensed BE binary, which already presents as
// Community Edition throughout the UI, also shows the hint, while a licensed
// one suppresses it.
var BusinessEditionActive = func() bool { return edition == "be" }

// showUpdateNotice raises the app-level "update available" dialog for the given
// latest release. Edition selects whether the CE→BE hint is shown. The dialog
// is an info-mode confirmdialog carrying an opt-out checkbox; ResultMsg
// handling persists the dismissal when it is ticked.
func (m *Model) showUpdateNotice(latest string) {
	m.updateDialog.Visible = true
	m.updateDialog.ErrorMode = false
	m.updateDialog.InfoMode = true
	m.updateDialog.CheckboxLabel = updateNoticeCheckbox
	m.updateDialog.CheckboxChecked = false
	m.updateDialog.Message = updateNoticeMessage(latest)
	m.updateDialogActive = true
	m.pendingUpdateVersion = latest
}

// updateNoticeMessage builds the notice body. Every edition gets the same "a new
// version is available" copy and the same install link — which edition a binary
// runs as is not what someone updating it needs to read. Only the subtle
// one-line BE upsell is conditional: it is shown to CE, and to an unlicensed BE
// presenting as Community Edition, but not to a licensed BE.
func updateNoticeMessage(latest string) string {
	current := strings.TrimSpace(version)
	if current == "" {
		current = "unknown"
	}
	msg := fmt.Sprintf(
		"A new version of SwarmCLI is available: %s (you have %s).\n\n"+
			"Update: %s",
		latest, current, installDocsURL)
	if BusinessEditionActive() {
		return msg
	}
	return msg + fmt.Sprintf(
		"\n\nNeed RBAC, SSO & port-forwarding? Try Business Edition →\n%s",
		installDocsURLBusiness)
}
