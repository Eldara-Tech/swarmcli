// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"sort"
	"strings"
)

// Availability is what the cached repository indexes know about one installed
// release's chart. It exists because "nothing newer" and "nothing to compare
// against" are different answers that an absent entry ran together: a local
// chart is in no index, and a release at the newest published version is in one
// — the second is up to date, the first is merely unknowable.
type Availability struct {
	// Repo is the repository that supplied Latest.
	Repo string
	// Latest is the newest version of this chart in the indexes the release is
	// compared against, whether or not it is newer than what is installed.
	Latest string
	// Newer reports that Latest is an upgrade from the installed version. A
	// release ahead of every index — installed from a local chart, say — is
	// current rather than outdated, so this is false there too.
	Newer bool
}

// Available joins installed releases against the cached repository indexes,
// keyed by release name. A release whose chart appears in no index is absent
// from the result, which is the one fact Outdated cannot report.
//
// A release that recorded its source repository is compared against that
// repository alone: the configured repository at its recorded URL, else the one
// with its recorded name (the URL moved). A release whose source is no longer
// configured is absent, like a local chart — a same-named chart in another
// repository is a different chart, not an upgrade. A release recorded before
// sources were, or from a source that reports none, resolves to the highest
// version across every repository carrying its chart name, reporting the
// repository that supplied it.
func Available(rels []Release, repos []RepoEntry, indexes map[string]*Index) map[string]Availability {
	out := make(map[string]Availability, len(rels))
	for _, rel := range rels {
		candidates := indexes
		if repo, recorded := sourceRepo(rel.Chart, repos); recorded {
			idx, ok := indexes[repo]
			if !ok {
				continue
			}
			candidates = map[string]*Index{repo: idx}
		}
		var bestVer, bestRepo string
		for repo, idx := range candidates {
			versions := idx.Entries[rel.Chart.Name]
			if len(versions) == 0 {
				continue
			}
			latest := latestVersion(versions)
			if bestVer == "" || compareVersions(latest.Version, bestVer) > 0 {
				bestVer, bestRepo = latest.Version, repo
			}
		}
		if bestVer == "" {
			continue
		}
		out[rel.Name] = Availability{
			Repo:   bestRepo,
			Latest: bestVer,
			Newer:  compareVersions(bestVer, rel.Chart.Version) > 0,
		}
	}
	return out
}

// sourceRepo names the configured repository rc was resolved from. recorded is
// false when rc names no source at all; name is empty when it names one that is
// no longer configured.
func sourceRepo(rc ReleaseChart, repos []RepoEntry) (name string, recorded bool) {
	if rc.Repo == "" && rc.RepoURL == "" {
		return "", false
	}
	if u := strings.TrimRight(rc.RepoURL, "/"); u != "" {
		for _, r := range repos {
			if strings.TrimRight(r.URL, "/") == u {
				return r.Name, true
			}
		}
	}
	for _, r := range repos {
		if r.Name == rc.Repo {
			return r.Name, true
		}
	}
	return "", true
}

// OutdatedEntry is one installed release with a newer chart version available.
type OutdatedEntry struct {
	Release   string
	Chart     string
	Repo      string
	Installed string
	Latest    string
}

// Outdated is Available filtered to the releases with an upgrade waiting.
// Releases already at the newest version, those ahead of every index, and those
// whose chart appears in no index (a local chart) are all omitted — a caller
// that needs to tell those apart wants Available.
func Outdated(rels []Release, repos []RepoEntry, indexes map[string]*Index) []OutdatedEntry {
	available := Available(rels, repos, indexes)
	var out []OutdatedEntry
	for _, rel := range rels {
		avail, ok := available[rel.Name]
		if !ok || !avail.Newer {
			continue
		}
		out = append(out, OutdatedEntry{
			Release:   rel.Name,
			Chart:     rel.Chart.Name,
			Repo:      avail.Repo,
			Installed: rel.Chart.Version,
			Latest:    avail.Latest,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Release < out[j].Release })
	return out
}
