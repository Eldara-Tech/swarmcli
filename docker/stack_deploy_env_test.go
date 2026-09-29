// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build !windows

package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// standInDocker puts a `docker` on PATH that records the environment it was
// started with and succeeds, and returns a reader for that record with a
// leading newline, so every variable can be matched as "\nNAME=".
func standInDocker(t *testing.T) func() string {
	t.Helper()
	dir := t.TempDir()
	envOut := filepath.Join(dir, "env")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"),
		[]byte("#!/bin/sh\nenv > \""+envOut+"\"\n"), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		got, err := os.ReadFile(envOut)
		require.NoError(t, err)
		return "\n" + string(got)
	}
}

func TestDeployStackInContextWithholdsUnsetEnv(t *testing.T) {
	env := standInDocker(t)
	t.Setenv("FOO", "x")
	t.Setenv("KEEP", "x")

	require.NoError(t, DeployStackInContext(context.Background(), "stand-in", "web", testManifest,
		ResolveImageDefault, nil, DeployOptions{UnsetEnv: []string{"FOO"}}))
	require.NotContains(t, env(), "\nFOO=")
	require.Contains(t, env(), "\nKEEP=x\n")
}

// The zero value is what the stacks view deploys a document the operator loaded
// or wrote with, which keeps the operator's whole environment.
func TestDeployStackInContextKeepsTheEnvironmentByDefault(t *testing.T) {
	env := standInDocker(t)
	t.Setenv("FOO", "x")

	require.NoError(t, DeployStackInContext(context.Background(), "stand-in", "web", testManifest,
		ResolveImageDefault, nil, DeployOptions{}))
	require.Contains(t, env(), "\nFOO=x\n")
}

// Outside Windows a variable name is case-sensitive, so withholding one spelling
// leaves every other alone.
func TestDeployEnvMatchesNamesExactly(t *testing.T) {
	got := deployEnv([]string{"FOO=1", "foo=2", "FOOBAR=3", "BAR=4"}, []string{"foo"})
	require.Equal(t, []string{"FOO=1", "FOOBAR=3", "BAR=4"}, got)
}
