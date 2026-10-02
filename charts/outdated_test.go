// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func idx(chart string, versions ...string) *Index {
	entries := make([]IndexEntry, 0, len(versions))
	for _, v := range versions {
		entries = append(entries, IndexEntry{Name: chart, Version: v})
	}
	return &Index{APIVersion: "v1", Entries: map[string][]IndexEntry{chart: entries}}
}

func rel(name, chart, version string) Release {
	return Release{Name: name, Chart: ReleaseChart{Name: chart, Version: version}}
}

func TestOutdated(t *testing.T) {
	got := Outdated(
		[]Release{rel("hello", "whoami", "0.1.6")},
		nil,
		map[string]*Index{"swarmcli-charts": idx("whoami", "0.1.6", "0.1.8", "0.1.7")},
	)
	require.Len(t, got, 1)
	require.Equal(t, OutdatedEntry{
		Release: "hello", Chart: "whoami", Repo: "swarmcli-charts",
		Installed: "0.1.6", Latest: "0.1.8",
	}, got[0])
}

func TestOutdatedSkipsCurrentReleases(t *testing.T) {
	got := Outdated(
		[]Release{rel("hello", "whoami", "0.1.8")},
		nil,
		map[string]*Index{"swarmcli-charts": idx("whoami", "0.1.8")},
	)
	require.Empty(t, got)
}

// A local chart is in no index; that is not an error, it is just not reportable.
func TestOutdatedSkipsChartsInNoIndex(t *testing.T) {
	got := Outdated(
		[]Release{rel("hello", "mine", "0.1.0")},
		nil,
		map[string]*Index{"swarmcli-charts": idx("whoami", "0.1.8")},
	)
	require.Empty(t, got)
}

// Someone running a version newer than any index offers is not "outdated".
func TestOutdatedIgnoresReleasesAheadOfTheIndex(t *testing.T) {
	got := Outdated(
		[]Release{rel("hello", "whoami", "0.2.0")},
		nil,
		map[string]*Index{"swarmcli-charts": idx("whoami", "0.1.8")},
	)
	require.Empty(t, got)
}

// A release recorded before sources were names no repository, so the highest
// version across all of them wins and the reported repo is the one that supplied
// it.
func TestOutdatedPicksHighestAcrossRepos(t *testing.T) {
	got := Outdated(
		[]Release{rel("hello", "whoami", "0.1.0")},
		nil,
		map[string]*Index{
			"stable": idx("whoami", "0.1.2"),
			"edge":   idx("whoami", "0.2.0"),
		},
	)
	require.Len(t, got, 1)
	require.Equal(t, "0.2.0", got[0].Latest)
	require.Equal(t, "edge", got[0].Repo)
}

// sourced is a release that recorded the repository its chart came from.
func sourced(name, chart, version, repo, repoURL string) Release {
	r := rel(name, chart, version)
	r.Chart.Repo, r.Chart.RepoURL = repo, repoURL
	return r
}

// The swarmcli-charts e2e repro: a release installed from a local test repository
// was reported against the seeded swarmcli-charts, which carries a chart of the
// same name at a higher version. The recorded URL picks the repository, and the
// trailing slash a repository can be configured with does not defeat it.
func TestAvailableUsesTheRepositoryAtTheRecordedURL(t *testing.T) {
	repos := []RepoEntry{
		{Name: "swarmcli-charts", URL: "https://eldara-tech.github.io/swarmcli-charts"},
		{Name: "localrepo", URL: "http://127.0.0.1:8080/"},
	}
	indexes := map[string]*Index{
		"swarmcli-charts": idx("whoami", "0.1.10"),
		"localrepo":       idx("whoami", "0.1.0"),
	}

	got := Available([]Release{
		sourced("ci", "whoami", "0.0.1", "localrepo", "http://127.0.0.1:8080"),
	}, repos, indexes)

	require.Equal(t, Availability{Repo: "localrepo", Latest: "0.1.0", Newer: true}, got["ci"])
}

// A repository re-added under the same name at a new URL is still the one the
// release came from.
func TestAvailableFallsBackToTheRecordedName(t *testing.T) {
	repos := []RepoEntry{
		{Name: "stable", URL: "https://new.example.com"},
		{Name: "edge", URL: "https://edge.example.com"},
	}
	indexes := map[string]*Index{
		"stable": idx("whoami", "0.1.2"),
		"edge":   idx("whoami", "0.2.0"),
	}

	got := Available([]Release{
		sourced("hello", "whoami", "0.1.0", "stable", "https://old.example.com"),
	}, repos, indexes)

	require.Equal(t, Availability{Repo: "stable", Latest: "0.1.2", Newer: true}, got["hello"])
}

