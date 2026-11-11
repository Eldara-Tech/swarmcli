// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// waveFile declares four releases across three waves, with the file order
// deliberately NOT the wave order: `web` is written first and belongs last, so a
// test asserting wave order cannot pass by accident on a plan that did nothing.
const waveFile = `repositories:
  - name: swarmcli-charts
    url: https://eldara-tech.github.io/swarmcli-charts
releases:
  - name: web
    chart: swarmcli-charts/demo
    version: "0.1.0"
    wave: 2
  - name: db
    chart: swarmcli-charts/demo
    version: "0.1.0"
  - name: migrate
    chart: swarmcli-charts/demo
    version: "0.1.0"
    wave: 1
  - name: api
    chart: swarmcli-charts/demo
    version: "0.1.0"
    wave: 2
`

// twoInOneWave is the case waves exist for: two releases that go out together.
const twoInOneWave = `repositories:
  - name: swarmcli-charts
    url: https://eldara-tech.github.io/swarmcli-charts
releases:
  - name: first
    chart: swarmcli-charts/demo
    version: "0.1.0"
  - name: second
    chart: swarmcli-charts/demo
    version: "0.1.0"
`

// settled is what a release that has finished rolling out looks like. Note that
// a release with NO states is Progressing rather than converged (Rollup calls it
// "no services are running yet"), so a barrier the test wants to pass has to be
// given these.
func settled(name string) []ServiceState {
	return []ServiceState{{Name: name, Running: 1, Desired: 1, NewestTaskAge: stableAge}}
}

// stalled is a release that will never converge: a task short of its target,
// with no update status, which is what a fresh install that cannot come up looks
// like. Not "wedged" — swarm only reports that for a rollout it has given up on,
// and a first install has no rollout to pause.
func stalled(name string) []ServiceState {
	return []ServiceState{{Name: name, Running: 0, Desired: 1, NewestTaskAge: stableAge}}
}

// quickPoll shrinks the convergence poll so a wave that never settles costs a
// unit test milliseconds instead of the wall-clock it would cost a swarm.
func quickPoll(t *testing.T) {
	t.Helper()
	prev := waitPollInterval
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = prev })
}

// The plan comes back in wave order, and within a wave in the order the file
// wrote them.
//
// Sorting here rather than grouping at each consumer is what makes plan order
// execution order everywhere at once — for Apply, for anything rendering a plan,
// and for a controller walking plan.Releases to correct drift.
func TestPlanApplySortsReleasesByWave(t *testing.T) {
	e, _, src, rf := applyEnv(t, waveFile, "0.1.0")

	plan, err := e.PlanApply(context.Background(), rf, src, PlanOptions{})
	require.NoError(t, err)

	require.Equal(t, []string{"db", "migrate", "web", "api"}, names(plan.Releases),
		"waves ascending, and within wave 2 the file's own order — web before api — is preserved")
	require.Equal(t, []int{0, 1, 2, 2}, waves(plan.Releases))
}

// A negative wave is legal and sorts first, which is how something is put in
// front of an existing set without renumbering every release after it.
func TestPlanApplySortsNegativeWavesFirst(t *testing.T) {
	body := `repositories:
  - name: swarmcli-charts
    url: https://eldara-tech.github.io/swarmcli-charts
releases:
  - name: app
    chart: swarmcli-charts/demo
    version: "0.1.0"
  - name: prereq
    chart: swarmcli-charts/demo
    version: "0.1.0"
    wave: -1
`
	e, _, src, rf := applyEnv(t, body, "0.1.0")

	plan, err := e.PlanApply(context.Background(), rf, src, PlanOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"prereq", "app"}, names(plan.Releases))
}

// A file that declares no wave plans in exactly the order it always did.
//
// This is the compatibility guard the whole feature rests on: every release file
// that exists today is a single-wave plan, and none of them may move.
func TestPlanApplyLeavesAnUndeclaredFileInFileOrder(t *testing.T) {
	e, _, src, rf := applyEnv(t, twoInOneWave, "0.1.0")

	plan, err := e.PlanApply(context.Background(), rf, src, PlanOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, names(plan.Releases))
	require.Equal(t, []int{0, 0}, waves(plan.Releases))
}

