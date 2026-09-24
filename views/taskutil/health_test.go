// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package taskutil

import (
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/stretchr/testify/require"
)

func TestAssessSwarm_Unknown(t *testing.T) {
	require.Equal(t, HealthUnknown, AssessSwarm(nil).State)
	require.Equal(t, HealthUnknown, AssessSwarm(&docker.SwarmSnapshot{Locked: true}).State,
		"a locked swarm lists nothing, which is not the same as nothing failing")
	require.Equal(t, HealthHealthy, AssessSwarm(&docker.SwarmSnapshot{}).State)
}

func TestUnsettledRollout(t *testing.T) {
	cases := []struct {
		state string
		job   bool
		want  string
	}{
		{"", false, ""},
		{"updating", false, "rolling update in progress"},
		{"paused", false, "update paused"},
		{"paused", true, ""},
		{"completed", false, ""},
		{"rollback_started", false, "rolling back"},
		{"rollback_paused", false, "rollback paused"},
		{"rollback_paused", true, "rollback paused"},
		{"rollback_completed", false, ""},
	}
	for _, c := range cases {
		require.Equal(t, c.want, unsettledRollout(docker.ServiceConvergence{UpdateState: c.state, Job: c.job}), "%s job=%v", c.state, c.job)
	}
}

func TestShortOfTarget(t *testing.T) {
	cases := []struct {
		name string
		c    docker.ServiceConvergence
		want string
	}{
		{"at target", docker.ServiceConvergence{Running: 2, Desired: 2}, ""},
		{"short", docker.ServiceConvergence{Running: 1, Desired: 2}, "1/2 tasks running"},
		{"scaled to zero", docker.ServiceConvergence{Desired: 0}, ""},
		{"completed one-shot", docker.ServiceConvergence{Completed: 1, Desired: 1, Job: true}, ""},
		{"dead one-shot never converges", docker.ServiceConvergence{Desired: 1, Job: true, DeadTask: true}, ""},
		{"native job running", docker.ServiceConvergence{Running: 1, Desired: 1, Job: true, NativeJob: true}, "0/1 tasks completed"},
		{"native job finished", docker.ServiceConvergence{Completed: 1, Desired: 1, Job: true, NativeJob: true}, ""},
	}
	for _, c := range cases {
		require.Equal(t, c.want, shortOfTarget(c.c), c.name)
	}
}
