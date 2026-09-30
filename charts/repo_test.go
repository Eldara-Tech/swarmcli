// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestStore returns a store in a fresh directory that accepts the plain-http
// URL an httptest server hands out. Chart repositories are https-only by default
// (see RepoStore.AllowPlaintext); the tests below are about everything else, and
// the gate itself is covered by TestRepoStoreRefusesPlaintextURL.
func newTestStore(t *testing.T) *RepoStore {
	t.Helper()
	s := NewRepoStoreAt(t.TempDir())
	s.AllowPlaintext = true
	return s
}

func TestRepoStoreAddListRemove(t *testing.T) {
	idx := `apiVersion: v1
entries:
  demo:
    - name: demo
      version: 0.1.0
      description: demo chart
      urls: ["demo-0.1.0.tgz"]
    - name: demo
      version: 0.2.0
      description: demo chart
      urls: ["demo-0.2.0.tgz"]
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.yaml") {
			_, _ = w.Write([]byte(idx))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))

	// duplicate rejected
	require.Error(t, s.Add("eldara", srv.URL))

	repos, err := s.List()
	require.NoError(t, err)
	require.Len(t, repos, 1)
	require.Equal(t, "eldara", repos[0].Name)

	// search finds the latest version only
	hits, err := s.Search("demo")
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "0.2.0", hits[0].Entry.Version)

	// resolve latest + specific
	e, base, err := s.Resolve("eldara/demo", "")
	require.NoError(t, err)
	require.Equal(t, "0.2.0", e.Version)
	require.Equal(t, srv.URL, base)

	e, _, err = s.Resolve("eldara/demo", "0.1.0")
	require.NoError(t, err)
	require.Equal(t, "0.1.0", e.Version)

	_, _, err = s.Resolve("eldara/demo", "9.9.9")
	require.Error(t, err)

	require.NoError(t, s.Remove("eldara"))
	require.Error(t, s.Remove("eldara")) // gone
}

// Resolve must accept both the plain SemVer chart version ("0.1.3") and the
// "v"-prefixed form users copy from the release git tag ("v0.1.3").
func TestResolveVersionPrefixNormalization(t *testing.T) {
	idx := `apiVersion: v1
entries:
  whoami:
    - name: whoami
      version: 0.1.3
      description: demo chart
      urls: ["whoami-v0.1.3.tgz"]
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.yaml") {
			_, _ = w.Write([]byte(idx))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))

	for _, want := range []string{"0.1.3", "v0.1.3"} {
		e, _, err := s.Resolve("eldara/whoami", want)
		require.NoErrorf(t, err, "version %q should resolve", want)
		require.Equal(t, "0.1.3", e.Version)
	}

	// A genuinely absent version still errors, prefixed or not.
	_, _, err := s.Resolve("eldara/whoami", "v9.9.9")
	require.Error(t, err)
}

