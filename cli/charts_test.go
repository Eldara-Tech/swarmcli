// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/features"
)

// capture redirects stdout/stderr to separate buffers for the duration of fn
// and returns (stdout, stderr) so callers can assert on the exact stream.
func capture(t *testing.T, fn func()) (string, string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	origOut, origErr := stdout, stderr
	stdout, stderr = &outBuf, &errBuf
	defer func() { stdout, stderr = origOut, origErr }()
	fn()
	return outBuf.String(), errBuf.String()
}

func TestDispatchVersion(t *testing.T) {
	var code int
	o, _ := capture(t, func() { code = Dispatch([]string{"version"}, "1.2.3") })
	require.Equal(t, 0, code)
	require.Contains(t, o, "1.2.3")
}

// An unstamped engine admits every chart's declared floor unchecked, and a
// release that has done that looks entirely normal otherwise. Saying so on the
// version line is the only place it surfaces, so it must not degrade to silence.
func TestDispatchVersion_UnstampedEngineSaysSo(t *testing.T) {
	var code int
	o, _ := capture(t, func() { code = Dispatch([]string{"version"}, "1.2.3") })
	require.Equal(t, 0, code)
	require.Contains(t, o, "chart engine unstamped",
		"tests run unstamped, so this is what a build whose ldflag did not take prints")
}

// One tag publishes two artefacts under this command name, and the version
// string is the same for both — so `version` has to say which build it is. The
// unset case is the one that matters most: a main that has not been taught to
// set this must print the bare version rather than claim to be the OSS build.
func TestDispatchVersion_NamesTheBuild(t *testing.T) {
	orig := buildEdition
	defer func() { buildEdition = orig }()

	for _, tc := range []struct{ edition, want string }{
		{"", "1.2.3 (chart engine unstamped)"},
		{"ce", "1.2.3 (oss build, chart engine unstamped)"},
		{"be", "1.2.3 (business build, chart engine unstamped)"},
		{"nonsense", "1.2.3 (chart engine unstamped)"},
	} {
		buildEdition = tc.edition
		o, _ := capture(t, func() { Dispatch([]string{"version"}, "1.2.3") })
		require.Equal(t, tc.want, strings.TrimSpace(o), "edition %q", tc.edition)
	}
}

func TestDispatchUnknownCommand(t *testing.T) {
	var code int
	capture(t, func() { code = Dispatch([]string{"frobnicate"}, "dev") })
	require.Equal(t, 2, code)
}

func TestChartsTemplateLocalDir(t *testing.T) {
	var code int
	o, _ := capture(t, func() {
		code = Dispatch([]string{"charts", "template", "my-demo", "../charts/testdata/demo", "--set", "replicas=3", "--set", "image.tag=v9"}, "dev")
	})
	require.Equal(t, 0, code)
	require.Contains(t, o, "traefik:v9")
	require.Contains(t, o, "replicas: 3")
	require.Contains(t, o, "com.swarmcli.release: my-demo")
}

func TestChartsTemplateSchemaRejection(t *testing.T) {
	var code int
	_, errOut := capture(t, func() {
		code = Dispatch([]string{"charts", "template", "x", "../charts/testdata/demo", "--set", "replicas=0"}, "dev")
	})
	require.Equal(t, 1, code)
	require.Contains(t, errOut, "schema validation")
}

func TestChartsTemplateUnknownFlag(t *testing.T) {
	var code int
	capture(t, func() {
		code = Dispatch([]string{"charts", "template", "x", "../charts/testdata/demo", "--bogus"}, "dev")
	})
	require.Equal(t, 2, code)
}

func TestChartsPruneTooManyArgs(t *testing.T) {
	var code int
	_, errOut := capture(t, func() {
		code = Dispatch([]string{"charts", "prune", "rel-a", "rel-b"}, "dev")
	})
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "charts prune [release]")
}

func TestChartsPruneUnknownFlag(t *testing.T) {
	var code int
	capture(t, func() {
		code = Dispatch([]string{"charts", "prune", "--bogus"}, "dev")
	})
	require.Equal(t, 2, code)
}

