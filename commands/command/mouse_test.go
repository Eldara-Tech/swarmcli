// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package command

import (
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/args"
	"github.com/Eldara-Tech/swarmcli/v2/registry"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	"github.com/stretchr/testify/require"
)

func TestMouseExecuteAsksTheAppToToggle(t *testing.T) {
	require.Equal(t, view.ToggleMouseMsg{}, Mouse{}.Execute(nil, args.Args{})())
}

// The stack-bar notice a toggle leaves names `:mouse`, so the command it
// advertises has to exist.
func TestMouseIsRegistered(t *testing.T) {
	c, ok := registry.Get(Mouse{}.Name())
	require.True(t, ok)
	require.IsType(t, Mouse{}, c)
}
