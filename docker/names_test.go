// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateSwarmObjectName_Accepts(t *testing.T) {
	for _, name := range []string{
		"a",
		"my-secret",
		"my.api.key",
		"swarmcli_staging_SUPABASE_PUBLISHABLE_KEY",
		strings.Repeat("a", MaxSwarmObjectNameLen),
	} {
		require.NoError(t, ValidateSwarmObjectName("secret", name), "name %q", name)
	}
}

func TestValidateSwarmObjectName_Rejects(t *testing.T) {
	for name, want := range map[string]string{
		"": "cannot be empty",
		strings.Repeat("a", MaxSwarmObjectNameLen+1): "64 characters or fewer",
		"has space":      "may contain only",
		"has/slash":      "may contain only",
		"-leading-dash":  "start and end",
		"trailing-dash-": "start and end",
		".leading.dot":   "start and end",
	} {
		err := ValidateSwarmObjectName("secret", name)
		require.Error(t, err, "name %q", name)
		require.Contains(t, err.Error(), want, "name %q", name)
	}
}

// The kind names the object in every message, so the configs dialog does not
// tell an operator about secrets.
func TestValidateSwarmObjectName_NamesTheKind(t *testing.T) {
	require.ErrorContains(t, ValidateSwarmObjectName("config", ""), "config name")
}