// Update reports a repo as unchanged when the served index is byte-identical to
// the cached copy, and as changed once the served content differs.
func TestUpdateReportsChangedVsUnchanged(t *testing.T) {
	body := `apiVersion: v1
entries:
  demo:
    - {name: demo, version: 0.1.0, urls: ["demo-0.1.0.tgz"]}
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.yaml") {
			_, _ = w.Write([]byte(body))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL)) // Add caches the index

	// Nothing changed since Add → already up-to-date.
	changed, unchanged, err := s.Update("eldara")
	require.NoError(t, err)
	require.Empty(t, changed)
	require.Equal(t, []string{"eldara"}, unchanged)

	// Serve different content → reported as changed.
	body = `apiVersion: v1
entries:
  demo:
    - {name: demo, version: 0.2.0, urls: ["demo-0.2.0.tgz"]}
`
	changed, unchanged, err = s.Update("eldara")
	require.NoError(t, err)
	require.Equal(t, []string{"eldara"}, changed)
	require.Empty(t, unchanged)
}

func TestRepoStoreAddRejectsBadURL(t *testing.T) {
	s := newTestStore(t)
	require.Error(t, s.Add("x", "not-a-url"))
	require.Error(t, s.Add("x", "ftp://example.com"))
}

// Seed writes only where nothing was ever configured, and fetches nothing: the
// entries below point at a URL no test server answers.
func TestRepoStoreSeed(t *testing.T) {
	seed := []RepoEntry{{Name: "default", URL: "https://charts.invalid"}}

	s := NewRepoStoreAt(t.TempDir())
	require.NoError(t, s.Seed(seed))
	repos, err := s.List()
	require.NoError(t, err)
	require.Equal(t, seed, repos)

	// A removed seed stays removed: the now-empty file is a choice.
	require.NoError(t, s.Remove("default"))
	require.NoError(t, s.Seed(seed))
	repos, err = s.List()
	require.NoError(t, err)
	require.Empty(t, repos)

	// Existing configuration is never touched.
	s = NewRepoStoreAt(t.TempDir())
	require.NoError(t, s.save([]RepoEntry{{Name: "mine", URL: "https://mine.invalid"}}))
	require.NoError(t, s.Seed(seed))
	repos, err = s.List()
	require.NoError(t, err)
	require.Equal(t, []RepoEntry{{Name: "mine", URL: "https://mine.invalid"}}, repos)
}

// Seed applies the same checks as Add, before writing anything.
func TestRepoStoreSeedRejectsInvalidEntries(t *testing.T) {
	s := NewRepoStoreAt(t.TempDir())
	require.Error(t, s.Seed([]RepoEntry{{Name: "..", URL: "https://charts.invalid"}}))
	require.Error(t, s.Seed([]RepoEntry{{Name: "plain", URL: "http://charts.invalid"}}))
	_, err := os.Stat(s.reposFile())
	require.True(t, os.IsNotExist(err), "a refused seed writes nothing")
}

// A repository name is a path component of its cached index, and indexFile glues
// it on *after* "index-" — so "index-.." is an ordinary segment that Clean has
// nothing to collapse, and a traversing name escapes for the price of one extra
// "../". These assertions are about where the bytes landed, not only about the
// error returned: containment is the property under test.
func TestRepoStoreContainsTraversingRepositoryName(t *testing.T) {
	body := "apiVersion: v1\nentries:\n  demo:\n    - {name: demo, version: 0.1.0, urls: [\"d.tgz\"]}\n"
	up := true
	srv := newIndexServer(t, &body, &up)

	// The store lives two directories below root, and the index would be written
	// a third down in "cache" — so four "../" reach root: one is eaten unescaping
	// the "index-.." segment, the other three walk back up.
	root := t.TempDir()
	s := NewRepoStoreAt(filepath.Join(root, "state"))
	s.AllowPlaintext = true
	const escape = "../../../../pwned"
	landing := filepath.Join(root, "pwned.yaml")

	require.ErrorContains(t, s.Add(escape, srv.URL), "invalid repository name")
	require.NoFileExists(t, landing)

	// Add settles the name before it fetches anything, so an unreachable
	// repository does not mask what is actually wrong with the request.
	up = false
	require.ErrorContains(t, s.Add(escape, srv.URL), "invalid repository name")
	up = true

	// The same name arriving from repos.json instead of the command line — an
	// older store, or one somebody hand-edited — must be refused just as hard.
	// That is why the check lives in indexFile and not only in Add.
	require.NoError(t, s.save([]RepoEntry{{Name: escape, URL: srv.URL}}))
	_, _, err := s.Update("")
	require.ErrorContains(t, err, "invalid repository name")
	require.NoFileExists(t, landing)

	_, err = s.LoadIndex(escape)
	require.ErrorContains(t, err, "invalid repository name")

	// And an ordinary name still lands exactly where it always did.
	good := newTestStore(t)
	require.NoError(t, good.Add("eldara", srv.URL))
	require.FileExists(t, filepath.Join(good.dir, "cache", "index-eldara.yaml"))
}

// Plain http is refused by default. A chart repository serves the tarball that
// *becomes* the deployed workload, so whoever is on the path chooses what runs —
// and the digest that would catch a swap is published in the same index, over the
// same channel. The refusal names its opt-out, because an internal registry on a
// trusted network is a setup that worked until this change.
func TestRepoStoreRefusesPlaintextURL(t *testing.T) {
	body := "apiVersion: v1\nentries: {}\n"
	up := true
	srv := newIndexServer(t, &body, &up)

	t.Run("add is refused and records nothing", func(t *testing.T) {
		s := NewRepoStoreAt(t.TempDir())
		err := s.Add("eldara", srv.URL)
		require.ErrorContains(t, err, "plaintext")
		require.ErrorContains(t, err, AllowPlaintextEnv)
		require.NotContains(t, err.Error(), "index download failed",
			"the scheme is settled before the fetch, so nothing may blame the download")

		repos, lerr := s.List()
		require.NoError(t, lerr)
		require.Empty(t, repos)
	})

	// Otherwise the default would only bind repositories nobody has added yet.
	t.Run("an already-configured http repository is refused too", func(t *testing.T) {
		s := NewRepoStoreAt(t.TempDir())
		require.NoError(t, s.save([]RepoEntry{{Name: "old", URL: srv.URL}}))
		_, _, err := s.Update("")
		require.ErrorContains(t, err, "plaintext")
	})

	// The index can point the tarball anywhere, so the repository's own scheme
	// does not settle the download's.
	t.Run("a plaintext tarball URL is refused", func(t *testing.T) {
		s := NewRepoStoreAt(t.TempDir())
		_, err := s.Pull(IndexEntry{Name: "demo", URLs: []string{"http://example.com/demo.tgz"}}, "https://example.com")
		require.ErrorContains(t, err, "plaintext")
	})

	t.Run("a malformed URL is still refused, and not as plaintext", func(t *testing.T) {
		s := NewRepoStoreAt(t.TempDir())
		err := s.Add("eldara", "not-a-url")
		require.ErrorContains(t, err, "absolute http(s)")
		require.NotContains(t, err.Error(), "plaintext")
		require.NotContains(t, err.Error(), "index download failed")
	})

	// https goes through. Only the test CA is added to the store's own client, so
	// its timeout and redirect policy still apply — this is the real path.
	t.Run("https is accepted", func(t *testing.T) {
		tsrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/index.yaml") {
				_, _ = w.Write([]byte(body))
				return
			}
			http.NotFound(w, r)
		}))
		defer tsrv.Close()

		s := NewRepoStoreAt(t.TempDir())
		s.client.Transport = tsrv.Client().Transport
		require.NoError(t, s.Add("secure", tsrv.URL))
	})

	t.Run("the opt-out restores an internal plaintext registry", func(t *testing.T) {
		s := newTestStore(t)
		require.NoError(t, s.Add("internal", srv.URL))
	})
}

// Pull must refuse a chart URL that is not http(s) (e.g. a malicious index
// pointing at file://), before any download happens.
func TestPullRejectsNonHTTPScheme(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Pull(IndexEntry{Name: "demo", URLs: []string{"file:///etc/passwd"}}, "http://example.com")
	require.ErrorContains(t, err, "http(s)")
}

func TestUpdateUnknownRepo(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.Update("ghost")
	require.ErrorContains(t, err, "not found")
}

func TestLoadIndexMissing(t *testing.T) {
	s := newTestStore(t)
	_, err := s.LoadIndex("ghost")
	require.ErrorContains(t, err, "no cached index")
}

// Update over all repos surfaces a mid-loop fetch failure with the repo name.
func TestUpdateReportsFetchFailure(t *testing.T) {
	serve := true
	idx := "apiVersion: v1\nentries: {}\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serve && strings.HasSuffix(r.URL.Path, "/index.yaml") {
			_, _ = w.Write([]byte(idx))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))
	serve = false
	_, _, err := s.Update("") // all repos
	require.ErrorContains(t, err, `update 'eldara'`)
}

// A failed index download must not leave the repository half-added: List stays
// empty and the name can be reused once the index is reachable.
func TestRepoStoreAddIndexFailureDoesNotPersist(t *testing.T) {
	var serve bool
	idx := "apiVersion: v1\nentries: {}\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serve && strings.HasSuffix(r.URL.Path, "/index.yaml") {
			_, _ = w.Write([]byte(idx))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s := newTestStore(t)

	// index 404s → add fails and persists nothing
	require.Error(t, s.Add("eldara", srv.URL))
	repos, err := s.List()
	require.NoError(t, err)
	require.Empty(t, repos)

	// same name succeeds once the index is reachable
	serve = true
	require.NoError(t, s.Add("eldara", srv.URL))
	repos, err = s.List()
	require.NoError(t, err)
	require.Len(t, repos, 1)
}

// A github.com repository URL never serves a chart index; the hint should point
// the user at the lowercased GitHub Pages URL. Other hosts get no hint.
func TestGithubPagesHint(t *testing.T) {
	require.Contains(t,
		githubPagesHint("https://github.com/Eldara-Tech/swarmcli-charts"),
		"https://eldara-tech.github.io/swarmcli-charts")
	require.Contains(t, githubPagesHint("https://github.com/Eldara-Tech"), "<org>.github.io")
	require.Empty(t, githubPagesHint("https://eldara-tech.github.io/swarmcli-charts"))
	require.Empty(t, githubPagesHint("https://example.com/charts"))
}

func TestPullChart(t *testing.T) {
	tgz := packDirToTgz(t, "testdata/demo", "demo")
	idx := `apiVersion: v1
entries:
  demo:
    - name: demo
      version: 0.1.0
      urls: ["demo-0.1.0.tgz"]
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/index.yaml"):
			_, _ = w.Write([]byte(idx))
		case strings.HasSuffix(r.URL.Path, "demo-0.1.0.tgz"):
			_, _ = w.Write([]byte(tgz))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))
	e, base, err := s.Resolve("eldara/demo", "")
	require.NoError(t, err)
	ch, err := s.Pull(e, base)
	require.NoError(t, err)
	require.Equal(t, "demo", ch.Metadata.Name)
}

