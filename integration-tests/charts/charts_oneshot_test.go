// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build integration

package charts

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"
)

// oneShotStack is the shape a compose v3 stack has to give an init or migration
// step: a replicated service whose restart policy declines to replace the task,
// since `docker stack deploy` cannot render mode: replicated-job.
//
// The marker only exists to make one revision's spec differ from the next, so
// the second deploy is an UPDATE. That is the whole precondition: swarm records
// no UpdateStatus at all on a first deploy, which is why this defect stayed
// invisible until a stack was redeployed.
const oneShotStack = `version: "3.9"

services:
  once:
    image: alpine:latest
    command: ["sh", "-c", "echo %s; exit %d"]
    deploy:
      replicas: 1
      restart_policy:
        condition: none
      update_config:
        monitor: %s
        failure_action: %s
`

// oneShotMonitor is declared rather than left to swarm's 5s default because the
// window is the mechanism under test: swarmkit counts a task that leaves RUNNING
// inside UpdateConfig.Monitor as an update failure, so the container has to
// finish comfortably inside it even on a slow runner or the service never
// reaches the state these tests are about.
const oneShotMonitor = 12 * time.Second

func oneShotManifest(marker string, exitCode int, failureAction string) string {
	return fmt.Sprintf(oneShotStack, marker, exitCode, oneShotMonitor, failureAction)
}

// awaitRelease polls a release's single service until cond holds, and fails the
// test if it never does.
//
// Reading convergence the instant a deploy returns is not enough. `docker stack
// deploy` returns once the manager has accepted the new spec; swarmkit creates
// the task and stamps UpdateStatus afterwards, so a poll can still be looking at
// the PREVIOUS generation — which for a one-shot is a completed task sitting at
// parity.
//
// Failing rather than returning early is the point: on a runner slow enough that
// the state never arrives, the assertions below would otherwise pass without
// exercising anything.
func awaitRelease(t *testing.T, eng *charts.Engine, release string, timeout time.Duration, what string, cond func([]charts.ServiceState) bool) []charts.ServiceState {
	t.Helper()

	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	last := "<never read>"
	for time.Now().Before(deadline) {
		require.NoError(t, eng.Backend.RefreshSnapshot(ctx))
		states := eng.Backend.StackServices(ctx, release)
		if len(states) == 1 {
			if cond(states) {
				return states
			}
			last = fmt.Sprintf("update=%q completed=%d desired=%d phase=%s",
				states[0].UpdateState, states[0].Completed, states[0].Desired, charts.Rollup(states).Phase)
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("release %q never reached %s within %s (last seen %s)", release, what, timeout, last)
	return nil
}

func updateStateIs(want string) func([]charts.ServiceState) bool {
	return func(s []charts.ServiceState) bool { return s[0].UpdateState == want }
}

// A one-shot that did its work must not be reported as a stack needing manual
// recovery.
//
// swarmkit exempts nothing from the monitor window — not even a restart policy
// of none — so a job's task ending is counted as an update failure and the
// default failure_action pauses the rollout. Every redeployed one-shot therefore
// sits at UpdateStatus "paused" forever, and reading that as the verdict made
// --wait fail on a migration that had exited 0.
func TestWaitAcceptsAOneShotSwarmPausedForFinishing(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()

	ctx := context.Background()
	release := fmt.Sprintf("itest-oneshot-%d", time.Now().UnixNano())
	chart := charts.ReleaseChart{Name: "itest", Version: "0.1.0"}

	eng := charts.NewEngine()
	defer func() { _, _ = eng.Uninstall(ctx, release, true) }()

	_, err := eng.Install(ctx, release, chart, nil, oneShotManifest("first", 0, "pause"),
		charts.InstallOptions{Wait: true, Timeout: 90 * time.Second})
	require.NoError(t, err, "a job that runs to completion converges on a first deploy; swarm records no UpdateStatus yet")

	// The redeploy is the case. Same service, different spec, so swarm runs an
	// update and watches the task it creates.
	_, err = eng.Upgrade(ctx, release, chart, nil, oneShotManifest("second", 0, "pause"),
		charts.InstallOptions{Wait: true, Timeout: 90 * time.Second})
	require.NoError(t, err, "the second run also exited 0; --wait must not report the release wedged")

	states := awaitRelease(t, eng, release, 60*time.Second, `UpdateStatus "paused"`, updateStateIs("paused"))
	require.True(t, states[0].Job, "a restart policy of none is what makes this a one-shot")
	require.GreaterOrEqual(t, states[0].Completed, 1, "the task exited 0, so it is Complete")

	// Converging is not instant even once the job is done: the task still has to
	// outlive the monitor window, which is the same window that got it paused.
	// Before the fix this poll never finished — a paused rollout was terminal.
	states = awaitRelease(t, eng, release, 60*time.Second, "converged", func(s []charts.ServiceState) bool {
		return charts.Rollup(s).Phase == charts.PhaseConverged
	})
	require.Equal(t, "paused", states[0].UpdateState,
		"it has to converge WHILE paused; swarm never clears the state, and a cleared one would prove nothing")
}

// The other half, and the more dangerous one: a job whose LATEST run failed must
// not inherit an earlier run's success.
//
// Swarm keeps terminal tasks up to --task-history-limit, so a service that has
// completed before still lists those tasks. Counting them met the replica target
// on its own, which let a migration that exited non-zero report converged — a
// silent green in any pipeline gating on it.
//
// failure_action: continue is deliberate. It keeps swarm from pausing, which is
// what isolates this from the case above: the release here is judged purely on
// which tasks are in the list.
func TestWaitRefusesAOneShotWhoseLatestRunFailed(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()

	ctx := context.Background()
	release := fmt.Sprintf("itest-oneshot-fail-%d", time.Now().UnixNano())
	chart := charts.ReleaseChart{Name: "itest", Version: "0.1.0"}

	eng := charts.NewEngine()
	defer func() { _, _ = eng.Uninstall(ctx, release, true) }()

	_, err := eng.Install(ctx, release, chart, nil, oneShotManifest("good", 0, "continue"),
		charts.InstallOptions{Wait: true, Timeout: 90 * time.Second})
	require.NoError(t, err, "the first run exits 0 and must converge, or the test proves nothing about the second")

	// Not Wait: the verdict is asserted below against a state swarm has finished
	// moving to, rather than against whatever a poll caught mid-update.
	_, err = eng.Upgrade(ctx, release, chart, nil, oneShotManifest("bad", 3, "continue"),
		charts.InstallOptions{})
	require.NoError(t, err, "the deploy itself succeeds; it is the task that fails")

	// "completed" is swarm having watched the new task for the whole monitor
	// window and stopped: the failing generation is the current one and nothing
	// further is coming.
	states := awaitRelease(t, eng, release, 90*time.Second, `UpdateStatus "completed"`, updateStateIs("completed"))
	s := states[0]
	require.Zero(t, s.Completed, "the newest task failed; its predecessors are history, not progress")
	require.NotEqual(t, charts.PhaseConverged, charts.Rollup(states).Phase,
		"a job whose latest run exited 3 has not converged, whatever its earlier runs did")
}
