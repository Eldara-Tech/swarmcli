// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"fmt"
	"os"
	"strings"
)

// ChartSource resolves a chart reference to a loaded chart. It is the seam that
// lets release planning be unit-tested without a repository, a network or a
// filesystem: the whole of Engine.PlanApply depends on this interface and not on
// RepoStore.
type ChartSource interface {
	// Load returns the chart named by ref. ref is either a local path (a chart
	// directory or a .tgz) or a "<repo>/<chart>" reference resolved through the
	// configured repositories. version selects a repository chart version; it is
	// meaningless for a local path and rejected there rather than ignored.
	Load(ref, version string) (*Chart, error)
}

// ChartOrigin is the repository a chart was resolved from: the zero value for a
// local chart path, which came from none.
type ChartOrigin struct {
	Repo string
	URL  string
}

// OriginSource is a ChartSource that also reports where a chart came from. It is
// optional, so ChartSource stays a one-method seam: a release loaded through a
// source without it records no origin, which Available treats exactly like a
// record written before origins were recorded.
type OriginSource interface {
	ChartSource
	LoadWithOrigin(ref, version string) (*Chart, ChartOrigin, error)
}

// LoadWithOrigin loads ref through src, with its origin when src reports one.
func LoadWithOrigin(src ChartSource, ref, version string) (*Chart, ChartOrigin, error) {
	if o, ok := src.(OriginSource); ok {
		return o.LoadWithOrigin(ref, version)
	}
	ch, err := src.Load(ref, version)
	return ch, ChartOrigin{}, err
}

// NewChartSource returns the standard source, backed by the configured chart
// repositories for "<repo>/<chart>" references. It implements OriginSource.
func NewChartSource(store *RepoStore) ChartSource { return &repoSource{store: store} }

type repoSource struct{ store *RepoStore }

func (s *repoSource) Load(ref, version string) (*Chart, error) {
	ch, _, err := s.LoadWithOrigin(ref, version)
	return ch, err
}

func (s *repoSource) LoadWithOrigin(ref, version string) (*Chart, ChartOrigin, error) {
	if IsPathRef(ref) {
		if version != "" {
			// Previously the flag was accepted and silently dropped here, so
			// `install foo ./chart --version 2.0.0` quietly installed whatever
			// Chart.yaml said. A local directory has exactly one version.
			return nil, ChartOrigin{}, fmt.Errorf("chart '%s' is a local path: --version does not apply (the chart's own Chart.yaml sets the version)", ref)
		}
		ch, err := loadLocalChart(ref)
		return ch, ChartOrigin{}, err
	}
	// Not syntactically a path, but it might still be a bare directory name
	// ("./" omitted). Keep resolving those for backwards compatibility.
	if info, err := os.Stat(ref); err == nil {
		if version != "" {
			return nil, ChartOrigin{}, fmt.Errorf("chart '%s' is a local path: --version does not apply (the chart's own Chart.yaml sets the version)", ref)
		}
		ch, err := loadStatted(ref, info.IsDir())
		return ch, ChartOrigin{}, err
	}
	if s.store == nil {
		return nil, ChartOrigin{}, fmt.Errorf("chart '%s' not found on disk and no repositories are configured", ref)
	}
	entry, base, err := s.store.Resolve(ref, version)
	if err != nil {
		return nil, ChartOrigin{}, err
	}
	ch, err := s.store.Pull(entry, base)
	if err != nil {
		return nil, ChartOrigin{}, err
	}
	repo, _, _ := strings.Cut(ref, "/")
	return ch, ChartOrigin{Repo: repo, URL: strings.TrimRight(base, "/")}, nil
}

// IsPathRef reports whether ref names a local chart path rather than a
// "<repo>/<chart>" reference. The test is deliberately SYNTACTIC: a release file
// is committed to git and must resolve the same way on every machine, so whether
// a reference is a path cannot depend on what happens to exist on the disk of
// whichever CI runner picked up the job.
func IsPathRef(ref string) bool {
	return strings.HasPrefix(ref, "./") ||
		strings.HasPrefix(ref, "../") ||
		strings.HasPrefix(ref, "/") ||
		strings.HasPrefix(ref, "~")
}

func loadLocalChart(path string) (*Chart, error) {
	info, err := os.Stat(path)
	if err != nil {
		// An explicit path that does not exist is a typo, not a repository
		// reference. Say so, instead of the misleading "must be <repo>/<chart>".
		return nil, fmt.Errorf("chart path '%s' not found", path)
	}
	return loadStatted(path, info.IsDir())
}

func loadStatted(path string, isDir bool) (*Chart, error) {
	if isDir {
		return LoadChartDir(path)
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	return LoadChartArchive(fh)
}

// ReleaseChartOf projects a loaded chart into the metadata recorded on a release.
func ReleaseChartOf(ch *Chart) ReleaseChart {
	return ReleaseChart{Name: ch.Metadata.Name, Version: ch.Metadata.Version, AppVersion: ch.Metadata.AppVersion}
}
