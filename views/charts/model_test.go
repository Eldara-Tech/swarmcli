// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package chartsview

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	inspectview "github.com/Eldara-Tech/swarmcli/v2/views/inspect"
	servicesview "github.com/Eldara-Tech/swarmcli/v2/views/services"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// --- mocks ---

type mockOps struct {
	allRevisionsFn  func(ctx context.Context) (map[string][]charts.Release, error)
	serviceStatesFn func(ctx context.Context, release string) []charts.ServiceState
	// indexes is the cached repository state, keyed by chart name → newest
	// version. Nil means no index at all, which is a different answer from an
	// index in which nothing is newer.
	indexes map[string]string
}

func (m *mockOps) AllRevisions(ctx context.Context) (map[string][]charts.Release, error) {
	return m.allRevisionsFn(ctx)
}

func (m *mockOps) ServiceStates(ctx context.Context, release string) []charts.ServiceState {
	if m.serviceStatesFn == nil {
		return nil
	}
	return m.serviceStatesFn(ctx, release)
}

// Available answers through the real charts.Available, over an index built
// from the fixture. Mirroring its rules by hand is how a fake comes to disagree
// with production — version comparison in particular is not string equality.
func (m *mockOps) Available(rels []charts.Release) (map[string]charts.Availability, bool) {
	if m.indexes == nil {
		return nil, false
	}
	entries := make(map[string][]charts.IndexEntry, len(m.indexes))
	for chart, version := range m.indexes {
		entries[chart] = []charts.IndexEntry{{Name: chart, Version: version}}
	}
	index := map[string]*charts.Index{"fixture": {APIVersion: "v1", Entries: entries}}
	return charts.Available(rels, nil, index), true
}

func noopOps() *mockOps {
	return &mockOps{
		allRevisionsFn: func(_ context.Context) (map[string][]charts.Release, error) {
			return nil, nil
		},
	}
}

// --- helpers ---

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func testModel(opts ...func(*Model)) *Model {
	m := New(120, 24)
	m.ops = noopOps()
	for _, o := range opts {
		o(m)
	}
	return m
}