// serveChart stands up a repository whose index entry for demo-0.1.0 carries the
// given digest (verbatim — "" means the entry publishes none), and returns the
// store plus the resolved entry and base URL, ready to Pull.
func serveChart(t *testing.T, digest string, tgz []byte) (*RepoStore, IndexEntry, string) {
	t.Helper()
	return serveChartFunc(t, digest, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(tgz)
	})
}

// serveChartFunc is serveChart with the tarball response under the test's
// control, for the cases that are about what the repository does on the second
// request rather than what it serves on the first.
func serveChartFunc(t *testing.T, digest string, tarball http.HandlerFunc) (*RepoStore, IndexEntry, string) {
	t.Helper()
	digestLine := ""
	if digest != "" {
		digestLine = "\n      digest: " + digest
	}
	idx := `apiVersion: v1
entries:
  demo:
    - name: demo
      version: 0.1.0
      urls: ["demo-0.1.0.tgz"]` + digestLine + "\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/index.yaml"):
			_, _ = w.Write([]byte(idx))
		case strings.HasSuffix(r.URL.Path, "demo-0.1.0.tgz"):
			tarball(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))
	e, base, err := s.Resolve("eldara/demo", "")
	require.NoError(t, err)
	return s, e, base
}

func TestPullVerifiesDigest(t *testing.T) {
	tgz := []byte(packDirToTgz(t, "testdata/demo", "demo"))
	sum := sha256.Sum256(tgz)
	good := hex.EncodeToString(sum[:])

	t.Run("prefixed digest matches", func(t *testing.T) {
		s, e, base := serveChart(t, "sha256:"+good, tgz)
		ch, err := s.Pull(e, base)
		require.NoError(t, err)
		require.Equal(t, "demo", ch.Metadata.Name)
	})

	// `helm repo index` writes a bare hex sum, with no algorithm prefix.
	t.Run("bare hex digest matches", func(t *testing.T) {
		s, e, base := serveChart(t, good, tgz)
		ch, err := s.Pull(e, base)
		require.NoError(t, err)
		require.Equal(t, "demo", ch.Metadata.Name)
	})

	// Hex is case-insensitive, and index generators differ. Without this case,
	// replacing strings.EqualFold with != still passes the suite — while rejecting
	// every repository that publishes uppercase hex.
	t.Run("uppercase hex digest matches", func(t *testing.T) {
		s, e, base := serveChart(t, "sha256:"+strings.ToUpper(good), tgz)
		ch, err := s.Pull(e, base)
		require.NoError(t, err)
		require.Equal(t, "demo", ch.Metadata.Name)
	})

	t.Run("mismatch is fatal", func(t *testing.T) {
		s, e, base := serveChart(t, "sha256:"+strings.Repeat("0", 64), tgz)
		ch, err := s.Pull(e, base)
		require.ErrorContains(t, err, "digest mismatch")
		require.ErrorContains(t, err, "charts repo update")
		require.Nil(t, ch)
	})

	// An algorithm we cannot check must fail closed rather than silently skip
	// verification.
	t.Run("unsupported algorithm is fatal", func(t *testing.T) {
		s, e, base := serveChart(t, "sha512:"+good, tgz)
		ch, err := s.Pull(e, base)
		require.ErrorContains(t, err, "unsupported digest algorithm")
		require.Nil(t, ch)
	})

	// Nothing was verified before this check existed, so an index that publishes
	// no digest still installs — loudly.
	t.Run("absent digest warns but installs", func(t *testing.T) {
		s, e, base := serveChart(t, "", tgz)
		var warnings []string
		s.Warnf = func(format string, a ...any) {
			warnings = append(warnings, fmt.Sprintf(format, a...))
		}
		ch, err := s.Pull(e, base)
		require.NoError(t, err)
		require.Equal(t, "demo", ch.Metadata.Name)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0], "no digest")
	})
}

