// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package chartsview

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

	writeIndex(t, base, chart, versions...)
	return base
}

// writeIndex replaces testrepo's cached index, as a refresh would.
func writeIndex(t *testing.T, base, chart string, versions ...string) {
	t.Helper()
	index := "apiVersion: v1\nentries:\n  " + chart + ":\n"
	for _, v := range versions {
		index += "    - name: " + chart + "\n      version: \"" + v + "\"\n"
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(base, "cache", "index-testrepo.yaml"), []byte(index), 0o600))
}

// touchIndex sets testrepo's cached index to a known modification time, so a
// test does not depend on two writes landing on distinct clock ticks.
func touchIndex(t *testing.T, base string, when time.Time) {
	t.Helper()
	require.NoError(t, os.Chtimes(filepath.Join(base, "cache", "index-testrepo.yaml"), when, when))
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
	avail, haveIndexes, _ := ops.Available([]charts.Release{{
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
	avail, haveIndexes, _ := ops.Available([]charts.Release{{
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
	avail, haveIndexes, _ := ops.Available([]charts.Release{{
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
	avail, haveIndexes, _ := ops.Available([]charts.Release{
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
	avail, haveIndexes, _ := ops.Available([]charts.Release{{
		Name: "app", Chart: charts.ReleaseChart{Name: "mychart", Version: "1.0.0"},
	}})

	require.False(t, haveIndexes, "no cached index is not the same answer as up to date")
	require.Empty(t, avail)
}

// #726: the view is open, `charts outdated` refreshes the cache from another
// terminal and finds a newer version, and the view has to agree by the next
// poll rather than at the next start.
func TestARefreshedCacheReachesAnOpenView(t *testing.T) {
	base := seedRepoCache(t, "mychart", "1.0.0")
	touchIndex(t, base, time.Now().Add(-time.Hour))
	ops := &engineOps{}
	rels := []charts.Release{{Name: "app", Chart: charts.ReleaseChart{Name: "mychart", Version: "1.0.0"}}}

	before, _, _ := ops.Available(rels)
	require.False(t, before["app"].Newer)

	writeIndex(t, base, "mychart", "1.0.0", "2.1.0")
	touchIndex(t, base, time.Now())

	after, haveIndexes, _ := ops.Available(rels)
	require.True(t, haveIndexes)
	require.Equal(t, "2.1.0", after["app"].Latest)
	require.True(t, after["app"].Newer)
}

// The poll asks on every tick, so an unchanged cache must not be re-parsed.
func TestAnUnchangedCacheIsNotReparsed(t *testing.T) {
	base := seedRepoCache(t, "mychart", "1.0.0", "2.1.0")
	when := time.Now().Add(-time.Hour)
	touchIndex(t, base, when)
	ops := &engineOps{}
	rels := []charts.Release{{Name: "app", Chart: charts.ReleaseChart{Name: "mychart", Version: "1.0.0"}}}

	first, _, _ := ops.Available(rels)
	require.Len(t, first, 1)

	// Different content under the same time: a re-parse would now find no
	// mychart at all, so an equal answer can only be the one already loaded.
	writeIndex(t, base, "other", "9.9.9")
	touchIndex(t, base, when)

	second, _, _ := ops.Available(rels)
	require.Equal(t, first, second)
}

// The repository list decides which index a recorded source resolves to, so a
// change to it alone — a repository moved to another URL — is a change too.
func TestAChangedRepositoryListIsPickedUp(t *testing.T) {
	base := seedRepoCache(t, "mychart", "1.0.0", "2.1.0")
	ops := &engineOps{}
	rels := []charts.Release{{Name: "app", Chart: charts.ReleaseChart{
		Name: "mychart", Version: "1.0.0", Repo: "elsewhere", RepoURL: "https://moved.example.com",
	}}}

	before, _, _ := ops.Available(rels)
	require.NotContains(t, before, "app", "its source is not configured yet")

	require.NoError(t, os.WriteFile(filepath.Join(base, "repos.json"),
		[]byte(`[{"name":"testrepo","url":"https://moved.example.com"}]`), 0o600))

	after, _, _ := ops.Available(rels)
	require.True(t, after["app"].Newer)
}

// checked is the least recently verified index: one stale repository is
// enough to make "nothing newer" an old answer.
func TestAvailableReportsTheOldestVerification(t *testing.T) {
	base := seedRepoCache(t, "mychart", "1.0.0")
	require.NoError(t, os.WriteFile(filepath.Join(base, "repos.json"),
		[]byte(`[{"name":"testrepo","url":"https://charts.example.com"},{"name":"fresh","url":"https://fresh.example.com"}]`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(base, "cache", "index-fresh.yaml"),
		[]byte("apiVersion: v1\nentries: {}\n"), 0o600))
	old := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	touchIndex(t, base, old)

	_, haveIndexes, checked := (&engineOps{}).Available(nil)
	require.True(t, haveIndexes)
	require.True(t, checked.Equal(old), "got %v, want %v", checked, old)
}

// A failed read keeps nothing stuck: the next poll reads again.
func TestAnUnreadableRepositoryListIsRetried(t *testing.T) {
	base := seedRepoCache(t, "mychart", "1.0.0", "2.1.0")
	good, err := os.ReadFile(filepath.Join(base, "repos.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(base, "repos.json"), []byte("not json"), 0o600))
	ops := &engineOps{}

	_, haveIndexes, _ := ops.Available(nil)
	require.False(t, haveIndexes)

	require.NoError(t, os.WriteFile(filepath.Join(base, "repos.json"), good, 0o600))
	_, haveIndexes, _ = ops.Available(nil)
	require.True(t, haveIndexes)
}
