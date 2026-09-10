// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
)

// dockerBackend implements Backend against the live github.com/Eldara-Tech/swarmcli/v2/docker package.
//
// Every operation is addressed to one Docker context. When ctxName is empty that
// is the ambient one — the session pin, resolved once from DOCKER_CONTEXT or
// `docker context show` and held for the life of the process — which is what
// the CLI has always used and what NewEngine still builds. When it is set, the
// backend resolves its own client and its own snapshots and touches no
// process-wide state, so two backends can target two swarms at once.
type dockerBackend struct {
	ctxName string
}

// Compile-time proof that the production backend still satisfies Backend: an
// interface implemented outside this repository is one whose in-repo
// implementation can drift out of it without any call site noticing.
var _ Backend = (*dockerBackend)(nil)

// NewDockerBackend returns a Backend bound to an explicitly named Docker
// context, for callers that must address a specific swarm rather than the one
// the process happens to be pointed at.
//
// Pair it with NewEngineWith. The three pieces of process-global state this
// avoids are the SDK client singleton, the session context pin the exec-based
// stack commands name, and the shared snapshot cache — all three, not
// just the last: a backend that deployed to one swarm and read its history,
// networks and convergence from another would be worse than an honestly
// single-swarm one, because nothing would report the mismatch.
func NewDockerBackend(ctxName string) Backend { return &dockerBackend{ctxName: ctxName} }

// client resolves the SDK client this backend's API calls go through.
func (b *dockerBackend) client() (*client.Client, error) {
	if b.ctxName == "" {
		return docker.GetClient()
	}
	return docker.ClientFor(b.ctxName)
}

// contextName resolves the context the exec-based stack commands are aimed at.
func (b *dockerBackend) contextName() (string, error) {
	if b.ctxName != "" {
		return b.ctxName, nil
	}
	return docker.GetDockerContext()
}

// snapshot reads the cluster state this backend's stack queries derive from.
//
// The ambient backend keeps using the shared cache, so the CLI's behaviour is
// unchanged. An explicitly targeted one fetches its own every time: the shared
// cache holds exactly one swarm, and a convergence poll that read another
// swarm's tasks out of it would report a rollout finished that never started.
func (b *dockerBackend) snapshot(ctx context.Context) (*docker.SwarmSnapshot, error) {
	if b.ctxName == "" {
		return docker.GetOrRefreshSnapshotCtx(ctx)
	}
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	return docker.SnapshotWith(ctx, cli)
}

func (b *dockerBackend) DeployStack(ctx context.Context, req DeployRequest) error {
	ctxName, err := b.contextName()
	if err != nil {
		return err
	}
	return docker.DeployStackInContext(ctx, ctxName, req.Name, req.Manifest, docker.ResolveImage(req.Resolve), req.Files)
}

func (b *dockerBackend) RemoveStack(ctx context.Context, name string) error {
	ctxName, err := b.contextName()
	if err != nil {
		return err
	}
	return docker.RemoveStackCLIInContext(ctx, ctxName, name)
}

// RefreshSnapshot invalidates the shared cache after a mutation. An explicitly
// targeted backend has no shared cache to invalidate — snapshot() always
// fetches — so there is nothing to do and nothing to get stale.
func (b *dockerBackend) RefreshSnapshot(ctx context.Context) error {
	if b.ctxName != "" {
		return nil
	}
	_, err := docker.RefreshSnapshotCtx(ctx)
	return err
}

func (b *dockerBackend) CreateConfig(ctx context.Context, name string, data []byte, labels map[string]string) error {
	cli, err := b.client()
	if err != nil {
		return err
	}
	_, err = docker.CreateConfigWith(ctx, cli, name, data, labels)
	return err
}

func (b *dockerBackend) ListConfigs(ctx context.Context) ([]ConfigMeta, error) {
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	configs, err := docker.ListConfigsWith(ctx, cli)
	if err != nil {
		return nil, err
	}
	out := make([]ConfigMeta, 0, len(configs))
	for _, c := range configs {
		out = append(out, ConfigMeta{Name: c.Spec.Name, Labels: c.Spec.Labels, Data: c.Spec.Data})
	}
	return out, nil
}

func (b *dockerBackend) InspectConfig(ctx context.Context, name string) ([]byte, error) {
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	cfg, err := docker.InspectConfigWith(ctx, cli, name)
	if err != nil {
		return nil, err
	}
	return cfg.Data, nil
}

func (b *dockerBackend) DeleteConfig(ctx context.Context, name string) error {
	cli, err := b.client()
	if err != nil {
		return err
	}
	return docker.DeleteConfigWith(ctx, cli, name)
}

