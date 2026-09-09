// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"testing"

	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// constraintNode is a node carrying every field a constraint can read.
func constraintNode() swarm.Node {
	return swarm.Node{
		ID: "abc123",
		Spec: swarm.NodeSpec{
			Annotations: swarm.Annotations{Labels: map[string]string{"az": "eu-1", "tier": "gold"}},
			Role:        swarm.NodeRoleManager,
		},
		Description: swarm.NodeDescription{
			Hostname: "mgr-1",
			Platform: swarm.Platform{OS: "linux", Architecture: "x86_64"},
			Engine:   swarm.EngineDescription{Labels: map[string]string{"gpu": "yes"}},
		},
		Status: swarm.NodeStatus{State: swarm.NodeStateReady, Addr: "10.0.1.7"},
	}
}

func TestNodeMatches(t *testing.T) {
	cases := []struct {
		name       string
		constraint string
		want       bool
	}{
		{"id equal", "node.id == abc123", true},
		{"id not equal", "node.id != abc123", false},
		{"hostname equal", "node.hostname == mgr-1", true},
		{"hostname other", "node.hostname == wrk-1", false},
		{"role manager", "node.role == manager", true},
		{"role worker", "node.role == worker", false},
		{"role not worker", "node.role != worker", true},
		{"os", "node.platform.os == linux", true},
		{"arch", "node.platform.arch == x86_64", true},
		{"arch mismatch", "node.platform.arch == arm64", false},
		{"node label", "node.labels.az == eu-1", true},
		{"node label mismatch", "node.labels.az == eu-2", false},
		{"engine label", "engine.labels.gpu == yes", true},
		{"ip literal", "node.ip == 10.0.1.7", true},
		{"ip literal mismatch", "node.ip == 10.0.1.8", false},
		{"ip subnet", "node.ip == 10.0.1.0/24", true},
		{"ip outside subnet", "node.ip == 10.0.2.0/24", false},
		{"ip not in subnet", "node.ip != 10.0.2.0/24", true},

		// Values are compared case-insensitively, so a constraint written
		// against a differently-cased hostname still schedules.
		{"case insensitive value", "node.hostname == MGR-1", true},

		// An absent label is the empty string: == cannot match it, != always
		// can. This is what lets `node.labels.foo != bar` schedule everywhere
		// rather than nowhere.
		{"absent label equal", "node.labels.missing == x", false},
		{"absent label not equal", "node.labels.missing != x", true},

		// Swarm schedules nothing for a key it cannot interpret, so neither
		// operator is satisfiable.
		{"unknown key equal", "node.zone == eu-1", false},
		{"unknown key not equal", "node.zone != eu-1", false},

		// A bare prefix names no label and is not a key swarm knows.
		{"bare label prefix", "node.labels. == x", false},

		// A malformed address matches nothing whichever operator it carries.
		{"malformed ip", "node.ip == not-an-ip", false},
		{"malformed ip negated", "node.ip != not-an-ip", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := parseConstraints([]string{tc.constraint})
			require.NotNil(t, cs, "constraint should parse")
			require.Equal(t, tc.want, nodeMatches(cs, constraintNode()))
		})
	}
}

// Every constraint must hold, not just one.
func TestNodeMatches_AllConstraintsMustHold(t *testing.T) {
	n := constraintNode()
	require.True(t, nodeMatches(parseConstraints([]string{"node.role == manager", "node.labels.tier == gold"}), n))
	require.False(t, nodeMatches(parseConstraints([]string{"node.role == manager", "node.labels.tier == bronze"}), n))
}

func TestNodeMatches_NoConstraints(t *testing.T) {
	require.True(t, nodeMatches(nil, constraintNode()))
}

func TestParseConstraints_SpacingIsOptional(t *testing.T) {
	spaced := parseConstraints([]string{"node.role == manager"})
	tight := parseConstraints([]string{"node.role==manager"})
	require.Equal(t, spaced, tight)
}

// One malformed expression discards the whole list, as swarmkit's parser does.
// The daemon rejects these at service-create time, so the fallback only decides
// what an impossible input degrades to: filtering nothing.
func TestParseConstraints_RejectsMalformed(t *testing.T) {
	for _, expr := range []string{"node.role manager", "node.role > manager", "== manager", "1bad == x"} {
		require.Nil(t, parseConstraints([]string{expr}), expr)
	}
	require.Nil(t, parseConstraints([]string{"node.role == manager", "node.role manager"}))
}

func TestEligibleNodeCount(t *testing.T) {
	mgr, wrk := constraintNode(), constraintNode()
	wrk.ID, wrk.Spec.Role = "wrk1", swarm.NodeRoleWorker
	nodes := []swarm.Node{mgr, wrk}

	global := func(constraints ...string) swarm.Service {
		svc := swarm.Service{Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{Global: &swarm.GlobalService{}}}}
		if len(constraints) > 0 {
			svc.Spec.TaskTemplate.Placement = &swarm.Placement{Constraints: constraints}
		}
		return svc
	}

	require.Equal(t, 2, eligibleNodeCount(global(), nodes))
	require.Equal(t, 1, eligibleNodeCount(global("node.role == manager"), nodes))
	require.Equal(t, 0, eligibleNodeCount(global("node.labels.az == nowhere"), nodes))
	require.Equal(t, 2, eligibleNodeCount(global("node.role manager"), nodes),
		"an unparseable constraint must not shrink the count")

	// A placement carrying only preferences constrains nothing.
	prefs := global()
	prefs.Spec.TaskTemplate.Placement = &swarm.Placement{
		Preferences: []swarm.PlacementPreference{{Spread: &swarm.SpreadOver{SpreadDescriptor: "node.labels.az"}}},
	}
	require.Equal(t, 2, eligibleNodeCount(prefs, nodes))
}