func TestChartsShowValues(t *testing.T) {
	var code int
	o, _ := capture(t, func() {
		code = Dispatch([]string{"charts", "show", "values", "../charts/testdata/demo"}, "dev")
	})
	require.Equal(t, 0, code)
	require.Contains(t, o, "replicas: 1")
}

// show values must print values.yaml verbatim — its comments and key order are
// documentation and must survive (re-marshalling the parsed map would drop both).
func TestChartsShowValuesPreservesComments(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "templates"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("name: demo\nversion: 1.0.0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "templates", "stack.yaml"), []byte("services: {}\n"), 0o644))
	raw := "# leading comment\nreplicas: 1  # inline comment\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(raw), 0o644))

	var code int
	o, _ := capture(t, func() {
		code = Dispatch([]string{"charts", "show", "values", dir}, "dev")
	})
	require.Equal(t, 0, code)
	require.Equal(t, raw, o)
}

// fileChartDir writes a chart whose single template gives a config its content
// with file: ref, plus one file under files/ for a well-formed ref to name.
func fileChartDir(t *testing.T, ref string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "templates"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "files"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"),
		[]byte("name: demo\nversion: 1.0.0\nswarmcliVersion: \">= 1.13.0\"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "templates", "stack.yaml"),
		[]byte("services:\n  web:\n    image: nginx\nconfigs:\n  site:\n    file: "+ref+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "files", "nginx.conf"),
		[]byte("server { listen 80; }"), 0o644))
	return dir
}

// prepare is the second of the two places a *Chart and a rendered manifest
// coexist, and it backs template, diff, install and upgrade alike — so a path
// the chart may not read is refused on all four, at the earliest point an author
// or an operator can see it. There is no compatibility flag behind this refusal,
// so the message it prints IS the migration path and has to survive to stderr
// whole: the path, the rule, and the operator-managed replacement.
func TestChartsTemplateRefusesAPathOutsideTheChart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ref   string
		wants []string
	}{
		{"absolute", "/etc/shadow", []string{`'/etc/shadow'`, "absolute path", "external: true", "docker config create"}},
		{"escapes the chart", "../../etc/shadow", []string{`'../../etc/shadow'`, "escapes the chart"}},
		{"outside files/", "nginx.conf", []string{`'nginx.conf'`, "outside files/", `'files/nginx.conf'`}},
		{"not in the chart", "files/missing.conf", []string{`'files/missing.conf'`, "not in the chart"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fileChartDir(t, tc.ref)
			var code int
			o, errOut := capture(t, func() {
				code = Dispatch([]string{"charts", "template", "site", dir}, "dev")
			})
			require.Equal(t, 1, code)
			require.Empty(t, o, "a manifest that cannot be deployed must not be printed as if it could")
			for _, want := range tc.wants {
				require.Contains(t, errOut, want)
			}
		})
	}
}

// A manifest the deploy would refuse for reading the environment is refused by
// template too, so a chart repository's render check sees it before a release.
func TestChartsTemplateRefusesAManifestThatInterpolates(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "templates"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("name: demo\nversion: 1.0.0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "templates", "stack.yaml"),
		[]byte("services:\n  web:\n    image: nginx\n    environment:\n      GREETING: \"${FOO}\"\n"), 0o644))
	var code int
	o, errOut := capture(t, func() {
		code = Dispatch([]string{"charts", "template", "site", dir}, "dev")
	})
	require.Equal(t, 1, code)
	require.Empty(t, o)
	require.Contains(t, errOut, "services.web.environment.GREETING:")
	require.Contains(t, errOut, "escape '$' as '$$'")
}

