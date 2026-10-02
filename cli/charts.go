// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/features"
	"github.com/Eldara-Tech/swarmcli/v2/utils/textdiff"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"
)

// chartsUsageProse is everything in `charts --help` that is not the command
// list: what a release file is, how waves and compatibility behave, and the
// option reference. The command list itself is rendered from chartsCommands —
// see renderCommands.
const chartsUsageProse = `
A release file pins each release to a chart version, so it is reproducible and an
automated updater (e.g. Renovate) has something concrete to bump. apply installs
what is missing, upgrades what changed, and skips what already matches. It never
removes a release the file does not mention — it reports those instead.

  # swarmcli-release.yaml
  repositories:
    - name: swarmcli-charts
      url: https://eldara-tech.github.io/swarmcli-charts
  releases:
    - name: edge
      chart: swarmcli-charts/traefik
      version: "0.1.1"
      values: [./traefik.yaml]     # relative to the release file, not the CWD
    - name: hello
      chart: swarmcli-charts/whoami
      version: "0.1.8"
      wave: 1                      # deployed only once wave 0 has converged

Releases apply in wave order, then file order. A wave that does not converge stops
every wave after it, so nothing that depends on a failed migration is deployed.
Waves default to 0, so a file declaring none applies in file order as it always
has. The barrier does not need --wait; --wait is the separate question of whether
each individual release blocks, and --timeout bounds each wave.

A chart may declare its external networks/secrets/configs in requirements.yaml:
install pre-flights them (auto-creating networks marked autoCreate, validating
the rest) and uninstall reports any auto-created networks it leaves in place.

A chart may also declare the swarmcli it needs, as a SemVer constraint:

  # Chart.yaml
  swarmcliVersion: ">= 1.13.0"

install, upgrade and apply refuse a chart this build is too old for, so the
failure names the version to upgrade to instead of surfacing as whatever error
the missing feature happens to produce. install and upgrade ask first when run
interactively; apply never does. template, diff and show only warn. Pass
--skip-compat-check to try anyway. Charts declaring nothing are unaffected.
`

// chartsUsageTail is the prose that follows the option reference.
const chartsUsageTail = `
lint renders a chart and reports every problem it finds: a broken template,
values that fail values.schema.json, a swarmcliVersion this build does not
satisfy. It renders from the chart defaults, layering any -f/--set on top — a
chart with a required, undefaulted input needs them supplied, exactly as an
install would. --for-version asks whether the chart's declared floor admits some
other version — it cannot tell you the chart RUNS on that version, because this
binary carries only its own engine's behaviour. Rendering with a real binary of
that version is the only thing that settles that.

apply takes only the options that make sense for a file-driven converge, and
REJECTS the rest rather than ignoring them: the release file is the only source
of truth, so a value passed on the command line would be a lie. Run
swarmcli charts apply --help for what it does take.

Resolving a <repo>/<chart> reference refreshes that repository's index first, so
install, upgrade, template, diff, show, lint and search see what the repository
publishes now rather than what the cache last held. Each repository is fetched at
most once per invocation. A repository that cannot be reached is reported and the
cached index is used. --no-repo-update skips the network entirely, for which
outdated reports against the cached indexes and says so.
`

// chartsMain dispatches `swarmcli charts ...` through chartsCommands.
func chartsMain(args []string) int {
	if len(args) == 0 {
		out(chartsUsage())
		return 0
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		// `charts help <command>` is the same page as `charts <command> --help`,
		// mirroring the TUI, where `:help <cmd>` and `:cmd --help` are one screen.
		if len(rest) > 0 {
			if c, ok := lookupCommand(rest[0]); ok {
				out(commandHelp(c))
				return 0
			}
		}
		out(chartsUsage())
		return 0
	}
	c, ok := lookupCommand(sub)
	if !ok {
		msg := fmt.Sprintf("unknown charts command '%s'", sub)
		if s := suggestCommand(sub, commandNames()); s != "" {
			msg += fmt.Sprintf(" — did you mean '%s'?", s)
		}
		return usageErr(fmt.Sprintf("%s\n\n%s", msg, chartsUsage()))
	}
	return c.Run(c, rest)
}

// defaultRepos is what a machine that has never configured a repository starts
// with: the Eldara-Tech community charts.
var defaultRepos = []charts.RepoEntry{
	{Name: "swarmcli-charts", URL: "https://eldara-tech.github.io/swarmcli-charts"},
}

