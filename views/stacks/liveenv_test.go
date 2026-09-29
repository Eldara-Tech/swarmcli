// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// liveStack is a reconstruction: every value is a string, and an escaped '$'
// is not empty.
const liveStack = `version: "3.9"
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
`

// A name is withheld while a running service holds it empty and the edit still
// gives it "" in any service, in either shape the edit writes it. A null or a
// bare name is compose's pass-through, which only the operator writes.
func TestLiveEmptyEnvFollowsTheEdit(t *testing.T) {
	for _, tc := range []struct {
		name, edited string
		want         []string
	}{
		{"unchanged", liveStack, []string{"BAR", "FOO"}},
		{"a value given in one service of two", `services:
  api:
    environment:
      FOO: ""
  web:
    environment:
      BAR: x
      FOO: x
`, []string{"FOO"}},
		{"the only empty service removed", `services:
  web:
    environment:
      BAR: x
      FOO: x
`, nil},
		{"list form", `services:
  web:
    environment:
      - BAR=
      - FOO=
`, []string{"BAR", "FOO"}},
		{"null or bare, the operator's pass-through", `services:
  web:
    environment:
      BAR:
      FOO: ~
  api:
    environment:
      - FOO
`, nil},
		{"carried into a service the edit renames", `services:
  web:
    environment:
      BAR: x
      FOO: x
  new:
    environment:
      FOO: ""
`, []string{"FOO"}},
		{"a name the edit adds", `services:
  web:
    environment:
      NEW: ""
`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			names, err := liveEmptyEnv(liveStack, tc.edited)
			require.NoError(t, err)
			require.Equal(t, tc.want, names)
		})
	}
}

func TestLiveEmptyEnvWithNoEnvironment(t *testing.T) {
	names, err := liveEmptyEnv("services:\n  web:\n    image: nginx\n", "services:\n  web:\n    image: nginx\n")
	require.NoError(t, err)
	require.Empty(t, names)
}

// A variable the docker CLI reads for itself, which this environment sets and
// the edit leaves empty, refuses the redeploy, naming the first service that
// holds one.
func TestLiveEmptyEnvRefusesAVariableTheCLIWouldFill(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy")
	live := "services:\n  web:\n    environment:\n      PATH: \"\"\n  api:\n    environment:\n      HTTP_PROXY: \"\"\n"
	_, err := liveEmptyEnv(live, live)
	require.EqualError(t, err, "services.api.environment: 'HTTP_PROXY' has no value in the running service, "+
		"and the docker CLI that redeploys the stack would fill it from its own 'HTTP_PROXY', which it reads itself — give it a value or remove it, "+
		"or unset it in the shell running swarmcli")
}

// Such a variable is neither withheld nor refused when nothing would fill it:
// the edit gives it a value, this environment does not set it, or sets it
// empty. The CLI's lookup matches names exactly, so a lower-case path is not
// filled from PATH.
func TestLiveEmptyEnvLeavesACLIVariableNothingFills(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	require.NoError(t, os.Unsetenv("HTTP_PROXY"))
	t.Setenv("NO_PROXY", "")
	live := "services:\n  web:\n    environment:\n      PATH: \"\"\n      path: \"\"\n      HTTP_PROXY: \"\"\n      NO_PROXY: \"\"\n      FOO: \"\"\n"
	edited := "services:\n  web:\n    environment:\n      PATH: /usr/bin\n      path: \"\"\n      HTTP_PROXY: \"\"\n      NO_PROXY: \"\"\n      FOO: \"\"\n"
	names, err := liveEmptyEnv(live, edited)
	require.NoError(t, err)
	require.Equal(t, []string{"FOO"}, names)
}

// The CLI can set GODEBUG from the docker context whatever this environment
// holds, so a GODEBUG the edit keeps empty is always refused; one it gives a
// value is not.
func TestLiveEmptyEnvRefusesAKeptEmptyGODEBUG(t *testing.T) {
	t.Setenv("GODEBUG", "")
	require.NoError(t, os.Unsetenv("GODEBUG"))
	live := "services:\n  web:\n    environment:\n      GODEBUG: \"\"\n"
	_, err := liveEmptyEnv(live, live)
	require.ErrorContains(t, err, "services.web.environment: 'GODEBUG' has no value in the running service")

	names, err := liveEmptyEnv(live, "services:\n  web:\n    environment:\n      GODEBUG: http2client=0\n")
	require.NoError(t, err)
	require.Empty(t, names)
}

func TestLiveEmptyEnvRefusesAnUnreadableStack(t *testing.T) {
	_, err := liveEmptyEnv("services: [", liveStack)
	require.ErrorContains(t, err, "parse the reconstructed stack")
	_, err = liveEmptyEnv(liveStack, "services: [")
	require.ErrorContains(t, err, "parse the edited stack")
}
