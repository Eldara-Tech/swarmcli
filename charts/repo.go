// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// httpTimeout bounds index and tarball downloads.
const httpTimeout = 30 * time.Second

// quickTimeout bounds an implicit RefreshAlways fetch. See RepoStore.quick.
const quickTimeout = 5 * time.Second

// maxIndexSize bounds an index.yaml download (defense against huge responses).
const maxIndexSize = 16 << 20 // 16 MiB

// maxChartArchiveSize bounds a chart tarball download. LoadChartArchive bounds
// the decompressed size; this bounds the compressed transfer, so the whole body
// can be buffered and hashed before any of it reaches the archive parser.
const maxChartArchiveSize = 20 << 20 // 20 MiB

// pullBackoff is how long a chart download waits before each retry, one entry
// per retry — so a tarball is fetched at most len(pullBackoff)+1 times. A var
// rather than a const so tests can shrink it; nothing else writes it.
//
// Short, and few, on purpose. Pull takes no context, so every entry here is
// time a shutdown cannot interrupt — and the failures worth waiting through, a
// gateway between here and the archive answering 502 or 504, clear in seconds
// or not at all.
var pullBackoff = []time.Duration{500 * time.Millisecond, 2 * time.Second}

// chartCacheTTL is how long an unused chart archive stays cached. A cache hit
// refreshes the file's modification time, so what ages out is what stopped
// being pulled: the versions a pinned release has moved past.
const chartCacheTTL = 30 * 24 * time.Hour

// staleAfter is how long a cached index may go unverified before reads of it
// warn. Under RefreshAlways nothing gets that far — the index is refreshed
// before it is read — so this is what is left for the cases where it was not:
// a refresh that failed, and a RefreshExplicit or RefreshNever store. There, an
// unrefreshed cache answers "latest" with whatever was newest when it was
// written, silently and for good.
const staleAfter = 24 * time.Hour

// AllowPlaintextEnv is the environment variable a host program may honour to set
// AllowPlaintext. The name lives here so the refusal message and whatever reads
// it cannot drift, but this package never reads the environment itself: whether
// an operator is allowed to opt out is the embedder's call, and cli's answer
// (yes, for an interactive user's own machine) is not automatically a daemon's.
const AllowPlaintextEnv = "SWARMCLI_CHARTS_ALLOW_PLAINTEXT"

// NoAutoUpdateEnv is the environment variable a host program may honour to
// select RefreshNever. It lives here beside AllowPlaintextEnv so the docs and
// the code cannot name it differently; as with that one, this package never
// reads the environment itself.
const NoAutoUpdateEnv = "SWARMCLI_CHARTS_NO_AUTO_UPDATE"

// RefreshPolicy says when a RepoStore may download a repository index.
//
// Resolving a chart is otherwise a pure cache read — install, upgrade,
// template, show, lint and search never refetch — which is how an install can
// deploy the version that was newest whenever the cache was last written.
type RefreshPolicy int

const (
	// RefreshExplicit downloads an index only when asked to: Update, Add and
	// EnsureRepos. It is the zero value because it is what programs embedding
	// this package already had, and because a daemon's network behaviour is not
	// something a CLI convenience should change underneath it — swarmcli-cd
	// builds a store per application on every reconcile and refreshes on its own
	// schedule.
	RefreshExplicit RefreshPolicy = iota
	// RefreshAlways additionally refreshes a repository before the first read of
	// its index in this process. An interactive user who types `install
	// repo/chart` means the chart the repository publishes, not the one their
	// cache happened to hold when they last ran `repo update`.
	RefreshAlways
	// RefreshNever downloads nothing, not even for EnsureRepos, and resolution
	// answers out of whatever is cached. For an air-gapped or deliberately
	// offline run, where the alternative is paying a network timeout per
	// repository to reach the same answer.
	RefreshNever
)

