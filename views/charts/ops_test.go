// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package chartsview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/charts"

	"github.com/stretchr/testify/require"
)

// seedRepoCache points the charts state directory at a temp dir holding one
// configured repository and its cached index, exactly as `charts repo add`
// would have left it.
func seedRepoCache(t *testing.T, chart string, versions ...string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	base := filepath.Join(dir, "swarmcli", "charts")
	require.NoError(t, os.MkdirAll(filepath.Join(base, "cache"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(base, "repos.json"),
		[]byte(`[{"name":"testrepo","url":"https://charts.example.com"}]`), 0o600))

	index := "apiVersion: v1\nentries:\n  " + chart + ":\n"
	for _, v := range versions {
		index += "    - name: " + chart + "\n      version: \"" + v + "\"\n"
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(base, "cache", "index-testrepo.yaml"), []byte(index), 0o600))
	return base
}

// The view polls every few seconds, and RepoStore's methods take no context, so
// a fetch started here could not even be cancelled when the view closes. The
// policy is what keeps that from happening, so assert the policy.
func TestRepoStoreNeverGoesToTheNetwork(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	store, err := newRepoStore()
	require.NoError(t, err)
	require.Equal(t, charts.RefreshNever, store.Refresh,
		"any other policy lets the poll fetch an index on its own initiative")
	require.NotNil(t, store.Warnf, "charts is silent unless the embedder wires this")
}

func TestAvailableReadsTheCachedIndex(t *testing.T) {
	seedRepoCache(t, "mychart", "1.0.0", "2.1.0")

	ops := &engineOps{}
	avail, haveIndexes := ops.Available([]charts.Release{{
		Name: "app", Chart: charts.ReleaseChart{Name: "mychart", Version: "1.0.0"},
	}})

	require.True(t, haveIndexes)
	require.Len(t, avail, 1)
	require.Equal(t, "2.1.0", avail["app"].Latest)
	require.True(t, avail["app"].Newer)
}

// A release already on the newest published version is in the map with Newer
// false — present, so the view can say "up to date" rather than "unknown".
func TestAvailableReportsACurrentReleaseRatherThanOmittingIt(t *testing.T) {
	seedRepoCache(t, "mychart", "1.0.0", "2.1.0")

	ops := &engineOps{}
	avail, haveIndexes := ops.Available([]charts.Release{{
		Name: "app", Chart: charts.ReleaseChart{Name: "mychart", Version: "2.1.0"},
	}})

	require.True(t, haveIndexes)
	require.Contains(t, avail, "app")
	require.False(t, avail["app"].Newer)
}

// A chart in no cached index is absent, which is what a local chart looks like
// and is a different answer from being current.
func TestAvailableOmitsAChartInNoIndex(t *testing.T) {
	seedRepoCache(t, "mychart", "1.0.0", "2.1.0")

	ops := &engineOps{}
	avail, haveIndexes := ops.Available([]charts.Release{{
		Name: "app", Chart: charts.ReleaseChart{Name: "local-only", Version: "0.1.0"},
	}})

	require.True(t, haveIndexes)
	require.NotContains(t, avail, "app")
}

// A release that recorded its source is compared against that repository, so
// the view needs the configured repositories as well as their indexes.
func TestAvailableComparesAgainstTheRecordedSource(t *testing.T) {
	seedRepoCache(t, "mychart", "1.0.0", "2.1.0")

	ops := &engineOps{}
	avail, haveIndexes := ops.Available([]charts.Release{
		{Name: "here", Chart: charts.ReleaseChart{Name: "mychart", Version: "1.0.0", Repo: "testrepo", RepoURL: "https://charts.example.com"}},
		{Name: "gone", Chart: charts.ReleaseChart{Name: "mychart", Version: "1.0.0", Repo: "localrepo", RepoURL: "http://127.0.0.1:8080"}},
	})

	require.True(t, haveIndexes)
	require.Equal(t, "testrepo", avail["here"].Repo)
	require.True(t, avail["here"].Newer)
	require.NotContains(t, avail, "gone", "a same-named chart in another repository is not this release's upgrade")
}

func TestAvailableReportsNoIndexesWhenNoneAreCached(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	ops := &engineOps{}
	avail, haveIndexes := ops.Available([]charts.Release{{
		Name: "app", Chart: charts.ReleaseChart{Name: "mychart", Version: "1.0.0"},
	}})

	require.False(t, haveIndexes, "no cached index is not the same answer as up to date")
	require.Empty(t, avail)
}

// The indexes are read once per view, which is what lets the poll ask on every
// tick without re-parsing them.
func TestIndexesAreReadOnce(t *testing.T) {
	base := seedRepoCache(t, "mychart", "1.0.0", "2.1.0")
	ops := &engineOps{}

	rels := []charts.Release{{Name: "app", Chart: charts.ReleaseChart{Name: "mychart", Version: "1.0.0"}}}
	first, haveIndexes := ops.Available(rels)
	require.True(t, haveIndexes)
	require.Len(t, first, 1)

	// Remove the cache entirely: a second read from disk would now find
	// nothing, so an equal answer can only have come from what was already
	// loaded.
	require.NoError(t, os.RemoveAll(base))

	second, haveIndexes := ops.Available(rels)
	require.True(t, haveIndexes)
	require.Equal(t, first, second)
}
