// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package command

import (
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/args"
	"github.com/Eldara-Tech/swarmcli/v2/registry"
	"github.com/Eldara-Tech/swarmcli/v2/telemetry"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	"github.com/stretchr/testify/require"
)

func TestTelemetryExecuteShowsTheStatus(t *testing.T) {
	t.Setenv(telemetry.Env, "")

	msg := Telemetry{}.Execute(nil, args.Args{})()

	info, ok := msg.(view.AppInfoMsg)
	require.True(t, ok, "the full text is shown on demand, in the info modal")
	require.Equal(t, telemetry.StatusText(), info.Message)
	require.Contains(t, info.Message, telemetry.NoticeBody)
}

// The stack-bar line names `:telemetry` and nothing else. If the command were
// not registered, the only disclosure a first run gets would point at a command
// that does not exist.
func TestTelemetryIsRegistered(t *testing.T) {
	name := Telemetry{}.Name()
	require.Contains(t, telemetry.NoticeLine, ":"+name)

	var found bool
	for _, c := range registry.All() {
		if c.Name() == name {
			found = true
			break
		}
	}
	require.True(t, found, "registry must carry the command the notice advertises")
}
