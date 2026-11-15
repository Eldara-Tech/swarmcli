// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package chartsview

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
)

// releaseOps is the read-only slice of the release engine this view needs.
//
// It is declared here rather than added to docker.Deps alongside the other op
// interfaces because charts imports docker: a docker.ChartOps returning a
// charts.Release would close an import cycle. Declaring it at the consumer is
// the idiomatic placement anyway, and the view is mocked by assigning this
// field directly.
type releaseOps interface {
	// AllRevisions returns every stored revision grouped by release name,
	// ascending. One call serves both the list and any expanded release's
	// history; List followed by a History per release would decode every
	// stored revision twice.
	AllRevisions(ctx context.Context) (map[string][]charts.Release, error)
	// ServiceStates returns the live state of a release's services. It reads
	// the shared Docker snapshot cache, so calling it once per release costs
	// one snapshot and N in-memory filters.
	ServiceStates(ctx context.Context, release string) []charts.ServiceState
	// Available joins the installed releases against the cached repository
	// indexes, one entry per release whose chart is in one. haveIndexes reports
	// whether there was anything to compare against, so the view can say "no
	// indexes" rather than "up to date" — and a release missing from the map
	// while haveIndexes is true is a chart in no index, which is a third answer
	// again. checked is when the least recently verified index was last
	// verified, so the view can say how old that comparison is.
	Available(rels []charts.Release) (avail map[string]charts.Availability, haveIndexes bool, checked time.Time)
}

// engineOps binds releaseOps to the ambient release engine.
//
// Ambient is deliberate: NewEngine goes through the shared Docker client and
// snapshot cache, both of which the TUI's context switcher already resets, so
// the view follows the active context with no extra wiring. Pinning a context
// name would bypass that cache and re-snapshot on every read.
type engineOps struct {
	engine *charts.Engine

	// The cached repository indexes, and the repository list and file times
	// they were read under. Parsing every index on every poll would be waste,
	// so Available re-parses only when the list or a time moved. Reading them
	// once per view instead left an open view calling releases up to date that
	// `charts outdated`, refreshing from another terminal, had just found
	// outdated (#726).
	//
	// mu serialises Available, which runs in a tea.Cmd goroutine.
	mu      sync.Mutex
	store   *charts.RepoStore
	repos   []charts.RepoEntry
	times   map[string]time.Time
	indexes map[string]*charts.Index
	// warned keeps a persistent failure to one log line, not one per poll.
	warned bool
}

func newEngineOps() releaseOps { return &engineOps{engine: charts.NewEngine()} }

func (o *engineOps) AllRevisions(ctx context.Context) (map[string][]charts.Release, error) {
	return o.engine.AllRevisions(ctx)
}

func (o *engineOps) ServiceStates(ctx context.Context, release string) []charts.ServiceState {
	return o.engine.Backend.StackServices(ctx, release)
}

func (o *engineOps) Available(rels []charts.Release) (map[string]charts.Availability, bool, time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.loadIndexes()
	if len(o.indexes) == 0 {
		return nil, false, time.Time{}
	}
	var checked time.Time
	for _, t := range o.times {
		if checked.IsZero() || t.Before(checked) {
			checked = t
		}
	}
	return charts.Available(rels, o.repos, o.indexes), true, checked
}

// loadIndexes reads the configured repositories and their cached indexes when
// either changed on disk since the last read, and nothing else. A failure keeps
// whatever the last read found, and the next poll tries again.
//
// RefreshNever is the point: this is a browser, and a view that polls must not
// reach the network behind the operator's back. The store's own timeouts would
// not save it either — none of RepoStore's methods take a context, so a fetch
// here could not be cancelled when the view is closed.
func (o *engineOps) loadIndexes() {
	if o.store == nil {
		store, err := newRepoStore()
		if err != nil {
			o.warn("ChartsView: no chart repository state: %v", err)
			return
		}
		o.store = store
	}
	repos, err := o.store.List()
	if err != nil {
		o.warn("ChartsView: could not read the configured repositories: %v", err)
		return
	}
	times, err := o.store.IndexTimes()
	if err != nil {
		o.warn("ChartsView: could not read the cached repository indexes: %v", err)
		return
	}
	if o.times != nil && slices.Equal(repos, o.repos) && maps.EqualFunc(times, o.times, time.Time.Equal) {
		return
	}
	indexes, err := o.store.Indexes()
	if err != nil {
		o.warn("ChartsView: could not read the cached repository indexes: %v", err)
		return
	}
	o.repos, o.times, o.indexes = repos, times, indexes
}

func (o *engineOps) warn(format string, a ...any) {
	if o.warned {
		return
	}
	o.warned = true
	l().Warnf(format, a...)
}

// newRepoStore builds the offline-only repository store this view reads.
//
// It is a function of its own so the policy is assertable: RefreshNever is the
// load-bearing line here, and every other policy would let a poll running
// every few seconds fetch an index over the network on its own initiative.
func newRepoStore() (*charts.RepoStore, error) {
	store, err := charts.NewRepoStore()
	if err != nil {
		return nil, err
	}
	store.Refresh = charts.RefreshNever
	store.Warnf = func(format string, a ...any) { l().Warnf(format, a...) }
	return store, nil
}