func TestPullRejectsOversizedArchive(t *testing.T) {
	s, e, base := serveChart(t, "", bytes.Repeat([]byte{0}, maxChartArchiveSize+1))
	_, err := s.Pull(e, base)
	require.ErrorContains(t, err, "exceeds")
}

// --- the archive cache: what a second Pull does, and what it refuses to do ---

// fastPullBackoff shrinks the retry schedule for a test that means to exercise
// it. The real one sleeps seconds, and the suite is not what it is there for.
func fastPullBackoff(t *testing.T) {
	t.Helper()
	saved := pullBackoff
	pullBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { pullBackoff = saved })
}

func TestPullCachesTheArchive(t *testing.T) {
	tgz := []byte(packDirToTgz(t, "testdata/demo", "demo"))
	sum := sha256.Sum256(tgz)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	// counting serves the archive and records how many times it was asked for.
	counting := func(hits *int) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			*hits++
			_, _ = w.Write(tgz)
		}
	}

	t.Run("a second pull does not download again", func(t *testing.T) {
		var hits int
		s, e, base := serveChartFunc(t, digest, counting(&hits))
		for range 3 {
			ch, err := s.Pull(e, base)
			require.NoError(t, err)
			require.Equal(t, "demo", ch.Metadata.Name)
		}
		require.Equal(t, 1, hits, "a pinned, digest-verified archive is worth downloading once")
	})

	// The failure this whole thing is about: swarmcli-cd re-plans every three
	// minutes, and a gateway blip on any one of those ticks used to fail the
	// application's whole reconcile (#605).
	t.Run("the cache answers when the repository is down", func(t *testing.T) {
		down := false
		s, e, base := serveChartFunc(t, digest, func(w http.ResponseWriter, _ *http.Request) {
			if down {
				http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
				return
			}
			_, _ = w.Write(tgz)
		})
		_, err := s.Pull(e, base)
		require.NoError(t, err)

		down = true
		ch, err := s.Pull(e, base)
		require.NoError(t, err)
		require.Equal(t, "demo", ch.Metadata.Name)
	})

	// The shape that makes this cache worth having: swarmcli-cd constructs a
	// fresh RepoStore for every reconcile, against the same directory. The win
	// has to survive the store, not merely the call.
	t.Run("a second store at the same directory reuses the cache", func(t *testing.T) {
		var hits int
		s, e, base := serveChartFunc(t, digest, counting(&hits))
		_, err := s.Pull(e, base)
		require.NoError(t, err)

		next := NewRepoStoreAt(s.dir)
		next.AllowPlaintext = true
		ch, err := next.Pull(e, base)
		require.NoError(t, err)
		require.Equal(t, "demo", ch.Metadata.Name)
		require.Equal(t, 1, hits)
	})

	// What a chart rebuilt and re-uploaded under one version looks like from
	// here. The cache is keyed on the digest and the bytes are re-checked
	// against it, so the stale copy cannot answer for the new entry — which is
	// the whole reason this cache is safe and a name-and-version one would not
	// be.
	t.Run("a moved digest misses, and the download is judged by the new one", func(t *testing.T) {
		var hits int
		s, e, base := serveChartFunc(t, digest, counting(&hits))
		_, err := s.Pull(e, base)
		require.NoError(t, err)
		require.Equal(t, 1, hits)

		moved := e
		moved.Digest = "sha256:" + strings.Repeat("a", 64)
		_, err = s.Pull(moved, base)
		require.ErrorContains(t, err, "digest mismatch")
		require.Equal(t, 2, hits, "the cache must not answer for a digest it does not hold")
	})

	t.Run("an entry with no digest is never cached", func(t *testing.T) {
		var hits int
		s, e, base := serveChartFunc(t, "", counting(&hits))
		s.Warnf = func(string, ...any) {}
		for range 2 {
			_, err := s.Pull(e, base)
			require.NoError(t, err)
		}
		require.Equal(t, 2, hits, "nothing could validate the read, so there is nothing to cache")
		require.NoDirExists(t, s.chartCacheDir())
	})

	t.Run("a corrupted cache file falls through to a download", func(t *testing.T) {
		var hits int
		s, e, base := serveChartFunc(t, digest, counting(&hits))
		_, err := s.Pull(e, base)
		require.NoError(t, err)

		cached := filepath.Join(s.chartCacheDir(), cacheKey(e)+".tgz")
		require.NoError(t, os.WriteFile(cached, []byte("not a chart"), 0o644))

		ch, err := s.Pull(e, base)
		require.NoError(t, err)
		require.Equal(t, "demo", ch.Metadata.Name)
		require.Equal(t, 2, hits)
	})

	// The half of the TTL bound that keeps it from evicting the one archive
	// every tick is using.
	t.Run("a hit keeps the archive from ageing out", func(t *testing.T) {
		s, e, base := serveChart(t, digest, tgz)
		_, err := s.Pull(e, base)
		require.NoError(t, err)

		cached := filepath.Join(s.chartCacheDir(), cacheKey(e)+".tgz")
		old := time.Now().Add(-chartCacheTTL - time.Hour)
		require.NoError(t, os.Chtimes(cached, old, old))

		_, err = s.Pull(e, base)
		require.NoError(t, err)
		info, err := os.Stat(cached)
		require.NoError(t, err)
		require.WithinDuration(t, time.Now(), info.ModTime(), time.Minute,
			"otherwise a pinned chart ages out of its own cache and is downloaded again")
	})
}

