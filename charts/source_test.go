// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// serveRepo stands up an httptest chart repository serving testdata/demo at the
// given versions, and returns a store with it already added.
func serveRepo(t *testing.T, versions ...string) *RepoStore {
	t.Helper()
	tgz := packDirToTgz(t, "testdata/demo", "demo")

	var b strings.Builder
	b.WriteString("apiVersion: v1\nentries:\n  demo:\n")
	for _, v := range versions {
		b.WriteString("    - name: demo\n      version: " + v + "\n      urls: [\"demo-" + v + ".tgz\"]\n")
	}
	idx := b.String()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/index.yaml"):
			_, _ = w.Write([]byte(idx))
		case strings.HasSuffix(r.URL.Path, ".tgz"):
			_, _ = w.Write([]byte(tgz))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))
	return s
}

func TestChartSourceLoadsFromRepository(t *testing.T) {
	src := NewChartSource(serveRepo(t, "0.1.0", "0.2.0"))

	ch, err := src.Load("eldara/demo", "0.1.0")
	require.NoError(t, err)
	require.Equal(t, "demo", ch.Metadata.Name)

	// No version selects the latest.
	ch, err = src.Load("eldara/demo", "")
	require.NoError(t, err)
	require.Equal(t, "demo", ch.Metadata.Name)

	_, err = src.Load("eldara/demo", "9.9.9")
	require.ErrorContains(t, err, "not found")
}

// A repository chart reports where it came from, so the release can record it.
func TestChartSourceReportsTheRepositoryOrigin(t *testing.T) {
	store := serveRepo(t, "0.1.0")
	repos, err := store.List()
	require.NoError(t, err)

	ch, origin, err := LoadWithOrigin(NewChartSource(store), "eldara/demo", "0.1.0")
	require.NoError(t, err)
	require.Equal(t, "demo", ch.Metadata.Name)
	require.Equal(t, ChartOrigin{Repo: "eldara", URL: repos[0].URL}, origin)
}

// A local chart came from no repository, whether the path is explicit or bare.
func TestChartSourceReportsNoOriginForALocalPath(t *testing.T) {
	src := NewChartSource(nil)
	for _, ref := range []string{"./testdata/demo", "testdata/demo"} {
		ch, origin, err := LoadWithOrigin(src, ref, "")
		require.NoError(t, err, ref)
		require.Equal(t, "demo", ch.Metadata.Name, ref)
		require.Zero(t, origin, ref)
	}
}

// A ChartSource that does not implement OriginSource still loads; it just
// reports no origin.
func TestLoadWithOriginAcceptsAPlainChartSource(t *testing.T) {
	want := &Chart{Metadata: Chartfile{Name: "demo", Version: "0.1.0"}}
	ch, origin, err := LoadWithOrigin(&fakeChartSource{charts: map[string]*Chart{"r/demo@0.1.0": want}}, "r/demo", "0.1.0")
	require.NoError(t, err)
	require.Same(t, want, ch)
	require.Zero(t, origin)
}

func TestChartSourceLoadsLocalDirAndArchive(t *testing.T) {
	src := NewChartSource(nil)

	ch, err := src.Load("./testdata/demo", "")
	require.NoError(t, err)
	require.Equal(t, "demo", ch.Metadata.Name)

	// The .tgz branch: previously only the directory branch was ever exercised.
	tgz := filepath.Join(t.TempDir(), "demo.tgz")
	require.NoError(t, os.WriteFile(tgz, []byte(packDirToTgz(t, "testdata/demo", "demo")), 0o600))

	ch, err = src.Load(tgz, "")
	require.NoError(t, err)
	require.Equal(t, "demo", ch.Metadata.Name)
}

// --version selects a version from a repository index. A local chart carries its
// version in its own Chart.yaml, so there is nothing to select — the flag used to
// be accepted and silently dropped, which meant `install foo ./chart --version
// 2.0.0` quietly installed whatever Chart.yaml said. Both the syntactic-path and
// the bare-directory-name branches must reject it, or half the fix rots.
func TestChartSourceRejectsVersionOnLocalPath(t *testing.T) {
	src := NewChartSource(nil)

	_, err := src.Load("./testdata/demo", "2.0.0")
	require.ErrorContains(t, err, "--version does not apply")

	// Same, via the os.Stat fallback for a path with no "./" prefix.
	_, err = src.Load("testdata/demo", "2.0.0")
	require.ErrorContains(t, err, "--version does not apply")
}

// An explicit path that does not exist is a typo, not a repository reference. It
// must say so, rather than the misleading "must be <repo>/<chart>".
func TestChartSourceMissingLocalPath(t *testing.T) {
	_, err := NewChartSource(nil).Load("./nope/not-here", "")
	require.ErrorContains(t, err, "chart path")
	require.ErrorContains(t, err, "not found")
	require.NotContains(t, err.Error(), "<repo>/<chart>")
}

func TestChartSourceNoRepositoriesConfigured(t *testing.T) {
	_, err := NewChartSource(nil).Load("eldara/demo", "0.1.0")
	require.ErrorContains(t, err, "no repositories are configured")
}

func TestReleaseChartOf(t *testing.T) {
	ch, err := LoadChartDir("testdata/demo")
	require.NoError(t, err)
	rc := ReleaseChartOf(ch)
	require.Equal(t, ch.Metadata.Name, rc.Name)
	require.Equal(t, ch.Metadata.Version, rc.Version)
	require.Equal(t, ch.Metadata.AppVersion, rc.AppVersion)
}