// newStore builds the repository store every charts subcommand resolves
// through, and is the single place CLI policy is applied to it.
func newStore(f flags) (*charts.RepoStore, int) {
	s, err := charts.NewRepoStore()
	if err != nil {
		return nil, fail(err)
	}
	s.Warnf = errf
	s.AllowPlaintext = plaintextAllowed()
	// Seeded here rather than in the charts package: embedders such as
	// swarmcli-cd build their own stores and must not grow a repository they
	// never declared.
	if err := s.Seed(defaultRepos); err != nil {
		return nil, fail(err)
	}
	// An interactive user who types `charts install foo repo/bar` means the bar
	// the repository publishes, not the one their cache happened to hold when
	// they last ran `repo update`. Programs embedding the charts package keep
	// the explicit-only default and decide for themselves.
	//
	// The opt-out is RefreshNever rather than RefreshExplicit: someone passing
	// --no-repo-update is saying "do not go to the network", and apply's
	// EnsureRepos would otherwise still spend a timeout per repository finding
	// out what they already told us.
	s.Refresh = charts.RefreshAlways
	if f.noRepoUpdate || noAutoUpdateEnv() {
		s.Refresh = charts.RefreshNever
	}
	return s, -1
}

// noAutoUpdateEnv reports whether the operator turned implicit refreshes off
// for this machine. The flag covers one invocation; this covers a CI job or an
// air-gapped host, where every invocation would otherwise pay the timeout.
func noAutoUpdateEnv() bool {
	off, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(charts.NoAutoUpdateEnv)))
	return err == nil && off
}

// plaintextAllowed reports whether the operator opted this machine out of the
// store's https-only default. It is an environment variable rather than a flag
// because a plaintext repository is not only reachable through `repo add`: once
// it is in repos.json, `repo update`, `search`, `install`, `upgrade` and `apply`
// all fetch from it, and a flag would have to be threaded — honestly — through
// every one of them. Someone running an internal registry sets this once, in a
// shell profile or a CI job, and everything keeps working.
func plaintextAllowed() bool {
	allowed, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(charts.AllowPlaintextEnv)))
	return err == nil && allowed
}

// --- repo ---

func chartsRepo(c chartsCmd, args []string) int {
	// repo takes no flags of its own, so this rejects any that turn up rather
	// than letting them land among the sub-verb's positionals.
	args, _, code := parse(c, args)
	if code >= 0 {
		return code
	}
	if len(args) == 0 {
		return usageErr("charts repo requires a subcommand: add|list|update|remove")
	}
	// Explicitly opted out rather than passed a zero flags: `repo update` IS the
	// refresh and `repo add` fetches by definition, so an implicit one before
	// either would be a second download of the same index.
	store, code := newStore(flags{noRepoUpdate: true})
	if code >= 0 {
		return code
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		if len(rest) != 2 {
			return usageErr("charts repo add <name> <url>")
		}
		if err := store.Add(rest[0], rest[1]); err != nil {
			return fail(err)
		}
		outf("%q has been added to your repositories\n", rest[0])
		return 0
	case "list", "ls":
		repos, err := store.List()
		if err != nil {
			return fail(err)
		}
		if len(repos) == 0 {
			outln("No repositories configured. Add one with: swarmcli charts repo add <name> <url>")
			return 0
		}
		rows := make([][]string, 0, len(repos))
		for _, r := range repos {
			rows = append(rows, []string{r.Name, r.URL})
		}
		table([]string{"NAME", "URL"}, rows)
		return 0
	case "update":
		name := ""
		if len(rest) == 1 {
			name = rest[0]
		} else if len(rest) > 1 {
			return usageErr("charts repo update [name]")
		}
		changed, unchanged, err := store.Update(name)
		if err != nil {
			return fail(err)
		}
		if len(changed)+len(unchanged) == 0 {
			outln("No repositories to update.")
			return 0
		}
		if len(changed) > 0 {
			outf("Updated %d repositor%s: %s\n", len(changed), plural(len(changed), "y", "ies"), strings.Join(changed, ", "))
		}
		if len(unchanged) > 0 {
			outf("Already up-to-date: %s\n", strings.Join(unchanged, ", "))
		}
		return 0
	case "remove", "rm":
		if len(rest) != 1 {
			return usageErr("charts repo remove <name>")
		}
		if err := store.Remove(rest[0]); err != nil {
			return fail(err)
		}
		outf("%q has been removed from your repositories\n", rest[0])
		return 0
	default:
		return usageErr(fmt.Sprintf("unknown charts repo command '%s'", sub))
	}
}