func TestCacheKey(t *testing.T) {
	hexsum := strings.Repeat("ab", sha256.Size)
	require.Equal(t, hexsum, cacheKey(IndexEntry{Digest: "sha256:" + hexsum}))
	// `helm repo index` writes a bare hex sum, and hex is case-insensitive.
	require.Equal(t, hexsum, cacheKey(IndexEntry{Digest: hexsum}))
	require.Equal(t, hexsum, cacheKey(IndexEntry{Digest: strings.ToUpper(hexsum)}))

	// Anything this package cannot verify is not cacheable: a read of it would
	// have nothing to validate it against.
	require.Empty(t, cacheKey(IndexEntry{}))
	require.Empty(t, cacheKey(IndexEntry{Digest: "sha512:" + hexsum}))
	require.Empty(t, cacheKey(IndexEntry{Digest: "sha256:" + hexsum[:32]}))
	require.Empty(t, cacheKey(IndexEntry{Digest: "sha256:" + strings.Repeat("zz", sha256.Size)}))
	// The key becomes a path component, and the digest is repository-supplied.
	require.Empty(t, cacheKey(IndexEntry{Digest: "sha256:../../../etc/passwd"}))
}

func TestSweepChartCacheDropsWhatNothingReads(t *testing.T) {
	dir := t.TempDir()
	fresh, stale := filepath.Join(dir, "fresh.tgz"), filepath.Join(dir, "stale.tgz")
	require.NoError(t, os.WriteFile(fresh, []byte("f"), 0o644))
	require.NoError(t, os.WriteFile(stale, []byte("s"), 0o644))
	old := time.Now().Add(-chartCacheTTL - time.Hour)
	require.NoError(t, os.Chtimes(stale, old, old))

	sweepChartCache(dir)

	require.FileExists(t, fresh)
	require.NoFileExists(t, stale)
}

func TestPullRetriesATransientFailure(t *testing.T) {
	tgz := []byte(packDirToTgz(t, "testdata/demo", "demo"))
	sum := sha256.Sum256(tgz)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	t.Run("a 504 that clears is ridden out", func(t *testing.T) {
		fastPullBackoff(t)
		var hits int
		s, e, base := serveChartFunc(t, digest, func(w http.ResponseWriter, _ *http.Request) {
			hits++
			if hits <= len(pullBackoff) {
				http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
				return
			}
			_, _ = w.Write(tgz)
		})
		var warnings []string
		s.Warnf = func(format string, a ...any) { warnings = append(warnings, fmt.Sprintf(format, a...)) }

		ch, err := s.Pull(e, base)
		require.NoError(t, err)
		require.Equal(t, "demo", ch.Metadata.Name)
		require.Equal(t, len(pullBackoff)+1, hits)
		require.Len(t, warnings, len(pullBackoff), "every wait the caller cannot interrupt is announced")
		require.Contains(t, warnings[0], "504")
	})

	t.Run("a persistent 504 gives up", func(t *testing.T) {
		fastPullBackoff(t)
		var hits int
		s, e, base := serveChartFunc(t, digest, func(w http.ResponseWriter, _ *http.Request) {
			hits++
			http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
		})
		s.Warnf = func(string, ...any) {}

		_, err := s.Pull(e, base)
		require.ErrorContains(t, err, "504")
		require.Equal(t, len(pullBackoff)+1, hits, "the schedule is the whole budget")
	})

	// A repository that does not have this chart has said so. Asking twice more
	// gets the same answer, three times as slowly.
	t.Run("a 404 is not retried", func(t *testing.T) {
		fastPullBackoff(t)
		var hits int
		s, e, base := serveChartFunc(t, digest, func(w http.ResponseWriter, r *http.Request) {
			hits++
			http.NotFound(w, r)
		})

		_, err := s.Pull(e, base)
		require.ErrorContains(t, err, "404")
		require.Equal(t, 1, hits)
	})
}

func TestCompareVersions(t *testing.T) {
	require.Equal(t, 1, compareVersions("0.2.0", "0.1.0"))
	require.Equal(t, -1, compareVersions("1.0.0", "1.0.1"))
	require.Equal(t, 1, compareVersions("v3.5.0", "3.4.0"))
}