func (b *dockerBackend) StackServices(ctx context.Context, name string) []ServiceState {
	snap, err := b.snapshot(ctx)
	if err != nil {
		return nil
	}
	return ServiceStatesFrom(snap, name)
}

// ServiceStatesFrom reads one stack's service states out of a swarm snapshot.
//
// It is exported for the sake of a caller implementing Backend itself, which
// NewEngineWith exists to allow: producing []ServiceState is the one part of
// that job with rules rather than plumbing, and a second copy of them would
// diverge silently in both directions — reporting a release converged when this
// package would still be waiting, or degraded when it is fine.
//
// The snapshot is the caller's: docker.SnapshotWith builds one against any
// client without touching the process-wide cache, so a consumer serving several
// swarms is not forced through the ambient one.
func ServiceStatesFrom(snap *docker.SwarmSnapshot, name string) []ServiceState {
	entries := snap.StackServices(name)
	// Convergence facts come from a separate loader because ServiceEntry mirrors
	// `docker service ls` — counting every generation, on any node — which is
	// right for the services view but wrong for deciding a rollout finished
	// (issues #473, #480). Both now read the same snapshot, so the display and
	// the decision cannot disagree.
	conv := make(map[string]docker.ServiceConvergence, len(entries))
	for _, c := range snap.StackConvergence(name) {
		conv[c.Name] = c
	}

	out := make([]ServiceState, 0, len(entries))
	for _, e := range entries {
		replicas := ""
		if e.Mode == "replicated" {
			replicas = fmt.Sprintf("%d/%d", e.ReplicasOnNode, e.ReplicasTotal)
		}
		st := ServiceState{
			Name:     e.ServiceName,
			Mode:     e.Mode,
			Replicas: replicas,
			Status:   e.Status,
		}
		if c, ok := conv[e.ServiceName]; ok {
			st.Running = c.Running
			st.Desired = c.Desired
			st.Completed = c.Completed
			st.Job = c.Job
			st.UpdateState = c.UpdateState
			st.Monitor = c.Monitor
			st.NewestTaskAge = c.NewestTaskAge
			st.DeadTask = c.DeadTask
			st.DeadTaskReason = c.DeadTaskReason
			// A finished job has no running task, so the replica ratio built
			// from ServiceEntry reads 0/N and the release looks degraded when
			// it is complete. Count the completed tasks toward the target and
			// say so in the status column (issue #443).
			if c.Job && c.Completed > 0 {
				st.Replicas = fmt.Sprintf("%d/%d", c.Running+c.Completed, c.Desired)
				st.Status = "completed"
			}
		}
		out = append(out, st)
	}
	return out
}

func (b *dockerBackend) StackVolumes(ctx context.Context, name string) ([]string, error) {
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	vols, err := docker.ListVolumesWith(ctx, cli)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range vols {
		if v.Stack == name {
			out = append(out, v.Name)
		}
	}
	return out, nil
}

func (b *dockerBackend) RemoveVolume(ctx context.Context, name string) error {
	cli, err := b.client()
	if err != nil {
		return err
	}
	return docker.RemoveVolumeWith(ctx, cli, name, false)
}

func (b *dockerBackend) NetworkScopes(ctx context.Context) (map[string]string, error) {
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	nets, err := docker.ListNetworksWith(ctx, cli)
	if err != nil {
		return nil, err
	}
	scopes := make(map[string]string, len(nets))
	for _, n := range nets {
		scopes[n.Name] = n.Scope
	}
	return scopes, nil
}

func (b *dockerBackend) CreateOverlayNetwork(ctx context.Context, name, driver string, attachable bool) error {
	cli, err := b.client()
	if err != nil {
		return err
	}
	_, _, err = docker.CreateNetworkWith(ctx, cli, name, network.CreateOptions{Driver: driver, Attachable: attachable})
	return err
}

func (b *dockerBackend) RemoveOverlayNetwork(ctx context.Context, name string) error {
	cli, err := b.client()
	if err != nil {
		return err
	}
	nets, err := docker.ListNetworksWith(ctx, cli)
	if err != nil {
		return err
	}
	for _, n := range nets {
		if n.Name == name {
			return docker.RemoveNetworkWith(ctx, cli, n.ID)
		}
	}
	return nil // already gone
}

func (b *dockerBackend) SecretNames(ctx context.Context) (map[string]struct{}, error) {
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	secs, err := docker.ListSecretsWith(ctx, cli)
	if err != nil {
		return nil, err
	}
	names := make(map[string]struct{}, len(secs))
	for _, s := range secs {
		names[s.Spec.Name] = struct{}{}
	}
	return names, nil
}