// --- search ---

func chartsSearch(c chartsCmd, args []string) int {
	pos, f, code := parse(c, args)
	if code >= 0 {
		return code
	}
	keyword := ""
	if len(pos) > 0 {
		keyword = pos[0]
	}
	store, code := newStore(f)
	if code >= 0 {
		return code
	}
	hits, err := store.Search(keyword)
	if err != nil {
		return fail(err)
	}
	if len(hits) == 0 {
		outln("No charts found.")
		return 0
	}
	rows := make([][]string, 0, len(hits))
	for _, h := range hits {
		rows = append(rows, []string{h.Repo + "/" + h.Entry.Name, h.Entry.Version, h.Entry.AppVersion, h.Entry.Description})
	}
	table([]string{"NAME", "VERSION", "APP VERSION", "DESCRIPTION"}, rows)
	return 0
}

// --- show ---

func chartsShow(c chartsCmd, args []string) int {
	if len(args) < 2 {
		return usageErr("charts show <chart|values|schema> <repo/chart>")
	}
	what, ref := args[0], args[1]
	pos, f, code := parse(c, args[2:])
	if code >= 0 {
		return code
	}
	_ = pos
	ch, _, code := loadChart(ref, f)
	if code >= 0 {
		return code
	}
	if code := applyCompat(charts.CheckCompat(ch.Metadata), compatWarn, f.skipCompatCheck); code >= 0 {
		return code
	}
	switch what {
	case "chart":
		printChartMeta(ch)
	case "values":
		// Print the chart's values.yaml verbatim so its comments and key order
		// survive (re-marshalling the parsed map would drop both). Fall back to
		// marshalling for a chart that ships no values.yaml.
		if len(ch.ValuesRaw) > 0 {
			_, _ = stdout.Write(ch.ValuesRaw)
			break
		}
		data, err := yaml.Marshal(ch.Values)
		if err != nil {
			return fail(err)
		}
		_, _ = stdout.Write(data)
	case "schema":
		if len(ch.Schema) == 0 {
			outln("Chart has no values.schema.json")
			return 0
		}
		_, _ = stdout.Write(ch.Schema)
	default:
		return usageErr(fmt.Sprintf("unknown show target '%s' (want chart|values|schema)", what))
	}
	return 0
}

func printChartMeta(ch *charts.Chart) {
	m := ch.Metadata
	outf("Name:        %s\n", m.Name)
	outf("Version:     %s\n", m.Version)
	if m.AppVersion != "" {
		outf("App Version: %s\n", m.AppVersion)
	}
	if m.Description != "" {
		outf("Description: %s\n", m.Description)
	}
	for _, mt := range m.Maintainers {
		outf("Maintainer:  %s <%s>\n", mt.Name, mt.Email)
	}
	if ch.Readme != "" {
		outln("\n--- README ---")
		outln(ch.Readme)
	}
}

// --- template / install ---

// chartsLint checks a chart without deploying anything: it is what a chart
// author runs before publishing, and what a chart repository's CI runs per
// chart. -f/--set supply the values the render check needs — a chart with a
// required, undefaulted input cannot render from bare defaults.
//
// --for-version lints against a chart-engine version other than this build's,
// which answers "does this chart's declared floor admit X?". It cannot answer
// "does this chart run on X" — only a real X can (see charts.CheckCompatAgainst).
func chartsLint(c chartsCmd, args []string) int {
	pos, f, code := parse(c, args)
	if code >= 0 {
		return code
	}
	if len(pos) != 1 {
		return usageErr("charts lint <chart>")
	}
	ch, _, code := loadChart(pos[0], f)
	if code >= 0 {
		return code
	}
	files, err := readValuesFiles(f.values)
	if err != nil {
		return fail(err)
	}

	engine := charts.EngineVersion()
	if f.forVersion != "" {
		engine = f.forVersion
	}
	findings := charts.Lint(ch, engine, files, f.sets)
	for _, fd := range findings {
		errf("%s: %s\n", fd.Severity, fd.Message)
	}
	if charts.HasErrors(findings) {
		return 1
	}
	outf("%s %s: ok\n", ch.Metadata.Name, ch.Metadata.Version)
	return 0
}

