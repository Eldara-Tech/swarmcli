// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build integration

package charts

import (
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
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"
)

// The rest of this package drives the charts *package*: it proves the engine
// converges a real swarm. These two tests drive the *CLI layer* — the thing an
// operator and a CI job actually type — because that layer now decides whether
// an invocation is accepted at all. Each subcommand carries a flag allow-list,
// and a flag outside it exits 2 rather than being parsed and dropped, so an
// allow-list that is too narrow rejects an invocation that used to work.
// cli.TestFlagAllowListsMatchWhatHandlersRead proves the lists agree with the
// handlers; agreement is not the command running against a daemon.
//
// Between them every row of the command table is invoked with every flag it
// lists, which cli.TestIntegrationSuiteExercisesEveryCommandAndFlag enforces by
// reading the invocations below back out of the syntax tree.

// dispatchOK runs one CLI invocation and fails naming the argv it ran. Exit 2
// is the failure worth recognising: it is the layer refusing a flag, not the
// command going wrong.
func dispatchOK(t *testing.T, argv ...string) {
	t.Helper()
	require.Equal(t, 0, cli.Dispatch(argv, "test"), "swarmcli %s", strings.Join(argv, " "))
}

// isolateCLIState points the repository store at a temp dir. loadChart builds a
// RepoStore for every command, including ones handed a local chart path, so
// without this the CLI reads — and refreshes over the network — whatever
// repositories the machine running the test happens to have configured.
func isolateCLIState(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// TestChartsCLIImperativeLifecycle runs the imperative path end to end through
// cli.Dispatch against the live swarm: template, install, get, diff, upgrade,
// history, status, list, rollback, prune, apply and uninstall, each with the
// flags its row lists.
//
// Exit 0 is necessary and not sufficient, so each mutating command is checked
// against the engine afterwards: a command that exits 0 while deploying nothing
// would otherwise pass. The three preview verbs (install --dry-run, upgrade
// --dry-run, diff upgrade) are asserted to have recorded no revision at all.
func TestChartsCLIImperativeLifecycle(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()
	isolateCLIState(t)

	ctx := context.Background()
	release := fmt.Sprintf("itest-cli-%d", time.Now().UnixNano())
	applyRelease := release + "-apply"

	chartDir := writeDemoChart(t)
	dir := t.TempDir()

	valuesFile := filepath.Join(dir, "values.yaml")
	require.NoError(t, os.WriteFile(valuesFile, []byte("replicas: 1\n"), 0o600))

	// notes is a values key no template reads. Its whole job is to be read back
	// out of the recorded revision, which is how --set-file is proven to have
	// reached the release rather than merely been accepted — and it keeps the
	// shared demo chart, which eight other tests render, untouched.
	notesFile := filepath.Join(dir, "notes.txt")
	notes := fmt.Sprintf("set-file-%d", time.Now().UnixNano())
	require.NoError(t, os.WriteFile(notesFile, []byte(notes), 0o600))

	relFile := filepath.Join(dir, "swarmcli-release.yaml")
	require.NoError(t, os.WriteFile(relFile, []byte(fmt.Sprintf(
		"releases:\n  - name: %s\n    chart: %s\n", applyRelease, chartDir)), 0o600))

	eng := charts.NewEngine()
	defer func() {
		_, _ = eng.Uninstall(ctx, release, true)
		_, _ = eng.Uninstall(ctx, applyRelease, true)
	}()

	// --- template: renders to stdout, deploys nothing ---
	dispatchOK(t, "charts", "template", release, chartDir,
		"--values", valuesFile, "--set", "replicas=1", "--set-file", "notes="+notesFile,
		"--skip-compat-check", "--no-repo-update")

	// --requirements needs a chart that declares some; the external-network
	// fixture is this package's only one. template deploys nothing, so the
	// network it declares is not created.
	dispatchOK(t, "charts", "template", release, writeExtNetChart(t, "itest-cli-requirements"),
		"--requirements")

	// --- install --dry-run: prints the prospective revision, records none ---
	dispatchOK(t, "charts", "install", release, chartDir,
		"--set", "replicas=1", "--dry-run", "--no-repo-update")
	_, err := eng.History(ctx, release)
	require.Error(t, err, "install --dry-run must not record a revision")

	// --- install ---
	dispatchOK(t, "charts", "install", release, chartDir,
		"--values", valuesFile, "--set", "replicas=1", "--set-file", "notes="+notesFile,
		"--wait", "--timeout", "90s", "--history-max", "5",
		"--resolve-image", "never", "--skip-compat-check", "--no-repo-update")

	cur, svcs, err := eng.Status(ctx, release)
	require.NoError(t, err)
	require.Equal(t, charts.StatusDeployed, cur.Status)
	require.NotEmpty(t, svcs, "install --wait must leave a running service behind")

	rev1, err := eng.GetRevision(ctx, release, 1)
	require.NoError(t, err)
	require.Equal(t, notes, rev1.Values["notes"], "--set-file must reach the recorded revision")

	// --- the read-only verbs, against a release that exists ---
	dispatchOK(t, "charts", "get", "values", release, "--revision", "1")
	dispatchOK(t, "charts", "history", release)
	dispatchOK(t, "charts", "status", release)
	dispatchOK(t, "charts", "list")
	dispatchOK(t, "charts", "outdated", "--no-repo-update")

	// --- diff upgrade: a preview verb, it must not deploy ---
	dispatchOK(t, "charts", "diff", "upgrade", release, chartDir,
		"--values", valuesFile, "--set", "replicas=2", "--set-file", "notes="+notesFile,
		"--reuse-values", "--skip-compat-check", "--no-repo-update")
	hist, err := eng.History(ctx, release)
	require.NoError(t, err)
	require.Len(t, hist, 1, "diff upgrade must not deploy")

	// --- upgrade --dry-run ---
	dispatchOK(t, "charts", "upgrade", release, chartDir,
		"--set", "replicas=2", "--dry-run", "--no-repo-update")
	hist, err = eng.History(ctx, release)
	require.NoError(t, err)
	require.Len(t, hist, 1, "upgrade --dry-run must not record a revision")

	// --- upgrade ---
	dispatchOK(t, "charts", "upgrade", release, chartDir,
		"--values", valuesFile, "--set", "replicas=2", "--set-file", "notes="+notesFile,
		"--install", "--reuse-values", "--wait", "--timeout", "90s", "--history-max", "5",
		"--resolve-image", "changed", "--skip-compat-check", "--no-repo-update")
	hist, err = eng.History(ctx, release)
	require.NoError(t, err)
	require.Len(t, hist, 2, "upgrade must record a second revision")

	// --- rollback: revision 3 carries revision 1's contents ---
	dispatchOK(t, "charts", "rollback", release, "1",
		"--wait", "--timeout", "90s", "--history-max", "5")
	hist, err = eng.History(ctx, release)
	require.NoError(t, err)
	require.Len(t, hist, 3)
	rev3, err := eng.GetRevision(ctx, release, 3)
	require.NoError(t, err)
	require.Equal(t, rev1.Manifest, rev3.Manifest, "rollback must re-deploy revision 1's manifest")

	// --- prune --dry-run: reports what it would delete, deletes nothing ---
	dispatchOK(t, "charts", "prune", release, "--history-max", "1", "--dry-run")
	hist, err = eng.History(ctx, release)
	require.NoError(t, err)
	require.Len(t, hist, 3, "prune --dry-run must delete nothing")

	// --- apply: its behaviour is covered by charts_apply_test.go; this is its
	// flag row, which no other test passes in full ---
	dispatchOK(t, "charts", "apply", "-f", relFile, "--diff", "--dry-run")
	_, err = eng.History(ctx, applyRelease)
	require.Error(t, err, "apply --diff --dry-run must not deploy")

	dispatchOK(t, "charts", "apply", "-f", relFile,
		"--wait", "--timeout", "90s", "--history-max", "5",
		"--resolve-image", "never", "--skip-compat-check", "--no-repo-update")
	applied, _, err := eng.Status(ctx, applyRelease)
	require.NoError(t, err)
	require.Equal(t, charts.StatusDeployed, applied.Status)

	// --- uninstall ---
	dispatchOK(t, "charts", "uninstall", release, "--purge-volumes")
	_, err = docker.InspectConfig(ctx, fmt.Sprintf("swarmcli.release.%s.v1", release))
	require.Error(t, err, "uninstall must remove the release configs")

	dispatchOK(t, "charts", "uninstall", applyRelease)
}

// TestChartsCLIRepositoryCommands runs the repository-backed commands through
// cli.Dispatch: the ones that resolve a <repo>/<chart> reference, and the only
// ones that can carry --version at all (it is an error against a local chart
// path). The chart is served by an httptest server, as the apply tests already
// do, and installed on the live swarm from the pinned version.
func TestChartsCLIRepositoryCommands(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()
	isolateCLIState(t)
	// The CLI builds its own store, so the store's https-only default is out of
	// reach — the env var is the documented way in, and the server below serves
	// plain http.
	t.Setenv("SWARMCLI_CHARTS_ALLOW_PLAINTEXT", "1")

	ctx := context.Background()
	release := fmt.Sprintf("itest-cli-repo-%d", time.Now().UnixNano())

	const tgzName = "itest-0.1.0.tgz"
	tgz := packChartTgz(t, writeDemoChart(t), "itest")
	index := "apiVersion: v1\n" +
		"entries:\n" +
		"  itest:\n" +
		"    - name: itest\n" +
		"      version: \"0.1.0\"\n" +
		"      description: integration test chart\n" +
		"      urls: [\"" + tgzName + "\"]\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/index.yaml"):
			_, _ = w.Write([]byte(index))
		case strings.HasSuffix(r.URL.Path, tgzName):
			_, _ = w.Write(tgz)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	valuesFile := filepath.Join(dir, "values.yaml")
	require.NoError(t, os.WriteFile(valuesFile, []byte("replicas: 1\n"), 0o600))

	eng := charts.NewEngine()
	defer func() { _, _ = eng.Uninstall(ctx, release, true) }()

	// --- repo: the four sub-verbs are one dispatch target and four things an
	// operator runs ---
	dispatchOK(t, "charts", "repo", "add", "itest-repo", srv.URL)
	dispatchOK(t, "charts", "repo", "list")
	dispatchOK(t, "charts", "repo", "update", "itest-repo")

	// --- discovery ---
	dispatchOK(t, "charts", "search", "itest", "--no-repo-update")
	dispatchOK(t, "charts", "show", "chart", "itest-repo/itest",
		"--version", "0.1.0", "--skip-compat-check", "--no-repo-update")
	dispatchOK(t, "charts", "show", "values", "itest-repo/itest")

	// --- authoring: lint renders from the chart defaults with the overrides
	// layered on, and --for-version asks whether some other engine version is
	// admitted by the chart's declared floor ---
	dispatchOK(t, "charts", "lint", "itest-repo/itest",
		"--values", valuesFile, "--set", "replicas=1", "--version", "0.1.0",
		"--for-version", "1.13.0", "--no-repo-update")

	dispatchOK(t, "charts", "template", release, "itest-repo/itest", "--version", "0.1.0")

	// --- install from the repository at the pinned version ---
	dispatchOK(t, "charts", "install", release, "itest-repo/itest",
		"--version", "0.1.0", "--wait", "--timeout", "90s")
	cur, _, err := eng.Status(ctx, release)
	require.NoError(t, err)
	require.Equal(t, charts.StatusDeployed, cur.Status)
	require.Equal(t, "0.1.0", cur.Chart.Version, "the pinned version must be the one installed")
	require.Equal(t, "itest-repo", cur.Chart.Repo, "install records the repository the chart came from")
	require.Equal(t, srv.URL, cur.Chart.RepoURL)

	dispatchOK(t, "charts", "diff", "upgrade", release, "itest-repo/itest",
		"--version", "0.1.0", "--reuse-values")
	hist, err := eng.History(ctx, release)
	require.NoError(t, err)
	require.Len(t, hist, 1, "diff upgrade must not deploy")

	// No --wait: the waited deploy path is proven above, and this invocation is
	// here for upgrade's --version.
	dispatchOK(t, "charts", "upgrade", release, "itest-repo/itest",
		"--version", "0.1.0", "--set", "replicas=1")
	hist, err = eng.History(ctx, release)
	require.NoError(t, err)
	require.Len(t, hist, 2)

	dispatchOK(t, "charts", "uninstall", release)
	_, err = docker.InspectConfig(ctx, fmt.Sprintf("swarmcli.release.%s.v1", release))
	require.Error(t, err, "uninstall must remove the release configs")

	dispatchOK(t, "charts", "repo", "remove", "itest-repo")
}