// RepoStore persists configured repositories and caches their indexes under a
// base directory (default: the XDG state dir, ~/.local/state/swarmcli/charts).
type RepoStore struct {
	dir    string
	client *http.Client
	// quick is the client a RefreshAlways fetch uses. An implicit refresh is
	// allowed to give up quickly because its fallback — the cached index — is a
	// correct answer, merely possibly an old one; making an operator wait
	// httpTimeout for that on a blackholed network would be a worse failure
	// than the staleness the refresh exists to prevent.
	quick *http.Client

	// Warnf, when set, receives non-fatal diagnostics: a chart whose index entry
	// publishes no digest, so its integrity could not be verified, and a
	// repository whose index could not be refreshed, so a cached one is in use.
	// charts is a library with no output of its own; nil is silent, and cli wires
	// this to stderr.
	Warnf func(format string, a ...any)

	// AllowPlaintext permits repositories reached over plain http. It defaults to
	// false because a chart repository serves the tarball that *becomes* the
	// deployed workload, so whoever sits on the path to it chooses what runs on
	// the swarm. The published digest does not close that hole: it travels in the
	// same index.yaml over the same channel, so an on-path attacker rewrites both
	// — and an entry that publishes no digest only warns. swarmcli-cd refuses
	// plaintext git remotes for exactly this reason; the same argument applies one
	// layer down, to where the chart itself comes from.
	//
	// Set it only for an internal registry on a network you already trust. cli
	// wires it to AllowPlaintextEnv so an existing plaintext setup keeps working
	// with one deliberate, greppable opt-out rather than none.
	AllowPlaintext bool

	// warnedStale records the repositories already reported as unverified, so an
	// apply resolving ten releases from one repository says it once rather than
	// ten times.
	warnedStale map[string]bool

	// Refresh decides when this store goes to the network for an index. Its zero
	// value is RefreshExplicit, which is what every embedder got before the
	// policy existed. cli sets it; see the constants.
	Refresh RefreshPolicy

	// refreshed records the repositories already refreshed in this process,
	// whether implicitly or by an explicit Update. With no staleness window
	// to rate-limit it, this is the only thing standing between an apply
	// resolving ten releases from one repository and ten downloads of the same
	// index — and it is what stops apply's own EnsureRepos being followed by a
	// second, implicit fetch of everything it just fetched.
	refreshed map[string]bool
}

// NewRepoStore returns a store rooted at the standard charts state directory.
func NewRepoStore() (*RepoStore, error) {
	dir, err := chartsStateDir()
	if err != nil {
		return nil, err
	}
	return NewRepoStoreAt(dir), nil
}

// NewRepoStoreAt returns a store rooted at dir (used in tests).
func NewRepoStoreAt(dir string) *RepoStore {
	return &RepoStore{
		dir:    dir,
		client: &http.Client{Timeout: httpTimeout, CheckRedirect: checkRedirect},
		quick:  &http.Client{Timeout: quickTimeout, CheckRedirect: checkRedirect},
	}
}

// checkRedirect bounds redirects and refuses any hop to a non-http(s) URL,
// blocking scheme-downgrade tricks (e.g. file://) when following a chart or
// index download to an attacker-influenced location.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("refusing redirect to non-http(s) URL '%s'", req.URL.Redacted())
	}
	return nil
}

func (s *RepoStore) reposFile() string { return filepath.Join(s.dir, "repos.json") }

// indexFile is where a repository's cached index lives. It validates rather than
// trusting its caller because names arrive here from repos.json as well as from
// the command line, and this process is not that file's only possible author.
//
// The concatenation is the sharp edge: the name is glued on *after* "index-", so
// "index-.." is an ordinary path segment and filepath.Join's Clean has nothing to
// collapse. A traversing name is not neutralised, it merely costs one extra "../"
// — and what then lands outside the cache directory is whatever the repository
// served for index.yaml.
func (s *RepoStore) indexFile(name string) (string, error) {
	if err := validateRepoName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, "cache", "index-"+name+".yaml"), nil
}

// validateRepoName constrains a repository name to the charset a release name
// uses. The name is not just a label: it is a path component of the cached index
// file (see indexFile) and the part before the "/" in a "<repo>/<chart>"
// reference, so anything exotic in it is a bug at best.
func validateRepoName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("repository name is required")
	case !isPlainName(name):
		return fmt.Errorf("invalid repository name '%s': use letters, digits, '-', '_', '.'", name)
	case name == "." || name == "..":
		// Harmless today only because indexFile prefixes the name, which leaves
		// "index-.." rather than "..". Refusing them keeps that accident from
		// quietly becoming load-bearing.
		return fmt.Errorf("invalid repository name '%s'", name)
	}
	return nil
}