// rev builds one stored revision.
func rev(name string, n int, status, chartName, version string) charts.Release {
	return charts.Release{
		Name:     name,
		Revision: n,
		Status:   status,
		Chart:    charts.ReleaseChart{Name: chartName, Version: version},
		Manifest: fmt.Sprintf("services:\n  app:\n    image: %s:%s\n", chartName, version),
		Values:   map[string]any{"replicas": n},
		Created:  time.Date(2026, 8, 10+n, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
}

// deployed is a single-revision release in the ordinary healthy shape.
func deployed(name, chartName, version string) []charts.Release {
	return []charts.Release{rev(name, 1, charts.StatusDeployed, chartName, version)}
}

// converged is a service that has held parity past its stability window.
func converged(name string) charts.ServiceState {
	return charts.ServiceState{
		Name: name, Mode: "replicated", Replicas: "1/1", Status: "running",
		Running: 1, Desired: 1, NewestTaskAge: time.Hour,
	}
}

// loadReleases drives a completed read into the model, skipping the async load.
// The optional index maps chart name → newest published version; omitting it
// means this machine has no cached repository index at all.
func loadReleases(t *testing.T, m *Model, all map[string][]charts.Release, svcs map[string][]charts.ServiceState, indexes ...map[string]string) {
	t.Helper()
	var index map[string]string
	if len(indexes) > 0 {
		index = indexes[0]
	}
	m.ops = &mockOps{
		allRevisionsFn: func(_ context.Context) (map[string][]charts.Release, error) { return all, nil },
		serviceStatesFn: func(_ context.Context, release string) []charts.ServiceState {
			return svcs[release]
		},
		indexes: index,
	}
	msg := runCmd(m.loadReleasesCmd())
	loaded, ok := msg.(ReleasesLoadedMsg)
	require.True(t, ok, "expected ReleasesLoadedMsg, got %T", msg)
	require.NoError(t, loaded.Err)
	m.Update(loaded)
}

// fastPoll shrinks the poll interval for a test that executes a returned tick
// cmd. tea.Tick sleeps, so running one at the real interval stalls the suite.
func fastPoll(t *testing.T) {
	t.Helper()
	original := PollInterval
	PollInterval = time.Millisecond
	t.Cleanup(func() { PollInterval = original })
}

func names(m *Model) []string {
	out := make([]string, 0, len(m.list.Filtered))
	for _, r := range m.list.Filtered {
		out = append(out, r.Name)
	}
	return out
}

// --- Tests ---

func TestNewStartsLoading(t *testing.T) {
	m := testModel()
	require.Equal(t, stateLoading, m.state)
	require.Equal(t, ViewName, m.Name())
	require.NotNil(t, m.list.Items, "a nil item slice makes the renderer skip padding")
}

func TestLoadSortsByNameAndDerivesHealth(t *testing.T) {
	m := testModel()
	loadReleases(t, m,
		map[string][]charts.Release{
			"zebra": deployed("zebra", "cz", "1.0.0"),
			"alpha": deployed("alpha", "ca", "2.0.0"),
		},
		map[string][]charts.ServiceState{
			"alpha": {converged("alpha_web")},
			// zebra reports no services: still rolling out.
		})

	require.Equal(t, stateReady, m.state)
	require.Equal(t, []string{"alpha", "zebra"}, names(m))
	require.Equal(t, charts.PhaseConverged, m.list.Filtered[0].Health.Phase)
	require.Equal(t, charts.PhaseProgressing, m.list.Filtered[1].Health.Phase)
	require.Equal(t, "ca-2.0.0", m.list.Filtered[0].chartRef())
}

// The uninstalled record AllRevisions deliberately keeps is history, not an
// installed release, so the browser must not list it.
func TestUninstalledReleasesAreNotListed(t *testing.T) {
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{
		"live": deployed("live", "cl", "1.0.0"),
		"gone": {rev("gone", 1, charts.StatusUninstalled, "cg", "1.0.0")},
	}, nil)

	require.Equal(t, []string{"live"}, names(m))
}

// HEALTH answers "what is the swarm doing"; it is meaningless for a record that
// never got as far as a rollout, and Rollup would call that "progressing".
func TestHealthOnlyAppliesToADeployedRecord(t *testing.T) {
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{
		"broken":  {rev("broken", 1, charts.StatusFailed, "cb", "1.0.0")},
		"pending": {rev("pending", 1, charts.StatusPendingInstall, "cp", "1.0.0")},
	}, nil)

	for _, it := range m.list.Filtered {
		require.False(t, it.HasHealth, "release %q must not carry a health phase", it.Name)
		require.Equal(t, "—", it.healthLabel())
	}
}

func TestHealthSurfacesAWedgedRollout(t *testing.T) {
	m := testModel()
	loadReleases(t, m,
		map[string][]charts.Release{"app": deployed("app", "ca", "1.0.0")},
		map[string][]charts.ServiceState{"app": {{
			Name: "app_web", Running: 1, Desired: 1,
			UpdateState: "rollback_paused", NewestTaskAge: time.Hour,
		}}})

	sel, ok := m.selected()
	require.True(t, ok)
	require.Equal(t, charts.PhaseWedged, sel.Health.Phase)
	require.Equal(t, "wedged", sel.healthLabel())
	require.Contains(t, m.View(), "rollback paused",
		"the view must say why, not just that something is wrong")
	require.Equal(t, charts.StatusDeployed, sel.status(),
		"the stored record still reads deployed — that divergence is the point")
}