// The URL outranks the name: a repository renamed locally keeps its origin, and
// a different repository that took over the old name does not.
func TestAvailablePrefersTheURLOverTheName(t *testing.T) {
	repos := []RepoEntry{
		{Name: "stable", URL: "https://impostor.example.com"},
		{Name: "renamed", URL: "https://stable.example.com"},
	}
	indexes := map[string]*Index{
		"stable":  idx("whoami", "9.0.0"),
		"renamed": idx("whoami", "0.1.2"),
	}

	got := Available([]Release{
		sourced("hello", "whoami", "0.1.0", "stable", "https://stable.example.com"),
	}, repos, indexes)

	require.Equal(t, "renamed", got["hello"].Repo)
	require.Equal(t, "0.1.2", got["hello"].Latest)
}

// A source that is no longer configured has nothing to compare against. A
// same-named chart elsewhere is a different chart, so the release is absent —
// unknowable, like a local chart — rather than guessed at.
func TestAvailableOmitsAReleaseWhoseSourceIsGone(t *testing.T) {
	repos := []RepoEntry{{Name: "swarmcli-charts", URL: "https://eldara-tech.github.io/swarmcli-charts"}}
	indexes := map[string]*Index{"swarmcli-charts": idx("whoami", "0.1.10")}

	got := Available([]Release{
		sourced("ci", "whoami", "0.0.1", "localrepo", "http://127.0.0.1:8080"),
	}, repos, indexes)

	require.NotContains(t, got, "ci")
	require.Empty(t, Outdated([]Release{
		sourced("ci", "whoami", "0.0.1", "localrepo", "http://127.0.0.1:8080"),
	}, repos, indexes))
}

// A configured source whose index is not cached is equally unknowable.
func TestAvailableOmitsAReleaseWhoseSourceHasNoIndex(t *testing.T) {
	repos := []RepoEntry{
		{Name: "localrepo", URL: "http://127.0.0.1:8080"},
		{Name: "swarmcli-charts", URL: "https://eldara-tech.github.io/swarmcli-charts"},
	}
	indexes := map[string]*Index{"swarmcli-charts": idx("whoami", "0.1.10")}

	got := Available([]Release{
		sourced("ci", "whoami", "0.0.1", "localrepo", "http://127.0.0.1:8080"),
	}, repos, indexes)

	require.NotContains(t, got, "ci")
}

func TestOutdatedSortsByRelease(t *testing.T) {
	got := Outdated(
		[]Release{rel("zeta", "whoami", "0.1.0"), rel("alpha", "whoami", "0.1.0")},
		nil,
		map[string]*Index{"r": idx("whoami", "0.1.8")},
	)
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0].Release)
	require.Equal(t, "zeta", got[1].Release)
}

// --- Available ---

// Available answers the question Outdated cannot: an entry with Newer false is
// a release on the newest published version, and no entry at all is a chart in
// no index. Outdated omits both, which made the two indistinguishable.
func TestAvailableSeparatesCurrentFromNotIndexed(t *testing.T) {
	indexes := map[string]*Index{"repo": idx("mychart", "1.0.0", "2.1.0")}

	got := Available([]Release{
		rel("stale", "mychart", "1.0.0"),
		rel("current", "mychart", "2.1.0"),
		rel("ahead", "mychart", "3.0.0"),
		rel("local", "unpublished", "0.1.0"),
	}, nil, indexes)

	require.Equal(t, Availability{Repo: "repo", Latest: "2.1.0", Newer: true}, got["stale"])
	require.Equal(t, Availability{Repo: "repo", Latest: "2.1.0", Newer: false}, got["current"])
	require.Equal(t, Availability{Repo: "repo", Latest: "2.1.0", Newer: false}, got["ahead"],
		"a release ahead of the index has nothing newer to install")
	require.NotContains(t, got, "local", "a chart in no index is absent, not current")
}

func TestAvailableIsEmptyWithoutIndexes(t *testing.T) {
	got := Available([]Release{rel("app", "mychart", "1.0.0")}, nil, nil)
	require.Empty(t, got)
}

// Outdated is now a filter over Available, so this pins the two together: every
// entry Outdated reports must be one Available marked as newer, and nothing
// else.
func TestOutdatedIsAvailableFilteredToUpgrades(t *testing.T) {
	indexes := map[string]*Index{"repo": idx("mychart", "1.0.0", "2.1.0")}
	rels := []Release{
		rel("stale", "mychart", "1.0.0"),
		rel("current", "mychart", "2.1.0"),
		rel("local", "unpublished", "0.1.0"),
	}

	avail := Available(rels, nil, indexes)
	var wantNewer []string
	for name, a := range avail {
		if a.Newer {
			wantNewer = append(wantNewer, name)
		}
	}

	got := Outdated(rels, nil, indexes)
	require.Len(t, got, len(wantNewer))
	for _, e := range got {
		require.Contains(t, wantNewer, e.Release)
		require.Equal(t, avail[e.Release].Latest, e.Latest)
		require.Equal(t, avail[e.Release].Repo, e.Repo)
	}
}
