// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// rolloutManifest has one ordinary service and three marked peers, the shape of
// the mariadb-galera chart. peer-1 uses the list form of deploy labels and
// peer-3 the mapping form; both count.
const rolloutManifest = `version: "3.9"
services:
  web:
    image: nginx
  peer-2:
    image: mariadb
    command: ["sh", "-c", "echo $$HOSTNAME"]
    deploy:
      labels:
        - "com.swarmcli.rollout=sequential"
        - "team=db"
  peer-1:
    image: mariadb
    deploy:
      labels:
        - "com.swarmcli.rollout=sequential"
  peer-3:
    image: mariadb
    deploy:
      labels:
        com.swarmcli.rollout: sequential
networks:
  net:
    external: true
volumes:
  data: {}
`

// manifestKeys is a manifest's service keys, sorted.
func manifestKeys(t *testing.T, manifest string) []string {
	t.Helper()
	var doc struct {
		Services map[string]any `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(manifest), &doc))
	keys := make([]string, 0, len(doc.Services))
	for k := range doc.Services {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestSequentialServicesReadsBothLabelForms(t *testing.T) {
	got, err := sequentialServices(rolloutManifest)
	require.NoError(t, err)
	require.Equal(t, []string{"peer-1", "peer-2", "peer-3"}, got)

	none, err := sequentialServices("services:\n  web:\n    image: x\n    deploy:\n      labels: [\"com.swarmcli.rollout=parallel\"]\n")
	require.NoError(t, err)
	require.Empty(t, none, "only the value sequential marks a service")
}

func TestWithoutServicesKeepsEverythingElse(t *testing.T) {
	out, kept, err := withoutServices(rolloutManifest, map[string]bool{"peer-1": true, "peer-3": true})
	require.NoError(t, err)
	require.Equal(t, 2, kept)
	require.Equal(t, []string{"peer-2", "web"}, manifestKeys(t, out))

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	require.Contains(t, doc, "networks")
	require.Contains(t, doc, "volumes")
	// The compose escape survives the round trip: docker stack deploy, not this
	// package, turns $$ into $.
	require.Contains(t, out, "echo $$HOSTNAME")
}

// rollBackend scripts a stack whose marked peers each update at the deploy that
// first carries them. Right after that deploy the peer still reads as the old
// generation, converged: the window in which swarm has not begun the update yet.
// The next poll shows the update in flight, the one after that the new task
// running past its monitor window.
type rollBackend struct {
	*fakeBackend
	manifests     []string
	pollsAtDeploy []int
	polls         int
	running       bool // whether the stack's services exist yet
	upgradeFrom   int  // index of the first deploy after the install
	unchanged     map[string]bool
	oldPaused     map[string]bool // the peer's previous update was left paused
	newPaused     map[string]bool // the peer's update pauses after it starts
}

func newRollBackend() *rollBackend {
	return &rollBackend{fakeBackend: newFakeBackend(), unchanged: map[string]bool{}, oldPaused: map[string]bool{}, newPaused: map[string]bool{}}
}

func (b *rollBackend) PreservesOmittedServices() bool { return true }

func (b *rollBackend) DeployStack(ctx context.Context, req DeployRequest) error {
	b.manifests = append(b.manifests, req.Manifest)
	b.pollsAtDeploy = append(b.pollsAtDeploy, b.polls)
	b.polls = 0
	return b.fakeBackend.DeployStack(ctx, req)
}

var (
	oldUpdate = time.Unix(1600000000, 0).UTC()
	newUpdate = time.Unix(1700000100, 0).UTC()
)

func (b *rollBackend) StackServices(_ context.Context, name string) []ServiceState {
	b.polls++
	if !b.running {
		return nil
	}
	settled := ServiceState{Running: 1, Desired: 1, UpdateState: "completed", Monitor: time.Second, NewestTaskAge: time.Minute}
	out := []ServiceState{withName(settled, name+"_web")}
	for _, key := range []string{"peer-1", "peer-2", "peer-3"} {
		st := withName(settled, name+"_"+key)
		st.UpdateStartedAt = oldUpdate
		if b.oldPaused[key] {
			st.UpdateState = "paused"
		}
		// The first upgrade deploy that carries this peer is the one that updates
		// it, and that update gets a start time of its own.
		at := -1
		for d := b.upgradeFrom; d < len(b.manifests); d++ {
			if contains(manifestKeysQuiet(b.manifests[d]), key) {
				at = d
				break
			}
		}
		started := newUpdate.Add(time.Duration(at) * time.Second)
		latest := at == len(b.manifests)-1
		switch {
		case at < 0:
		case b.unchanged[key]:
			st.TaskSpecChanged = false
		case latest && b.polls <= 1:
			st.TaskSpecChanged = true // swarm has not started the update yet
		case latest && b.polls == 2:
			st.TaskSpecChanged, st.UpdateStartedAt, st.UpdateState = true, started, "updating"
		default:
			st.TaskSpecChanged, st.UpdateStartedAt, st.UpdateState = true, started, "completed"
			if b.newPaused[key] {
				st.UpdateState = "paused"
			}
		}
		out = append(out, st)
	}
	return out
}

func withName(s ServiceState, name string) ServiceState { s.Name = name; return s }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func manifestKeysQuiet(manifest string) []string {
	var doc struct {
		Services map[string]any `yaml:"services"`
	}
	_ = yaml.Unmarshal([]byte(manifest), &doc)
	var keys []string
	for k := range doc.Services {
		keys = append(keys, k)
	}
	return keys
}

func fastPolls(t *testing.T) {
	prev := waitPollInterval
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = prev })
}

func installRolling(t *testing.T, b *rollBackend) *Engine {
	t.Helper()
	e := testEngine(b)
	_, err := e.Install(context.Background(), "db", ReleaseChart{Name: "galera", Version: "1"}, nil, rolloutManifest, InstallOptions{})
	require.NoError(t, err)
	require.Len(t, b.manifests, 1, "an install has nothing running to protect, so every service starts at once")
	require.Equal(t, []string{"peer-1", "peer-2", "peer-3", "web"}, manifestKeys(t, b.manifests[0]))
	b.running = true
	b.upgradeFrom = len(b.manifests)
	return e
}

func TestUpgradeRollsMarkedServicesOneAtATime(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)

	_, err := e.Upgrade(context.Background(), "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
	require.NoError(t, err)

	require.Len(t, b.manifests, 5, "one deploy without the peers, then one per peer")
	require.Equal(t, []string{"web"}, manifestKeys(t, b.manifests[1]))
	require.Equal(t, []string{"peer-1", "web"}, manifestKeys(t, b.manifests[2]))
	require.Equal(t, []string{"peer-1", "peer-2", "web"}, manifestKeys(t, b.manifests[3]))
	require.Equal(t, []string{"peer-1", "peer-2", "peer-3", "web"}, manifestKeys(t, b.manifests[4]))
	// The next peer is deployed only after the previous one's update started and
	// converged: the stale poll, the in-flight one and the converged one, plus the
	// next step's own read of the state before its deploy.
	require.Equal(t, 4, b.pollsAtDeploy[3], "peer-2 deployed before peer-1 had converged")
	require.Equal(t, 4, b.pollsAtDeploy[4], "peer-3 deployed before peer-2 had converged")
}

func TestRolloutDoesNotWaitForAServiceThatDidNotChange(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)
	b.unchanged["peer-2"] = true

	_, err := e.Upgrade(context.Background(), "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
	require.NoError(t, err)
	require.Len(t, b.manifests, 5)
	require.Equal(t, 2, b.pollsAtDeploy[4], "an update that changed nothing restarts nothing, so there is nothing to wait for")
}

func TestRolloutIgnoresThePausedUpdateItReplaces(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)
	b.oldPaused["peer-1"] = true

	_, err := e.Upgrade(context.Background(), "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
	require.NoError(t, err, "a pause left by an earlier deploy is not this update's verdict")
	require.Len(t, b.manifests, 5)
}

func TestRolloutStopsAtAServiceThatWedges(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)
	b.newPaused["peer-2"] = true

	_, err := e.Upgrade(context.Background(), "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
	require.ErrorContains(t, err, "service 'db_peer-2'")
	require.ErrorContains(t, err, "the services after it were not updated")
	require.Len(t, b.manifests, 4, "peer-3 must not be updated after peer-2 wedged")
	// The revision is recorded, as after a --wait that fails, so a re-run rolls on from it.
	rels, err := e.History(context.Background(), "db")
	require.NoError(t, err)
	require.Len(t, rels, 2)
}

func TestRolloutTimesOutPerService(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)
	// A clock that moves a minute per read, and a peer whose update never starts.
	tick := time.Unix(1700000000, 0).UTC()
	e.now = func() time.Time { tick = tick.Add(time.Minute); return tick }
	e.Backend = &stuckRollBackend{rollBackend: b}

	_, err := e.Upgrade(context.Background(), "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{Timeout: 3 * time.Minute})
	require.ErrorContains(t, err, "timed out waiting for service 'db_peer-1' to roll out")
	require.Len(t, b.manifests, 3, "no peer after the stuck one is updated")
}

// stuckRollBackend never lets peer-1's update start.
type stuckRollBackend struct{ *rollBackend }

func (s *stuckRollBackend) StackServices(ctx context.Context, name string) []ServiceState {
	out := s.rollBackend.StackServices(ctx, name)
	for i := range out {
		if out[i].Name == name+"_peer-1" && len(s.manifests) > 1 {
			out[i].TaskSpecChanged, out[i].UpdateStartedAt, out[i].UpdateState = true, oldUpdate, "completed"
		}
	}
	return out
}

func TestBackendThatMayPruneGetsTheWholeManifest(t *testing.T) {
	fb := newFakeBackend() // does not implement OmittedServicesPreserver
	e := testEngine(fb)
	ctx := context.Background()
	_, err := e.Install(ctx, "db", ReleaseChart{Name: "galera", Version: "1"}, nil, rolloutManifest, InstallOptions{})
	require.NoError(t, err)
	calls := fb.stackServiceCalls
	_, err = e.Upgrade(ctx, "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"peer-1", "peer-2", "peer-3", "web"}, manifestKeys(t, fb.deployed["db"]))
	require.Equal(t, calls, fb.stackServiceCalls, "a backend that cannot deploy a partial manifest is not even asked what runs")
}

// onlyPeersManifest is a stack of marked services and nothing else, as the
// mariadb-galera chart renders without its proxy and exporters.
const onlyPeersManifest = `services:
  peer-1:
    image: mariadb
    deploy:
      labels: ["com.swarmcli.rollout=sequential"]
  peer-2:
    image: mariadb
    deploy:
      labels: ["com.swarmcli.rollout=sequential"]
`

func TestRolloutWhenEveryServiceIsMarked(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := testEngine(b)
	ctx := context.Background()
	_, err := e.Install(ctx, "db", ReleaseChart{Name: "galera", Version: "1"}, nil, onlyPeersManifest, InstallOptions{})
	require.NoError(t, err)
	b.running = true
	b.upgradeFrom = len(b.manifests)

	_, err = e.Upgrade(ctx, "db", ReleaseChart{Name: "galera", Version: "2"}, nil, onlyPeersManifest, InstallOptions{})
	require.NoError(t, err)
	require.Len(t, b.manifests, 3, "the install, then one deploy per peer")
	require.Equal(t, []string{"peer-1"}, manifestKeys(t, b.manifests[1]), "a first deploy must carry a service; the first held one goes in it")
	require.Equal(t, []string{"peer-1", "peer-2"}, manifestKeys(t, b.manifests[2]))
	require.Equal(t, 4, b.pollsAtDeploy[2], "peer-2 deployed before peer-1 had converged")
}