// --- EnsureRepos: the gate every `charts apply` passes through ---

// newIndexServer serves a mutable index and reports whether it is currently up.
func newIndexServer(t *testing.T, body *string, up *bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *up && strings.HasSuffix(r.URL.Path, "/index.yaml") {
			_, _ = w.Write([]byte(*body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEnsureReposAddsAbsentRepository(t *testing.T) {
	body := "apiVersion: v1\nentries:\n  demo:\n    - {name: demo, version: 0.1.0, urls: [\"d.tgz\"]}\n"
	up := true
	srv := newIndexServer(t, &body, &up)

	s := newTestStore(t)
	require.NoError(t, s.EnsureRepos([]RepoSpec{{Name: "eldara", URL: srv.URL}}))

	repos, err := s.List()
	require.NoError(t, err)
	require.Len(t, repos, 1)
	// The index must actually be cached, not merely the name recorded — apply
	// resolves against the cache immediately afterwards.
	idx, err := s.LoadIndex("eldara")
	require.NoError(t, err)
	require.Contains(t, idx.Entries, "demo")
}

// A trailing slash in the manifest's URL must take the refresh branch, not the
// "different URL" hard error. Add normalises with TrimRight, so without this the
// refusal would fire on every manifest whose url ends in "/".
func TestEnsureReposRefreshesSameRepositoryAndToleratesTrailingSlash(t *testing.T) {
	body := "apiVersion: v1\nentries:\n  demo:\n    - {name: demo, version: 0.1.0, urls: [\"d.tgz\"]}\n"
	up := true
	srv := newIndexServer(t, &body, &up)

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))

	body = "apiVersion: v1\nentries:\n  demo:\n    - {name: demo, version: 0.2.0, urls: [\"d.tgz\"]}\n"
	require.NoError(t, s.EnsureRepos([]RepoSpec{{Name: "eldara", URL: srv.URL + "/"}}))

	idx, err := s.LoadIndex("eldara")
	require.NoError(t, err)
	require.Equal(t, "0.2.0", idx.Entries["demo"][0].Version, "the index must have been refreshed")
}

// Never silently repoint an existing repository at a different origin: that would
// let a manifest swap out where every one of its charts comes from.
func TestEnsureReposRefusesToRepointADifferentURL(t *testing.T) {
	body := "apiVersion: v1\nentries: {}\n"
	up := true
	srvA := newIndexServer(t, &body, &up)
	srvB := newIndexServer(t, &body, &up)

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srvA.URL))

	err := s.EnsureRepos([]RepoSpec{{Name: "eldara", URL: srvB.URL}})
	require.ErrorContains(t, err, "different URL")

	// The refusal is the point: assert the store was not repointed.
	repos, lerr := s.List()
	require.NoError(t, lerr)
	require.Equal(t, srvA.URL, repos[0].URL)
}

// A network blip must not fail the apply. Every version in a manifest is pinned,
// so a stale cache can only fail to resolve a chart — never resolve it to the
// wrong one. This is the "CI keeps working when the network wobbles" contract.
func TestEnsureReposWarnsAndUsesCacheWhenRefreshFails(t *testing.T) {
	body := "apiVersion: v1\nentries:\n  demo:\n    - {name: demo, version: 0.1.0, urls: [\"d.tgz\"]}\n"
	up := true
	srv := newIndexServer(t, &body, &up)

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))

	var warnings []string
	s.Warnf = func(format string, a ...any) { warnings = append(warnings, fmt.Sprintf(format, a...)) }

	up = false // the repository goes away
	require.NoError(t, s.EnsureRepos([]RepoSpec{{Name: "eldara", URL: srv.URL}}),
		"a failed refresh must not fail the apply")
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "could not refresh repository")

	// The cached index is still usable.
	idx, err := s.LoadIndex("eldara")
	require.NoError(t, err)
	require.Equal(t, "0.1.0", idx.Entries["demo"][0].Version)
}

func TestEnsureReposNoSpecsIsANoOp(t *testing.T) {
	s := newTestStore(t)
	require.NoError(t, s.EnsureRepos(nil))
	repos, err := s.List()
	require.NoError(t, err)
	require.Empty(t, repos)
}

// Indexes backs `charts outdated`, which must not blow up on a repository whose
// index was never fetched.
func TestIndexesSkipsRepositoriesWithNoCachedIndex(t *testing.T) {
	body := "apiVersion: v1\nentries:\n  demo:\n    - {name: demo, version: 0.1.0, urls: [\"d.tgz\"]}\n"
	up := true
	srv := newIndexServer(t, &body, &up)

	s := newTestStore(t)
	require.NoError(t, s.Add("good", srv.URL))

	// A repo recorded with no cached index (e.g. the cache file was cleared).
	require.NoError(t, s.save([]RepoEntry{{Name: "good", URL: srv.URL}, {Name: "stale", URL: srv.URL}}))

	idxs, err := s.Indexes()
	require.NoError(t, err)
	require.Len(t, idxs, 1)
	require.Contains(t, idxs, "good")
	require.NotContains(t, idxs, "stale")
}