func chartsTemplate(c chartsCmd, args []string) int {
	pos, f, code := parse(c, args)
	if code >= 0 {
		return code
	}
	if len(pos) != 2 {
		return usageErr("charts template <release> <repo/chart>")
	}
	release, ref := pos[0], pos[1]
	manifest, _, _, req, _, code := prepare(release, ref, f, nil, compatWarn)
	if code >= 0 {
		return code
	}
	// --requirements emits the chart's requirements.yaml rendered with the same
	// values as the manifest (the resolved external-resource contract), instead of
	// the manifest. A chart with no requirements.yaml emits nothing.
	if f.requirements {
		if req == nil {
			return 0
		}
		out, err := yaml.Marshal(req)
		if err != nil {
			return fail(err)
		}
		outln(strings.TrimRight(string(out), "\n"))
		return 0
	}
	outln(manifest)
	return 0
}

func chartsInstall(c chartsCmd, args []string) int {
	pos, f, code := parse(c, args)
	if code >= 0 {
		return code
	}
	if len(pos) != 2 {
		return usageErr("charts install <release> <repo/chart>")
	}
	release, ref := pos[0], pos[1]
	manifest, values, rc, req, chartFiles, code := prepare(release, ref, f, nil, compatEnforce)
	if code >= 0 {
		return code
	}
	rel, err := charts.NewEngine().Install(context.Background(), release, rc, values, manifest, charts.InstallOptions{
		DryRun:       f.dryRun,
		Wait:         f.wait,
		Timeout:      f.timeout,
		HistoryMax:   f.historyMax,
		Requirements: req,
		Files:        chartFiles,
		ResolveImage: f.resolveImage,
	})
	if err != nil {
		return fail(err)
	}
	return reportRelease(rel, manifest, f.dryRun)
}

func chartsUpgrade(c chartsCmd, args []string) int {
	pos, f, code := parse(c, args)
	if code >= 0 {
		return code
	}
	if len(pos) != 2 {
		return usageErr("charts upgrade <release> <repo/chart>")
	}
	release, ref := pos[0], pos[1]

	var base map[string]any
	if f.reuseValues {
		cur, err := charts.NewEngine().GetRevision(context.Background(), release, 0)
		switch {
		case err == nil:
			base = cur.Values
		case !f.install:
			return fail(err)
			// With --install and no existing release there are no prior values to
			// reuse; fall through with base=nil so chart defaults apply. Upgrade
			// re-validates the release, so a genuine backend error still surfaces.
		}
	}
	manifest, values, rc, req, chartFiles, code := prepare(release, ref, f, base, compatEnforce)
	if code >= 0 {
		return code
	}
	rel, err := charts.NewEngine().Upgrade(context.Background(), release, rc, values, manifest, charts.InstallOptions{
		DryRun:       f.dryRun,
		Wait:         f.wait,
		Install:      f.install,
		Timeout:      f.timeout,
		HistoryMax:   f.historyMax,
		Requirements: req,
		Files:        chartFiles,
		ResolveImage: f.resolveImage,
	})
	if err != nil {
		return fail(err)
	}
	return reportRelease(rel, manifest, f.dryRun)
}

func reportRelease(rel *charts.Release, manifest string, dryRun bool) int {
	if dryRun {
		outf("NAME: %s\nREVISION: %d\nSTATUS: %s (dry-run, not deployed)\n\n", rel.Name, rel.Revision, rel.Status)
		outln(manifest)
		return 0
	}
	outf("NAME: %s\nREVISION: %d\nSTATUS: %s\nCHART: %s-%s\n", rel.Name, rel.Revision, rel.Status, rel.Chart.Name, rel.Chart.Version)
	return 0
}

func chartsRollback(c chartsCmd, args []string) int {
	pos, f, code := parse(c, args)
	if code >= 0 {
		return code
	}
	if len(pos) != 2 {
		return usageErr("charts rollback <release> <revision>")
	}
	rev, err := parseInt(pos[1])
	if err != nil {
		return usageErr(fmt.Sprintf("invalid revision '%s'", pos[1]))
	}
	rel, err := charts.NewEngine().Rollback(context.Background(), pos[0], rev, charts.InstallOptions{
		Wait: f.wait, Timeout: f.timeout, HistoryMax: f.historyMax,
	})
	if err != nil {
		return fail(err)
	}
	outf("Rolled back %s to the contents of revision %d (new revision %d)\n", rel.Name, rev, rel.Revision)
	return 0
}