// Apply deploys wave by wave, and the assertion is on what reached the backend
// rather than on what Apply reports having done.
func TestApplyDeploysWaveByWave(t *testing.T) {
	quickPoll(t)
	e, fb, src, rf := applyEnv(t, waveFile, "0.1.0")
	for _, r := range []string{"db", "migrate", "web", "api"} {
		fb.services[r] = settled(r)
	}

	plan, err := e.PlanApply(context.Background(), rf, src, PlanOptions{})
	require.NoError(t, err)

	// A short timeout on every wave test, deliberately. A barrier that fires
	// when it should not has nothing to converge and would otherwise poll out
	// the five-minute default — and a test that hangs is far worse than one that
	// fails, because it reports as a suite-level timeout naming nothing.
	_, err = e.Apply(context.Background(), plan, InstallOptions{Timeout: 50 * time.Millisecond})
	require.NoError(t, err)
	require.Equal(t, []string{"db", "migrate", "web", "api"}, fb.deployOrder)

	// Two boundaries for three waves, one read each because everything had
	// already settled — and nothing after the last wave. Waiting there would
	// block an apply the caller asked not to block, for no later wave's benefit.
	require.Equal(t, 2, fb.stackServiceCalls,
		"a barrier belongs between waves and not after the final one")
}

// A wave that does not converge stops every wave after it: nothing later is
// deployed at all, and the results returned name only what was.
//
// This is the whole point of declaring a wave. A migration that fails must not
// let the application depending on it start, and "must not start" has to mean no
// service and no revision record rather than a service that was created and then
// reported as unhealthy.
func TestApplyStopsAtAWaveThatDoesNotConverge(t *testing.T) {
	quickPoll(t)
	e, fb, src, rf := applyEnv(t, waveFile, "0.1.0")
	fb.services["db"] = settled("db")
	fb.services["migrate"] = stalled("migrate") // wave 1 never comes up

	plan, err := e.PlanApply(context.Background(), rf, src, PlanOptions{})
	require.NoError(t, err)

	results, err := e.Apply(context.Background(), plan, InstallOptions{Timeout: 20 * time.Millisecond})
	require.ErrorContains(t, err, "timed out")
	require.ErrorContains(t, err, `'migrate'`, "the release that held the wave up is the one to name")

	require.Equal(t, []string{"db", "migrate"}, fb.deployOrder,
		"wave 2 must not be deployed at all — no service and no revision record")
	require.Equal(t, []string{"db", "migrate"}, resultNames(results),
		"a partial apply still reports what it did")
}

// The barrier waits for what the wave deployed, not for what the wave contains.
//
// A release the plan called unchanged was not touched by this apply, so blocking
// on it would let one already-deployed, degraded release stop every later wave on
// every apply forever — with nothing any redeploy could do about it, since apply
// skips unchanged releases by design.
func TestApplyDoesNotWaitForAWaveItDidNotDeploy(t *testing.T) {
	quickPoll(t)
	e, fb, src, rf := applyEnv(t, waveFile, "0.1.0")
	for _, r := range []string{"migrate", "web", "api"} {
		fb.services[r] = settled(r)
	}

	plan, err := e.PlanApply(context.Background(), rf, src, PlanOptions{})
	require.NoError(t, err)
	// Wave 0 is already deployed and, as far as this apply is concerned, broken:
	// it has no running task and would never satisfy a barrier.
	for i := range plan.Releases {
		if plan.Releases[i].Name == "db" {
			plan.Releases[i].Action = ActionUnchanged
		}
	}
	fb.services["db"] = stalled("db")

	_, err = e.Apply(context.Background(), plan, InstallOptions{Timeout: 20 * time.Millisecond})
	require.NoError(t, err, "an unchanged release this apply never wrote must not gate the waves after it")
	require.Equal(t, []string{"migrate", "web", "api"}, fb.deployOrder)
}

