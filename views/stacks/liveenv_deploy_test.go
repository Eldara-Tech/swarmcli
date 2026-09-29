// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build !windows

package stacksview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"

	"github.com/stretchr/testify/require"
)

// standInDocker puts a `docker` on PATH that records the environment it was
// started with and succeeds, pins the session to a context it answers for, and
// returns a reader for that record. A failure then names one variable rather
// than printing the whole environment.
func standInDocker(t *testing.T) func() map[string]string {
	t.Helper()
	dir := t.TempDir()
	envOut := filepath.Join(dir, "env")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"),
		[]byte("#!/bin/sh\nenv > \""+envOut+"\"\n"), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	docker.SetSessionContext("stand-in")
	t.Cleanup(docker.ResetSessionContext)
	return func() map[string]string {
		got, err := os.ReadFile(envOut)
		require.NoError(t, err)
		env := map[string]string{}
		for _, kv := range strings.Split(string(got), "\n") {
			if name, value, ok := strings.Cut(kv, "="); ok {
				env[name] = value
			}
		}
		return env
	}
}

// deployingModel is a stacks view that deploys through the real docker
// package, so what reaches the docker CLI is what production sends.
func deployingModel(t *testing.T) *Model {
	t.Helper()
	fastSpinner(t)
	return testModel(func(m *Model) { m.deps.Stacks = docker.DefaultDeps().Stacks })
}

// A stack reconstructed from its running services holds FOO empty, so the
// redeploy of an edit keeps it empty rather than taking this shell's FOO. BAR,
// which the operator added empty in the editor, is looked up as the docker CLI
// always does.
func TestRedeployOfAnEditKeepsALiveEmptyValueEmpty(t *testing.T) {
	env := standInDocker(t)
	t.Setenv("FOO", "from-the-shell")
	t.Setenv("BAR", "from-the-shell")
	t.Setenv("KEEP", "from-the-shell")

	reconstructed := "version: \"3.9\"\nservices:\n  web:\n    image: nginx\n    environment:\n      FOO: \"\"\n      KEEP: x\n"
	m := deployingModel(t)
	cmd := m.Update(editorContentMsg{StackName: "web", Content: reconstructed + "      BAR: \"\"\n", OriginalContent: reconstructed})

	_, ok := firstOfType[stackDeployedMsg](runBatch(cmd))
	require.True(t, ok)
	got := env()
	_, leaked := got["FOO"]
	require.False(t, leaked, "FOO reached the docker CLI")
	require.Equal(t, "from-the-shell", got["BAR"])
	require.Equal(t, "from-the-shell", got["KEEP"])
}

// A document the operator loads or writes keeps the whole environment: BAR:
// with no value is theirs to have filled from the shell.
func TestCreateKeepsTheOperatorsEnvironment(t *testing.T) {
	env := standInDocker(t)
	t.Setenv("BAR", "from-the-shell")

	m := deployingModel(t)
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createNameInput.SetValue("web")
	m.createDialogContent = "version: \"3.9\"\nservices:\n  web:\n    image: nginx\n    environment:\n      BAR:\n"
	cmd := m.Update(key("enter"))

	_, ok := firstOfType[stackDeployedMsg](runBatch(cmd))
	require.True(t, ok)
	require.Equal(t, "from-the-shell", env()["BAR"])
}