func TestFilterMatchesNameAndChart(t *testing.T) {
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{
		"alpha": deployed("alpha", "traefik", "1.0.0"),
		"beta":  deployed("beta", "whoami", "1.0.0"),
	}, nil)

	m.ApplySearchQuery("whoami")
	require.Equal(t, []string{"beta"}, names(m), "the query must match the chart, not only the name")

	m.ApplySearchQuery("alph")
	require.Equal(t, []string{"alpha"}, names(m))

	m.ClearSearchQuery()
	require.Len(t, m.list.Filtered, 2)
	require.Equal(t, 0, m.list.Cursor)
}

// Ascending health puts the release worth looking at first.
func TestSortByHealthIsWorstFirst(t *testing.T) {
	m := testModel()
	loadReleases(t, m,
		map[string][]charts.Release{
			"ok":      deployed("ok", "c", "1.0.0"),
			"slow":    deployed("slow", "c", "1.0.0"),
			"stuck":   deployed("stuck", "c", "1.0.0"),
			"unknown": {rev("unknown", 1, charts.StatusFailed, "c", "1.0.0")},
		},
		map[string][]charts.ServiceState{
			"ok":   {converged("ok_web")},
			"slow": {{Name: "slow_web", Running: 1, Desired: 3, NewestTaskAge: time.Hour}},
			"stuck": {{
				Name: "stuck_web", Running: 1, Desired: 1,
				UpdateState: "paused", NewestTaskAge: time.Hour,
			}},
		})

	m.Update(key("H"))
	require.Equal(t, []string{"stuck", "slow", "ok", "unknown"}, names(m))

	m.Update(key("H")) // repeat toggles direction
	require.Equal(t, []string{"unknown", "ok", "slow", "stuck"}, names(m))
}

func TestSortByRevisionAndUpdated(t *testing.T) {
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{
		"one": {rev("one", 1, charts.StatusDeployed, "c", "1.0.0")},
		"two": {
			rev("two", 1, charts.StatusSuperseded, "c", "1.0.0"),
			rev("two", 2, charts.StatusDeployed, "c", "2.0.0"),
		},
	}, nil)

	m.Update(key("R"))
	require.Equal(t, []string{"one", "two"}, names(m))
	m.Update(key("R"))
	require.Equal(t, []string{"two", "one"}, names(m))

	// rev() dates each revision later than the last, so "two" is the newer.
	m.Update(key("U"))
	require.Equal(t, []string{"one", "two"}, names(m))
	m.Update(key("U"))
	require.Equal(t, []string{"two", "one"}, names(m))
}

// The row carries the current revision, so the release's chart columns and the
// manifest shown by `i` come from the same record.
func TestInspectOpensTheCurrentRevisionsManifest(t *testing.T) {
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{
		"app": {
			rev("app", 1, charts.StatusSuperseded, "c", "1.0.0"),
			rev("app", 2, charts.StatusDeployed, "c", "2.0.0"),
		},
	}, nil)

	msg := runCmd(m.Update(key("i")))
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok, "expected navigation, got %T", msg)
	require.Equal(t, inspectview.ViewName, nav.ViewName)

	payload := nav.Payload.(map[string]any)
	require.Contains(t, payload["title"], "rev 2")
	require.Contains(t, payload["json"], "c:2.0.0", "the manifest must be the current revision's")
	require.Equal(t, inspectview.FormatYAML, payload["format"])
}

func TestValuesOpensStoredValues(t *testing.T) {
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{"app": deployed("app", "c", "1.0.0")}, nil)

	payload := runCmd(m.Update(key("v"))).(view.NavigateToMsg).Payload.(map[string]any)
	require.Contains(t, payload["title"], "values")
	require.Contains(t, payload["json"], "replicas: 1")
}

func TestValuesSaysSoWhenTheRevisionStoredNone(t *testing.T) {
	m := testModel()
	bare := rev("app", 1, charts.StatusDeployed, "c", "1.0.0")
	bare.Values = nil
	loadReleases(t, m, map[string][]charts.Release{"app": {bare}}, nil)

	payload := runCmd(m.Update(key("v"))).(view.NavigateToMsg).Payload.(map[string]any)
	require.Contains(t, payload["json"], "stored no values")
	require.Equal(t, inspectview.FormatRaw, payload["format"])
}