// checkRepoURL rejects a URL this store must not fetch an index from: anything
// that is not an absolute http(s) URL, and plain http unless AllowPlaintext.
func (s *RepoStore) checkRepoURL(repoURL string) error {
	u, err := url.ParseRequestURI(repoURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("repository URL must be an absolute http(s) URL")
	}
	return s.checkPlaintext(u)
}

// checkPlaintext refuses a plain-http URL unless the caller opted in; see
// AllowPlaintext for why the default is a refusal. It echoes the URL redacted,
// as checkRedirect does: a repository URL may carry credentials, and this error
// goes to a terminal or a CI log.
func (s *RepoStore) checkPlaintext(u *url.URL) error {
	if u.Scheme != "http" || s.AllowPlaintext {
		return nil
	}
	return fmt.Errorf("refusing the plaintext URL '%s': anything on the path to it decides what gets "+
		"deployed on your swarm, and the index digest travels the same path; set %s=1 if this is an "+
		"internal registry on a network you already trust", u.Redacted(), AllowPlaintextEnv)
}

// List returns the configured repositories sorted by name.
func (s *RepoStore) List() ([]RepoEntry, error) {
	data, err := os.ReadFile(s.reposFile())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read repos: %w", err)
	}
	var repos []RepoEntry
	if err := json.Unmarshal(data, &repos); err != nil {
		return nil, fmt.Errorf("parse repos: %w", err)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

// Seed writes repos as the configured repositories when none have ever been
// configured, that is when repos.json does not exist. An existing file, even an
// empty one, is the user's choice and is left alone, so a seeded repository that
// was removed stays removed. Nothing is downloaded: the first read of an index
// fetches it under RefreshAlways, and a first run without network still works.
func (s *RepoStore) Seed(repos []RepoEntry) error {
	for _, r := range repos {
		if err := validateRepoName(r.Name); err != nil {
			return err
		}
		if err := s.checkRepoURL(r.URL); err != nil {
			return err
		}
	}
	if _, err := os.Stat(s.reposFile()); !os.IsNotExist(err) {
		return err
	}
	return s.save(repos)
}

// Add registers a repository and downloads its index. It rejects duplicate
// names, names it will not build a cache path from, and URLs it will not fetch
// from — all before the download, so a bad request is reported as one.
func (s *RepoStore) Add(name, repoURL string) error {
	if err := validateRepoName(name); err != nil {
		return err
	}
	if err := s.checkRepoURL(repoURL); err != nil {
		return err
	}
	repos, err := s.List()
	if err != nil {
		return err
	}
	for _, r := range repos {
		if r.Name == name {
			return fmt.Errorf("repository '%s' already exists", name)
		}
	}
	repoURL = strings.TrimRight(repoURL, "/")
	// Download and validate the index before persisting anything, so a failed
	// download leaves no half-added repository behind.
	idx, err := s.fetchIndex(repoURL, s.client)
	if err != nil {
		return fmt.Errorf("index download failed, repository not added: %w%s", err, githubPagesHint(repoURL))
	}
	path, err := s.indexFile(name)
	if err != nil {
		return err
	}
	if err := writeCache(path, idx); err != nil {
		return err
	}
	// Add has just been to the network for this repository, so a RefreshAlways
	// read must not go again. This is not hypothetical: EnsureRepos adds the
	// repositories a release file declares and then resolves charts from them
	// in the same process.
	s.markRefreshed(name)
	repos = append(repos, RepoEntry{Name: name, URL: repoURL})
	return s.save(repos)
}

// writeCache replaces a cached file in one step. os.WriteFile truncates before
// it writes, so a reader that arrives mid-write sees an empty or half-written
// one — and under RefreshAlways a write happens on every install, not only
// on an explicit `repo update`, so two swarmcli processes sharing a state
// directory (a CI matrix, an apply running beside an install) really do
// overlap. Writing beside the target and renaming over it means a reader sees
// either the old index or the new one, and a failed write leaves the old one
// readable rather than a truncated file.
//
// The temporary file is deliberately in the same directory: os.Rename is atomic
// only within a filesystem, and anywhere else would be a different mount on
// somebody's machine.
func writeCache(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op once the rename succeeds
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file 0600; the cache is world-readable like every
	// other index this package writes.
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Remove deletes a repository and its cached index.
func (s *RepoStore) Remove(name string) error {
	repos, err := s.List()
	if err != nil {
		return err
	}
	out := repos[:0]
	found := false
	for _, r := range repos {
		if r.Name == name {
			found = true
			continue
		}
		out = append(out, r)
	}
	if !found {
		return fmt.Errorf("repository '%s' not found", name)
	}
	if err := s.save(out); err != nil {
		return err
	}
	// Best-effort, and deliberately skipped for a name indexFile will not vouch
	// for: the entry is gone from repos.json either way, and a path we refused to
	// build is not one to hand to os.Remove.
	if path, perr := s.indexFile(name); perr == nil {
		_ = os.Remove(path)
	}
	return nil
}

// Update downloads and caches the index for one repository (or all when name is
// empty) and returns the names refreshed.
func (s *RepoStore) Update(name string) (changed, unchanged []string, err error) {
	return s.update(name, s.client)
}

func (s *RepoStore) update(name string, client *http.Client) (changed, unchanged []string, err error) {
	repos, err := s.List()
	if err != nil {
		return nil, nil, err
	}
	for _, r := range repos {
		if name != "" && r.Name != name {
			continue
		}
		// Before the network round trip: a name repos.json should never have held
		// is not one to fetch an index for, let alone write one under.
		path, err := s.indexFile(r.Name)
		if err != nil {
			return changed, unchanged, fmt.Errorf("update '%s': %w", r.Name, err)
		}
		// Recorded before the fetch, not after, and whatever the outcome: this
		// says "this process has already been to the network for you", which is
		// as true of a failure as of a success. Recording only successes would
		// make a down repository cost one timeout per chart resolved.
		s.markRefreshed(r.Name)
		idx, err := s.fetchIndex(r.URL, client)
		if err != nil {
			return changed, unchanged, fmt.Errorf("update '%s': %w", r.Name, err)
		}
		// The served index is byte-stable between releases, so an identical
		// payload means nothing new — report it as already up-to-date.
		existing, _ := os.ReadFile(path)
		if existing != nil && bytes.Equal(existing, idx) {
			// The cache's mtime records when it was last *verified*, not last
			// changed, because that is the question staleness asks. Skipping the
			// touch would leave a repository that publishes rarely looking
			// abandoned however often its index is confirmed current.
			now := time.Now()
			_ = os.Chtimes(path, now, now)
			unchanged = append(unchanged, r.Name)
			continue
		}
		if err := writeCache(path, idx); err != nil {
			return changed, unchanged, err
		}
		changed = append(changed, r.Name)
	}
	if name != "" && len(changed)+len(unchanged) == 0 {
		return nil, nil, fmt.Errorf("repository '%s' not found", name)
	}
	return changed, unchanged, nil
}

// LoadIndex returns the cached, parsed index for a repository.
func (s *RepoStore) LoadIndex(name string) (*Index, error) {
	path, err := s.indexFile(name)
	if err != nil {
		return nil, err
	}
	s.autoRefresh(name)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no cached index for '%s'; run `charts repo update`", name)
	}
	if err != nil {
		return nil, err
	}
	s.warnStale(path, name)
	var idx Index
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse index for '%s': %w", name, err)
	}
	return &idx, nil
}