func chartsHistory(c chartsCmd, args []string) int {
	pos, _, code := parse(c, args)
	if code >= 0 {
		return code
	}
	if len(pos) != 1 {
		return usageErr("charts history <release>")
	}
	hist, err := charts.NewEngine().History(context.Background(), pos[0])
	if err != nil {
		return fail(err)
	}
	rows := make([][]string, 0, len(hist))
	for _, r := range hist {
		rows = append(rows, []string{strconv.Itoa(r.Revision), r.Status, r.Chart.Name + "-" + r.Chart.Version, r.Created})
	}
	table([]string{"REVISION", "STATUS", "CHART", "UPDATED"}, rows)
	return 0
}

func chartsPrune(c chartsCmd, args []string) int {
	pos, f, code := parse(c, args)
	if code >= 0 {
		return code
	}
	if len(pos) > 1 {
		return usageErr("charts prune [release] [--history-max <n>] [--dry-run]")
	}
	e := charts.NewEngine()
	ctx := context.Background()

	var results []charts.PruneResult
	var err error
	if len(pos) == 1 {
		var res charts.PruneResult
		res, err = e.Prune(ctx, pos[0], f.historyMax, f.dryRun)
		results = []charts.PruneResult{res}
	} else {
		results, err = e.PruneAll(ctx, f.historyMax, f.dryRun)
	}
	if err != nil {
		return fail(err)
	}

	if f.historyMax <= 0 {
		errf("no --history-max retention window given; all revisions kept (pass --history-max <n> to prune)\n")
		return 0
	}

	rows := make([][]string, 0)
	deleted := 0
	for _, res := range results {
		for _, a := range res.Actions {
			action := "keep"
			switch {
			case a.Delete:
				action = "delete"
				deleted++
			case a.Current:
				action = "keep (current)"
			}
			rows = append(rows, []string{res.Release, strconv.Itoa(a.Revision), action})
		}
	}
	table([]string{"RELEASE", "REVISION", "ACTION"}, rows)

	if f.dryRun {
		outf("dry-run: %d revision(s) would be pruned\n", deleted)
	} else {
		outf("pruned %d revision(s)\n", deleted)
	}
	return 0
}

func chartsGet(c chartsCmd, args []string) int {
	if len(args) < 2 {
		return usageErr("charts get <values|manifest> <release> [--revision N]")
	}
	what, release := args[0], args[1]
	_, f, code := parse(c, args[2:])
	if code >= 0 {
		return code
	}
	rel, err := charts.NewEngine().GetRevision(context.Background(), release, f.revision)
	if err != nil {
		return fail(err)
	}
	switch what {
	case "values":
		data, err := yaml.Marshal(rel.Values)
		if err != nil {
			return fail(err)
		}
		_, _ = stdout.Write(data)
	case "manifest":
		outln(rel.Manifest)
	default:
		return usageErr(fmt.Sprintf("unknown get target '%s' (want values|manifest)", what))
	}
	return 0
}

func chartsDiff(c chartsCmd, args []string) int {
	if len(args) < 1 {
		return usageErr("charts diff upgrade <release> <repo/chart>")
	}
	if args[0] != "upgrade" {
		return usageErr(fmt.Sprintf("unknown diff target '%s' (want upgrade)", args[0]))
	}
	pos, f, code := parse(c, args[1:])
	if code >= 0 {
		return code
	}
	if len(pos) != 2 {
		return usageErr("charts diff upgrade <release> <repo/chart>")
	}
	release, ref := pos[0], pos[1]

	cur, err := charts.NewEngine().GetRevision(context.Background(), release, 0)
	if err != nil {
		return fail(err)
	}
	var base map[string]any
	if f.reuseValues {
		base = cur.Values
	}
	next, _, _, _, _, code := prepare(release, ref, f, base, compatWarn)
	if code >= 0 {
		return code
	}
	if cur.Manifest == next {
		outln("No changes.")
		return 0
	}
	out(textdiff.Lines(cur.Manifest, next))
	return 0
}

