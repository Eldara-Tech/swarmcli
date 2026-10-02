// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	contextsview "github.com/Eldara-Tech/swarmcli/v2/views/contexts"
	systeminfoview "github.com/Eldara-Tech/swarmcli/v2/views/systeminfo"
)

// stubContexts points the drift check at values the test controls: what this
// session is pinned to, and what the config file says right now.
func stubContexts(t *testing.T, pinned, shell string) (setPinned, setShell func(string)) {
	t.Helper()
	origSession, origConfig := sessionContextFn, configFileContextFn
	origSet, origEnv := setSessionContextFn, envPinsContextFn
	t.Cleanup(func() {
		sessionContextFn, configFileContextFn = origSession, origConfig
		setSessionContextFn, envPinsContextFn = origSet, origEnv
	})

	sessionContextFn = func() (string, error) { return pinned, nil }
	configFileContextFn = func() (string, error) { return shell, nil }
	setSessionContextFn = func(name string) { pinned = name }
	envPinsContextFn = func() bool { return false }
	return func(s string) { pinned = s }, func(s string) { shell = s }
}

// driftOf runs one drift check and returns what it found.
func driftOf(t *testing.T) contextDriftMsg {
	t.Helper()
	msg, ok := checkContextDriftCmd()().(contextDriftMsg)
	require.True(t, ok)
	return msg
}

func TestCheckContextDrift_AgreementReportsNoDrift(t *testing.T) {
	stubContexts(t, "swarm-a", "swarm-a")
	require.Empty(t, driftOf(t).Shell)
}

func TestCheckContextDrift_ReportsTheContextTheConfigNowNames(t *testing.T) {
	stubContexts(t, "swarm-a", "swarm-b")
	require.Equal(t, "swarm-b", driftOf(t).Shell)
}

// A subprocess that fails for a moment must not raise a dialog over the
// operator's screen; the check runs again five seconds later.
func TestCheckContextDrift_AFailedLookupIsNotDrift(t *testing.T) {
	origSession, origConfig := sessionContextFn, configFileContextFn
	t.Cleanup(func() { sessionContextFn, configFileContextFn = origSession, origConfig })

	sessionContextFn = func() (string, error) { return "swarm-a", nil }
	configFileContextFn = func() (string, error) { return "", errors.New("docker not running") }
	require.Empty(t, driftOf(t).Shell)

	configFileContextFn = func() (string, error) { return "swarm-b", nil }
	sessionContextFn = func() (string, error) { return "", errors.New("docker not running") }
	require.Empty(t, driftOf(t).Shell)
}

// DOCKER_CONTEXT is resolved ahead of ~/.docker/config.json, so while it is set
// the two naming different contexts is the documented arrangement — and the
// context switcher would refuse the move anyway. Prompting at every startup
// that uses the variable, including the DinD test flow, would be pure noise.
func TestCheckContextDrift_TheEnvironmentPinIsNotDrift(t *testing.T) {
	stubContexts(t, "from-env", "from-config")
	envPinsContextFn = func() bool { return true }

	require.Empty(t, driftOf(t).Shell)
}

func TestHandleContextDrift_RaisesThePrompt(t *testing.T) {
	m := newTestAppModel(&stubView{})
	stubContexts(t, "swarm-a", "swarm-b")

	require.Nil(t, m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"}))

	require.True(t, m.contextDriftDialogActive)
	require.True(t, m.contextDriftDialog.Visible)
	require.Equal(t, "swarm-b", m.pendingDriftContext)
	// Both names, or "the context changed" is not actionable.
	require.Contains(t, m.contextDriftDialog.Message, "swarm-a")
	require.Contains(t, m.contextDriftDialog.Message, "swarm-b")
}

// Declining is remembered, or an operator keeping two terminals on two swarms
// is asked again every five seconds.
func TestHandleContextDrift_DecliningSuppressesTheSameTarget(t *testing.T) {
	m := newTestAppModel(&stubView{})
	stubContexts(t, "swarm-a", "swarm-b")

	m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"})
	m.resolveContextDrift(false)

	require.False(t, m.contextDriftDialogActive)
	require.Equal(t, "swarm-b", m.declinedDriftContext)

	m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"})
	require.False(t, m.contextDriftDialogActive, "the same target must not be offered twice")
}

// Declining one context is not declining every context.
func TestHandleContextDrift_ADifferentTargetIsStillOffered(t *testing.T) {
	m := newTestAppModel(&stubView{})
	stubContexts(t, "swarm-a", "swarm-b")

	m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"})
	m.resolveContextDrift(false)

	m.handleContextDrift(contextDriftMsg{Shell: "swarm-c"})
	require.True(t, m.contextDriftDialogActive)
	require.Equal(t, "swarm-c", m.pendingDriftContext)
}

// Switching the shell back and away again is a new decision, not the one that
// was already declined.
func TestHandleContextDrift_ReturningToThePinClearsTheRefusal(t *testing.T) {
	m := newTestAppModel(&stubView{})
	stubContexts(t, "swarm-a", "swarm-b")

	m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"})
	m.resolveContextDrift(false)

	m.handleContextDrift(contextDriftMsg{}) // back in agreement
	require.Empty(t, m.declinedDriftContext)

	m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"})
	require.True(t, m.contextDriftDialogActive)
}

// A dialog drawn on top of the one the user is reading is worse than one that
// arrives five seconds later.
func TestHandleContextDrift_WaitsForTheScreen(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Model)
	}{
		{"an app error is up", func(m *Model) { m.appErrorDialogActive = true }},
		{"the unlock dialog is up", func(m *Model) { m.unlockDialogActive = true }},
		{"the update notice is up", func(m *Model) { m.updateDialogActive = true }},
		{"the prompt is already up", func(m *Model) { m.contextDriftDialogActive = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestAppModel(&stubView{})
			stubContexts(t, "swarm-a", "swarm-b")
			tc.set(m)
			before := m.contextDriftDialog.Visible

			m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"})

			require.Equal(t, before, m.contextDriftDialog.Visible)
			require.Empty(t, m.pendingDriftContext)
		})
	}
}

