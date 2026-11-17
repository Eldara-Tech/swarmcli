// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"context"
	"fmt"
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

// A service left out may carry an anchor another one aliases; the result must still
// parse and mean the same.
func TestWithoutServicesExpandsAliasesItWouldStrand(t *testing.T) {
	m := "services:\n  web:\n    image: nginx\n    environment: &env\n      A: \"1\"\n  peer-1:\n    image: mariadb\n    environment: *env\n    deploy:\n      labels: &l\n        - \"x=y\"\n  peer-2:\n    image: mariadb\n    deploy:\n      labels: *l\n"
	for _, keep := range []string{"peer-1", "peer-2"} {
		out, kept, err := withoutServices(m, func(k string) bool { return k != keep })
		require.NoError(t, err)
		require.Equal(t, 1, kept)
		var doc struct {
			Services map[string]struct {
				Environment map[string]string `yaml:"environment"`
				Deploy      struct {
					Labels []string `yaml:"labels"`
				} `yaml:"deploy"`
			} `yaml:"services"`
		}
		require.NoError(t, yaml.Unmarshal([]byte(out), &doc), "the %s step:\n%s", keep, out)
		require.Equal(t, []string{"x=y"}, doc.Services[keep].Deploy.Labels)
		if keep == "peer-1" {
			require.Equal(t, map[string]string{"A": "1"}, doc.Services[keep].Environment)
		}
	}
}

func TestWithoutServicesKeepsEverythingElse(t *testing.T) {
	out, kept, err := withoutServices(rolloutManifest, func(k string) bool { return k == "peer-1" || k == "peer-3" })
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
	down          map[string]bool // the peer runs no task, as while it waits for its cluster
	starting      map[string]bool // the peer's task is on its way up, healthcheck pending, until it is updated
	scaledToZero  bool            // every peer is scaled to 0, as before a recovery
}

func newRollBackend() *rollBackend {
	return &rollBackend{fakeBackend: newFakeBackend(), unchanged: map[string]bool{}, oldPaused: map[string]bool{}, newPaused: map[string]bool{}, down: map[string]bool{}, starting: map[string]bool{}}
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
		if b.down[key] {
			st.Running = 0
		}
		if b.scaledToZero {
			st.Running, st.Desired = 0, 0
		}
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
		if b.starting[key] && at < 0 {
			st.Running, st.Starting = 0, 1
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
	require.Equal(t, []string{"peer-1"}, manifestKeys(t, b.manifests[2]))
	require.Equal(t, []string{"peer-2"}, manifestKeys(t, b.manifests[3]))
	require.Equal(t, []string{"peer-3"}, manifestKeys(t, b.manifests[4]))
	// The next peer is deployed only after the previous one's update started and
	// converged: the stale poll, the in-flight one and the converged one, plus the
	// next step's own read of the state before its deploy.
	require.Equal(t, 4, b.pollsAtDeploy[3], "peer-2 deployed before peer-1 had converged")
	require.Equal(t, 4, b.pollsAtDeploy[4], "peer-3 deployed before peer-2 had converged")
}

// Every step deploys its peer alone: docker stack deploy re-sends every service it
// is handed, and swarm refuses one an earlier deploy is still recording an update
// for ("update out of sequence", #728). So across the whole rollout every service
// is deployed exactly once, and every step keeps the stack's top-level keys.
func TestRolloutDeploysEveryServiceOnce(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)
	// Bounded: a step that sends a peer before its own step leaves this backend
	// waiting on an update that never starts, so that shows as an error, not a hang.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := e.Upgrade(ctx, "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
	require.NoError(t, err)

	seen := map[string]int{}
	for _, m := range b.manifests[b.upgradeFrom:] {
		for _, k := range manifestKeys(t, m) {
			seen[k]++
		}
	}
	require.Equal(t, map[string]int{"web": 1, "peer-1": 1, "peer-2": 1, "peer-3": 1}, seen)
	for i, m := range b.manifests[b.upgradeFrom:] {
		var doc map[string]any
		require.NoError(t, yaml.Unmarshal([]byte(m), &doc))
		for _, k := range []string{"version", "networks", "volumes"} {
			require.Contains(t, doc, k, "deploy %d dropped the stack's top-level %s", i, k)
		}
	}
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
	var re *RolloutError
	require.ErrorAs(t, err, &re, "a caller has to be able to say which services were left behind")
	require.Equal(t, "db_peer-2", re.Service)
	require.False(t, re.TimedOut)
	require.Equal(t, []string{"db_peer-3"}, re.Pending)
	// The revision is recorded before its rollout, and a failed copy on top says
	// the rollout did not finish (#730), so a re-run upgrade or apply rolls on.
	requireUnfinished(t, e, 2)
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
	var re *RolloutError
	require.ErrorAs(t, err, &re)
	require.True(t, re.TimedOut)
	require.Equal(t, 3*time.Minute, re.Timeout)
	require.Equal(t, []string{"db_peer-2", "db_peer-3"}, re.Pending)
	requireUnfinished(t, e, 2)
}