func TestServicesDrillDownScopesToTheReleaseStack(t *testing.T) {
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{"app": deployed("app", "c", "1.0.0")}, nil)

	nav := runCmd(m.Update(key("s"))).(view.NavigateToMsg)
	require.Equal(t, servicesview.ViewName, nav.ViewName)
	require.Equal(t, map[string]any{"stackName": "app"}, nav.Payload)
}

func TestKeysOnAnEmptyListDoNotPanic(t *testing.T) {
	m := testModel()
	loadReleases(t, m, nil, nil)
	for _, k := range []string{"i", "v", "s", "up", "down", "pgup", "pgdown", "N", "H"} {
		require.Nil(t, runCmd(m.Update(key(k))), "key %q on an empty list", k)
	}
}

// The "?" key is routed by the app, not by this view — see app.Model.openHelp
// and its tests. What the view still owns is the content the app asks it for.
func TestHelpContent(t *testing.T) {
	m := testModel()
	require.NotEmpty(t, m.HelpContent())
}

// A read failure with good data on screen must not blank the view: one
// unreadable record fails the whole listing, and that must not cost the
// operator everything the last poll showed.
func TestRefreshFailureKeepsTheLastGoodData(t *testing.T) {
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{"app": deployed("app", "c", "1.0.0")}, nil)

	m.Update(ReleasesLoadedMsg{Err: errors.New("decode release config 'x': boom")})

	require.Equal(t, stateReady, m.state)
	require.Equal(t, []string{"app"}, names(m))
	require.False(t, m.errorDialogActive)
	require.Contains(t, m.baseFooter(), "Refresh failed")
}

func TestFirstLoadFailureShowsTheError(t *testing.T) {
	m := testModel()
	m.Update(ReleasesLoadedMsg{Err: errors.New("docker unreachable")})

	require.Equal(t, stateError, m.state)
	require.True(t, m.errorDialogActive)
	require.True(t, m.CapturesInput())

	m.Update(key("esc"))
	require.False(t, m.errorDialogActive)
}

// The poll gate must not fire on a reason that counts down in milliseconds, or
// it would report a change on every tick and never save anything.
func TestPollGateIgnoresACountingDownReason(t *testing.T) {
	within := charts.ServiceState{
		Name: "app_web", Running: 1, Desired: 1,
		Monitor: time.Hour, NewestTaskAge: time.Minute,
	}
	later := within
	later.NewestTaskAge = 2 * time.Minute

	build := func(s charts.ServiceState) []releaseItem {
		relRev := rev("app", 1, charts.StatusDeployed, "c", "1.0.0")
		conv, ok := health(relRev, []charts.ServiceState{s})
		return []releaseItem{{
			Name: "app", Revisions: []charts.Release{relRev},
			Health: conv, HasHealth: ok, Services: []charts.ServiceState{s},
		}}
	}

	a, b := build(within), build(later)
	require.NotEqual(t, a[0].Health.Reason, b[0].Health.Reason,
		"the fixture must actually differ, or this test proves nothing")
	require.Equal(t, stableReleases(a), stableReleases(b))
}

func TestPollGateNoticesAPhaseChange(t *testing.T) {
	relRev := rev("app", 1, charts.StatusDeployed, "c", "1.0.0")
	build := func(s charts.ServiceState) []releaseItem {
		conv, ok := health(relRev, []charts.ServiceState{s})
		return []releaseItem{{
			Name: "app", Revisions: []charts.Release{relRev},
			Health: conv, HasHealth: ok, Services: []charts.ServiceState{s},
		}}
	}
	healthy := build(converged("app_web"))
	wedged := build(charts.ServiceState{
		Name: "app_web", Running: 1, Desired: 1,
		UpdateState: "paused", NewestTaskAge: time.Hour,
	})
	require.NotEqual(t, stableReleases(healthy), stableReleases(wedged))
}

