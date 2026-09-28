// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build integration

package charts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"
)

// writeEnvChart writes a chart whose one service declares environment (YAML,
// indented under environment:) and reads files/app.env, which holds envFile.
func writeEnvChart(t *testing.T, environment, envFile string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"),
		[]byte("apiVersion: v1\nname: envchart\nversion: 0.1.0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("{}\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "templates"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "files"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "files", "app.env"), []byte(envFile), 0o644))
	stack := "version: \"3.9\"\n\nservices:\n  app:\n" +
		"    image: traefik/whoami:v1.10\n" +
		"    environment:\n      " + environment + "\n" +
		"    env_file:\n      - files/app.env\n" +
		"    deploy:\n      replicas: 1\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "templates", "stack.yaml"), []byte(stack), 0o644))
	return dir
}

// installEnvChart renders the chart at dir and installs it as release, the way
// the CLI does: files resolved against the chart, then the engine's deploy.
func installEnvChart(t *testing.T, eng *charts.Engine, release, dir string) error {
	t.Helper()
	ch, err := charts.LoadChartDir(dir)
	require.NoError(t, err)
	rc := charts.ReleaseChartOf(ch)
	manifest, err := charts.Render(ch, charts.RenderContext{
		Values:  ch.Values,
		Release: charts.ReleaseMeta{Name: release, Namespace: release, Revision: 1},
		Chart:   charts.ChartMeta{Name: rc.Name, Version: rc.Version},
	})
	require.NoError(t, err)
	files, err := charts.ResolveManifestFiles(manifest, ch.Files, ch.Values)
	require.NoError(t, err)
	_, err = eng.Install(context.Background(), release, rc, ch.Values, manifest, charts.InstallOptions{Files: files})
	return err
}

// An empty value a chart declares deploys as empty, whatever the shell running
// the deploy has under that name — through environment: and through an env
// file alike, each under its own name so neither can mask the other.
//
// This is the regression test against the docker CLI that actually runs, which
// is whatever `docker` is on PATH rather than the docker/cli this module pins.
func TestChartDeployDoesNotInheritTheInvokingEnvironment(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()
	t.Setenv("FOO", "from-the-shell")
	t.Setenv("BAR", "from-the-shell")

	ctx := context.Background()
	release := fmt.Sprintf("itest-env-%d", time.Now().UnixNano())
	eng := charts.NewEngine()
	defer func() { _, _ = eng.Uninstall(ctx, release, true) }()

	require.NoError(t, installEnvChart(t, eng, release, writeEnvChart(t, `FOO: ""`, "BAR=\n")))

	cli, err := docker.GetClient()
	require.NoError(t, err)
	svc, _, err := cli.ServiceInspectWithRaw(ctx, release+"_app", swarm.ServiceInspectOptions{})
	require.NoError(t, err)
	env := svc.Spec.TaskTemplate.ContainerSpec.Env
	require.Contains(t, env, "FOO=")
	require.Contains(t, env, "BAR=")
	for _, e := range env {
		require.NotContains(t, e, "from-the-shell")
	}
}

// A manifest that interpolates is refused before anything reaches the swarm:
// no service, and no release record.
func TestChartDeployRefusesAnInterpolatingManifest(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()
	t.Setenv("FOO", "from-the-shell")

	ctx := context.Background()
	release := fmt.Sprintf("itest-env-refused-%d", time.Now().UnixNano())
	eng := charts.NewEngine()
	defer func() { _, _ = eng.Uninstall(ctx, release, true) }()

	err := installEnvChart(t, eng, release, writeEnvChart(t, `GREETING: "${FOO}"`, "KEEP=1\n"))
	require.ErrorContains(t, err, "services.app.environment.GREETING:")
	require.ErrorContains(t, err, "escape '$' as '$$'")
	require.NotContains(t, err.Error(), "from-the-shell")

	cli, err := docker.GetClient()
	require.NoError(t, err)
	svcs, err := cli.ServiceList(ctx, swarm.ServiceListOptions{
		Filters: filters.NewArgs(filters.Arg("label", "com.docker.stack.namespace="+release)),
	})
	require.NoError(t, err)
	require.Empty(t, svcs, "a refused deploy must create nothing")
	hist, _ := eng.History(ctx, release)
	require.Empty(t, hist, "a refused deploy must record nothing")
}