// autoRefresh re-downloads a repository's index before it is read, once per
// repository per process. It is best-effort by construction: every caller has a
// cached index to fall back on, and refusing to resolve a chart because a
// repository was briefly unreachable would trade a stale answer for no answer.
func (s *RepoStore) autoRefresh(name string) {
	if s.Refresh != RefreshAlways || s.refreshed[name] {
		return
	}
	// The short-deadline client, not s.client: see RepoStore.quick.
	if _, _, err := s.update(name, s.quick); err != nil {
		// The same sentence EnsureRepos emits for the same situation, so one
		// thing going wrong reads as one thing wherever it is noticed.
		s.warnf("could not refresh repository '%s' (%v); using the cached index\n", name, err)
	}
}

// markRefreshed records that this process has already been to the network for a
// repository. update calls it; autoRefresh reads it.
func (s *RepoStore) markRefreshed(name string) {
	if s.refreshed == nil {
		s.refreshed = map[string]bool{}
	}
	s.refreshed[name] = true
}

// warnStale reports a cached index that has not been verified for staleAfter.
// It is the fallback for when the refresh policy is not RefreshAlways or its
// fetch failed: those are
// the only paths left on which resolution answers out of a cache nobody has
// checked, and an install that quietly deploys the version that stopped being
// the latest days ago is what this exists to prevent.
func (s *RepoStore) warnStale(path, name string) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	age := time.Since(fi.ModTime())
	if age < staleAfter || s.warnedStale[name] {
		return
	}
	if s.warnedStale == nil {
		s.warnedStale = map[string]bool{}
	}
	s.warnedStale[name] = true
	days := int(age / (24 * time.Hour))
	unit := "days"
	if days == 1 {
		unit = "day"
	}
	s.warnf("cached index for '%s' is %d %s old; run `swarmcli charts repo update` to pick up newer chart versions\n",
		name, days, unit)
}