// A single-wave plan reads the swarm exactly as often as it did before waves
// existed, which for an apply without --wait is not at all.
//
// The guard is on the *absence* of the new behaviour. Without it, waves would
// have quietly added a convergence wait to every release file in existence.
func TestApplyOfASingleWaveNeverWaits(t *testing.T) {
	quickPoll(t)
	e, fb, src, rf := applyEnv(t, twoInOneWave, "0.1.0")

	plan, err := e.PlanApply(context.Background(), rf, src, PlanOptions{})
	require.NoError(t, err)

	// No services are staged, so a barrier that fired here would find nothing
	// converged. The short timeout is what turns that regression into a failure
	// rather than a five-minute hang.
	_, err = e.Apply(context.Background(), plan, InstallOptions{Timeout: 50 * time.Millisecond})
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, fb.deployOrder)
	require.Zero(t, fb.stackServiceCalls,
		"one wave is no boundary, so nothing should have asked the swarm whether it had converged")
}

// waitWave says exactly what waitReady always said when it is waiting on one
// release, so a caller matching that text — the integration suite does — is
// unaffected by the generalisation.
func TestWaitWaveNamesOneReleaseTheWayItAlwaysHas(t *testing.T) {
	quickPoll(t)
	fb := newFakeBackend()
	fb.services["solo"] = stalled("solo")
	e := NewEngineWith(fb)

	err := e.waitReady(context.Background(), "solo", 20*time.Millisecond)
	require.ErrorContains(t, err, `timed out after 20ms waiting for release 'solo' to converge`)
}

// And it names all of them, but only the ones that had not converged: a wave of
// five where one is slow should point at the one.
func TestWaitWaveNamesOnlyWhatDidNotConverge(t *testing.T) {
	quickPoll(t)
	fb := newFakeBackend()
	fb.services["quick"] = settled("quick")
	fb.services["slow"] = stalled("slow")
	fb.services["alsoSlow"] = stalled("alsoSlow")
	e := NewEngineWith(fb)

	err := e.waitWave(context.Background(), []string{"quick", "slow", "alsoSlow"}, 20*time.Millisecond)
	require.ErrorContains(t, err, `releases 'slow', 'alsoSlow'`)
	require.NotContains(t, err.Error(), "quick")
}

// A wedged release ends the wait on the first observation rather than serving
// out the deadline, and names itself rather than the group it was in.
func TestWaitWaveFailsFastOnAWedgedRelease(t *testing.T) {
	quickPoll(t)
	fb := newFakeBackend()
	fb.services["fine"] = settled("fine")
	fb.services["stuck"] = []ServiceState{{Name: "stuck", Running: 1, Desired: 1, UpdateState: "paused"}}
	e := NewEngineWith(fb)

	err := e.waitWave(context.Background(), []string{"fine", "stuck"}, time.Hour)
	require.ErrorContains(t, err, `release 'stuck'`)
	require.NotContains(t, err.Error(), "timed out", "swarm will not continue on its own, so waiting is pointless")
}

// A cancelled context ends a wave wait with the context's own error, so a caller
// can tell a shutdown from a wave that would not come up.
func TestWaitWaveStopsOnACancelledContext(t *testing.T) {
	quickPoll(t)
	fb := newFakeBackend()
	fb.services["slow"] = stalled("slow")
	e := NewEngineWith(fb)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, e.waitWave(ctx, []string{"slow"}, time.Hour), context.Canceled)
}

// waveGroups splits on the wave boundary and nowhere else.
func TestWaveGroups(t *testing.T) {
	require.Nil(t, waveGroups(nil))

	got := waveGroups([]ReleasePlan{
		{Name: "a", Wave: 0}, {Name: "b", Wave: 0},
		{Name: "c", Wave: 1},
		{Name: "d", Wave: 7}, {Name: "e", Wave: 7}, {Name: "f", Wave: 7},
	})
	require.Len(t, got, 3)
	require.Equal(t, []string{"a", "b"}, names(got[0]))
	require.Equal(t, []string{"c"}, names(got[1]))
	require.Equal(t, []string{"d", "e", "f"}, names(got[2]))
}

func names(releases []ReleasePlan) []string {
	out := make([]string, 0, len(releases))
	for _, r := range releases {
		out = append(out, r.Name)
	}
	return out
}

func waves(releases []ReleasePlan) []int {
	out := make([]int, 0, len(releases))
	for _, r := range releases {
		out = append(out, r.Wave)
	}
	return out
}

func resultNames(results []ApplyResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Name)
	}
	return out
}