// Accepting moves the pin and drops what was built for the context being left.
func TestResolveContextDrift_AcceptingMovesThePinAndReloads(t *testing.T) {
	m := newTestAppModel(&stubView{})
	stubContexts(t, "swarm-a", "swarm-b")

	m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"})
	cmd := m.resolveContextDrift(true)

	require.NotNil(t, cmd, "the switch has to reload the cluster")
	pinned, err := sessionContextFn()
	require.NoError(t, err)
	require.Equal(t, "swarm-b", pinned, "the pin moves to the context being entered")
	require.False(t, m.contextDriftDialogActive)
	require.Empty(t, m.pendingDriftContext)
	require.Empty(t, m.declinedDriftContext)
	require.Equal(t, "swarm-a", m.previousContext, "kept so a failed load can revert")
}

// A switch made in the contexts view wrote ~/.docker/config.json, so undoing it
// has to write it back. One accepted at the drift prompt did not.
func TestEnterContext_RecordsWhetherARevertMayWriteTheConfigFile(t *testing.T) {
	m := newTestAppModel(&stubView{})
	stubContexts(t, "swarm-a", "swarm-b")

	m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"})
	m.resolveContextDrift(true)
	require.False(t, m.revertWritesConfig, "the operator's own switch is not ours to take back")

	m.Update(contextsview.ContextChangedNotification{PreviousContext: "swarm-a"})
	require.True(t, m.revertWritesConfig, "swarmcli wrote the config file, so it reverts it")
}

// A context accepted at the prompt but unable to load must put the session back
// where it was, and stop offering the context that failed — otherwise the
// prompt returns on the next tick, and every tick after that.
func TestSnapshotFailureAfterDrift_RevertsThePinAndStopsAsking(t *testing.T) {
	m := newTestAppModel(&stubView{})

	docker.SetSessionContext("swarm-a")
	t.Cleanup(docker.ResetSessionContext)
	m.previousContext = "swarm-a"
	m.revertWritesConfig = false
	docker.SetSessionContext("swarm-b") // what accepting the prompt did

	m.Update(snapshotLoadedMsg{Err: errors.New("cannot connect to the docker daemon")})

	pinned, err := docker.SessionContext()
	require.NoError(t, err)
	require.Equal(t, "swarm-a", pinned, "the session goes back to the context that worked")
	require.Equal(t, "swarm-b", m.declinedDriftContext, "the failed context is not offered again")
	require.Empty(t, m.previousContext)
}

// The prompt is a y/n question. An info-mode dialog would close on any key and
// report Confirmed:false, so "switch" would be unreachable.
func TestContextDriftPrompt_IsAConfirmation(t *testing.T) {
	m := newTestAppModel(&stubView{})
	stubContexts(t, "swarm-a", "swarm-b")
	m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"})

	require.False(t, m.contextDriftDialog.InfoMode)
	require.False(t, m.contextDriftDialog.ErrorMode)

	cmd := m.contextDriftDialog.Update(runeKey('y'))
	require.NotNil(t, cmd)
	res, ok := cmd().(confirmdialog.ResultMsg)
	require.True(t, ok)
	require.True(t, res.Confirmed)
}

// While the prompt is up it owns the keyboard, or "y" reaches the view behind
// it and does something else entirely.
func TestContextDriftPrompt_TakesTheKeyboard(t *testing.T) {
	v := &hidingStubView{}
	m := newTestAppModel(v)
	stubContexts(t, "swarm-a", "swarm-b")
	m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"})

	m.handleKey(runeKey('n'))

	require.Empty(t, v.received, "the view behind the prompt must not see the answer")
}