// Indexes returns the cached index of every configured repository, keyed by
// repository name. Repositories with no cached index are skipped, as in Search.
func (s *RepoStore) Indexes() (map[string]*Index, error) {
	repos, err := s.List()
	if err != nil {
		return nil, err
	}
	out := make(map[string]*Index, len(repos))
	for _, r := range repos {
		idx, err := s.LoadIndex(r.Name)
		if err != nil {
			continue
		}
		out[r.Name] = idx
	}
	return out, nil
}

// EnsureRepos makes the repositories a release manifest declares available,
// adding those that are absent and refreshing the rest. It exists so that
// `charts apply -f file` is the only command a CI job needs to run.
//
// What it writes is a name-to-URL mapping plus a cached index — a cache, not user
// data. The one thing it will not do is silently repoint an existing repository
// at a different origin.
func (s *RepoStore) EnsureRepos(specs []RepoSpec) error {
	if len(specs) == 0 {
		return nil
	}
	repos, err := s.List()
	if err != nil {
		return err
	}
	existing := make(map[string]string, len(repos))
	for _, r := range repos {
		existing[r.Name] = r.URL
	}

	for _, spec := range specs {
		want := strings.TrimRight(spec.URL, "/")
		have, ok := existing[spec.Name]
		switch {
		case !ok:
			if err := s.Add(spec.Name, want); err != nil {
				return err
			}
		case have != want:
			return fmt.Errorf("repository '%s' is already configured with a different URL (%s); "+
				"refusing to repoint it — run `swarmcli charts repo remove %s` first if that is intended",
				spec.Name, have, spec.Name)
		default:
			// RefreshNever means the caller has said there is no network to reach,
			// so trying anyway buys the same cached answer one timeout later.
			if s.Refresh == RefreshNever {
				continue
			}
			// Refreshing is best-effort. Every version in the manifest is pinned,
			// so a stale cache can only fail to resolve a chart — it can never
			// resolve one to the wrong version. Failing the whole apply because
			// the network blipped would be worse than proceeding offline.
			if _, _, err := s.Update(spec.Name); err != nil {
				s.warnf("could not refresh repository '%s' (%v); using the cached index\n", spec.Name, err)
			}
		}
	}
	return nil
}

// ChartHit is a search result: one chart version in a repository.
type ChartHit struct {
	Repo  string
	Entry IndexEntry
}

// Search returns the latest version of every chart across all repos whose name
// or description contains keyword (case-insensitive). An empty keyword lists
// all charts. Results are sorted by "repo/name".
func (s *RepoStore) Search(keyword string) ([]ChartHit, error) {
	repos, err := s.List()
	if err != nil {
		return nil, err
	}
	kw := strings.ToLower(keyword)
	var hits []ChartHit
	for _, r := range repos {
		idx, err := s.LoadIndex(r.Name)
		if err != nil {
			continue // skip repos without a cached index
		}
		for chartName, versions := range idx.Entries {
			if len(versions) == 0 {
				continue
			}
			latest := latestVersion(versions)
			if kw != "" && !strings.Contains(strings.ToLower(chartName), kw) &&
				!strings.Contains(strings.ToLower(latest.Description), kw) {
				continue
			}
			hits = append(hits, ChartHit{Repo: r.Name, Entry: latest})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Repo != hits[j].Repo {
			return hits[i].Repo < hits[j].Repo
		}
		return hits[i].Entry.Name < hits[j].Entry.Name
	})
	return hits, nil
}