// apply gates the whole plan before converging any of it, so a release the
// deploy would refuse stops the apply before an earlier one is deployed, and a
// preview says so instead of looking clean. A release apply leaves alone is not
// its to refuse.
func TestApplyGateRefusesAReleaseTheDeployWould(t *testing.T) {
	refused := "services:\n  web:\n    image: nginx\n    environment:\n      GREETING: \"${FOO}\"\n"
	ok := charts.CompatFinding{Status: charts.CompatOK}
	plan := &charts.Plan{Releases: []charts.ReleasePlan{
		{Name: "a", Action: charts.ActionUnchanged, Manifest: refused, Compat: ok},
		{Name: "b", Action: charts.ActionInstall, Manifest: "services:\n  web:\n    image: nginx\n", Compat: ok},
		{Name: "c", Action: charts.ActionUpgrade, Manifest: refused, Compat: ok},
	}}
	for _, pol := range []compatPolicy{compatEnforceNoPrompt, compatWarn} {
		var code int
		_, errOut := capture(t, func() { code = gateApply(plan, pol, false) })
		require.Equal(t, 1, code)
		require.Contains(t, errOut, "release 'c': services.web.environment.GREETING:")
	}

	plan.Releases = plan.Releases[:2]
	var code int
	capture(t, func() { code = gateApply(plan, compatEnforceNoPrompt, false) })
	require.Equal(t, -1, code)
}

// A declaration the deploy would refuse for its reserved label or name is
// refused by template too, the same way.
func TestChartsTemplateRefusesAReservedDeclaration(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "templates"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("name: demo\nversion: 1.0.0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "templates", "stack.yaml"),
		[]byte("services:\n  web:\n    image: nginx\nconfigs:\n  app:\n    external: true\n    labels:\n      com.swarmcli.type: release\n"), 0o644))
	var code int
	o, errOut := capture(t, func() {
		code = Dispatch([]string{"charts", "template", "site", dir}, "dev")
	})
	require.Equal(t, 1, code)
	require.Empty(t, o)
	require.Contains(t, errOut, "configs.app.labels: label 'com.swarmcli.type'")
}

// And apply's gate refuses it before converging anything, preview or not.
func TestApplyGateRefusesAReservedDeclaration(t *testing.T) {
	refused := "services:\n  web:\n    image: nginx\nsecrets:\n  key:\n    external: true\n    name: swarmcli.release.x.v1\n"
	ok := charts.CompatFinding{Status: charts.CompatOK}
	plan := &charts.Plan{Releases: []charts.ReleasePlan{
		{Name: "a", Action: charts.ActionUnchanged, Manifest: refused, Compat: ok},
		{Name: "b", Action: charts.ActionInstall, Manifest: "services:\n  web:\n    image: nginx\n", Compat: ok},
		{Name: "c", Action: charts.ActionUpgrade, Manifest: refused, Compat: ok},
	}}
	for _, pol := range []compatPolicy{compatEnforceNoPrompt, compatWarn} {
		var code int
		_, errOut := capture(t, func() { code = gateApply(plan, pol, false) })
		require.Equal(t, 1, code)
		require.Contains(t, errOut, "release 'c': secrets.key: name 'swarmcli.release.x.v1'")
	}
}

// And the shape the refusals exist to permit still renders.
func TestChartsTemplateAcceptsAFileTheChartShips(t *testing.T) {
	var code int
	o, errOut := capture(t, func() {
		code = Dispatch([]string{"charts", "template", "site", fileChartDir(t, "files/nginx.conf")}, "dev")
	})
	require.Equal(t, 0, code, errOut)
	require.Contains(t, o, "file: files/nginx.conf")
}

// valueChartDir writes the chart shape #537 exists for: a config the chart does
// not ship, supplied by the operator, whose swarm object is named after the
// content it carries so that changing the content rotates it. Swarm config data
// is immutable — `docker stack deploy` reacts to changed content under an
// existing name with ConfigUpdate, which swarmkit refuses with "only updates to
// Labels are allowed" — so a stable name would not update, it would fail.
func valueChartDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "templates"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"),
		[]byte("name: renovate\nversion: 1.0.0\nswarmcliVersion: \">= 1.13.0\"\n"), 0o644))
	// config: "" so the name below renders before anything supplies it; an
	// operator who forgets --set-file is then refused rather than deploying it.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("config: \"\"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "templates", "stack.yaml"), []byte(
		"services:\n  renovate:\n    image: renovate/renovate\n    configs:\n"+
			"      - source: config\n        target: /usr/src/app/config.js\n"+
			"configs:\n  config:\n    file: values/config\n"+
			"    name: \"{{ .Release.Name }}_config_{{ .Values.config | sha256sum | trunc 12 }}\"\n"), 0o644))
	return dir
}

