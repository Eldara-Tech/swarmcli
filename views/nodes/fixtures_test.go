// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package nodesview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// TestNodeHealthFixtures runs the shared cases in testdata/health/nodes: /nodes
// as the Engine API returns it, and per node ID whether the nodes view flags it
// red. The cases are plain Engine API JSON so that other clients implementing
// the same rule can be checked against the same data.
func TestNodeHealthFixtures(t *testing.T) {
	files, err := filepath.Glob("../../testdata/health/nodes/*.json")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			require.NoError(t, err)
			var c struct {
				Nodes  []swarm.Node    `json:"nodes"`
				Expect map[string]bool `json:"expect"`
			}
			require.NoError(t, json.Unmarshal(data, &c))
			got := map[string]bool{}
			for _, n := range (docker.SwarmSnapshot{Nodes: c.Nodes}).ToNodeEntries() {
				got[n.ID] = nodeIsUnhealthy(n)
			}
			require.Equal(t, c.Expect, got)
		})
	}
}