// prepare loads the chart, merges + validates values, and renders the manifest.
// base, when non-nil, replaces the chart defaults as the merge base (used by
// `upgrade --reuse-values` to layer overrides over the previous release).
//
// chartFiles are the chart files the rendered manifest's file:/env_file: keys
// name. They are resolved here because this is one of only two places a *Chart
// and a rendered manifest coexist — the chart is gone by the time any of this
// returns — and every command routed through prepare wants the same answer:
// template and diff show a manifest that could actually be deployed, install
// and upgrade deploy it.
func prepare(release, ref string, f flags, base map[string]any, pol compatPolicy) (manifest string, values map[string]any, rc charts.ReleaseChart, req *charts.Requirements, chartFiles map[string][]byte, code int) {
	ch, loaded, c := loadChart(ref, f)
	if c >= 0 {
		return "", nil, rc, nil, nil, c
	}
	// Before rendering, not after: a chart that needs a newer engine would
	// otherwise fail somewhere inside Render, and "function X not defined" is a
	// far worse answer than naming the version it wants.
	if c := applyCompat(charts.CheckCompat(ch.Metadata), pol, f.skipCompatCheck); c >= 0 {
		return "", nil, rc, nil, nil, c
	}
	if base == nil {
		base = ch.Values
	}
	files, err := readValuesFiles(f.values)
	if err != nil {
		return "", nil, rc, nil, nil, fail(err)
	}
	values, err = charts.MergeValues(base, files, f.sets)
	if err != nil {
		return "", nil, rc, nil, nil, fail(err)
	}
	setFiles, err := readSetFiles(f.setFiles)
	if err != nil {
		return "", nil, rc, nil, nil, fail(err)
	}
	// After the merge and before validation: a --set-file is an override like
	// --set, and the schema should see the content the render will.
	if err := charts.ApplySetFiles(values, setFiles); err != nil {
		return "", nil, rc, nil, nil, fail(err)
	}
	if err := charts.ValidateValues(ch.Schema, values); err != nil {
		return "", nil, rc, nil, nil, fail(err)
	}
	ctx := charts.RenderContext{
		Values:  values,
		Release: charts.ReleaseMeta{Name: release, Namespace: release, Revision: 1},
		Chart:   charts.ChartMeta{Name: ch.Metadata.Name, Version: ch.Metadata.Version, AppVersion: ch.Metadata.AppVersion},
	}
	manifest, err = charts.Render(ch, ctx)
	if err != nil {
		return "", nil, rc, nil, nil, fail(err)
	}
	// requirements.yaml is rendered with the same values as the manifest, so an
	// operator-chosen network/secret name (e.g. database.network) is validated
	// against the name the manifest actually references.
	req, err = charts.RenderRequirements(ch, ctx)
	if err != nil {
		return "", nil, rc, nil, nil, fail(err)
	}
	// fail() prints the error and nothing else: a path a chart may not read is a
	// breaking change with no compatibility flag behind it, so this message is
	// the whole of the migration path and must reach the operator verbatim.
	chartFiles, err = charts.ResolveManifestFiles(manifest, ch.Files, values)
	if err != nil {
		return "", nil, rc, nil, nil, fail(err)
	}
	// The deploy enforces this too; checking here as well means template and
	// diff refuse the manifest a deploy would, rather than print it.
	if _, err := charts.EnvLookups(manifest, chartFiles); err != nil {
		return "", nil, rc, nil, nil, fail(err)
	}
	if err := charts.CheckReserved(manifest, release); err != nil {
		return "", nil, rc, nil, nil, fail(err)
	}
	rc = loaded
	return manifest, values, rc, req, chartFiles, -1
}

// --- uninstall / list / status ---

func chartsUninstall(c chartsCmd, args []string) int {
	pos, f, code := parse(c, args)
	if code >= 0 {
		return code
	}
	if len(pos) != 1 {
		return usageErr("charts uninstall <release> [--purge-volumes]")
	}
	res, err := charts.NewEngine().Uninstall(context.Background(), pos[0], f.purge)
	if err != nil {
		return fail(err)
	}
	outf("release %q uninstalled\n", pos[0])
	if res != nil && len(res.OrphanedNetworks) > 0 {
		out("\nswarmcli auto-created the following external network(s) on install and left\n" +
			"them in place (they may be shared with other stacks):\n")
		for _, n := range res.OrphanedNetworks {
			outf("  %s\n", n)
		}
		out("remove any you no longer need:\n")
		for _, n := range res.OrphanedNetworks {
			outf("  docker network rm %s\n", n)
		}
	}
	if res != nil && res.VolumesMayRemain {
		errf("%s", volumesMayRemainWarning(pos[0]))
	}
	return 0
}