func TestChartsTemplateSetFileSuppliesAConfig(t *testing.T) {
	dir := valueChartDir(t)
	js := filepath.Join(t.TempDir(), "config.js")
	require.NoError(t, os.WriteFile(js, []byte("module.exports = { platform: 'github' };\n"), 0o644))

	render := func() string {
		var code int
		o, errOut := capture(t, func() {
			code = Dispatch([]string{"charts", "template", "renovate", dir, "--set-file", "config=" + js}, "dev")
		})
		require.Equal(t, 0, code, errOut)
		return o
	}

	first := render()
	require.Contains(t, first, "file: values/config")
	require.Regexp(t, `name: renovate_config_[0-9a-f]{12}`, first)

	// Rotation: new content, new name — which is the whole mechanism, and the
	// only thing Swarm offers for changing a config's contents.
	require.NoError(t, os.WriteFile(js, []byte("module.exports = { platform: 'gitea' };\n"), 0o644))
	require.NotEqual(t, first, render(), "changed content must render a different config name")
}

func TestChartsTemplateSetFileRefusals(t *testing.T) {
	dir := valueChartDir(t)
	js := filepath.Join(t.TempDir(), "config.js")
	require.NoError(t, os.WriteFile(js, []byte("module.exports = {};\n"), 0o644))

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			// The chart renders — the refusal is the resolution, and without it
			// the operator would get an empty config deployed over a working one.
			"forgotten flag", nil, `'values/config' is empty`,
		},
		{"unreadable file", []string{"--set-file", "config=" + filepath.Join(dir, "nope.js")}, "read"},
		{"no key", []string{"--set-file", js}, "expected key=path"},
		{"empty path", []string{"--set-file", "config="}, "expected key=path"},
		{"malformed key", []string{"--set-file", "config..x=" + js}, "--set-file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var code int
			o, errOut := capture(t, func() {
				code = Dispatch(append([]string{"charts", "template", "renovate", dir}, tc.args...), "dev")
			})
			require.Equal(t, 1, code)
			require.Empty(t, o, "a manifest that cannot be deployed must not be printed as if it could")
			require.Contains(t, errOut, tc.want)
		})
	}
}

func TestParseArgs(t *testing.T) {
	pos, f, err := parseArgs([]string{"rel", "repo/chart", "-f", "a.yaml", "--values", "b.yaml", "--set", "x=1", "--dry-run", "--timeout", "10m"})
	require.NoError(t, err)
	require.Equal(t, []string{"rel", "repo/chart"}, pos)
	require.Equal(t, []string{"a.yaml", "b.yaml"}, f.values)
	require.Equal(t, []string{"x=1"}, f.sets)
	require.True(t, f.dryRun)
	require.Equal(t, "10m0s", f.timeout.String())
}

func TestParseArgsInlineValue(t *testing.T) {
	_, f, err := parseArgs([]string{"--set=a=1", "--set-file=b=./b.js", "--version=2.0.0"})
	require.NoError(t, err)
	require.Equal(t, []string{"a=1"}, f.sets)
	require.Equal(t, []string{"b=./b.js"}, f.setFiles)
	require.Equal(t, "2.0.0", f.version)
}

func TestParseArgsSetFile(t *testing.T) {
	_, f, err := parseArgs([]string{"--set-file", "a=./a.js", "--set-file", "b.c=./b.js"})
	require.NoError(t, err)
	require.Equal(t, []string{"a=./a.js", "b.c=./b.js"}, f.setFiles)

	_, _, err = parseArgs([]string{"--set-file"})
	require.ErrorContains(t, err, "requires a value")
}

