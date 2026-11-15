// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package chartsview

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/core/primitives/hash"
	"github.com/Eldara-Tech/swarmcli/v2/utils/textdiff"
	inspectview "github.com/Eldara-Tech/swarmcli/v2/views/inspect"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"

	"gopkg.in/yaml.v3"
)

// PollInterval matches the other resource views. It is a var, not a const, so
// tests can shrink it: a tea.Tick cmd invoked synchronously blocks for the full
// interval, and a test that runs one to see what it scheduled would otherwise
// sit here for five seconds.
var PollInterval = 5 * time.Second

// loadTimeout bounds one read of the release records. The whole read is a
// single Docker config listing plus cached snapshot reads, so this is a
// backstop against an unreachable daemon rather than a working budget.
const loadTimeout = 10 * time.Second

type TickMsg struct{ Gen uint64 }

// PollRetryMsg signals that polling found no changes; the Update handler
// schedules the next tick.
type PollRetryMsg struct{}

type SpinnerTickMsg time.Time

// ReleasesLoadedMsg carries a completed read of the release records.
type ReleasesLoadedMsg struct {
	Releases []releaseItem
	// HaveIndexes reports whether there were cached repository indexes to
	// compare versions against. Without them the LATEST column is empty for every
	// release, which must not read as "everything is up to date".
	HaveIndexes bool
	// IndexesChecked is when the least recently verified of those indexes was
	// last verified; zero without HaveIndexes.
	IndexesChecked time.Time
	Err            error
}

// spinnerTickInterval is a var, not a const, so tests can shrink it: a tea.Tick
// cmd invoked synchronously blocks for the full interval.
var spinnerTickInterval = 80 * time.Millisecond

func tickCmd(gen uint64) tea.Cmd {
	return tea.Tick(PollInterval, func(time.Time) tea.Msg { return TickMsg{Gen: gen} })
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(t time.Time) tea.Msg { return SpinnerTickMsg(t) })
}

func (m *Model) loadReleasesCmd() tea.Cmd {
	ops := m.ops
	return func() tea.Msg { return readReleases(ops) }
}

// checkReleasesCmd reloads only when something a reader would notice changed.
func (m *Model) checkReleasesCmd(lastHash uint64) tea.Cmd {
	ops := m.ops
	return func() tea.Msg {
		msg := readReleases(ops)
		if msg.Err != nil {
			return msg
		}
		newHash, hErr := snapshotHash(msg)
		if hErr != nil {
			l().Errorf("Error computing hash: %v", hErr)
			return PollRetryMsg{}
		}
		if newHash != lastHash {
			return msg
		}
		return PollRetryMsg{}
	}
}

// snapshotHash is what the poll compares: the rows, plus the index facts the
// LATEST column and the footer render from. A refreshed cache can leave every
// row as it was — the "?" of no index becoming the "—" of a local chart, or an
// age note going away — and that must still reach the screen.
func snapshotHash(msg ReleasesLoadedMsg) (uint64, error) {
	return hash.Compute(struct {
		Releases    []stableRelease
		HaveIndexes bool
		Checked     time.Time
	}{stableReleases(msg.Releases), msg.HaveIndexes, msg.IndexesChecked})
}