// Resolve looks up a "repo/chart" reference and returns the chosen index entry
// (the requested version, or the latest when version is empty) plus the repo's
// base URL for resolving relative tarball URLs.
func (s *RepoStore) Resolve(ref, version string) (IndexEntry, string, error) {
	repoName, chartName, ok := strings.Cut(ref, "/")
	if !ok || repoName == "" || chartName == "" {
		return IndexEntry{}, "", fmt.Errorf("chart reference must be <repo>/<chart>, got '%s'", ref)
	}
	repos, err := s.List()
	if err != nil {
		return IndexEntry{}, "", err
	}
	var baseURL string
	for _, r := range repos {
		if r.Name == repoName {
			baseURL = r.URL
		}
	}
	if baseURL == "" {
		return IndexEntry{}, "", fmt.Errorf("repository '%s' not found", repoName)
	}
	idx, err := s.LoadIndex(repoName)
	if err != nil {
		return IndexEntry{}, "", err
	}
	versions := idx.Entries[chartName]
	if len(versions) == 0 {
		return IndexEntry{}, "", fmt.Errorf("chart '%s' not found in repository '%s'", chartName, repoName)
	}
	if version == "" {
		return latestVersion(versions), baseURL, nil
	}
	for _, v := range versions {
		// SemVer-aware match: a chart version is plain SemVer ("0.1.3"), but the
		// release git tag carries a "v" prefix ("v0.1.3"), so users naturally type
		// either. compareVersions normalizes both and falls back to exact string
		// equality for non-SemVer versions.
		if compareVersions(v.Version, version) == 0 {
			return v, baseURL, nil
		}
	}
	return IndexEntry{}, "", fmt.Errorf("chart '%s' version '%s' not found", chartName, version)
}

// Pull downloads and loads the chart described by entry, resolving relative URLs
// against baseURL.
func (s *RepoStore) Pull(entry IndexEntry, baseURL string) (*Chart, error) {
	if len(entry.URLs) == 0 {
		return nil, fmt.Errorf("chart '%s' has no download URL", entry.Name)
	}
	tarURL := entry.URLs[0]
	u, err := url.Parse(tarURL)
	if err != nil {
		return nil, fmt.Errorf("invalid chart download URL '%s': %w", tarURL, err)
	}
	if !u.IsAbs() {
		tarURL = strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(tarURL, "/")
		if u, err = url.Parse(tarURL); err != nil {
			return nil, fmt.Errorf("invalid chart download URL '%s': %w", tarURL, err)
		}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("chart download URL must be http(s), got '%s'", tarURL)
	}
	// The index may point the tarball anywhere — a CDN, a release asset — so the
	// scheme has to be checked here too, not only where the repository was added.
	if err := s.checkPlaintext(u); err != nil {
		return nil, err
	}
	// Before the network: this archive may already be on disk, and the entry
	// carries the digest that decides whether what is on disk is still it. The
	// checks above stay ahead of the lookup so that a repository this store
	// refuses to download from is not one it serves out of cache either.
	key := cacheKey(entry)
	if body, ok := s.cachedArchive(key, entry); ok {
		return LoadChartArchive(bytes.NewReader(body))
	}
	body, err := s.downloadArchive(tarURL)
	if err != nil {
		return nil, err
	}
	if err := verifyDigest(entry, body); err != nil {
		if errors.Is(err, errNoDigest) {
			s.warnf("chart '%s' version %s: repository index publishes no digest; integrity not verified\n",
				entry.Name, entry.Version)
		} else {
			return nil, err
		}
	}
	s.cacheArchive(key, body)
	return LoadChartArchive(bytes.NewReader(body))
}

// downloadArchive fetches a chart tarball, retrying a failure that says nothing
// about this chart.
//
// The case it exists for is a gateway between here and the archive — a CDN, a
// release-asset host — answering 502 or 504 for a few seconds. Without a retry
// that is a failed apply and not merely a failed download: Pull's error is not
// scoped to the release that could not be fetched, it propagates out of
// PlanApply and takes the whole plan with it (#605).
//
// Every retry is announced. Pull takes no context, so the waits are time the
// caller cannot interrupt, and an operator watching a pull take four seconds
// instead of one is owed the reason.
func (s *RepoStore) downloadArchive(tarURL string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		body, retry, err := s.fetchArchive(tarURL)
		if err == nil || !retry || attempt == len(pullBackoff) {
			return body, err
		}
		s.warnf("%v; retrying %s in %s\n", err, tarURL, pullBackoff[attempt])
		time.Sleep(pullBackoff[attempt])
	}
}