// A repository in the manifest that cannot be reached must fail the apply loudly,
// not be silently skipped — every chart it provides would otherwise fail to
// resolve with a far more confusing error.
func TestEnsureReposFailsWhenAnAbsentRepositoryCannotBeAdded(t *testing.T) {
	body := "apiVersion: v1\nentries: {}\n"
	up := false
	srv := newIndexServer(t, &body, &up)

	s := newTestStore(t)
	err := s.EnsureRepos([]RepoSpec{{Name: "eldara", URL: srv.URL}})
	require.Error(t, err)

	repos, lerr := s.List()
	require.NoError(t, lerr)
	require.Empty(t, repos, "a failed add must persist nothing")
}

// age backdates a repository's cached index so it reads as unverified.
func age(t *testing.T, s *RepoStore, name string, d time.Duration) {
	t.Helper()
	path, err := s.indexFile(name)
	require.NoError(t, err)
	when := time.Now().Add(-d)
	require.NoError(t, os.Chtimes(path, when, when))
}

// Resolution reads the cache and never refetches, so a cache nobody has
// refreshed keeps answering "latest" with a version that stopped being it. Say
// so, or an install deploys the stale answer with nothing on screen to suggest
// there is a newer chart.
func TestResolveWarnsOnceTheCachedIndexGoesUnverified(t *testing.T) {
	body := "apiVersion: v1\nentries:\n  demo:\n    - {name: demo, version: 0.1.0, urls: [\"d.tgz\"]}\n"
	up := true
	srv := newIndexServer(t, &body, &up)

	var warnings []string
	s := newTestStore(t)
	s.Warnf = func(format string, a ...any) { warnings = append(warnings, fmt.Sprintf(format, a...)) }
	require.NoError(t, s.Add("eldara", srv.URL))

	// Just fetched: nothing to say.
	_, _, err := s.Resolve("eldara/demo", "")
	require.NoError(t, err)
	require.Empty(t, warnings)

	age(t, s, "eldara", 3*24*time.Hour)

	// Twice: an apply resolves every release in the manifest against the same
	// repository, and one warning per release would bury the plan it precedes.
	for range 2 {
		_, _, err = s.Resolve("eldara/demo", "")
		require.NoError(t, err)
	}
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "'eldara'")
	require.Contains(t, warnings[0], "3 days old")
	require.Contains(t, warnings[0], "charts repo update")
}

// A repository that publishes rarely serves the same bytes for weeks. Update
// writes nothing in that case, so unless it records the check some other way,
// the operator who refreshes daily is the one who gets nagged.
func TestUpdateMarksAnUnchangedIndexAsVerified(t *testing.T) {
	body := "apiVersion: v1\nentries:\n  demo:\n    - {name: demo, version: 0.1.0, urls: [\"d.tgz\"]}\n"
	up := true
	srv := newIndexServer(t, &body, &up)

	var warnings []string
	s := newTestStore(t)
	s.Warnf = func(format string, a ...any) { warnings = append(warnings, fmt.Sprintf(format, a...)) }
	require.NoError(t, s.Add("eldara", srv.URL))
	age(t, s, "eldara", 3*24*time.Hour)

	_, unchanged, err := s.Update("eldara")
	require.NoError(t, err)
	require.Equal(t, []string{"eldara"}, unchanged)

	_, _, err = s.Resolve("eldara/demo", "")
	require.NoError(t, err)
	require.Empty(t, warnings, "an index just confirmed current is not stale")
}

// countingIndexServer serves an index whose body the test can change, and
// counts index requests. The count is what the refresh-policy tests actually
// assert: whether a fetch happened is not visible in any return value.
func countingIndexServer(t *testing.T, body *string, up *bool, hits *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/index.yaml") {
			http.NotFound(w, r)
			return
		}
		*hits++
		if !*up {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(*body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// reopen returns a second store over the same state directory. Add and a later
// install are separate swarmcli invocations, and the once-per-process
// bookkeeping is per store — a test that resolves through the same store Add
// used is testing one process doing both, which is not the case being fixed.
func reopen(t *testing.T, s *RepoStore) *RepoStore {
	t.Helper()
	n := NewRepoStoreAt(s.dir)
	n.AllowPlaintext = true
	n.Warnf = s.Warnf
	return n
}

func indexWith(version string) string {
	return "apiVersion: v1\nentries:\n  demo:\n    - {name: demo, version: " + version + ", urls: [\"d.tgz\"]}\n"
}

// The point of the whole feature: a resolution answers with what the repository
// publishes now, not with what it published when the cache was written. No
// ageing of the cache — under RefreshAlways a cache written a second ago is
// refreshed too, and that is the behaviour being pinned down.
func TestRefreshAlwaysResolvesWhatTheRepositoryPublishesNow(t *testing.T) {
	body, up, hits := indexWith("0.1.0"), true, 0
	srv := countingIndexServer(t, &body, &up, &hits)

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL)) // caches 0.1.0

	body = indexWith("0.2.0")

	s = reopen(t, s)
	s.Refresh = RefreshAlways
	e, _, err := s.Resolve("eldara/demo", "")
	require.NoError(t, err)
	require.Equal(t, "0.2.0", e.Version)
}

// The embedder contract. swarmcli-cd builds a store per application on every
// reconcile and refreshes on its own schedule; a store that was never told to
// refresh must make no request of its own. Deleting this test is how that
// becomes untrue by accident.
func TestRefreshExplicitIsTheZeroValueAndFetchesNothing(t *testing.T) {
	body, up, hits := indexWith("0.1.0"), true, 0
	srv := countingIndexServer(t, &body, &up, &hits)

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))
	require.Equal(t, 1, hits, "Add fetches once, by definition")

	body = indexWith("0.2.0")

	s = reopen(t, s)
	require.Equal(t, RefreshExplicit, s.Refresh, "the zero value must be the safe one")
	e, _, err := s.Resolve("eldara/demo", "")
	require.NoError(t, err)
	require.Equal(t, "0.1.0", e.Version, "an unasked store answers from the cache")
	require.Equal(t, 1, hits, "and makes no request of its own")
}

