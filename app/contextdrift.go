// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
)

// The Docker context is pinned for the life of the session (docker.SessionContext),
// so a `docker context use` run in another terminal moves nothing here. That is
// the fix for #611 — but a switch the operator made deliberately should not be
// silently ignored either, so the app watches for the divergence and offers it.
//
// Watching, not following: the prompt is the only thing that moves the pin, and
// declining is remembered, so an operator who keeps two terminals on two swarms
// is asked once per target rather than every five seconds.

// contextDriftMsg reports the result of one drift check.
//
//	Shell != ""                    the config file names a different context
//	Shell == "" && !Inconclusive    the config file agrees with the session pin
//	Inconclusive                    the check could not be made, and says
//	                                nothing either way
//
// The third case has to be distinct from the second. Both used to arrive as an
// empty Shell, and handleContextDrift reads an empty Shell as "back in
// agreement" — which clears the record of a drift the operator has already
// declined. One transient lookup failure, or one tick while DOCKER_CONTEXT is
// set, was enough to forget it and re-raise a prompt that had been answered.
type contextDriftMsg struct {
	Shell        string
	Inconclusive bool
}

// Seams, so the drift logic is testable without a Docker CLI on PATH.
var (
	sessionContextFn    = docker.SessionContext
	configFileContextFn = docker.ConfigFileContext
	setSessionContextFn = docker.SetSessionContext
	envPinsContextFn    = docker.EnvPinsContext
)

// checkContextDriftCmd compares the context Docker would use now against the
// one this session is pinned to.
//
// A failed lookup is inconclusive, never drift: reading the config file can
// fail transiently, and a dialog raised over that would be worse than a check
// that quietly runs again five seconds later.
func checkContextDriftCmd() tea.Cmd {
	return func() tea.Msg {
		// DOCKER_CONTEXT outranks the config file, so while it is set the two
		// naming different contexts is the arrangement the operator asked for,
		// not a switch. Offering to follow it would also be offering something
		// the context switcher refuses to do.
		if envPinsContextFn() {
			return contextDriftMsg{Inconclusive: true}
		}
		pinned, err := sessionContextFn()
		if err != nil || pinned == "" {
			return contextDriftMsg{Inconclusive: true}
		}
		shell, err := configFileContextFn()
		if err != nil || shell == "" {
			return contextDriftMsg{Inconclusive: true}
		}
		if shell == pinned {
			return contextDriftMsg{}
		}
		return contextDriftMsg{Shell: shell}
	}
}

// handleContextDrift decides what one drift check means for the UI.
func (m *Model) handleContextDrift(msg contextDriftMsg) tea.Cmd {
	if msg.Inconclusive {
		// Nothing was learned, so nothing is revised — in particular the
		// declined record survives. Reading this as agreement is what used to
		// clear it, so the next successful check re-raised a prompt the
		// operator had already answered.
		return nil
	}
	if msg.Shell == "" {
		// Back in agreement. Clearing the record means a later switch to the
		// same context is offered again — the operator declined one drift, not
		// that context forever.
		m.declinedDriftContext = ""
		return nil
	}
	if msg.Shell == m.declinedDriftContext {
		return nil
	}
	if m.dialogActive() {
		// Try again on the next tick rather than stacking a second dialog on
		// top of one the user is reading.
		return nil
	}
	m.showContextDriftNotice(msg.Shell)
	return nil
}

// dialogActive reports whether something already owns the screen. The startup
// overlay is included: a BE proactive nudge is the first thing an operator
// sees, and a drift prompt drawn over it would be unreadable.
func (m *Model) dialogActive() bool {
	if startupOverlay != nil && startupOverlay.Active() {
		return true
	}
	return m.appErrorDialogActive ||
		m.unlockDialogActive ||
		m.updateDialogActive ||
		m.contextDriftDialogActive ||
		m.connectApp.Visible()
}

// showContextDriftNotice raises the confirm-mode dialog offering the switch.
func (m *Model) showContextDriftNotice(shell string) {
	pinned, _ := sessionContextFn()
	m.contextDriftDialog.Visible = true
	m.contextDriftDialog.ErrorMode = false
	m.contextDriftDialog.InfoMode = false
	m.contextDriftDialog.CheckboxLabel = ""
	m.contextDriftDialog.CheckboxChecked = false
	m.contextDriftDialog.Message = contextDriftMessage(pinned, shell)
	m.contextDriftDialogActive = true
	m.pendingDriftContext = shell
}

// contextDriftMessage builds the prompt body. It names both contexts in every
// line that matters: "the context changed" is not actionable without knowing
// which one is still being used.
func contextDriftMessage(pinned, shell string) string {
	return fmt.Sprintf(
		"The Docker context changed outside swarmcli: '%s' → '%s'.\n\n"+
			"swarmcli is still using '%s'. Switch to '%s'?\n\n"+
			"y — switch to '%s' and reload the cluster\n"+
			"n — keep using '%s' (swarmcli will not ask again for '%s')",
		pinned, shell, pinned, shell, shell, pinned, shell)
}

// resolveContextDrift applies the answer to the drift prompt.
func (m *Model) resolveContextDrift(confirmed bool) tea.Cmd {
	shell := m.pendingDriftContext
	m.contextDriftDialogActive = false
	m.contextDriftDialog.Visible = false
	m.pendingDriftContext = ""

	if !confirmed {
		m.declinedDriftContext = shell
		return nil
	}
	m.declinedDriftContext = ""

	previous, _ := sessionContextFn()
	setSessionContextFn(shell)
	// No `docker context use` here, unlike a switch made from the contexts
	// view: the config file already names shell — that is what drift means —
	// and for the same reason a revert must not write it back.
	return m.enterContext(previous, false)
}
