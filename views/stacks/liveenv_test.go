// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Every empty value in every service is named once, sorted; a value with any
// content, an escaped '$' included, is not.
func TestLiveEmptyEnvNamesEveryEmptyValue(t *testing.T) {
	names, err := liveEmptyEnv(`version: "3.9"
services:
  api:
    image: nginx
    environment:
      FOO: ""
      PRICE: $$5
  web:
    image: nginx
    environment:
      BAR: ""
      FOO: ""
      KEEP: x
`)
	require.NoError(t, err)
	require.Equal(t, []string{"BAR", "FOO"}, names)
}

func TestLiveEmptyEnvWithNoEnvironment(t *testing.T) {
	names, err := liveEmptyEnv("services:\n  web:\n    image: nginx\n")
	require.NoError(t, err)
	require.Empty(t, names)
}

// A variable the docker CLI reads for itself is refused in any case, and the
// refusal names the first service that holds one.
func TestLiveEmptyEnvRefusesAVariableTheCLIReads(t *testing.T) {
	_, err := liveEmptyEnv(`services:
  web:
    environment:
      path: ""
  api:
    environment:
      HTTP_PROXY: ""
      KEEP: ""
`)
	require.EqualError(t, err, "services.api.environment: 'HTTP_PROXY' is empty in the running service, "+
		"and the docker CLI that redeploys the stack reads that variable itself, so it cannot stay empty — give the service a value for it first")

	_, err = liveEmptyEnv("services:\n  web:\n    environment:\n      path: \"\"\n")
	require.ErrorContains(t, err, "services.web.environment: 'path' is empty")
}

// A variable the CLI reads is only refused empty; with a value it is left to
// the service.
func TestLiveEmptyEnvLeavesAVariableTheCLIReadsWithAValue(t *testing.T) {
	names, err := liveEmptyEnv("services:\n  web:\n    environment:\n      PATH: /usr/bin\n      FOO: \"\"\n")
	require.NoError(t, err)
	require.Equal(t, []string{"FOO"}, names)
}

func TestLiveEmptyEnvRefusesAnUnreadableStack(t *testing.T) {
	_, err := liveEmptyEnv("services: [")
	require.ErrorContains(t, err, "parse the reconstructed stack")
}