func TestParseArgsRequirements(t *testing.T) {
	pos, f, err := parseArgs([]string{"rel", "repo/chart", "--requirements"})
	require.NoError(t, err)
	require.Equal(t, []string{"rel", "repo/chart"}, pos)
	require.True(t, f.requirements)
}

// --debug was parsed into a field nothing ever read, so it was accepted and did
// nothing — the exact "reads as if it worked" shape #451 is about. It is gone,
// which means it now falls through to the unknown-flag error like any other
// name the CLI does not know.
func TestParseArgsRejectsDebug(t *testing.T) {
	_, _, err := parseArgs([]string{"--debug"})
	require.EqualError(t, err, `unknown flag '--debug'`)
}

func TestParseIntRejectsGarbage(t *testing.T) {
	n, err := parseInt("3")
	require.NoError(t, err)
	require.Equal(t, 3, n)
	for _, bad := range []string{"2x", "-1", "", " ", "1.5", "0x10"} {
		_, err := parseInt(bad)
		require.Errorf(t, err, "expected %q to be rejected", bad)
	}
}

// --- apply (Docker-free paths only; the engine is covered in charts/) ---

func TestChartsApplyRequiresExactlyOneFile(t *testing.T) {
	var code int
	capture(t, func() { code = chartsApply(cmd(t, "apply"), nil) })
	require.Equal(t, 2, code, "no release file")

	capture(t, func() { code = chartsApply(cmd(t, "apply"), []string{"-f", "a.yaml", "-f", "b.yaml"}) })
	require.Equal(t, 2, code, "two release files")
}

func TestChartsApplyMissingFile(t *testing.T) {
	var code int
	_, e := capture(t, func() { code = chartsApply(cmd(t, "apply"), []string{"-f", filepath.Join(t.TempDir(), "nope.yaml")}) })
	require.Equal(t, 1, code)
	require.Contains(t, e, "nope.yaml")
}

// A typo in a file an automated updater rewrites must fail loudly and name the key.
//
//nolint:misspell // "verison" is a deliberate typo — it is what the test rejects.
func TestChartsApplyRejectsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rel.yaml")
	require.NoError(t, os.WriteFile(path, []byte("releases:\n  - name: a\n    chart: r/c\n    verison: \"1\"\n"), 0o600))

	var code int
	_, e := capture(t, func() { code = chartsApply(cmd(t, "apply"), []string{"-f", path}) })
	require.Equal(t, 1, code)
	require.Contains(t, e, "verison")
}

// The charts flag set is global, so every subcommand parses every flag. apply must
// REJECT the ones it does not honour rather than silently ignoring them: its whole
// contract is that the file is the only source of truth.
func TestChartsApplyRejectsUnsupportedFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rel.yaml")
	require.NoError(t, os.WriteFile(path, []byte("releases:\n  - {name: a, chart: r/c, version: \"1\"}\n"), 0o600))

	for _, flag := range [][]string{
		{"--set", "a=1"},
		{"--set-file", "a=/dev/null"},
		{"--version", "1.0.0"},
		{"--reuse-values"},
		{"--install"},
		{"--purge-volumes"},
	} {
		t.Run(flag[0], func(t *testing.T) {
			var code int
			_, e := capture(t, func() { code = chartsApply(cmd(t, "apply"), append([]string{"-f", path}, flag...)) })
			require.Equal(t, 2, code)
			require.Contains(t, e, flag[0])
			require.Contains(t, e, "only source of truth")
		})
	}
}

