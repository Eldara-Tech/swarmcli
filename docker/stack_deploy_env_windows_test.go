// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build windows

package docker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// On Windows a variable name is case-insensitive, so withholding one spelling
// withholds the variable however the process spells it.
func TestDeployEnvMatchesNamesWithoutCase(t *testing.T) {
	got := deployEnv([]string{"Path=1", "FOO=2", "FOOBAR=3", "=C:=C:\\"}, []string{"foo", "PATH"})
	require.Equal(t, []string{"FOOBAR=3", "=C:=C:\\"}, got)
}
