// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build integration

package charts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/cli"
	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"
)

// TestChartsApplyIsIdempotentAgainstARealSwarm is the one thing the unit tests
// cannot prove. Release history is one Docker Config per revision, so an apply
// that recorded a revision even when nothing changed would grow the swarm's
// config store on every CI run, forever. The fake backend can only simulate that;
// this counts the real Configs on a real swarm before and after a second apply.
func TestChartsApplyIsIdempotentAgainstARealSwarm(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()

	ctx := context.Background()
	release := fmt.Sprintf("itest-apply-%d", time.Now().UnixNano())

	chartDir := writeDemoChart(t)
	dir := t.TempDir()
	relFile := filepath.Join(dir, "swarmcli-release.yaml")
	require.NoError(t, os.WriteFile(relFile, []byte(fmt.Sprintf(
		"releases:\n  - name: %s\n    chart: %s\n", release, chartDir)), 0o600))

	rf, err := charts.LoadReleaseFile(relFile)
	require.NoError(t, err)

	eng := charts.NewEngine()
	src := charts.NewChartSource(nil) // local chart path: no repository needed
	defer func() { _, _ = eng.Uninstall(ctx, release, true) }()

	opts := charts.InstallOptions{Wait: true, Timeout: 90 * time.Second}

	// First apply installs.
	plan, err := eng.PlanApply(ctx, rf, src, charts.PlanOptions{})
	require.NoError(t, err)
	require.Len(t, plan.Releases, 1)
	require.Equal(t, charts.ActionInstall, plan.Releases[0].Action)

	res, err := eng.Apply(ctx, plan, opts)
	require.NoError(t, err)
	require.Equal(t, 1, res[0].Revision)

	hist, err := eng.History(ctx, release)
	require.NoError(t, err)
	require.Len(t, hist, 1)

	// Second apply, nothing changed: it must plan `unchanged`, deploy nothing, and
	// record NO new revision.
	plan2, err := eng.PlanApply(ctx, rf, src, charts.PlanOptions{})
	require.NoError(t, err)
	require.Equal(t, charts.ActionUnchanged, plan2.Releases[0].Action)

	res2, err := eng.Apply(ctx, plan2, opts)
	require.NoError(t, err)
	require.Equal(t, charts.ActionUnchanged, res2[0].Action)
	require.Zero(t, res2[0].Revision)

	hist2, err := eng.History(ctx, release)
	require.NoError(t, err)
	require.Len(t, hist2, 1, "a no-op apply must not record a revision on a real swarm")

	// A changed value is a real upgrade, so the mechanism is not simply inert.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "v.yaml"), []byte("replicas: 2\n"), 0o600))
	require.NoError(t, os.WriteFile(relFile, []byte(fmt.Sprintf(
		"releases:\n  - name: %s\n    chart: %s\n    values: [./v.yaml]\n", release, chartDir)), 0o600))

	rf2, err := charts.LoadReleaseFile(relFile)
	require.NoError(t, err)
	plan3, err := eng.PlanApply(ctx, rf2, src, charts.PlanOptions{})
	require.NoError(t, err)
	require.Equal(t, charts.ActionUpgrade, plan3.Releases[0].Action)

	res3, err := eng.Apply(ctx, plan3, opts)
	require.NoError(t, err)
	require.Equal(t, 2, res3[0].Revision)

	hist3, err := eng.History(ctx, release)
	require.NoError(t, err)
	require.Len(t, hist3, 2)
}