func TestCheckReportsNoChangeWhenNothingMoved(t *testing.T) {
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{"app": deployed("app", "c", "1.0.0")},
		map[string][]charts.ServiceState{"app": {converged("app_web")}})

	msg := runCmd(m.checkReleasesCmd(m.lastSnapshot))
	require.IsType(t, PollRetryMsg{}, msg)
}

func TestCheckReloadsWhenARevisionLands(t *testing.T) {
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{"app": deployed("app", "c", "1.0.0")}, nil)

	m.ops = &mockOps{allRevisionsFn: func(_ context.Context) (map[string][]charts.Release, error) {
		return map[string][]charts.Release{"app": {
			rev("app", 1, charts.StatusSuperseded, "c", "1.0.0"),
			rev("app", 2, charts.StatusDeployed, "c", "2.0.0"),
		}}, nil
	}}
	msg := runCmd(m.checkReleasesCmd(m.lastSnapshot))
	loaded, ok := msg.(ReleasesLoadedMsg)
	require.True(t, ok, "expected a reload, got %T", msg)
	require.Equal(t, 2, loaded.Releases[0].revision())
}

// A malformed timestamp must degrade to a dash, not to a misleading date.
func TestUnparseableCreatedRendersADash(t *testing.T) {
	m := testModel()
	odd := rev("app", 1, charts.StatusDeployed, "c", "1.0.0")
	odd.Created = "not-a-timestamp"
	loadReleases(t, m, map[string][]charts.Release{"app": {odd}}, nil)

	require.True(t, m.list.Filtered[0].Created.IsZero())
	require.Equal(t, "—", formatCreated(m.list.Filtered[0].Created))
}

// A tick must schedule exactly one successor, or the ticker multiplies and the
// poll rate doubles on every beat.
// drive models the bubbletea runtime for one round: run each pending command,
// flattening tea.Batch — whose inner commands the runtime runs, not the caller
// — feed every resulting message back into Update, and return the commands
// that came out. It reports how many TickMsgs fired during the round.
//
// Flattening is the whole reason this exists. Asserting on the command Update
// returns cannot see past a batch, so a test written that way passes whatever
// the batch contains.
func drive(m *Model, pending []tea.Cmd) (next []tea.Cmd, ticks int) {
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		msg := c()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				run(sub)
			}
			return
		}
		if _, ok := msg.(TickMsg); ok {
			ticks++
		}
		if cmd := m.Update(msg); cmd != nil {
			next = append(next, cmd)
		}
	}
	for _, c := range pending {
		run(c)
	}
	return next, ticks
}

// The poll loop must be a loop, not a tree. A tick that starts a poll and
// re-arms, while the poll's result re-arms too, yields two successors per beat
// — and each of those does the same, so an idle view climbs into thousands of
// concurrent reads within a minute.
func TestPollLoopDoesNotMultiply(t *testing.T) {
	fastPoll(t)
	m := testModel()
	// Steady state: the poll finds exactly what is loaded, so it reports no
	// change. That is what a browser left open does all day.
	all := map[string][]charts.Release{"app": deployed("app", "c", "1.0.0")}
	loadReleases(t, m, all, nil)

	sawRetry := false
	m.ops = &mockOps{allRevisionsFn: func(_ context.Context) (map[string][]charts.Release, error) {
		sawRetry = true
		return all, nil
	}}

	// The model already armed a tick when the load landed; this is that one.
	pending := []tea.Cmd{tickCmd(m.pollGen)}
	for round := 0; round < 12; round++ {
		var ticks int
		pending, ticks = drive(m, pending)
		require.LessOrEqual(t, len(pending), 1,
			"round %d: %d commands in flight — the loop has branched", round, len(pending))
		require.LessOrEqual(t, ticks, 1, "round %d fired %d ticks", round, ticks)
		time.Sleep(2 * time.Millisecond)
	}
	require.True(t, sawRetry, "the fixture must actually poll, or this proves nothing")
	require.Len(t, pending, 1, "and the loop must still be alive at the end")
}

