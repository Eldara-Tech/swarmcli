// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build integration

package charts

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"
)

// rolloutService is one whoami service at a revision, marked for a sequential
// rollout or not. A short monitor window keeps each step quick; stop-first is
// how a clustered service that owns a volume has to update.
func rolloutService(name string, rev int, marked bool) string {
	label := ""
	if marked {
		label = "\n      labels:\n        " + charts.RolloutLabel + ": " + charts.RolloutSequential
	}
	return fmt.Sprintf(`
  %s:
    image: traefik/whoami:v1.10
    environment:
      REV: "%d"
    deploy:
      replicas: 1
      placement:
        constraints: [node.role == manager]
      update_config:
        monitor: 5s
        order: stop-first%s`, name, rev, label)
}

func rolloutStack(rev int, services ...string) string {
	var b strings.Builder
	b.WriteString("version: \"3.9\"\n\nservices:")
	for _, s := range services {
		switch s {
		case "plain":
			b.WriteString(rolloutService(s, rev, false))
		default:
			b.WriteString(rolloutService(s, rev, true))
		}
	}
	return b.String() + "\n"
}

// revTaskCreated is when swarm created the service's task running REV=rev.
//
// Not the service's UpdateStatus: every later stage of a sequential rollout
// re-sends the services already rolled, unchanged, and that no-op update clears
// their UpdateStatus. Only the last service's survives to be read afterwards.
func revTaskCreated(t *testing.T, ctx context.Context, service string, rev int) time.Time {
	t.Helper()
	cli, err := docker.GetClient()
	require.NoError(t, err)
	tasks, err := cli.TaskList(ctx, swarm.TaskListOptions{Filters: filters.NewArgs(filters.Arg("service", service))})
	require.NoError(t, err)
	var created time.Time
	for _, task := range tasks {
		for _, e := range task.Spec.ContainerSpec.Env {
			if e == fmt.Sprintf("REV=%d", rev) && task.CreatedAt.After(created) {
				created = task.CreatedAt
			}
		}
	}
	require.False(t, created.IsZero(), "service %s never ran a task at REV=%d", service, rev)
	return created
}

func inspectService(t *testing.T, ctx context.Context, name string) swarm.Service {
	t.Helper()
	cli, err := docker.GetClient()
	require.NoError(t, err)
	svc, _, err := cli.ServiceInspectWithRaw(ctx, name, swarm.ServiceInspectOptions{})
	require.NoError(t, err)
	return svc
}

// Services labelled for a sequential rollout update one at a time on a real
// swarm: the second marked service's update starts only after the first one's
// new task has run past its monitor window, while the unmarked service goes first
// with the rest of the stack. The unit suite proves the sequencing against a fake; only
// docker stack deploy proves a manifest that leaves a service out leaves it alone.
func TestUpgradeRollsSequentialServicesOneAtATime(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()

	ctx := context.Background()
	release := fmt.Sprintf("itest-seq-%d", time.Now().UnixNano())
	eng := charts.NewEngine()
	defer func() { _, _ = eng.Uninstall(ctx, release, true) }()
	ch := charts.ReleaseChart{Name: "seq", Version: "0.1.0"}
	opts := charts.InstallOptions{Wait: true, Timeout: 2 * time.Minute}

	_, err := eng.Install(ctx, release, ch, nil, rolloutStack(1, "a", "b", "plain"), opts)
	require.NoError(t, err)
	_, err = eng.Upgrade(ctx, release, ch, nil, rolloutStack(2, "a", "b", "plain"), opts)
	require.NoError(t, err)

	for _, name := range []string{"a", "b", "plain"} {
		require.Contains(t, inspectService(t, ctx, release+"_"+name).Spec.TaskTemplate.ContainerSpec.Env, "REV=2",
			"service %s was not upgraded", name)
	}
	// b is the last service rolled, so its UpdateStatus is still the one this
	// upgrade started: it began only after a's new task had run past a's 5s
	// monitor window. And the unmarked service went in the first deploy.
	b := inspectService(t, ctx, release+"_b")
	require.NotNil(t, b.UpdateStatus)
	require.NotNil(t, b.UpdateStatus.StartedAt)
	aNew := revTaskCreated(t, ctx, release+"_a", 2)
	require.False(t, b.UpdateStatus.StartedAt.Before(aNew.Add(5*time.Second)),
		"b's update started at %s, before a's new task (created %s) was past its monitor window", b.UpdateStatus.StartedAt, aNew)
	require.True(t, revTaskCreated(t, ctx, release+"_plain", 2).Before(aNew),
		"the unmarked service is updated by the first deploy, before any marked one")
}