// apply never removes a release the file does not describe: a release records
// nothing about which manifest produced it, so a prune could not distinguish one
// owned by a second file, or installed by hand, from a genuinely obsolete one.
func TestChartsApplyLeavesUnmanagedReleasesRunning(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()

	ctx := context.Background()
	managed := fmt.Sprintf("itest-managed-%d", time.Now().UnixNano())
	byHand := fmt.Sprintf("itest-byhand-%d", time.Now().UnixNano())

	chartDir := writeDemoChart(t)
	eng := charts.NewEngine()
	src := charts.NewChartSource(nil)
	opts := charts.InstallOptions{Wait: true, Timeout: 90 * time.Second}

	defer func() {
		_, _ = eng.Uninstall(ctx, managed, true)
		_, _ = eng.Uninstall(ctx, byHand, true)
	}()

	// A release installed outside the file.
	ch, err := charts.LoadChartDir(chartDir)
	require.NoError(t, err)
	manifest, err := charts.Render(ch, charts.RenderContext{
		Values:  ch.Values,
		Release: charts.ReleaseMeta{Name: byHand, Namespace: byHand, Revision: 1},
		Chart:   charts.ChartMeta{Name: ch.Metadata.Name, Version: ch.Metadata.Version},
	})
	require.NoError(t, err)
	_, err = eng.Install(ctx, byHand, charts.ReleaseChartOf(ch), ch.Values, manifest, opts)
	require.NoError(t, err)

	dir := t.TempDir()
	relFile := filepath.Join(dir, "swarmcli-release.yaml")
	require.NoError(t, os.WriteFile(relFile, []byte(fmt.Sprintf(
		"releases:\n  - name: %s\n    chart: %s\n", managed, chartDir)), 0o600))
	rf, err := charts.LoadReleaseFile(relFile)
	require.NoError(t, err)

	plan, err := eng.PlanApply(ctx, rf, src, charts.PlanOptions{})
	require.NoError(t, err)
	require.Contains(t, plan.Unmanaged, byHand, "the hand-installed release must be reported")

	_, err = eng.Apply(ctx, plan, opts)
	require.NoError(t, err)

	// It is still there.
	cur, _, err := eng.Status(ctx, byHand)
	require.NoError(t, err)
	require.Equal(t, charts.StatusDeployed, cur.Status, "apply must not touch an unmanaged release")
}

// --diff (and --dry-run) are preview verbs: if either ever deployed, that would be
// a production incident. This drives the real CLI against the real swarm, because
// with no Docker daemon a unit test fails at the ping long before reaching the
// short-circuit — and would pass for entirely the wrong reason.
func TestChartsApplyDiffDoesNotDeploy(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()

	ctx := context.Background()
	release := fmt.Sprintf("itest-diff-%d", time.Now().UnixNano())

	chartDir := writeDemoChart(t)
	dir := t.TempDir()
	relFile := filepath.Join(dir, "swarmcli-release.yaml")
	require.NoError(t, os.WriteFile(relFile, []byte(fmt.Sprintf(
		"releases:\n  - name: %s\n    chart: %s\n", release, chartDir)), 0o600))

	eng := charts.NewEngine()
	defer func() { _, _ = eng.Uninstall(ctx, release, true) }()

	for _, flag := range []string{"--diff", "--dry-run"} {
		t.Run(flag, func(t *testing.T) {
			code := cli.Dispatch([]string{"charts", "apply", "-f", relFile, flag}, "test")
			require.Equal(t, 0, code)

			// Nothing may have been deployed and no revision recorded.
			list, err := eng.List(ctx)
			require.NoError(t, err)
			for _, r := range list {
				require.NotEqual(t, release, r.Name, "%s must not deploy", flag)
			}
			_, err = eng.History(ctx, release)
			require.Error(t, err, "%s must not record a revision", flag)
		})
	}

	// The same file, applied for real, does deploy — so the guard above is not
	// simply testing a broken path.
	rf, err := charts.LoadReleaseFile(relFile)
	require.NoError(t, err)
	plan, err := eng.PlanApply(ctx, rf, charts.NewChartSource(nil), charts.PlanOptions{})
	require.NoError(t, err)
	_, err = eng.Apply(ctx, plan, charts.InstallOptions{Wait: true, Timeout: 90 * time.Second})
	require.NoError(t, err)

	hist, err := eng.History(ctx, release)
	require.NoError(t, err)
	require.Len(t, hist, 1)
}

