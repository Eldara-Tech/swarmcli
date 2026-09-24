// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package taskutil

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
)

// HealthState is the swarm-level verdict: the colour a client gives the whole
// swarm, or the part of it a view shows.
type HealthState string

const (
	HealthHealthy HealthState = "healthy"
	// HealthConverging means nothing is failing, but swarm has not finished:
	// a service is short of its target or a rollout has not settled.
	HealthConverging HealthState = "converging"
	// HealthDegraded means something is failing — a service with an active
	// deployment error, or an unhealthy node. It wins over converging.
	HealthDegraded HealthState = "degraded"
	// HealthUnknown means there is nothing to judge: no snapshot, or a locked
	// swarm whose entity lists are empty until it is unlocked.
	HealthUnknown HealthState = "unknown"
)

// Reason kinds. The first two make a swarm degraded, the last two converging.
const (
	ReasonDeploymentError = "deployment-error"
	ReasonNode            = "node"
	ReasonReplicas        = "replicas"
	ReasonRollout         = "rollout"
)

// HealthReason names one service or node behind a verdict.
type HealthReason struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// SwarmHealth is the verdict and every reason behind it, each list sorted by
// kind, then name, then ID. A service with an active deployment error is listed
// only as failing, never also as converging.
type SwarmHealth struct {
	State      HealthState    `json:"state"`
	Failing    []HealthReason `json:"failing"`
	Converging []HealthReason `json:"converging"`
}

// AssessSwarm judges a snapshot by the rules the views already apply, so the
// verdict cannot disagree with a row: ActiveDeploymentErrorsByService and
// NodeEntry.UnhealthyReason decide failing, and StackConvergence's counts —
// which already leave out drained and down nodes — decide converging.
//
// A service is converging when it is not failing and either
//   - it has fewer tasks done than desired: running (or completed, for a
//     restart-none/on-failure job) for a service, completed in the current
//     iteration for a native job, so a finished job is settled and a running
//     one is not; a one-shot whose task died is excluded, since it will never
//     converge and is reported as a deployment error instead; or
//   - its rollout is unsettled: updating, rollback_started, paused or
//     rollback_paused. A paused rollout counts because it is unfinished and
//     nothing in it is failing any more — while its task still fails, the
//     deployment error makes it degraded. A job's "paused" is ignored, as
//     charts.ServiceState.Convergence ignores it: swarm pauses every re-run
//     one-shot, so it says nothing about the job.
//
// The monitor window is deliberately not consulted: it depends on the clock,
// and a verdict clients share must be a function of the snapshot alone.
func AssessSwarm(snap *docker.SwarmSnapshot) SwarmHealth {
	h := SwarmHealth{State: HealthUnknown, Failing: []HealthReason{}, Converging: []HealthReason{}}
	if snap == nil || snap.Locked {
		return h
	}

	for _, n := range snap.ToNodeEntries() {
		if why := n.UnhealthyReason(); why != "" {
			h.Failing = append(h.Failing, HealthReason{Kind: ReasonNode, ID: n.ID, Name: n.Hostname, Detail: why})
		}
	}

	errs := ActiveDeploymentErrorsByService(snap.Tasks)
	for _, c := range snap.ServicesConvergence() {
		if msg, failing := errs[c.ID]; failing {
			h.Failing = append(h.Failing, HealthReason{Kind: ReasonDeploymentError, ID: c.ID, Name: c.Name, Detail: msg})
			continue
		}
		if why := unsettledRollout(c); why != "" {
			h.Converging = append(h.Converging, HealthReason{Kind: ReasonRollout, ID: c.ID, Name: c.Name, Detail: why})
		}
		if why := shortOfTarget(c); why != "" {
			h.Converging = append(h.Converging, HealthReason{Kind: ReasonReplicas, ID: c.ID, Name: c.Name, Detail: why})
		}
	}

	byKindNameID := func(a, b HealthReason) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	}
	slices.SortFunc(h.Failing, byKindNameID)
	slices.SortFunc(h.Converging, byKindNameID)

	switch {
	case len(h.Failing) > 0:
		h.State = HealthDegraded
	case len(h.Converging) > 0:
		h.State = HealthConverging
	default:
		h.State = HealthHealthy
	}
	return h
}

// unsettledRollout describes a rollout swarm has not finished, or "".
func unsettledRollout(c docker.ServiceConvergence) string {
	switch c.UpdateState {
	case "updating":
		return "rolling update in progress"
	case "rollback_started":
		return "rolling back"
	case "paused":
		if c.Job {
			return ""
		}
		return "update paused"
	case "rollback_paused":
		return "rollback paused"
	default:
		return ""
	}
}

// shortOfTarget describes a service with fewer tasks done than desired, or "".
// "Done" is charts.ServiceState.done's rule: completions for a native job, a
// running or completed task for anything else.
func shortOfTarget(c docker.ServiceConvergence) string {
	if c.DeadTask {
		return ""
	}
	if c.NativeJob {
		if c.Completed < c.Desired {
			return fmt.Sprintf("%d/%d tasks completed", c.Completed, c.Desired)
		}
		return ""
	}
	if done := c.Running + c.Completed; done < c.Desired {
		return fmt.Sprintf("%d/%d tasks running", done, c.Desired)
	}
	return ""
}