// fetchArchive is one download attempt. It reports whether the failure is one
// another attempt could plausibly get past: a missing or oversized archive is
// the repository's answer about this chart and will not change, while a
// transport error or a temporary status is about the path to it.
func (s *RepoStore) fetchArchive(tarURL string) (body []byte, retry bool, err error) {
	resp, err := s.client.Get(tarURL)
	if err != nil {
		// Refused, reset, timed out, DNS. None of it is about this chart.
		return nil, true, fmt.Errorf("download chart: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, transientStatus(resp.StatusCode), fmt.Errorf("download chart: HTTP %s", resp.Status)
	}
	// Buffer the whole archive before parsing it: the digest must be checked
	// against the complete body, and a streaming parser would have consumed
	// unverified bytes by the time the hash was known.
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxChartArchiveSize+1))
	if err != nil {
		return nil, true, fmt.Errorf("download chart: %w", err)
	}
	if len(body) > maxChartArchiveSize {
		return nil, false, fmt.Errorf("chart archive exceeds %d bytes", maxChartArchiveSize)
	}
	return body, false, nil
}

// transientStatus reports a status code that says the request failed on the way
// rather than that this archive is not there. 404 and 403 are the repository's
// answer about the chart and repeating the question gets the same one; the
// codes below come from a gateway, a load balancer or a rate limiter, which is
// often enough not the same one next time to be worth asking again.
func transientStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// errNoDigest reports an index entry that carries no digest, so the archive
// cannot be verified. It is a warning rather than an error: nothing was verified
// before this check existed, and failing closed would break every repository —
// including hand-written and older ones — that does not publish digests.
var errNoDigest = errors.New("no digest in index entry")

// staleIndexHint is appended to a digest-mismatch error: the common cause is a
// chart rebuilt and republished after the local index was cached, so refreshing
// the index makes the two agree again.
const staleIndexHint = "\nhint: the cached repository index may be stale (e.g. the chart was " +
	"rebuilt since you last refreshed); run `swarmcli charts repo update` to update it"

// verifyDigest checks a downloaded chart archive against the digest published in
// the repository index. The index is fetched over HTTPS from the repository, but
// the tarball URL may point anywhere (a GitHub Release asset, a CDN) — the digest
// is what binds the two together, so a mismatch is always fatal.
func verifyDigest(entry IndexEntry, body []byte) error {
	want := entry.Digest
	if want == "" {
		return errNoDigest
	}
	// Helm's `repo index` writes a bare hex sha256; this repo's generated index
	// writes the "sha256:" prefixed form. Accept both, but reject any algorithm
	// we cannot actually check rather than silently skipping verification.
	if alg, hexsum, ok := strings.Cut(want, ":"); ok {
		if alg != "sha256" {
			return fmt.Errorf("chart '%s' version %s: unsupported digest algorithm '%s'",
				entry.Name, entry.Version, alg)
		}
		want = hexsum
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("chart '%s' version %s: digest mismatch (index says %s, download is %s); "+
			"the repository index and the chart archive disagree — refusing to install"+staleIndexHint,
			entry.Name, entry.Version, want, got)
	}
	return nil
}

// chartCacheDir is where verified chart archives are kept, beside the cached
// indexes they were resolved from.
func (s *RepoStore) chartCacheDir() string { return filepath.Join(s.dir, "cache", "charts") }

// cacheKey is the name a chart archive is cached under: the lowercase hex
// sha256 the index publishes for it.
//
// Empty whenever the entry publishes no checkable sha256 — no digest at all, or
// an algorithm this package cannot verify — and such an entry is never cached.
// A cached archive is only as safe as the check that validates the read, and
// for those there is none. Keying on name and version instead would mean
// trusting a version to be immutable, which is precisely what a repository is
// free to break by republishing one.
//
// The key is the content address and nothing else, which is also what makes it
// safe as a path component: the digest is the only part of an IndexEntry that
// has been validated by the time it gets here. Name and version are strings the
// repository chose, and gluing one of those into a filename is how this file
// got its last traversal (#527).
func cacheKey(entry IndexEntry) string {
	want := entry.Digest
	if alg, hexsum, ok := strings.Cut(want, ":"); ok {
		if alg != "sha256" {
			return ""
		}
		want = hexsum
	}
	if len(want) != hex.EncodedLen(sha256.Size) {
		return ""
	}
	if _, err := hex.DecodeString(want); err != nil {
		return ""
	}
	return strings.ToLower(want)
}