// The repository path, end to end on a real swarm: a release file with a
// `repositories:` block -> EnsureRepos -> Resolve -> Pull -> render -> apply, then
// `charts outdated` against a newer published version — and not against a
// same-named chart in another configured repository.
//
// This is the flow a downstream user actually runs, and it was covered by nothing:
// the unit tests use a fake chart source that never touches RepoStore, and the
// other integration cases pass a nil store with a local chart directory.
func TestChartsApplyFromARepositoryAndOutdated(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()

	ctx := context.Background()
	release := fmt.Sprintf("itest-repo-%d", time.Now().UnixNano())

	tgz := packChartToTgz(t, writeDemoChart(t))
	// Start with only 0.1.0 published.
	published := []string{"0.1.0"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.yaml") {
			var b strings.Builder
			b.WriteString("apiVersion: v1\nentries:\n  itest:\n")
			for _, v := range published {
				b.WriteString("    - name: itest\n      version: " + v + "\n      urls: [\"itest-" + v + ".tgz\"]\n")
			}
			_, _ = w.Write([]byte(b.String()))
			return
		}
		if strings.HasSuffix(r.URL.Path, ".tgz") {
			_, _ = w.Write(tgz)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	// Keep the repo store out of the developer's real XDG state dir.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// This test drives the CLI, which builds its own store, so the field is out
	// of reach — the env var is the documented way in. The httptest server
	// above serves plain http.
	t.Setenv("SWARMCLI_CHARTS_ALLOW_PLAINTEXT", "1")

	dir := t.TempDir()
	relFile := filepath.Join(dir, "swarmcli-release.yaml")
	require.NoError(t, os.WriteFile(relFile, []byte(fmt.Sprintf(
		"repositories:\n  - name: itest-repo\n    url: %s\n"+
			"releases:\n  - name: %s\n    chart: itest-repo/itest\n    version: \"0.1.0\"\n",
		srv.URL, release)), 0o600))

	eng := charts.NewEngine()
	defer func() { _, _ = eng.Uninstall(ctx, release, true) }()

	// apply adds the repository itself — `apply -f` is the only command a CI job
	// should need to run.
	require.Equal(t, 0, cli.Dispatch([]string{"charts", "apply", "-f", relFile, "--wait"}, "test"))

	cur, _, err := eng.Status(ctx, release)
	require.NoError(t, err)
	require.Equal(t, charts.StatusDeployed, cur.Status)
	require.Equal(t, "0.1.0", cur.Chart.Version, "the PINNED version must be installed")
	require.Equal(t, "itest-repo", cur.Chart.Repo, "the release records the repository it came from")
	require.Equal(t, srv.URL, cur.Chart.RepoURL)

	// A second repository carrying a same-named chart at a far higher version —
	// a fork, a mirror, the seeded swarmcli-charts — is a different chart, and
	// must not be reported as this release's upgrade.
	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.yaml") {
			_, _ = w.Write([]byte("apiVersion: v1\nentries:\n  itest:\n    - name: itest\n      version: 9.9.9\n      urls: [\"itest-9.9.9.tgz\"]\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer decoy.Close()
	require.Equal(t, 0, cli.Dispatch([]string{"charts", "repo", "add", "decoy", decoy.URL}, "test"))

	// Nothing newer is published yet.
	require.Equal(t, 0, cli.Dispatch([]string{"charts", "outdated"}, "test"))

	// Publish 0.2.0; `outdated` must now see it, without the file changing.
	published = append(published, "0.2.0")
	require.Equal(t, 0, cli.Dispatch([]string{"charts", "outdated"}, "test"))

	rels, err := eng.List(ctx)
	require.NoError(t, err)
	repos, idxs := repoIndexes(t)
	entries := charts.Outdated(rels, repos, idxs)

	var found bool
	for _, e := range entries {
		if e.Release == release {
			found = true
			require.Equal(t, "0.1.0", e.Installed)
			require.Equal(t, "0.2.0", e.Latest)
			require.Equal(t, "itest-repo", e.Repo)
		}
	}
	require.True(t, found, "outdated must report the release once a newer chart is published")
}

// repoIndexes reads the repositories and indexes `charts outdated` would compare
// against.
func repoIndexes(t *testing.T) ([]charts.RepoEntry, map[string]*charts.Index) {
	t.Helper()
	store, err := charts.NewRepoStore()
	require.NoError(t, err)
	_, _, _ = store.Update("")
	repos, err := store.List()
	require.NoError(t, err)
	idxs, err := store.Indexes()
	require.NoError(t, err)
	return repos, idxs
}

// packChartToTgz packs a chart directory into a .tgz the repo store can pull.
func packChartToTgz(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	require.NoError(t, filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: filepath.ToSlash(filepath.Join("itest", rel)),
			Mode: 0o644,
			Size: int64(len(body)),
		}); err != nil {
			return err
		}
		_, err = tw.Write(body)
		return err
	}))
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}
