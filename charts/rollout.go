// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RolloutLabel, set to RolloutSequential as a deploy label, marks a service that
// must not be updated together with the others marked the same way. On an
// upgrade, rollback or apply of a release whose marked services already run,
// swarmcli deploys everything else first, then each marked service on its own,
// in name order, waiting for it to converge before the next.
//
// `docker stack deploy` updates every changed service at once. For a clustered
// database that runs one service per peer, as the mariadb-galera chart does,
// that stops every peer together, a full outage the cluster may not recover
// from by itself.
//
// It is a label in the manifest rather than a Chart.yaml field so that a
// revision carries it: a rollback replays the stored manifest and rolls out the
// same way. A swarmcli that predates it sees an ordinary label and deploys
// everything at once, as it always has.
const (
	RolloutLabel      = "com.swarmcli.rollout"
	RolloutSequential = "sequential"
)

// OmittedServicesPreserver is the optional interface a Backend implements when a
// DeployStack whose manifest leaves a service out leaves that service running as
// it is, as `docker stack deploy` without --prune does. A sequential rollout
// deploys such partial manifests, so it is attempted only against a backend that
// says so; any other gets the whole manifest in one deploy, as before. Optional
// so that adding it breaks no Backend implemented outside this module.
type OmittedServicesPreserver interface {
	PreservesOmittedServices() bool
}

// heldForRollout returns the marked services that already run, by manifest key
// and in rollout order: the ones this deploy must not update together. It is
// empty on an install, where nothing runs yet and every service starts at once
// as before, and for a backend that cannot deploy a partial manifest.
func (e *Engine) heldForRollout(ctx context.Context, rel *Release) ([]string, error) {
	if p, ok := e.Backend.(OmittedServicesPreserver); !ok || !p.PreservesOmittedServices() {
		return nil, nil
	}
	marked, err := sequentialServices(rel.Manifest)
	if err != nil || len(marked) == 0 {
		return nil, err
	}
	_ = e.Backend.RefreshSnapshot(ctx)
	live := map[string]bool{}
	for _, s := range e.Backend.StackServices(ctx, rel.Name) {
		live[s.Name] = true
	}
	var held []string
	for _, key := range marked {
		if live[rel.Name+"_"+key] {
			held = append(held, key)
		}
	}
	return held, nil
}

// rollOneAtATime deploys each held service on its own, each deploy carrying the
// manifest minus the held services still to come, and waits for it before the
// next. The first deploy, without any of them, has already happened.
//
// It waits whether or not --wait was asked for: one at a time is the point, and
// without a wait between them the deploys would overlap just as before.
func (e *Engine) rollOneAtATime(ctx context.Context, rel *Release, opts InstallOptions, held []string) error {
	pending := make(map[string]bool, len(held))
	for _, key := range held {
		pending[key] = true
	}
	for _, key := range held {
		delete(pending, key)
		manifest := rel.Manifest
		if len(pending) > 0 {
			var err error
			if manifest, err = withoutServices(rel.Manifest, pending); err != nil {
				return err
			}
		}
		name := rel.Name + "_" + key
		before, _ := e.serviceState(ctx, rel.Name, name)
		if err := e.Backend.DeployStack(ctx, DeployRequest{
			Name: rel.Name, Manifest: manifest, Resolve: opts.ResolveImage, Files: rel.Files,
		}); err != nil {
			return fmt.Errorf("deploying service '%s' on its own: %w", name, err)
		}
		_ = e.Backend.RefreshSnapshot(ctx)
		if err := e.waitRolled(ctx, rel.Name, name, before.UpdateStartedAt, opts.Timeout); err != nil {
			return err
		}
	}
	return nil
}

// waitRolled waits for one service's update to finish converging.
//
// An update that left the service's task template as it was restarts nothing,
// so there is nothing to wait for. Otherwise only a state from an update that
// started after `before` counts, in either direction: right after the deploy,
// swarm may not have begun the update yet, so the service still reads as the
// previous generation, converged, or as the paused rollout a previous deploy
// left behind. Both are answers about an update this one replaces.
//
// The timeout is per service, on top of the service's own monitor window, which
// the convergence check sits out after the new task starts.
func (e *Engine) waitRolled(ctx context.Context, release, name string, before time.Time, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := e.now().Add(timeout)
	extended := false
	for {
		if st, ok := e.serviceState(ctx, release, name); ok {
			if !st.TaskSpecChanged {
				return nil
			}
			if !extended {
				deadline = deadline.Add(st.Monitor)
				extended = true
			}
			if st.UpdateStartedAt.After(before) {
				switch c := st.Convergence(); c.Phase {
				case PhaseWedged:
					return fmt.Errorf("service '%s': %s; the services after it were not updated", name, c.Reason)
				case PhaseConverged:
					return nil
				}
			}
		}
		if !e.now().Before(deadline) {
			return fmt.Errorf("timed out waiting for service '%s' to roll out; the services after it were not updated", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitPollInterval):
		}
	}
}

// serviceState is one service of a release, by its swarm name.
func (e *Engine) serviceState(ctx context.Context, release, name string) (ServiceState, bool) {
	for _, s := range e.Backend.StackServices(ctx, release) {
		if s.Name == name {
			return s, true
		}
	}
	return ServiceState{}, false
}

// manifestServices returns the parsed manifest and its services mapping, nil
// when it has none.
func manifestServices(manifest string) (*yaml.Node, *yaml.Node, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(manifest), &root); err != nil {
		return nil, nil, fmt.Errorf("parse manifest: %w", err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return &root, nil, nil
	}
	return &root, mappingValue(root.Content[0], "services"), nil
}

// mappingValue is the value under key in a mapping node, nil when absent or when
// n is not a mapping.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// sequentialServices returns the keys of the services whose deploy labels set
// RolloutLabel to RolloutSequential, sorted. Compose writes labels either as a
// mapping or as a list of key=value strings; both count.
func sequentialServices(manifest string) ([]string, error) {
	_, services, err := manifestServices(manifest)
	if err != nil || services == nil || services.Kind != yaml.MappingNode {
		return nil, err
	}
	var out []string
	for i := 0; i+1 < len(services.Content); i += 2 {
		labels := mappingValue(mappingValue(services.Content[i+1], "deploy"), "labels")
		if labels == nil {
			continue
		}
		marked := false
		switch labels.Kind {
		case yaml.MappingNode:
			marked = mappingValue(labels, RolloutLabel) != nil && mappingValue(labels, RolloutLabel).Value == RolloutSequential
		case yaml.SequenceNode:
			for _, item := range labels.Content {
				if k, v, ok := strings.Cut(item.Value, "="); ok && k == RolloutLabel && v == RolloutSequential {
					marked = true
				}
			}
		}
		if marked {
			out = append(out, services.Content[i].Value)
		}
	}
	sort.Strings(out)
	return out, nil
}

// withoutServices returns the manifest with the named services left out and
// everything else, networks, volumes, configs and secrets included, as it was.
func withoutServices(manifest string, drop map[string]bool) (string, error) {
	root, services, err := manifestServices(manifest)
	if err != nil {
		return "", err
	}
	if services == nil || services.Kind != yaml.MappingNode {
		return manifest, nil
	}
	kept := make([]*yaml.Node, 0, len(services.Content))
	for i := 0; i+1 < len(services.Content); i += 2 {
		if !drop[services.Content[i].Value] {
			kept = append(kept, services.Content[i], services.Content[i+1])
		}
	}
	services.Content = kept
	out, err := yaml.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("write manifest: %w", err)
	}
	return string(out), nil
}
