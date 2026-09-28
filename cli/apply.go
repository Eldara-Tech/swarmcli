// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/utils/textdiff"
)

// chartsApply converges the swarm to a declarative release manifest.
func chartsApply(c chartsCmd, args []string) int {
	pos, f, code := parse(c, args)
	if code >= 0 {
		return code
	}
	// apply's -f names the release manifest, not a values file: per-release values
	// live inside the manifest. A bare positional works too.
	paths := append(append([]string{}, f.values...), pos...)
	if len(paths) != 1 {
		return usageErr("charts apply -f <release-file>")
	}
	rf, err := charts.LoadReleaseFile(paths[0])
	if err != nil {
		return fail(err)
	}

	store, code := newStore(f)
	if code >= 0 {
		return code
	}
	if err := store.EnsureRepos(rf.Repositories); err != nil {
		return fail(err)
	}

	e := charts.NewEngine()
	plan, err := e.PlanApply(context.Background(), rf, charts.NewChartSource(store), charts.PlanOptions{})
	if err != nil {
		return fail(err)
	}

	printPlan(plan, f.diff)

	// Gate the whole plan before converging any of it, matching PlanApply: a
	// release that cannot run on this build must not leave the swarm half
	// converged. Unchanged releases are exempt — they are already deployed, and
	// apply is not going to touch them.
	//
	// The preview verbs only report, so they warn; the rest refuse without ever
	// prompting, because apply is meant to run unattended and must not block on
	// stdin just because a terminal happens to be attached.
	pol := compatEnforceNoPrompt
	if f.dryRun || f.diff {
		pol = compatWarn
	}
	if code := gateApply(plan, pol, f.skipCompatCheck); code >= 0 {
		return code
	}

	// --diff is a preview verb; it must never deploy.
	//
	// Both lists are reported here rather than after the guard: a preview is the
	// most useful place to learn that the file has left a release behind or that
	// the swarm holds one nothing claims, since that is exactly what an operator
	// is checking before committing to the apply.
	if f.dryRun || f.diff {
		reportUnclaimed(plan)
		outln("\ndry-run: nothing was deployed")
		return 0
	}

	install, upgrade, _ := plan.Counts()
	if install+upgrade == 0 {
		reportUnclaimed(plan)
		return 0
	}

	results, err := e.Apply(context.Background(), plan, charts.InstallOptions{
		Wait:         f.wait,
		Timeout:      f.timeout,
		HistoryMax:   f.historyMax,
		ResolveImage: f.resolveImage,
		// From the plan, not recomputed from the file: apply must stamp the
		// same owner it classified against, or a release would be installed
		// under one id and go looking for orphans under another.
		Owner: plan.Owner,
	})
	// Report what did happen before surfacing the failure: a partial apply has
	// still changed the swarm, and the operator needs to know how far it got.
	for _, r := range results {
		if r.Action == charts.ActionUnchanged {
			continue
		}
		outf("%s %s (revision %d)\n", r.Name, r.Action, r.Revision)
	}
	if err != nil {
		return fail(err)
	}
	reportUnclaimed(plan)
	return 0
}

// gateApply returns an exit code for the first release in plan that must stop
// the apply, or -1 when every release may proceed.
func gateApply(plan *charts.Plan, pol compatPolicy, skipCompat bool) int {
	for _, r := range plan.Releases {
		if r.Action == charts.ActionUnchanged {
			continue
		}
		if code := applyCompat(r.Compat, pol, skipCompat); code >= 0 {
			return code
		}
		// The deploy refuses these too, but by then an earlier release may be
		// converged; a preview must say so rather than look clean.
		if _, err := charts.EnvLookups(r.Manifest, r.Files); err != nil {
			return fail(fmt.Errorf("release '%s': %w", r.Name, err))
		}
	}
	return -1
}

