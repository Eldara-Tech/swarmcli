// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeReach is an in-memory VolumeReach.
type fakeReach struct {
	vols    []NodeVolume
	all     bool
	listErr error
	rmErrs  map[NodeVolume][]error // errors RemoveVolume returns, one per call, before it succeeds
	removed []NodeVolume
}

func (f *fakeReach) StackVolumes(context.Context, string) ([]NodeVolume, bool, error) {
	return f.vols, f.all, f.listErr
}

func (f *fakeReach) RemoveVolume(_ context.Context, v NodeVolume) error {
	if errs := f.rmErrs[v]; len(errs) > 0 {
		f.rmErrs[v] = errs[1:]
		return errs[0]
	}
	f.removed = append(f.removed, v)
	return nil
}

// reachEngine is an engine over a three-node fake swarm whose connected node
// holds demo_data, with r as its VolumeReach.
func reachEngine(t *testing.T, r VolumeReach) (*Engine, *fakeBackend) {
	t.Helper()
	fb := newFakeBackend()
	fb.nodes = 3
	fb.volumes["demo"] = []string{"demo_data"}
	e := testEngine(fb)
	e.reach = r
	_, err := e.Install(context.Background(), "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, "services:\n  s:\n    image: x\n", InstallOptions{})
	require.NoError(t, err)
	return e, fb
}

func TestUninstallPurgesEveryNodeThroughVolumeReach(t *testing.T) {
	onA, onB := NodeVolume{"demo_data", "node-a"}, NodeVolume{"demo_data", "node-b"}
	r := &fakeReach{vols: []NodeVolume{onA, onB}, all: true}
	e, fb := reachEngine(t, r)

	res, err := e.Uninstall(context.Background(), "demo", true)
	require.NoError(t, err)
	require.Equal(t, []NodeVolume{onA, onB}, r.removed)
	require.False(t, res.VolumesMayRemain)
	require.Empty(t, fb.rmVolCalls, "the connected-node path is not used")
}

func TestUninstallVolumeReachMissedNodes(t *testing.T) {
	r := &fakeReach{vols: []NodeVolume{{"demo_data", "node-a"}}, all: false}
	e, _ := reachEngine(t, r)

	res, err := e.Uninstall(context.Background(), "demo", true)
	require.NoError(t, err)
	require.Len(t, r.removed, 1)
	require.True(t, res.VolumesMayRemain)
}

func TestUninstallVolumeReachUnavailableFallsBack(t *testing.T) {
	r := &fakeReach{listErr: ErrVolumeReachUnavailable}
	e, fb := reachEngine(t, r)

	res, err := e.Uninstall(context.Background(), "demo", true)
	require.NoError(t, err)
	require.Empty(t, fb.volumes["demo"], "the connected node's volume is purged")
	require.True(t, res.VolumesMayRemain, "three nodes, so the fallback warns")
}

func TestUninstallVolumeReachListErrorIsReported(t *testing.T) {
	r := &fakeReach{listErr: errors.New("node-b unreachable")}
	e, fb := reachEngine(t, r)

	res, err := e.Uninstall(context.Background(), "demo", true)
	require.EqualError(t, err, "listing volumes: node-b unreachable")
	require.True(t, res.VolumesMayRemain)
	require.Equal(t, []string{"demo_data"}, fb.volumes["demo"], "no fallback on a real failure")
	require.Empty(t, fb.configs, "release records are still deleted")
}

func TestUninstallVolumeReachWaitsForRelease(t *testing.T) {
	fastVolumeRelease(t, time.Minute)
	onB := NodeVolume{"demo_data", "node-b"}
	r := &fakeReach{vols: []NodeVolume{onB}, all: true, rmErrs: map[NodeVolume][]error{onB: {volumeInUse, volumeInUse}}}
	e, _ := reachEngine(t, r)

	_, err := e.Uninstall(context.Background(), "demo", true)
	require.NoError(t, err)
	require.Equal(t, []NodeVolume{onB}, r.removed)
}

func TestVolumeReachOnlyReachesNewEngine(t *testing.T) {
	r := &fakeReach{}
	SetVolumeReach(r)
	t.Cleanup(func() { SetVolumeReach(nil) })

	require.Same(t, r, NewEngine().reach)
	require.Nil(t, NewEngineWith(NewDockerBackend("other")).reach)
	require.Nil(t, NewEngineWith(newFakeBackend()).reach)
}

func TestUninstallVolumeReachRemoveErrorIsReported(t *testing.T) {
	onB := NodeVolume{"demo_data", "node-b"}
	r := &fakeReach{vols: []NodeVolume{onB}, all: true, rmErrs: map[NodeVolume][]error{onB: {errors.New("permission denied")}}}
	e, _ := reachEngine(t, r)

	res, err := e.Uninstall(context.Background(), "demo", true)
	require.EqualError(t, err, "permission denied")
	require.False(t, res.VolumesMayRemain, "every node was reached; the failure is reported as an error")
}

func TestUninstallConnectedNodeListErrorIsReported(t *testing.T) {
	e, fb := reachEngine(t, nil)
	fb.volumesErr = errors.New("daemon unreachable")

	res, err := e.Uninstall(context.Background(), "demo", true)
	require.EqualError(t, err, "listing volumes: daemon unreachable")
	require.True(t, res.VolumesMayRemain)
}
