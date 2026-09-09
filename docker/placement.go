// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"net"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types/swarm"
)

// Placement-constraint matching, ported from swarmkit's manager/constraint.
//
// A global service does not target every node: swarm's global orchestrator
// creates one task per schedulable node that also satisfies the service's
// placement constraints. Reporting the bare node count made a service pinned to
// `node.role == manager` on a 3+3 swarm read 3/6 forever (issue #643).
//
// The port is deliberate rather than a dependency: swarmkit's constraint package
// matches against its own protobuf node type, not the API's swarm.Node, and
// pulling the module in for one file would drag grpc and raft behind it. Keeping
// the semantics identical is therefore this file's job — including the parts
// that look like quirks: matching is case-insensitive, an absent label matches
// the empty string (so `!=` succeeds where `==` fails), and a key swarm does not
// recognise matches nothing.

const (
	nodeLabelPrefix   = "node.labels."
	engineLabelPrefix = "engine.labels."
)

var (
	// constraintKey and constraintValue are swarmkit's own validation patterns.
	// The value pattern excludes the characters reserved for current and future
	// operators.
	constraintKey   = regexp.MustCompile(`^(?i)[a-z_][a-z0-9\-_.]+$`)
	constraintValue = regexp.MustCompile(`^(?i)[a-z0-9:\-_\s\.\*\(\)\?\+\[\]\\\^\$\|\/]+$`)
)

// nodeConstraint is one parsed `key == value` or `key != value` expression.
type nodeConstraint struct {
	key   string
	exp   string
	equal bool
}

// parseConstraints parses a service's placement constraints.
//
// It returns nil if any expression is malformed, matching swarmkit's Parse,
// which discards the whole list on the first error. The daemon validates
// constraints when the service is created, so an unparseable one cannot reach a
// live service; the fallback is simply to filter nothing.
func parseConstraints(exprs []string) []nodeConstraint {
	out := make([]nodeConstraint, 0, len(exprs))
	for _, e := range exprs {
		c, ok := parseConstraint(e)
		if !ok {
			return nil
		}
		out = append(out, c)
	}
	return out
}

func parseConstraint(e string) (nodeConstraint, bool) {
	for _, op := range []string{"==", "!="} {
		if !strings.Contains(e, op) {
			continue
		}
		parts := strings.SplitN(e, op, 2)
		key := strings.TrimSpace(parts[0])
		exp := strings.TrimSpace(parts[1])
		if !constraintKey.MatchString(key) || !constraintValue.MatchString(exp) {
			return nodeConstraint{}, false
		}
		return nodeConstraint{key: key, exp: exp, equal: op == "=="}, true
	}
	return nodeConstraint{}, false
}

// match reports whether the node's value for this constraint's key satisfies it.
func (c nodeConstraint) match(what string) bool {
	return strings.EqualFold(c.exp, what) == c.equal
}

// nodeMatches reports whether a node satisfies every constraint.
func nodeMatches(constraints []nodeConstraint, n swarm.Node) bool {
	for _, c := range constraints {
		var ok bool
		switch {
		case strings.EqualFold(c.key, "node.id"):
			ok = c.match(n.ID)
		case strings.EqualFold(c.key, "node.hostname"):
			ok = c.match(n.Description.Hostname)
		case strings.EqualFold(c.key, "node.ip"):
			ok = c.matchIP(n.Status.Addr)
		case strings.EqualFold(c.key, "node.role"):
			ok = c.match(string(n.Spec.Role))
		case strings.EqualFold(c.key, "node.platform.os"):
			ok = c.match(n.Description.Platform.OS)
		case strings.EqualFold(c.key, "node.platform.arch"):
			ok = c.match(n.Description.Platform.Architecture)
		case hasPrefixFold(c.key, nodeLabelPrefix):
			ok = c.match(n.Spec.Labels[c.key[len(nodeLabelPrefix):]])
		case hasPrefixFold(c.key, engineLabelPrefix):
			ok = c.match(n.Description.Engine.Labels[c.key[len(engineLabelPrefix):]])
		default:
			// A key swarm cannot interpret schedules nothing, so neither
			// operator can be satisfied.
			return false
		}
		if !ok {
			return false
		}
	}
	return true
}

// matchIP compares against a single address or a CIDR subnet. A malformed
// expression matches nothing, whichever operator it carries.
func (c nodeConstraint) matchIP(addr string) bool {
	nodeIP := net.ParseIP(addr)
	if ip := net.ParseIP(c.exp); ip != nil {
		return ip.Equal(nodeIP) == c.equal
	}
	if _, subnet, err := net.ParseCIDR(c.exp); err == nil {
		return subnet.Contains(nodeIP) == c.equal
	}
	return false
}

// hasPrefixFold is strings.HasPrefix with the case-insensitivity swarmkit
// applies to constraint keys. The key must be longer than the prefix: a bare
// "node.labels." names no label.
func hasPrefixFold(key, prefix string) bool {
	return len(key) > len(prefix) && strings.EqualFold(key[:len(prefix)], prefix)
}

// eligibleNodeCount is how many of the given nodes swarm would place a global
// service's task on.
func eligibleNodeCount(svc swarm.Service, nodes []swarm.Node) int {
	pl := svc.Spec.TaskTemplate.Placement
	if pl == nil || len(pl.Constraints) == 0 {
		return len(nodes)
	}
	constraints := parseConstraints(pl.Constraints)
	if constraints == nil {
		return len(nodes)
	}
	count := 0
	for _, n := range nodes {
		if nodeMatches(constraints, n) {
			count++
		}
	}
	return count
}