// While the view is off screen or showing an error dialog, the ticker keeps
// beating but must not poll: the data would be thrown away, and a refresh
// would be fighting the dialog in front of it.
func TestTickDoesNotPollWhenHiddenOrDialogIsUp(t *testing.T) {
	fastPoll(t)
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{"app": deployed("app", "c", "1.0.0")}, nil)

	m.ops = &mockOps{allRevisionsFn: func(_ context.Context) (map[string][]charts.Release, error) {
		t.Error("the view must not poll while hidden or showing a dialog")
		return nil, nil
	}}

	for _, hide := range []func(){
		func() { m.SetVisible(false); m.errorDialogActive = false },
		func() { m.SetVisible(true); m.errorDialogActive = true },
	} {
		hide()
		m.tickScheduled = true // the chain the model is already running
		pending := []tea.Cmd{tickCmd(m.pollGen)}
		for round := 0; round < 3; round++ {
			pending, _ = drive(m, pending)
			require.Len(t, pending, 1, "the ticker must stay alive so it can resume")
			time.Sleep(2 * time.Millisecond)
		}
	}
}

// One unreadable release record fails the whole listing. A view that stopped
// polling on the first failure would stay blank until the operator navigated
// out and back in.
func TestPollingResumesAfterAFailedFirstLoad(t *testing.T) {
	fastPoll(t)
	m := testModel()

	m.Update(ReleasesLoadedMsg{Err: errors.New("docker unreachable")})
	require.Equal(t, stateError, m.state)
	require.True(t, m.errorDialogActive)

	// The operator dismisses the dialog; the daemon comes back.
	m.Update(key("esc"))
	all := map[string][]charts.Release{"app": deployed("app", "c", "1.0.0")}
	m.ops = &mockOps{allRevisionsFn: func(_ context.Context) (map[string][]charts.Release, error) {
		return all, nil
	}}

	pending := []tea.Cmd{tickCmd(m.pollGen)}
	for round := 0; round < 4 && m.state != stateReady; round++ {
		pending, _ = drive(m, pending)
		time.Sleep(2 * time.Millisecond)
	}

	require.Equal(t, stateReady, m.state, "the view must recover on its own")
	require.Equal(t, []string{"app"}, names(m))
	require.NoError(t, m.err)
}

func TestResizeKeepsTheViewportAnchoredOnFirstSize(t *testing.T) {
	m := testModel()
	loadReleases(t, m, map[string][]charts.Release{
		"a": deployed("a", "c", "1.0.0"),
		"b": deployed("b", "c", "1.0.0"),
	}, nil)

	m.list.Viewport.YOffset = 7
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	require.Equal(t, 0, m.list.Viewport.YOffset, "the first resize anchors to the top")
	require.Equal(t, 100, m.width)

	// Later resizes only re-anchor while the cursor is at the top, so a
	// scrolled list is not yanked back under the operator.
	m.Update(key("down"))
	m.list.Viewport.YOffset = 5
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	require.Equal(t, 5, m.list.Viewport.YOffset)
}

func TestCursorSurvivesAReloadThatReordersNothing(t *testing.T) {
	m := testModel()
	all := map[string][]charts.Release{
		"a": deployed("a", "c", "1.0.0"),
		"b": deployed("b", "c", "1.0.0"),
		"c": deployed("c", "c", "1.0.0"),
	}
	loadReleases(t, m, all, nil)
	m.Update(key("down"))
	require.Equal(t, "b", m.list.Filtered[m.list.Cursor].Name)

	loadReleases(t, m, all, nil)
	require.Equal(t, "b", m.list.Filtered[m.list.Cursor].Name,
		"a background refresh must not move the operator's selection")
}

// dismiss is the confirm dialog's own result message, which the app loop
// delivers back to the view after an esc.
func dismiss() confirmdialog.ResultMsg { return confirmdialog.ResultMsg{} }