func printPlan(plan *charts.Plan, withDiff bool) {
	// The wave column appears only for a plan that has more than one, which is
	// the difference between telling an operator something and adding a column of
	// zeroes to every plan anyone has ever printed.
	waved := spansWaves(plan.Releases)

	var rows [][]string
	for _, r := range plan.Releases {
		from := r.FromVersion
		if from == "" {
			from = "-"
		}
		row := []string{r.Name, r.Ref, from, r.ToVersion, string(r.Action)}
		if waved {
			row = append(row, strconv.Itoa(r.Wave))
		}
		rows = append(rows, row)
	}
	headers := []string{"RELEASE", "CHART", "FROM", "TO", "ACTION"}
	if waved {
		headers = append(headers, "WAVE")
	}
	table(headers, rows)

	if withDiff {
		for _, r := range plan.Releases {
			if r.Action == charts.ActionUnchanged {
				continue
			}
			outf("\n--- %s (%s) ---\n", r.Name, r.Action)
			out(textdiff.Lines(r.CurrentManifest, r.Manifest))
		}
	}

	install, upgrade, unchanged := plan.Counts()
	outf("\n%d to install, %d to upgrade, %d unchanged\n", install, upgrade, unchanged)
	if waved {
		outln("applied in wave order; each wave converges before the next begins")
	}
}

// spansWaves reports whether a plan declares more than one wave, which is the
// only case where the ordering is worth saying anything about.
func spansWaves(releases []charts.ReleasePlan) bool {
	for _, r := range releases {
		if r.Wave != releases[0].Wave {
			return true
		}
	}
	return false
}

// reportUnclaimed names everything the swarm holds that this apply did not just
// deploy — the file's own abandoned releases first, then releases nothing here
// claims. The two are always reported together: they answer one question from
// two directions, and reporting only one of them was the whole defect (a
// dry-run showed neither, which is where they are most useful).
func reportUnclaimed(plan *charts.Plan) {
	reportOrphaned(plan)
	reportUnmanaged(plan)
}

// reportOrphaned names releases this file's own owner installed that it no
// longer declares. Unlike the unmanaged set these are provably obsolete — the
// stamp says this manifest produced them — but apply still removes nothing.
func reportOrphaned(plan *charts.Plan) {
	if len(plan.Orphaned) == 0 {
		return
	}
	outf("\n%d release(s) were installed by this release file but are no longer declared in it:\n", len(plan.Orphaned))
	for _, n := range plan.Orphaned {
		outf("  %s\n", n)
	}
	out("apply does not remove them; uninstall to reclaim them:\n")
	for _, n := range plan.Orphaned {
		outf("  swarmcli charts uninstall %s\n", n)
	}
}

// reportUnmanaged names releases on the swarm that the file neither describes
// nor claims. apply never removes them: nothing says a second file, or a human,
// did not install them, so a genuinely obsolete one is indistinguishable from
// somebody else's. Releases this file does claim are reported by reportOrphaned
// instead.
func reportUnmanaged(plan *charts.Plan) {
	if len(plan.Unmanaged) == 0 {
		return
	}
	outf("\n%d release(s) exist on this swarm but are not in the release file:\n", len(plan.Unmanaged))
	for _, n := range plan.Unmanaged {
		outf("  %s\n", n)
	}
	out("apply does not remove them; uninstall explicitly if they are obsolete:\n")
	for _, n := range plan.Unmanaged {
		outf("  swarmcli charts uninstall %s\n", n)
	}
}

// chartsOutdated compares every installed release against the newest chart
// version its repositories offer. It is the human-facing complement to an
// automated updater watching the release file.
func chartsOutdated(c chartsCmd, args []string) int {
	_, f, code := parse(c, args)
	if code >= 0 {
		return code
	}
	store, code := newStore(f)
	if code >= 0 {
		return code
	}
	// A read-only informational command must degrade, not fail: report against the
	// cached indexes when the network is unavailable. --no-repo-update says the
	// network is not to be used at all, which is the same destination without the
	// timeout — outdated then reports against whatever is cached, which is the
	// honest answer to "what does this machine know".
	if f.noRepoUpdate || noAutoUpdateEnv() {
		errf("not refreshing repositories; comparing against the cached indexes\n")
	} else if _, _, err := store.Update(""); err != nil {
		errf("could not refresh repositories (%v); comparing against the cached indexes\n", err)
	}
	indexes, err := store.Indexes()
	if err != nil {
		return fail(err)
	}
	rels, err := charts.NewEngine().List(context.Background())
	if err != nil {
		return fail(err)
	}

	entries := charts.Outdated(rels, indexes)
	if len(entries) == 0 {
		outln("All releases are up to date.")
		return 0
	}
	var rows [][]string
	for _, e := range entries {
		rows = append(rows, []string{e.Release, e.Chart, e.Repo, e.Installed, e.Latest})
	}
	table([]string{"RELEASE", "CHART", "REPO", "INSTALLED", "LATEST"}, rows)
	outf("\n%d of %d release(s) outdated.\n", len(entries), len(rels))
	return 0
}
