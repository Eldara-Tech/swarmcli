// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build integration

package service

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/stretchr/testify/require"
)

// unpinnedStack declares the same volume mount several ways. Every service is
// scaled to zero: which volumes count is read off the spec `docker stack deploy`
// writes, so no task needs to run, and none leaves a volume on a node.
const unpinnedStack = `version: "3.9"
volumes:
  data:
  shared:
    driver_opts:
      type: nfs
      o: addr=127.0.0.1,ro
      device: ":/export"
  ext:
    external: true
    name: unpinned-ext
services:
  loose:
    image: alpine:latest
    volumes: [data:/data]
    deploy: {replicas: 0}
  pinned:
    image: alpine:latest
    volumes: [data:/data]
    deploy:
      replicas: 0
      placement: {constraints: ["node.hostname == %s"]}
  nfs:
    image: alpine:latest
    volumes: [shared:/data]
    deploy: {replicas: 0}
  external:
    image: alpine:latest
    volumes: [ext:/data]
    deploy: {replicas: 0}
`

// TestUnpinnedVolumes checks, through the real `docker stack deploy`, that the
// services view lists a local volume a service can move away from, and not one
// it is pinned to or one on network storage, and marks an external volume's
// unknown driver.
func TestUnpinnedVolumes(t *testing.T) {
	snap, err := docker.RefreshSnapshot()
	require.NoError(t, err)
	if len(snap.Nodes) < 2 {
		t.Skipf("needs a swarm of at least 2 nodes, have %d", len(snap.Nodes))
	}

	const stack = "unpinned"
	file := filepath.Join(t.TempDir(), "stack.yml")
	require.NoError(t, os.WriteFile(file, fmt.Appendf(nil, unpinnedStack, snap.Nodes[0].Description.Hostname), 0o600))
	var stderr bytes.Buffer
	deploy := exec.Command("docker", "stack", "deploy", "-c", file, stack)
	deploy.Stderr = &stderr
	require.NoError(t, deploy.Run(), stderr.String())
	t.Cleanup(func() {
		_ = exec.Command("docker", "stack", "rm", stack).Run()
		for range 30 {
			out, _ := exec.Command("docker", "stack", "services", stack, "--format", "{{.Name}}").Output()
			if len(bytes.TrimSpace(out)) == 0 {
				return
			}
			time.Sleep(time.Second)
		}
	})

	_, err = docker.RefreshSnapshot()
	require.NoError(t, err)
	got := map[string][]docker.UnpinnedVolume{}
	for _, e := range docker.LoadStackServices(stack) {
		got[e.ServiceName] = e.UnpinnedVolumes
	}
	require.Equal(t, map[string][]docker.UnpinnedVolume{
		stack + "_loose":    {{Name: "data"}},
		stack + "_pinned":   nil,
		stack + "_nfs":      nil,
		stack + "_external": {{Name: "unpinned-ext", DriverUnknown: true}},
	}, got)
}