// cachedArchive returns an archive downloaded by an earlier Pull, and whether
// there was one to return.
//
// What makes this safe is that the file is accepted only if its bytes satisfy
// verifyDigest against the entry the caller just resolved — the same check the
// download path runs, against an index EnsureRepos has just refreshed. So a
// chart republished under the same version, a truncated write and a hand-edited
// file all miss and fall through to a download, rather than deploying something
// the repository no longer serves.
//
// Every failure here is a miss and never an error. The cache is an
// optimisation; the download behind it is the answer.
func (s *RepoStore) cachedArchive(key string, entry IndexEntry) ([]byte, bool) {
	if key == "" {
		return nil, false
	}
	path := filepath.Join(s.chartCacheDir(), key+".tgz")
	// Stat before read: an archive was size-checked before it was cached, so an
	// oversized file here is not one this package wrote — and reading it to find
	// that out is the thing maxChartArchiveSize exists to prevent.
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxChartArchiveSize {
		return nil, false
	}
	body, err := os.ReadFile(path)
	if err != nil || verifyDigest(entry, body) != nil {
		return nil, false
	}
	// Keep what is still in use from ageing out of the sweep in cacheArchive.
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return body, true
}

// cacheArchive stores a verified archive under its content address.
//
// Best-effort throughout: a cache that cannot be written is slower, not wrong.
func (s *RepoStore) cacheArchive(key string, body []byte) {
	if key == "" {
		return
	}
	dir := s.chartCacheDir()
	if err := writeCache(filepath.Join(dir, key+".tgz"), body); err != nil {
		return
	}
	// Swept on a write and not on every read, because a store that is pulling
	// nothing new is a store whose cache is not growing.
	sweepChartCache(dir)
}

// sweepChartCache deletes archives nothing has read for chartCacheTTL.
//
// That bounds the cache at the versions actually in use: a hit refreshes the
// file's modification time, so what ages out is what a pinned release has moved
// past — and the cost of ageing out something still wanted is one download,
// which is what would have happened without a cache at all.
func sweepChartCache(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-chartCacheTTL)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

func (s *RepoStore) warnf(format string, a ...any) {
	if s.Warnf != nil {
		s.Warnf(format, a...)
	}
}

// githubPagesHint suggests the GitHub Pages URL when a user points the store at
// a github.com repository page, which serves an HTML view rather than a chart
// index. Returns "" for any other host.
func githubPagesHint(repoURL string) string {
	u, err := url.Parse(repoURL)
	if err != nil || (u.Host != "github.com" && u.Host != "www.github.com") {
		return ""
	}
	const base = "\nhint: github.com serves repository pages, not chart indexes; use the GitHub Pages URL"
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return base + " (https://<org>.github.io/<repo>)"
	}
	return fmt.Sprintf("%s https://%s.github.io/%s", base, strings.ToLower(parts[0]), parts[1])
}

func (s *RepoStore) fetchIndex(repoURL string, client *http.Client) ([]byte, error) {
	// Add checks this before anything else, to fail without a round trip; the
	// re-check is for Update, whose URLs come out of repos.json — including ones
	// added before this store had an opinion about plain http.
	if err := s.checkRepoURL(repoURL); err != nil {
		return nil, err
	}
	indexURL := strings.TrimRight(repoURL, "/") + "/index.yaml"
	resp, err := client.Get(indexURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", indexURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: HTTP %s", indexURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxIndexSize {
		return nil, fmt.Errorf("index.yaml exceeds %d bytes", maxIndexSize)
	}
	var probe Index
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("index.yaml is not valid: %w", err)
	}
	return data, nil
}

func (s *RepoStore) save(repos []RepoEntry) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(repos, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.reposFile(), data, 0o644)
}

// latestVersion returns the entry with the highest SemVer; on parse ambiguity it
// falls back to lexical ordering.
func latestVersion(versions []IndexEntry) IndexEntry {
	best := versions[0]
	for _, v := range versions[1:] {
		if compareVersions(v.Version, best.Version) > 0 {
			best = v
		}
	}
	return best
}

// chartsStateDir resolves the base directory for charts state, mirroring the
// logger's XDG-with-fallback strategy so all swarmcli state lives together.
func chartsStateDir() (string, error) {
	const appName = "swarmcli"
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, appName, "charts"), nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", appName, "charts"), nil
	}
	return filepath.Join(os.TempDir(), appName, "charts"), nil
}
