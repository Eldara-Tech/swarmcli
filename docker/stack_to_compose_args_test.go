// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// These two commands used to carry no --context at all, so they followed
// ~/.docker/config.json — neither the context the SDK client is connected to
// nor the one DOCKER_CONTEXT names. A stack selected from one swarm could be
// reconstructed from another's services and networks (#611).
func TestStackToComposeArgs_NameTheSessionContext(t *testing.T) {
	stubActiveContext(t, "swarm-a")
	_, err := SessionContext()
	require.NoError(t, err)

	require.Equal(t,
		[]string{"--context", "swarm-a", "stack", "services", "web", "--format", "{{.Name}}"},
		stackServicesArgs("web"))
	require.Equal(t,
		[]string{"--context", "swarm-a", "network", "ls", "--no-trunc", "--format", "{{.ID}}\t{{.Name}}"},
		networkListArgs())
}

// TestStackToComposeArgs_UnresolvableContextStillRuns — with no context to
// name, the command is better run against Docker's own default than not run at
// all: that is the behaviour these call sites had before the flag was added.
func TestStackToComposeArgs_UnresolvableContextStillRuns(t *testing.T) {
	orig := activeContextFn
	ResetSessionContext()
	t.Cleanup(func() { activeContextFn = orig; ResetSessionContext() })
	t.Setenv(envContextVar, "")
	activeContextFn = func() (string, error) { return "", errors.New("docker not running") }

	require.Equal(t,
		[]string{"stack", "services", "web", "--format", "{{.Name}}"},
		stackServicesArgs("web"))
}