// requireUnfinished asserts that revision rev was recorded and that a failed
// copy of it sits on top: what a rollout that stopped leaves behind.
func requireUnfinished(t *testing.T, e *Engine, rev int) {
	t.Helper()
	rels, err := e.History(context.Background(), "db")
	require.NoError(t, err)
	require.Len(t, rels, rev+1)
	require.Equal(t, StatusSuperseded, rels[rev-1].Status)
	top := rels[rev]
	require.Equal(t, StatusFailed, top.Status)
	require.Equal(t, rels[rev-1].Manifest, top.Manifest, "the failed revision is a copy of the one whose rollout stopped")
	require.Equal(t, rels[rev-1].Chart, top.Chart)

	// What `charts ls` shows, and what a plan compares against.
	cur, err := e.List(context.Background())
	require.NoError(t, err)
	require.Len(t, cur, 1)
	require.Equal(t, StatusFailed, cur[0].Status, "charts ls must not say deployed for a rollout that stopped")
	diff, err := changes(&cur[0], top.Chart, top.Values, top.Manifest, "", top.Files)
	require.NoError(t, err)
	require.Equal(t, []Change{{Kind: ChangeUnfinished}}, diff, "a plan must call it unfinished, not unchanged")
}

// #730: a controller stopped mid-rollout cancels the context the rollout waits
// on. That interruption is the one nothing else would remember, so it is
// recorded too — on a context of its own, since the caller's is gone.
func TestARolloutCutShortByItsContextIsRecordedUnfinished(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)
	e.Backend = &stuckRollBackend{rollBackend: b}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := e.Upgrade(ctx, "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{Timeout: time.Hour})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	requireUnfinished(t, e, 2)
}

// The way forward from a failed revision is the next upgrade, which rolls on
// and records a deployed one on top.
func TestAnUpgradeAfterAnUnfinishedRolloutFinishesIt(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)
	b.newPaused["peer-2"] = true
	_, err := e.Upgrade(context.Background(), "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
	require.Error(t, err)
	requireUnfinished(t, e, 2)

	b.newPaused["peer-2"] = false
	// The fake reads each peer's update off the first deploy after upgradeFrom,
	// so a second upgrade has to start a fresh window to be seen at all.
	b.upgradeFrom = len(b.manifests)
	_, err = e.Upgrade(context.Background(), "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
	require.NoError(t, err)
	cur, err := e.List(context.Background())
	require.NoError(t, err)
	require.Equal(t, StatusDeployed, cur[0].Status)
	require.Equal(t, 4, cur[0].Revision)
}

// Eldara-Tech/swarmcli-cd#361: a controller correcting drift redeployed a whole
// manifest in one call, restarting every drifted peer together. Redeploy rolls
// them one at a time as an upgrade does, and records nothing.
func TestRedeployRollsMarkedServicesOneAtATimeAndRecordsNothing(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)

	require.NoError(t, e.Redeploy(context.Background(), "db", rolloutManifest, nil, InstallOptions{}))

	require.Len(t, b.manifests, 5, "one deploy without the peers, then one per peer")
	require.Equal(t, []string{"web"}, manifestKeys(t, b.manifests[1]))
	require.Equal(t, []string{"peer-1"}, manifestKeys(t, b.manifests[2]))
	require.Equal(t, []string{"peer-2"}, manifestKeys(t, b.manifests[3]))
	require.Equal(t, []string{"peer-3"}, manifestKeys(t, b.manifests[4]))
	require.Equal(t, 4, b.pollsAtDeploy[3], "peer-2 deployed before peer-1 had converged")
	rels, err := e.History(context.Background(), "db")
	require.NoError(t, err)
	require.Len(t, rels, 1, "a correction puts the swarm back to a revision; it records no new one")
}