// A stack of marked services and nothing else, as the mariadb-galera chart
// renders without its proxy and exporters: a first deploy that left all of them
// out would have no services, which docker stack deploy refuses.
func TestUpgradeRollsAStackOfOnlySequentialServices(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()

	ctx := context.Background()
	release := fmt.Sprintf("itest-seqonly-%d", time.Now().UnixNano())
	eng := charts.NewEngine()
	defer func() { _, _ = eng.Uninstall(ctx, release, true) }()
	ch := charts.ReleaseChart{Name: "seq", Version: "0.1.0"}
	opts := charts.InstallOptions{Wait: true, Timeout: 2 * time.Minute}

	_, err := eng.Install(ctx, release, ch, nil, rolloutStack(1, "a", "b"), opts)
	require.NoError(t, err)
	_, err = eng.Upgrade(ctx, release, ch, nil, rolloutStack(2, "a", "b"), opts)
	require.NoError(t, err)

	b := inspectService(t, ctx, release+"_b")
	require.Contains(t, inspectService(t, ctx, release+"_a").Spec.TaskTemplate.ContainerSpec.Env, "REV=2")
	require.Contains(t, b.Spec.TaskTemplate.ContainerSpec.Env, "REV=2")
	require.NotNil(t, b.UpdateStatus)
	require.NotNil(t, b.UpdateStatus.StartedAt)
	aNew := revTaskCreated(t, ctx, release+"_a", 2)
	require.False(t, b.UpdateStatus.StartedAt.Before(aNew.Add(5*time.Second)),
		"b's update started before a's new task was past its monitor window")
}

// downService is a marked service no node can run, a stand-in for a database
// peer waiting for its cluster after a full stop: it exists, and runs nothing.
func downService(rev int) string {
	return fmt.Sprintf(`
  b:
    image: traefik/whoami:v1.10
    environment:
      REV: "%d"
    deploy:
      replicas: 1
      placement:
        constraints: [node.labels.swarmcli_absent_label == true]
      labels:
        %s: %s`, rev, charts.RolloutLabel, charts.RolloutSequential)
}

// A marked service that is down is not held back: it goes out with the first
// deploy, and the upgrade does not wait on it. Held back, b would be rolled after
// a, never converge, and time the upgrade out, which is how the mariadb-galera
// chart's forceBootstrap recovery would deadlock.
func TestUpgradeDoesNotHoldADownSequentialService(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()

	ctx := context.Background()
	release := fmt.Sprintf("itest-seqdown-%d", time.Now().UnixNano())
	eng := charts.NewEngine()
	defer func() { _, _ = eng.Uninstall(ctx, release, true) }()
	ch := charts.ReleaseChart{Name: "seq", Version: "0.1.0"}

	// a up and converged first, then b added, so the upgrade below finds a
	// running and b live but down.
	_, err := eng.Install(ctx, release, ch, nil, rolloutStack(1, "a"), charts.InstallOptions{Wait: true, Timeout: 2 * time.Minute})
	require.NoError(t, err)
	_, err = eng.Upgrade(ctx, release, ch, nil, rolloutStack(1, "a")+downService(1)+"\n", charts.InstallOptions{})
	require.NoError(t, err)

	_, err = eng.Upgrade(ctx, release, ch, nil, rolloutStack(2, "a")+downService(2)+"\n", charts.InstallOptions{Timeout: 45 * time.Second})
	require.NoError(t, err, "the upgrade waited on a service that is down")
	for _, name := range []string{"a", "b"} {
		require.Contains(t, inspectService(t, ctx, release+"_"+name).Spec.TaskTemplate.ContainerSpec.Env, "REV=2",
			"service %s was not upgraded", name)
	}
}