// With no staleness window to rate-limit it, this is the only thing between an
// apply resolving ten releases from one repository and ten downloads of the
// same index. It is a correctness test, not a performance one.
func TestRefreshAlwaysFetchesOncePerRepositoryPerProcess(t *testing.T) {
	body, up, hits := indexWith("0.1.0"), true, 0
	srv := countingIndexServer(t, &body, &up, &hits)

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))
	s = reopen(t, s)
	s.Refresh = RefreshAlways
	hits = 0

	for range 3 {
		_, _, err := s.Resolve("eldara/demo", "")
		require.NoError(t, err)
	}
	require.Equal(t, 1, hits)
}

// apply calls EnsureRepos, which refreshes every declared repository, and then
// resolves charts from them. Without Update recording what it fetched, every
// index would be downloaded twice per apply — green, and visible only in
// somebody's bandwidth.
func TestAnExplicitUpdateSuppressesTheImplicitOne(t *testing.T) {
	body, up, hits := indexWith("0.1.0"), true, 0
	srv := countingIndexServer(t, &body, &up, &hits)

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))
	s = reopen(t, s)
	s.Refresh = RefreshAlways
	hits = 0

	_, _, err := s.Update("")
	require.NoError(t, err)
	require.Equal(t, 1, hits)

	_, _, err = s.Resolve("eldara/demo", "")
	require.NoError(t, err)
	require.Equal(t, 1, hits, "the index was already fetched in this process")
}

// A repository that is briefly unreachable must not stop a chart resolving:
// the cache is a correct answer, merely possibly an old one, and refusing to
// install would trade a stale answer for no answer.
func TestRefreshFailureFallsBackToTheCache(t *testing.T) {
	body, up, hits := indexWith("0.1.0"), true, 0
	srv := countingIndexServer(t, &body, &up, &hits)

	var warnings []string
	s := newTestStore(t)
	s.Warnf = func(format string, a ...any) { warnings = append(warnings, fmt.Sprintf(format, a...)) }
	require.NoError(t, s.Add("eldara", srv.URL))
	s = reopen(t, s)
	s.Refresh = RefreshAlways
	up, hits = false, 0

	// Three resolutions, one attempt: recording the refresh before the fetch
	// rather than after is what keeps a dead repository from costing a timeout
	// per chart in the manifest.
	for range 3 {
		e, _, err := s.Resolve("eldara/demo", "")
		require.NoError(t, err)
		require.Equal(t, "0.1.0", e.Version)
	}
	require.Equal(t, 1, hits)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "'eldara'")
	require.Contains(t, warnings[0], "using the cached index")
}

// A configured repository whose cache file is gone resolves instead of telling
// the user to run the command that would have been run for them.
func TestRefreshAlwaysHealsAMissingCache(t *testing.T) {
	body, up, hits := indexWith("0.1.0"), true, 0
	srv := countingIndexServer(t, &body, &up, &hits)

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))
	path, err := s.indexFile("eldara")
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))

	s = reopen(t, s)
	s.Refresh = RefreshExplicit
	_, err = s.LoadIndex("eldara")
	require.ErrorContains(t, err, "no cached index")

	s.Refresh = RefreshAlways
	e, _, err := s.Resolve("eldara/demo", "")
	require.NoError(t, err)
	require.Equal(t, "0.1.0", e.Version)
}

// RefreshNever is what --no-repo-update sets, and it has to reach EnsureRepos:
// an air-gapped apply would otherwise pay a network timeout per repository to
// arrive at the cached answer it was told to use.
func TestRefreshNeverKeepsEnsureReposOffTheNetwork(t *testing.T) {
	body, up, hits := indexWith("0.1.0"), true, 0
	srv := countingIndexServer(t, &body, &up, &hits)

	s := newTestStore(t)
	require.NoError(t, s.Add("eldara", srv.URL))
	s = reopen(t, s)
	s.Refresh = RefreshNever
	hits = 0

	require.NoError(t, s.EnsureRepos([]RepoSpec{{Name: "eldara", URL: srv.URL}}))
	require.Equal(t, 0, hits)

	e, _, err := s.Resolve("eldara/demo", "")
	require.NoError(t, err)
	require.Equal(t, "0.1.0", e.Version)
}

// os.WriteFile truncates before it writes, so a reader arriving mid-write saw a
// partial index. With a refresh on every install that window is no longer
// theoretical. Assert the two observable consequences of writing through a
// rename: nothing is left behind, and a failed write does not destroy what was
// already there.
func TestWriteCacheReplacesTheIndexInOneStep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache", "index-eldara.yaml")

	require.NoError(t, writeCache(path, []byte("first")))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "first", string(got))

	require.NoError(t, writeCache(path, []byte("second")))
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "second", string(got))

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary file survives a successful write")

	// A directory in place of the target: the rename fails, and the point is
	// that the failure is reported rather than leaving a truncated file.
	blocked := filepath.Join(dir, "cache", "blocked.yaml")
	require.NoError(t, os.Mkdir(blocked, 0o755))
	require.Error(t, writeCache(blocked, []byte("third")))

	entries, err = os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 2, "the failed write leaves no temporary file either")
}