// The guard runs both ways: an update notice drawn over the drift prompt would
// take the keyboard from a question the operator is in the middle of, and leave
// the prompt visible underneath it.
func TestUpdateNotice_WaitsForTheContextDriftPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // redirect the settings file
	m := newTestAppModel(&stubView{})
	stubContexts(t, "swarm-a", "swarm-b")
	m.handleContextDrift(contextDriftMsg{Shell: "swarm-b"})

	m.Update(systeminfoview.LatestVersionMsg{LatestVersion: "v1.9.0"})

	require.False(t, m.updateDialogActive, "the drift prompt owns the screen")
	require.True(t, m.contextDriftDialogActive)
}

// TestHandleTick_ReArms — the timer armed in Init fired once and stopped,
// which froze the header and would have made the drift check a one-shot.
func TestHandleTick_ReArms(t *testing.T) {
	orig := tickInterval
	tickInterval = time.Millisecond
	t.Cleanup(func() { tickInterval = orig })

	m := newTestAppModel(&stubView{})
	_, cmd := m.handleTick(tickMsg(time.Now()))
	require.NotNil(t, cmd)

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 3, "re-arm, header refresh, drift check")
	// tea.Batch keeps the order it was given, and the re-arm is first.
	_, isTick := batch[0]().(tickMsg)
	require.True(t, isTick, "the tick must re-arm itself")
}

// TestHandleContextDrift_AnInconclusiveCheckKeepsTheRefusal is the regression
// guard for a check that could not be made being read as agreement.
//
// A failed lookup and an environment pin both used to arrive as an empty Shell,
// which handleContextDrift treats as "back in agreement" — so it cleared the
// record of a drift the operator had already declined, and the next successful
// check re-raised the prompt they had just answered.
func TestHandleContextDrift_AnInconclusiveCheckKeepsTheRefusal(t *testing.T) {
	for _, tc := range []struct {
		name         string
		inconclusive func()
	}{
		{"the lookup failed", func() {
			configFileContextFn = func() (string, error) { return "", errors.New("config unreadable") }
		}},
		{"the environment pins the context", func() {
			envPinsContextFn = func() bool { return true }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestAppModel(&stubView{})
			stubContexts(t, "swarm-a", "swarm-b")
			workingConfig, workingEnv := configFileContextFn, envPinsContextFn

			// The operator is asked once, and declines.
			m.handleContextDrift(driftOf(t))
			m.resolveContextDrift(false)
			require.Equal(t, "swarm-b", m.declinedDriftContext)

			// A tick the check cannot answer says nothing either way.
			tc.inconclusive()
			require.True(t, driftOf(t).Inconclusive)
			m.handleContextDrift(driftOf(t))
			require.Equal(t, "swarm-b", m.declinedDriftContext,
				"a check that could not be made is not agreement")

			// The check recovers, and the answer still stands.
			configFileContextFn, envPinsContextFn = workingConfig, workingEnv
			m.handleContextDrift(driftOf(t))
			require.False(t, m.contextDriftDialogActive,
				"the operator already answered this prompt")
		})
	}
}

// Agreement is still agreement: only an inconclusive check is exempt from
// clearing the refusal, or declining one drift would silence that target
// forever.
func TestCheckContextDrift_AgreementIsNotInconclusive(t *testing.T) {
	stubContexts(t, "swarm-a", "swarm-a")

	msg := driftOf(t)
	require.Empty(t, msg.Shell)
	require.False(t, msg.Inconclusive)
}

// measuredHeader gives the model a header that has finished its first CPU/MEM
// round, so a test can see whether a context change puts the spinner back.
func measuredHeader(t *testing.T, m *Model) {
	t.Helper()
	m.systemInfo = systeminfoview.New(docker.Deps{ClusterInfo: stubClusterInfo{}}, "test", "ce")
	m.systemInfo.Update(m.systemInfo.LoadSlowStatus()())
	require.Contains(t, m.systemInfo.View(), "0%")
}

// CPU/MEM describe the swarm being left, so entering another context shows the
// spinner until the new one is measured.
func TestEnterContext_ResetsTheResourceUsage(t *testing.T) {
	m := newTestAppModel(&stubView{})
	stubContexts(t, "swarm-a", "swarm-b")
	measuredHeader(t, m)

	m.Update(contextsview.ContextChangedNotification{PreviousContext: "swarm-a"})

	require.NotContains(t, m.systemInfo.View(), "0%")
}

// A revert after a failed load moves the session again; whatever was measured
// in between belongs to the context that failed.
func TestSnapshotFailure_RevertResetsTheResourceUsage(t *testing.T) {
	m := newTestAppModel(&stubView{})
	docker.SetSessionContext("swarm-b")
	t.Cleanup(docker.ResetSessionContext)
	m.previousContext = "swarm-a"
	measuredHeader(t, m)

	m.Update(snapshotLoadedMsg{Err: errors.New("cannot connect to the docker daemon")})

	require.NotContains(t, m.systemInfo.View(), "0%")
}