func TestPrintPlan(t *testing.T) {
	plan := &charts.Plan{Releases: []charts.ReleasePlan{
		{Name: "hello", Ref: "r/whoami", Action: charts.ActionInstall, ToVersion: "0.1.8"},
		{Name: "edge", Ref: "r/traefik", Action: charts.ActionUpgrade, FromVersion: "0.1.0", ToVersion: "0.1.1"},
		{Name: "cache", Ref: "r/redis", Action: charts.ActionUnchanged, FromVersion: "0.1.1", ToVersion: "0.1.1"},
	}}
	o, _ := capture(t, func() { printPlan(plan, false) })

	require.Contains(t, o, "RELEASE")
	require.Contains(t, o, "hello")
	require.Contains(t, o, "install")
	require.Contains(t, o, "upgrade")
	require.Contains(t, o, "unchanged")
	// An install has no prior version; it must render as "-", not empty.
	require.Regexp(t, `hello\s+r/whoami\s+-\s+0\.1\.8\s+install`, o)
	require.Contains(t, o, "1 to install, 1 to upgrade, 1 unchanged")
	// A plan with one wave says nothing about waves. Every plan printed before
	// they existed is one of these, and a column of zeroes on all of them would
	// be noise rather than information.
	require.NotContains(t, o, "WAVE")
}

// A plan spanning waves shows which one each release is in, and says what that
// means for the order.
func TestPrintPlanShowsWavesWhenThereIsMoreThanOne(t *testing.T) {
	plan := &charts.Plan{Releases: []charts.ReleasePlan{
		{Name: "db", Ref: "r/pg", Action: charts.ActionInstall, ToVersion: "1.0.0"},
		{Name: "migrate", Ref: "r/migrate", Action: charts.ActionInstall, ToVersion: "1.0.0", Wave: 1},
		{Name: "api", Ref: "r/api", Action: charts.ActionInstall, ToVersion: "1.0.0", Wave: 2},
	}}
	o, _ := capture(t, func() { printPlan(plan, false) })

	require.Contains(t, o, "WAVE")
	require.Regexp(t, `db\s+r/pg\s+-\s+1\.0\.0\s+install\s+0`, o)
	require.Regexp(t, `api\s+r/api\s+-\s+1\.0\.0\s+install\s+2`, o)
	require.Contains(t, o, "each wave converges before the next begins")
}

// --diff shows a manifest diff for changed releases and skips unchanged ones.
func TestPrintPlanWithDiff(t *testing.T) {
	plan := &charts.Plan{Releases: []charts.ReleasePlan{
		{
			Name: "edge", Ref: "r/traefik", Action: charts.ActionUpgrade,
			FromVersion: "0.1.0", ToVersion: "0.1.1",
			CurrentManifest: "services:\n  a: 1\n", Manifest: "services:\n  a: 2\n",
		},
		{Name: "cache", Ref: "r/redis", Action: charts.ActionUnchanged, ToVersion: "0.1.1"},
	}}
	o, _ := capture(t, func() { printPlan(plan, true) })

	require.Contains(t, o, "--- edge (upgrade) ---")
	require.NotContains(t, o, "--- cache", "an unchanged release has nothing to diff")
}

// apply never deletes; it reports what it left alone, with the command to remove it.
func TestReportUnmanaged(t *testing.T) {
	o, _ := capture(t, func() {
		reportUnmanaged(&charts.Plan{Unmanaged: []string{"legacy", "scratch"}})
	})
	require.Contains(t, o, "2 release(s) exist on this swarm but are not in the release file")
	require.Contains(t, o, "swarmcli charts uninstall legacy")
	require.Contains(t, o, "swarmcli charts uninstall scratch")

	// Nothing unmanaged must print nothing at all — not a stray header on every
	// clean apply.
	o, _ = capture(t, func() { reportUnmanaged(&charts.Plan{}) })
	require.Empty(t, o)
}

// An orphan is reported separately from an unmanaged release, and in its own
// words: the stamp says this file installed it, so "not in the release file"
// would understate what is actually known about it.
func TestReportOrphaned(t *testing.T) {
	o, _ := capture(t, func() {
		reportOrphaned(&charts.Plan{Owner: "apply/prod", Orphaned: []string{"gone"}})
	})
	require.Contains(t, o, "1 release(s) were installed by this release file but are no longer declared in it")
	require.Contains(t, o, "swarmcli charts uninstall gone")

	o, _ = capture(t, func() { reportOrphaned(&charts.Plan{}) })
	require.Empty(t, o)
}

