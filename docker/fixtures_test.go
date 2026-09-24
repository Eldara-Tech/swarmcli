// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// fixtureReplicas is what one service row shows: the REPLICAS column
// (running/desired), the rollout progress (upToDate, shown only while
// rollingOut), and the convergence facts
// a job-aware reading of that ratio needs.
type fixtureReplicas struct {
	Running          int  `json:"running"`
	Desired          int  `json:"desired"`
	UpToDate         int  `json:"upToDate"`
	RollingOut       bool `json:"rollingOut"`
	ConvergedRunning int  `json:"convergedRunning"`
	Completed        int  `json:"completed"`
	Job              bool `json:"job"`
	DeadTask         bool `json:"deadTask"`
}

// TestReplicaFixtures runs the shared cases in testdata/health/replicas:
// /nodes, /services and /tasks as the Engine API returns them, and per service
// ID the counts the services view and StackConvergence derive from them. The
// cases are plain Engine API JSON so that other clients implementing the same
// rules can be checked against the same data.
func TestReplicaFixtures(t *testing.T) {
	files, err := filepath.Glob("../testdata/health/replicas/*.json")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			require.NoError(t, err)
			var c struct {
				Nodes    []swarm.Node               `json:"nodes"`
				Services []swarm.Service            `json:"services"`
				Tasks    []swarm.Task               `json:"tasks"`
				Expect   map[string]fixtureReplicas `json:"expect"`
			}
			require.NoError(t, json.Unmarshal(data, &c))
			snap := &SwarmSnapshot{Nodes: c.Nodes, Services: c.Services, Tasks: c.Tasks}

			got := map[string]fixtureReplicas{}
			for _, svc := range c.Services {
				stack := svc.Spec.Labels["com.docker.stack.namespace"]
				row := stack
				if row == "" {
					row = "-"
				}
				var r fixtureReplicas
				for _, e := range snap.StackServices(row) {
					if e.ServiceID == svc.ID {
						r.Running, r.Desired, r.UpToDate, r.RollingOut = e.ReplicasOnNode, e.ReplicasTotal, e.UpToDate, e.RollingOut
					}
				}
				for _, conv := range snap.StackConvergence(stack) {
					if conv.Name == svc.Spec.Name {
						require.Equal(t, r.Desired, conv.Desired, "convergence and the services view disagree on desired")
						r.ConvergedRunning, r.Completed, r.Job, r.DeadTask = conv.Running, conv.Completed, conv.Job, conv.DeadTask
					}
				}
				got[svc.ID] = r
			}
			require.Equal(t, c.Expect, got)
		})
	}
}
