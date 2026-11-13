// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package servicesview

import (
	"fmt"
	"strings"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	filterlist "github.com/Eldara-Tech/swarmcli/v2/ui/components/filterable/list"
)

// serviceColumn pairs a shared-layout column with its sort metadata. The set is
// the single source of truth for the header, widths, sort indices, and rows.
type serviceColumn struct {
	col       filterlist.Column[docker.ServiceEntry]
	sort      SortField
	hasSort   bool
	isStack   bool
	isHealth  bool
	isAnonVol bool
}

// serviceColumns returns the columns for the current scope. The STACK column is
// dropped unless we're showing services from every stack (AllFilter), since in a
// single-stack/node scope every row carries the same stack value. The HEALTH
// column shows live per-container health when a ServiceOps decorator populates
// ServiceEntry.Health; when there is none but a footer note explains why (CE
// upsell, or a non-managed context — see healthFooterHint) it stays visible with
// a "*" placeholder tying rows to that footnote, and is dropped only when there
// is neither. ANON VOL appears only while some row has an anonymous volume.
// Cell closures capture the model so the ERROR column can read the
// per-service error text.
func (m *Model) serviceColumns() []serviceColumn {
	// footnoted: HEALTH carries no live data but a footer note explains it, so
	// keep the column with a "*" placeholder instead of dropping it.
	footnoted := healthFooterHint() != ""
	all := []serviceColumn{
		{sort: SortByName, hasSort: true, col: filterlist.Column[docker.ServiceEntry]{
			Label: "SERVICE", MinWidth: 8, Flex: true,
			Cell: func(e docker.ServiceEntry) string { return e.ServiceName }}},
		{isStack: true, col: filterlist.Column[docker.ServiceEntry]{
			Label: "STACK", MinWidth: 6,
			Cell: func(e docker.ServiceEntry) string { return e.StackName }}},
		{col: filterlist.Column[docker.ServiceEntry]{
			Label: "REPLICAS", MinWidth: 5,
			Cell: func(e docker.ServiceEntry) string {
				if e.ReplicasTotal == 0 {
					return "—"
				}
				return fmt.Sprintf("%d/%d", e.ReplicasOnNode, e.ReplicasTotal)
			}}},
		{sort: SortByStatus, hasSort: true, col: filterlist.Column[docker.ServiceEntry]{
			Label: "STATUS", MinWidth: 8,
			Cell: func(e docker.ServiceEntry) string {
				// A service still pulling its image reads as a bare "active"; show
				// the pull instead while one is in flight.
				if e.PullProgress != "" {
					return e.PullProgress
				}
				// REPLICAS counts every running replica, superseded ones included,
				// so a rollout that has not moved a single replica onto the new
				// generation still reads as fully converged. Say how far it got
				// while it is in flight — that is the question "updating" raises
				// and the ratio beside it cannot answer.
				if e.RollingOut && e.ReplicasTotal > 0 && e.UpToDate < e.ReplicasTotal {
					return fmt.Sprintf("%s · %d/%d new", e.Status, e.UpToDate, e.ReplicasTotal)
				}
				return e.Status
			}}},
		{isHealth: true, col: filterlist.Column[docker.ServiceEntry]{
			Label: "HEALTH", MinWidth: 6,
			Cell: func(e docker.ServiceEntry) string {
				if e.Health != "" {
					return e.Health
				}
				if footnoted {
					return "*"
				}
				return "—"
			}}},
		{col: filterlist.Column[docker.ServiceEntry]{
			Label: "MODE", MinWidth: 6,
			Cell: func(e docker.ServiceEntry) string { return e.Mode }}},
		{sort: SortByImage, hasSort: true, col: filterlist.Column[docker.ServiceEntry]{
			Label: "IMAGE", MinWidth: 8, Flex: true,
			Cell: func(e docker.ServiceEntry) string { return e.Image }}},
		{sort: SortByPorts, hasSort: true, col: filterlist.Column[docker.ServiceEntry]{
			Label: "PORTS", MinWidth: 5, Flex: true,
			Cell: func(e docker.ServiceEntry) string { return e.Ports }}},
		{sort: SortByCreated, hasSort: true, col: filterlist.Column[docker.ServiceEntry]{
			Label: "CREATED", MinWidth: 7,
			Cell: func(e docker.ServiceEntry) string { return formatRelativeTime(e.CreatedAt) }}},
		{sort: SortByUpdated, hasSort: true, col: filterlist.Column[docker.ServiceEntry]{
			Label: "UPDATED", MinWidth: 7,
			Cell: func(e docker.ServiceEntry) string { return formatRelativeTime(e.UpdatedAt) }}},
		{isAnonVol: true, col: filterlist.Column[docker.ServiceEntry]{
			Label: "ANON VOL", MinWidth: 8, Flex: true,
			Cell: anonVolumesCell}},
		{sort: SortByError, hasSort: true, col: filterlist.Column[docker.ServiceEntry]{
			Label: "ERROR", MinWidth: 6, Flex: true,
			Cell: func(e docker.ServiceEntry) string { return m.serviceErrorText[e.ServiceID] }}},
	}
	dropStack := m.filterType != AllFilter
	dropHealth := !m.anyServiceHealth() && !footnoted
	dropAnonVol := !m.anyAnonVolumes()
	if !dropStack && !dropHealth && !dropAnonVol {
		return all
	}
	out := make([]serviceColumn, 0, len(all))
	for _, c := range all {
		if c.isStack && dropStack {
			continue
		}
		if c.isHealth && dropHealth {
			continue
		}
		if c.isAnonVol && dropAnonVol {
			continue
		}
		out = append(out, c)
	}
	return out
}

// anyServiceHealth reports whether any loaded service row carries a health
// summary — one of the two conditions (the other being a footer note) that keep
// the HEALTH column visible.
func (m *Model) anyServiceHealth() bool {
	for _, e := range m.List.Items {
		if e.Health != "" {
			return true
		}
	}
	return false
}

// anyAnonVolumes reports whether any loaded service row has an anonymous
// volume, the only thing that shows the ANON VOL column: a swarm without one
// should not spend a column on saying so.
func (m *Model) anyAnonVolumes() bool {
	for _, e := range m.List.Items {
		if len(e.AnonVolumes) > 0 {
			return true
		}
	}
	return false
}

// anonVolumesCell lists a service's anonymous-volume paths. A "?" marks one
// only the spec declares: nothing has seen it on a running container yet.
func anonVolumesCell(e docker.ServiceEntry) string {
	paths := make([]string, len(e.AnonVolumes))
	for i, v := range e.AnonVolumes {
		paths[i] = v.Path
		if !v.Confirmed {
			paths[i] += "?"
		}
	}
	return strings.Join(paths, ", ")
}

// layoutColumns extracts the shared-layout column set for the current scope.
func (m *Model) layoutColumns() []filterlist.Column[docker.ServiceEntry] {
	active := m.serviceColumns()
	cols := make([]filterlist.Column[docker.ServiceEntry], len(active))
	for i, c := range active {
		cols[i] = c.col
	}
	return cols
}

// sortIndicator reports which active column carries the current sort field and
// the sort direction, so the header arrow tracks the column even when STACK is
// dropped and indices shift.
func (m *Model) sortIndicator() (int, bool) {
	for i, c := range m.serviceColumns() {
		if c.hasSort && c.sort == m.sortField {
			return i, m.sortAscending
		}
	}
	return -1, true
}
