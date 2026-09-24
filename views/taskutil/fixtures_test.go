// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package taskutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// TestActiveDeploymentErrorsFixtures runs the shared cases in
// testdata/health/deployment-errors: /tasks as the Engine API returns it, and
// the serviceID -> error text the rule must produce (a service absent from
// expect is healthy). The cases are plain Engine API JSON so that other clients
// implementing the same rule can be checked against the same data.
func TestActiveDeploymentErrorsFixtures(t *testing.T) {
	files, err := filepath.Glob("../../testdata/health/deployment-errors/*.json")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			require.NoError(t, err)
			var c struct {
				Tasks  []swarm.Task      `json:"tasks"`
				Expect map[string]string `json:"expect"`
			}
			require.NoError(t, json.Unmarshal(data, &c))
			got := ActiveDeploymentErrorsByService(c.Tasks)
			if got == nil {
				got = map[string]string{}
			}
			require.Equal(t, c.Expect, got)
		})
	}
}