// The two lists always travel together. They answer one question — what does the
// swarm hold that this apply did not deploy — from two directions, and every
// exit path routes through the pair rather than picking one. Reporting only one
// of them is what the dry-run path used to do, by reporting neither.
func TestReportUnclaimedCoversBothLists(t *testing.T) {
	o, _ := capture(t, func() {
		reportUnclaimed(&charts.Plan{
			Owner:     "apply/prod",
			Orphaned:  []string{"gone"},
			Unmanaged: []string{"legacy"},
		})
	})
	require.Contains(t, o, "no longer declared in it")
	require.Contains(t, o, "not in the release file")

	o, _ = capture(t, func() { reportUnclaimed(&charts.Plan{}) })
	require.Empty(t, o, "a clean apply prints no headers at all")
}

// The store is https-only by default, and this env var is the whole of the
// compatibility story for anyone already pointing swarmcli at an internal
// plaintext registry — so assert the wiring, not just the parsing.
func TestNewStoreWiresThePlaintextOptOut(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	s, code := newStore(flags{})
	require.Equal(t, -1, code)
	require.False(t, s.AllowPlaintext, "https-only unless the operator says otherwise")

	t.Setenv(charts.AllowPlaintextEnv, "1")
	s, code = newStore(flags{})
	require.Equal(t, -1, code)
	require.True(t, s.AllowPlaintext)

	// Anything that is not a truthy boolean leaves the default in place, rather
	// than any non-empty value opting out by accident.
	t.Setenv(charts.AllowPlaintextEnv, "maybe")
	s, _ = newStore(flags{})
	require.False(t, s.AllowPlaintext)
}

// The CLI is where "resolve what the repository publishes now" is turned on,
// and both opt-outs have to reach the same place — an air-gapped machine sets
// the environment variable once, a one-off offline run passes the flag.
func TestNewStoreWiresTheRefreshPolicy(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	s, code := newStore(flags{})
	require.Equal(t, -1, code)
	require.Equal(t, charts.RefreshAlways, s.Refresh, "an interactive install means the current chart")

	s, code = newStore(flags{noRepoUpdate: true})
	require.Equal(t, -1, code)
	require.Equal(t, charts.RefreshNever, s.Refresh, "--no-repo-update means no network at all")

	t.Setenv(charts.NoAutoUpdateEnv, "1")
	s, _ = newStore(flags{})
	require.Equal(t, charts.RefreshNever, s.Refresh)

	// As with the plaintext opt-out, only a truthy boolean counts: a stray
	// non-empty value must not silently turn the feature off.
	t.Setenv(charts.NoAutoUpdateEnv, "maybe")
	s, _ = newStore(flags{})
	require.Equal(t, charts.RefreshAlways, s.Refresh)
}

func TestNoRepoUpdateFlagParses(t *testing.T) {
	_, f, err := parseArgs([]string{"--no-repo-update"})
	require.NoError(t, err)
	require.True(t, f.noRepoUpdate)

	_, f, err = parseArgs([]string{})
	require.NoError(t, err)
	require.False(t, f.noRepoUpdate)
}

// Without the all-nodes volume feature the warning says where purging every
// node is available; with it (an extension that could not reach some nodes
// this time) it gives only the remedy.
func TestVolumesMayRemainWarning(t *testing.T) {
	remedy := "  docker volume ls --filter label=com.docker.stack.namespace=mongodb\n"
	upsell := "Purging volumes on every node is a Business Edition feature: https://swarmcli.io/be\n"

	w := volumesMayRemainWarning("mongodb")
	require.Contains(t, w, remedy)
	require.True(t, strings.HasSuffix(w, upsell))

	features.Enable(volumesAllNodesFeature)
	t.Cleanup(func() { features.Disable(volumesAllNodesFeature) })
	w = volumesMayRemainWarning("mongodb")
	require.True(t, strings.HasSuffix(w, remedy))
	require.NotContains(t, w, "Business Edition")
}
