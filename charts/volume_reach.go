// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"context"
	"errors"
)

// NodeVolume is one volume on one swarm node. A stack's volume is created by
// the engine that runs the task mounting it, so a replicated or global service
// can leave a volume of the same name on several nodes.
type NodeVolume struct {
	Name   string
	NodeID string
}

// VolumeReach is an extension point for a build that can reach volumes on every
// swarm node, not only the one the Docker context points at. The Docker API
// lists and removes volumes per node, so without one, uninstall --purge-volumes
// can remove only the connected node's volumes and says so.
type VolumeReach interface {
	// StackVolumes lists the stack's volumes on the nodes it reached. all
	// reports whether that was every node of the swarm. It returns
	// ErrVolumeReachUnavailable when it cannot be used at all right now, and
	// Uninstall then falls back to the connected node.
	StackVolumes(ctx context.Context, stack string) (vols []NodeVolume, all bool, err error)
	// RemoveVolume removes one volume. While a container still references the
	// volume it must return an error that cerrdefs.IsConflict accepts, so that
	// Uninstall waits for the stack's containers to stop, as it does locally.
	RemoveVolume(ctx context.Context, v NodeVolume) error
}

// ErrVolumeReachUnavailable is what a VolumeReach returns when it cannot list
// volumes across nodes at all, as opposed to failing part-way.
var ErrVolumeReachUnavailable = errors.New("cross-node volume access unavailable")

var volumeReach VolumeReach

// SetVolumeReach registers the VolumeReach Uninstall uses. Call it from init().
// Only NewEngine picks it up, because that engine addresses the process's own
// Docker context: a backend bound to a named context, or supplied through
// NewEngineWith, may be talking to a different swarm than r does.
func SetVolumeReach(r VolumeReach) { volumeReach = r }