// readReleases builds the rows: one config listing for every record, then one
// cached-snapshot read per release for the live half, then an offline join
// against the cached repository indexes.
func readReleases(ops releaseOps) ReleasesLoadedMsg {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()

	all, err := ops.AllRevisions(ctx)
	if err != nil {
		return ReleasesLoadedMsg{Err: fmt.Errorf("failed to read chart releases: %w", err)}
	}

	items := make([]releaseItem, 0, len(all))
	for name, revs := range all {
		if len(revs) == 0 {
			continue
		}
		cur := revs[len(revs)-1]
		// An uninstalled record is history, not a release: AllRevisions keeps
		// it deliberately, and this view is a list of what is installed.
		if cur.Status == charts.StatusUninstalled {
			continue
		}
		svcs := ops.ServiceStates(ctx, name)
		conv, ok := health(cur, svcs)
		items = append(items, releaseItem{
			Name:      name,
			Revisions: revs,
			Created:   parseCreated(cur.Created),
			Health:    conv,
			HasHealth: ok,
			Services:  svcs,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })

	currents := make([]charts.Release, 0, len(items))
	for _, it := range items {
		currents = append(currents, it.current())
	}
	avail, haveIndexes, checked := ops.Available(currents)
	for i := range items {
		a := avail[items[i].Name]
		items[i].Latest, items[i].Newer = a.Latest, a.Newer
	}
	return ReleasesLoadedMsg{Releases: items, HaveIndexes: haveIndexes, IndexesChecked: checked}
}

// parseCreated returns the zero time for a record whose timestamp is missing or
// malformed, which the CREATED cell renders as an em-dash.
func parseCreated(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// stableReleases projects the fields that define a meaningful change.
//
// Convergence.Reason is deliberately excluded: while a service serves out its
// stability window the reason counts down in milliseconds, so hashing it would
// report a change on every single poll and defeat the gate entirely. The phase
// is what a reader sees change.
func stableReleases(items []releaseItem) []stableRelease {
	out := make([]stableRelease, len(items))
	for i, it := range items {
		s := stableRelease{
			Name:     it.Name,
			Revision: it.revision(),
			Status:   it.status(),
			Chart:    it.chartRef(),
			Created:  it.Created,
			Phase:    string(it.Health.Phase),
			Latest:   it.Latest,
		}
		for _, svc := range it.Services {
			s.Services = append(s.Services, stableService{
				Name: svc.Name, Replicas: svc.Replicas, Status: svc.Status,
			})
		}
		out[i] = s
	}
	return out
}

type stableRelease struct {
	Name     string
	Revision int
	Status   string
	Chart    string
	Created  time.Time
	Phase    string
	Latest   string
	Services []stableService
}

type stableService struct {
	Name     string
	Replicas string
	Status   string
}

// inspectRevisionCmd opens a stored revision's rendered manifest.
func (m *Model) inspectRevisionCmd(release string, rev charts.Release) tea.Cmd {
	return func() tea.Msg {
		return view.NavigateToMsg{
			ViewName: inspectview.ViewName,
			Payload: map[string]any{
				"title":  fmt.Sprintf("Release: %s (rev %d) — manifest", release, rev.Revision),
				"json":   rev.Manifest,
				"format": inspectview.FormatYAML,
			},
		}
	}
}

// diffRevisionCmd shows what a revision changed against the one below it.
//
// Both manifests are already in hand — every revision record stores the
// document it deployed — so this needs no chart, no re-render and no network,
// which is what `charts diff upgrade` cannot say.
func (m *Model) diffRevisionCmd(release string, child childRow) tea.Cmd {
	return func() tea.Msg {
		cur := child.rev
		prev := child.prev
		title := fmt.Sprintf("Release: %s — rev %d → rev %d", release, prev.Revision, cur.Revision)
		if prev.Revision == 0 {
			title = fmt.Sprintf("Release: %s — rev %d (first revision)", release, cur.Revision)
		}
		out := textdiff.Lines(prev.Manifest, cur.Manifest)
		if strings.TrimSpace(out) == "" {
			out = "# No changes."
		}
		return view.NavigateToMsg{
			ViewName: inspectview.ViewName,
			Payload: map[string]any{
				"title": title,
				"json":  out,
				// Raw: a diff is not a YAML document, and the tree view would
				// refuse it or, worse, parse away the +/- prefixes.
				"format": inspectview.FormatRaw,
			},
		}
	}
}

// inspectValuesCmd opens a stored revision's values.
func (m *Model) inspectValuesCmd(release string, rev charts.Release) tea.Cmd {
	return func() tea.Msg {
		title := fmt.Sprintf("Release: %s (rev %d) — values", release, rev.Revision)
		if len(rev.Values) == 0 {
			return view.NavigateToMsg{
				ViewName: inspectview.ViewName,
				Payload: map[string]any{
					"title":  title,
					"json":   "# This revision stored no values.",
					"format": inspectview.FormatRaw,
				},
			}
		}
		out, err := yaml.Marshal(rev.Values)
		if err != nil {
			return view.NavigateToMsg{
				ViewName: inspectview.ViewName,
				Payload: map[string]any{
					"title":  title,
					"json":   fmt.Sprintf("# Error rendering values:\n# %v", err),
					"format": inspectview.FormatRaw,
				},
			}
		}
		return view.NavigateToMsg{
			ViewName: inspectview.ViewName,
			Payload: map[string]any{
				"title":  title,
				"json":   string(out),
				"format": inspectview.FormatYAML,
			},
		}
	}
}