// volumesAllNodesFeature is the feature an extension build enables when it can
// reach volumes on every swarm node; the volumes view gates its hint on it too.
const volumesAllNodesFeature = "volumes-all-nodes"

// volumesMayRemainWarning is what uninstall prints when a purge could not reach
// every node. Without the feature (the base build, or an unlicensed extension
// build) it also says where purging every node is available.
func volumesMayRemainWarning(release string) string {
	w := "warning: the purge could not reach every node of this swarm; volumes on the others are left\n" +
		"in place. On each node, list them with:\n" +
		"  docker volume ls --filter label=com.docker.stack.namespace=" + release + "\n"
	if !features.IsEnabled(volumesAllNodesFeature) {
		w += "Purging volumes on every node is a Business Edition feature: " + view.BELandingURL + "\n"
	}
	return w
}

func chartsList(c chartsCmd, args []string) int {
	if _, _, code := parse(c, args); code >= 0 {
		return code
	}
	rels, err := charts.NewEngine().List(context.Background())
	if err != nil {
		return fail(err)
	}
	if len(rels) == 0 {
		outln("No releases found.")
		return 0
	}
	rows := make([][]string, 0, len(rels))
	for _, r := range rels {
		rows = append(rows, []string{r.Name, strconv.Itoa(r.Revision), r.Status, r.Chart.Name + "-" + r.Chart.Version, r.Created})
	}
	table([]string{"NAME", "REVISION", "STATUS", "CHART", "UPDATED"}, rows)
	return 0
}

func chartsStatus(c chartsCmd, args []string) int {
	pos, _, code := parse(c, args)
	if code >= 0 {
		return code
	}
	if len(pos) != 1 {
		return usageErr("charts status <release>")
	}
	rel, svcs, err := charts.NewEngine().Status(context.Background(), pos[0])
	if err != nil {
		return fail(err)
	}
	outf("NAME: %s\nREVISION: %d\nSTATUS: %s\nCHART: %s-%s\nUPDATED: %s\n\n",
		rel.Name, rel.Revision, rel.Status, rel.Chart.Name, rel.Chart.Version, rel.Created)
	if len(svcs) == 0 {
		outln("No services running.")
		return 0
	}
	rows := make([][]string, 0, len(svcs))
	for _, s := range svcs {
		rows = append(rows, []string{s.Name, s.Mode, s.Replicas, s.Status})
	}
	table([]string{"SERVICE", "MODE", "REPLICAS", "STATUS"}, rows)
	return 0
}

// --- chart source resolution ---

// loadChart resolves a chart reference: an existing local directory or .tgz
// path, otherwise a "repo/chart" reference pulled from a configured repository.
// The resolution itself lives in charts.ChartSource, so apply and the imperative
// commands cannot drift apart.
func loadChart(ref string, f flags) (*charts.Chart, charts.ReleaseChart, int) {
	store, code := newStore(f)
	if code >= 0 {
		return nil, charts.ReleaseChart{}, code
	}
	ch, origin, err := charts.LoadWithOrigin(charts.NewChartSource(store), ref, f.version)
	if err != nil {
		return nil, charts.ReleaseChart{}, fail(err)
	}
	rc := charts.ReleaseChartOf(ch)
	rc.Repo, rc.RepoURL = origin.Repo, origin.URL
	return ch, rc, -1
}

func readValuesFiles(paths []string) ([][]byte, error) {
	var out [][]byte
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read values file '%s': %w", p, err)
		}
		out = append(out, b)
	}
	return out, nil
}

// readSetFiles parses each --set-file "key=path" and reads the file the
// operator named.
//
// Split on the FIRST "=", so a path containing one still works; a key cannot
// contain one, since a values path is dots and bracketed indices.
func readSetFiles(specs []string) ([]charts.SetFile, error) {
	var out []charts.SetFile
	for _, s := range specs {
		key, path, ok := strings.Cut(s, "=")
		if !ok {
			return nil, fmt.Errorf("--set-file '%s': expected key=path", s)
		}
		key, path = strings.TrimSpace(key), strings.TrimSpace(path)
		if key == "" || path == "" {
			return nil, fmt.Errorf("--set-file '%s': expected key=path", s)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("--set-file %s: read '%s': %w", key, path, err)
		}
		out = append(out, charts.SetFile{Key: key, Data: b})
	}
	return out, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