func TestRedeployStopsAtAServiceThatWedgesAndRecordsNothing(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)
	b.newPaused["peer-2"] = true

	err := e.Redeploy(context.Background(), "db", rolloutManifest, nil, InstallOptions{})
	var re *RolloutError
	require.ErrorAs(t, err, &re)
	require.Equal(t, []string{"db_peer-3"}, re.Pending)
	require.Len(t, b.manifests, 4, "peer-3 must not be redeployed after peer-2 wedged")
	rels, err := e.History(context.Background(), "db")
	require.NoError(t, err)
	require.Len(t, rels, 1)
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
	require.Equal(t, []string{"peer-2"}, manifestKeys(t, b.manifests[2]))
	require.Equal(t, 4, b.pollsAtDeploy[2], "peer-2 deployed before peer-1 had converged")
}

func TestRolloutDeploysADownServiceWithTheFirstStage(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)
	b.down["peer-2"] = true

	_, err := e.Upgrade(context.Background(), "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
	require.NoError(t, err)
	require.Len(t, b.manifests, 4, "the first deploy, then one per peer that is up")
	require.Equal(t, []string{"peer-2", "web"}, manifestKeys(t, b.manifests[1]), "a peer that is down has nothing to protect")
	require.Equal(t, []string{"peer-1"}, manifestKeys(t, b.manifests[2]))
	require.Equal(t, []string{"peer-3"}, manifestKeys(t, b.manifests[3]))
}

// After a full stop every peer waits for its cluster. The upgrade that forces one
// of them to bootstrap must reach that peer at once, not after peer 1, which can
// only come up once the forced peer has.
func TestRolloutHoldsNothingWhenEveryMarkedServiceIsDown(t *testing.T) {
	for _, scaled := range []bool{false, true} {
		t.Run(fmt.Sprintf("scaledToZero=%t", scaled), func(t *testing.T) {
			fastPolls(t)
			b := newRollBackend()
			e := installRolling(t, b)
			for _, key := range []string{"peer-1", "peer-2", "peer-3"} {
				b.down[key] = true
			}
			b.scaledToZero = scaled

			_, err := e.Upgrade(context.Background(), "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
			require.NoError(t, err)
			require.Len(t, b.manifests, 2, "the install, then one deploy of everything")
			require.Equal(t, []string{"peer-1", "peer-2", "peer-3", "web"}, manifestKeys(t, b.manifests[1]))
		})
	}
}

// A peer that has just restarted is Starting until its healthcheck passes, while
// the others serve. It is held like them and rolled in its turn, not sent out with
// the first deploy: there the next peer would follow it at once, and two peers
// would restart together.
func TestRolloutHoldsAStartingServiceWhileOthersAreUp(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)
	b.starting["peer-1"] = true

	_, err := e.Upgrade(context.Background(), "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
	require.NoError(t, err)
	require.Len(t, b.manifests, 5, "one deploy without the peers, then one per peer")
	require.Equal(t, []string{"web"}, manifestKeys(t, b.manifests[1]), "a starting peer is not down")
	require.Equal(t, []string{"peer-1"}, manifestKeys(t, b.manifests[2]))
	require.Equal(t, 4, b.pollsAtDeploy[3], "peer-2 deployed before peer-1 had converged")
}

// After a full stop every peer is Starting, waiting for its cluster, while the
// stack's other services run. None is up, so none is held, exactly as when they
// run no task at all: the forced peer of a recovery must not wait behind peer 1.
func TestRolloutHoldsNothingWhenEveryMarkedServiceIsStarting(t *testing.T) {
	fastPolls(t)
	b := newRollBackend()
	e := installRolling(t, b)
	for _, key := range []string{"peer-1", "peer-2", "peer-3"} {
		b.starting[key] = true
	}

	_, err := e.Upgrade(context.Background(), "db", ReleaseChart{Name: "galera", Version: "2"}, nil, rolloutManifest, InstallOptions{})
	require.NoError(t, err)
	require.Len(t, b.manifests, 2, "the install, then one deploy of everything")
	require.Equal(t, []string{"peer-1", "peer-2", "peer-3", "web"}, manifestKeys(t, b.manifests[1]))
}
