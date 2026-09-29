// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build integration

package stacksview

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"

	"github.com/charmbracelet/x/ansi"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

const itestCompose = `version: "3.9"

services:
  whoami:
    image: traefik/whoami:v1.10
    deploy:
      replicas: 1
`

// TestDeployProgressAgainstRealSwarm covers the one seam the unit tests have to
// mock: that a real `docker stack deploy` produces the terminal message the view
// expects, and that the list the view rebuilds from a real post-deploy snapshot
// actually contains the new stack.
//
// The view's own state machine is unit-tested against mocks — every transition
// is message-driven, so a real daemon adds nothing there — and the shell-out in
// docker.DeployStackInContext is already exercised by the charts integration
// tests. This fills the gap between the two.
func TestDeployProgressAgainstRealSwarm(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()
	fastSpinner(t)

	stackName := fmt.Sprintf("itest-deploy-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := docker.RemoveStackCLI(context.Background(), stackName); err != nil {
			t.Logf("cleanup: removing stack %s: %v", stackName, err)
		}
	})

	m := New(80, 24)
	m.deps = docker.DefaultDeps()
	// New() leaves the view hidden and View() short-circuits to "" — the factory
	// is what flips this in production.
	m.Visible = true
	m.List.Viewport.Width = 80
	m.List.Viewport.Height = 20
	m.setRenderItem()

	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createNameInput.SetValue(stackName)
	m.createDialogContent = itestCompose

	cmd := m.Update(key("enter"))
	require.NotNil(t, cmd)

	// The indicator is set synchronously, before the deploy goroutine runs —
	// that is the whole point, since the deploy itself blocks for seconds.
	require.True(t, m.deploying)
	require.Equal(t, stackName, m.deployingStack)
	require.Contains(t, ansi.Strip(m.View()), "Deploying stack")

	deployed, ok := firstOfType[stackDeployedMsg](runBatch(cmd))
	require.True(t, ok, "a real deploy must report success with stackDeployedMsg")
	require.Equal(t, stackName, deployed.StackName)

	reload := m.Update(deployed)
	require.False(t, m.deploying, "the indicator must clear once the deploy returns")
	require.Contains(t, m.toastMessage, stackName)

	// The blank-list guard, against a real snapshot rather than a canned one:
	// both create paths used to return a Msg with no Stacks, which emptied the
	// list until the next poll.
	loaded, ok := firstOfType[Msg](runBatch(reload))
	require.True(t, ok, "deploy success must reload the stack list")
	require.NotEmpty(t, loaded.Stacks, "the list must not be blanked after a deploy")

	names := make([]string, 0, len(loaded.Stacks))
	for _, s := range loaded.Stacks {
		names = append(names, s.Name)
	}
	require.Contains(t, names, stackName)
}

// TestRedeployOfAnEditKeepsAnEmptyValueAgainstRealSwarm round-trips a service
// holding FOO empty through the reconstruction the editor opens and the
// redeploy of the edit, with FOO set in this environment: the service keeps FOO
// empty and takes the edit.
func TestRedeployOfAnEditKeepsAnEmptyValueAgainstRealSwarm(t *testing.T) {
	swarmlog.InitTestIfTestLogEnv()
	fastSpinner(t)

	stackName := fmt.Sprintf("itest-edit-env-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := docker.RemoveStackCLI(context.Background(), stackName); err != nil {
			t.Logf("cleanup: removing stack %s: %v", stackName, err)
		}
	})
	// Set but empty, so the first deploy fills FOO with "" whatever the
	// environment running the tests holds.
	t.Setenv("FOO", "")
	require.NoError(t, docker.DeployStack(stackName,
		"version: \"3.9\"\nservices:\n  app:\n    image: traefik/whoami:v1.10\n    environment:\n      FOO: \"\"\n"))

	t.Setenv("FOO", "from-the-shell")
	m := New(80, 24)
	m.deps = docker.DefaultDeps()
	reconstructed, err := m.deps.Stacks.ReconstructStackCompose(stackName)
	require.NoError(t, err)
	edited := regexp.MustCompile(`(?m)^(\s*)FOO: ""$`).ReplaceAllString(reconstructed, "${1}FOO: \"\"\n${1}EDITED: \"yes\"")
	require.NotEqual(t, reconstructed, edited, "the reconstruction must hold FOO empty")

	m.editStackName = stackName
	_, ok := firstOfType[stackDeployedMsg](runBatch(m.Update(editorContentMsg{Content: edited, OriginalContent: reconstructed})))
	require.True(t, ok, "the redeploy must succeed")

	cli, err := docker.GetClient()
	require.NoError(t, err)
	svc, _, err := cli.ServiceInspectWithRaw(context.Background(), stackName+"_app", swarm.ServiceInspectOptions{})
	require.NoError(t, err)
	env := svc.Spec.TaskTemplate.ContainerSpec.Env
	require.Contains(t, env, "EDITED=yes", "the edit must have been deployed")
	require.Contains(t, env, "FOO=")
	require.NotContains(t, env, "FOO=from-the-shell")
}
