// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build integration_ssh

// Package sshcontext proves swarmcli can reach a daemon over an ssh:// Docker
// context (#449).
//
// It is gated behind its own `integration_ssh` build tag, not the regular
// `integration` one, because it stands up a throwaway dind running an sshd and
// rewrites HOME — neither of which should touch the shared multi-node swarm the
// rest of the suite uses. A separate CI job runs only this tag, mirroring
// integration-tests/swarmlock.
//
// The bug this guards: client.WithHost routes through
// go-connections/sockets.ConfigureTransport, which special-cases only unix and
// npipe, so an ssh:// host was TCP-dialled and the ping failed. The fix resolves
// a connection helper and dials `ssh … docker system dial-stdio` instead.
//
// HOME is redirected to a temp dir so the Docker context store is under test
// control. That is NOT enough to redirect the ssh client config: OpenSSH
// resolves ~/.ssh/config through getpwuid(), not $HOME. An `ssh` shim on PATH
// supplies -F instead — see shimSSH.
//
// Either way the run exercises ssh_config handling, which is the parity with
// `docker --context` that justified depending on docker/cli's connhelper rather
// than hand-rolling the dialer.
package sshcontext

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
)

const (
	dindImage     = "docker:29-dind"
	containerName = "swarmcli-ssh-dind"
	sshHostPort   = "22222"
	sshContext    = "swarmcli-ssh"
)

func hostDocker(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("docker", append([]string{"--context", "default"}, args...)...).CombinedOutput()
	return string(out), err
}

func inner(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return hostDocker(t, append([]string{"exec", containerName}, args...)...)
}

// isolateHome points HOME at a temp dir so the ssh config written below and the
// Docker context created later are both scoped to this test.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	return home
}

// shimSSH puts an `ssh` wrapper at the front of PATH that adds -F <config>.
//
// This is necessary, not decorative: OpenSSH resolves the default config path
// through getpwuid(), NOT $HOME, so redirecting HOME does not redirect
// ~/.ssh/config. Without the shim the run ignores our config entirely — no
// port, no identity, no host-key policy — and fails with "Host key verification
// failed" against whatever is listening on port 22.
//
// The alternative was appending to the developer's real ~/.ssh/config, which
// this test declines to do. The fidelity cost is small and worth naming: the
// options reach ssh via -F rather than by being found at the default path, so
// this proves connhelper honours ssh_config, not that it finds the user's file.
func shimSSH(t *testing.T, home, cfg string) {
	t.Helper()
	realSSH, err := exec.LookPath("ssh")
	require.NoError(t, err)

	bin := filepath.Join(home, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	// Absolute path to the real ssh, so the shim cannot recurse into itself.
	script := "#!/bin/sh\nexec " + realSSH + " -F " + cfg + " \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755))

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// writeSSHConfig generates a keypair and an ssh config for the throwaway host,
// returning the public key and the config path.
//
// The config is what makes this a real test of connhelper: GetConnectionHelper
// shells out to the system ssh with no options of its own, so the identity file,
// port and host-key policy can only come from ssh_config. A hand-rolled dialer
// would have ignored all of it.
func writeSSHConfig(t *testing.T, home string) (string, string) {
	t.Helper()
	key := filepath.Join(home, ".ssh", "id_ed25519")
	out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput()
	require.NoErrorf(t, err, "ssh-keygen: %s", out)

	cfg := strings.Join([]string{
		"Host localhost",
		"  User root",
		"  Port " + sshHostPort,
		"  IdentityFile " + key,
		"  IdentitiesOnly yes",
		"  StrictHostKeyChecking no",
		"  UserKnownHostsFile /dev/null",
		"  LogLevel ERROR",
		"",
	}, "\n")
	cfgPath := filepath.Join(home, ".ssh", "config")
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o600))

	pub, err := os.ReadFile(key + ".pub")
	require.NoError(t, err)
	return strings.TrimSpace(string(pub)), cfgPath
}

// startSSHDind brings up a dind daemon with an sshd in front of it and waits
// until `ssh … docker version` actually answers — the same path the connection
// helper will take.
func startSSHDind(t *testing.T, pubKey string) {
	t.Helper()

	_, _ = hostDocker(t, "rm", "-f", containerName)
	t.Cleanup(func() { _, _ = hostDocker(t, "rm", "-f", containerName) })

	out, err := hostDocker(t, "run", "-d", "--privileged",
		"-e", "DOCKER_TLS_CERTDIR=",
		"-p", sshHostPort+":22",
		"--name", containerName, dindImage)
	require.NoErrorf(t, err, "starting dind: %s", out)

	// Wait for the inner daemon before installing anything, so apk has a working
	// container and the error below means "sshd setup failed", not "too early".
	deadline := time.Now().Add(90 * time.Second)
	for {
		if _, err := inner(t, "docker", "version"); err == nil {
			break
		}
		require.Truef(t, time.Now().Before(deadline), "inner daemon never became ready")
		time.Sleep(time.Second)
	}

	setup := strings.Join([]string{
		// --upgrade with the client: the dind image ships openssh-client at the
		// release it was built with, and the server package requires the client
		// at its own. Once Alpine moves on (10.3_p1-r1 to -r2 broke it), a plain
		// add refuses: "openssh-client-common-…-r1: breaks: openssh-client-default-…-r2".
		"apk add --no-cache --upgrade openssh-server openssh-client-default",
		"ssh-keygen -A",
		"mkdir -p /root/.ssh",
		"printf '%s\\n' \"" + pubKey + "\" > /root/.ssh/authorized_keys",
		"chmod 700 /root/.ssh && chmod 600 /root/.ssh/authorized_keys",
		// Explicit rather than relying on Alpine's commented-out default.
		"echo 'PermitRootLogin prohibit-password' >> /etc/ssh/sshd_config",
		// The docker CLI lives in /usr/local/bin, which is NOT on sshd's default
		// PATH for a non-interactive session — and `ssh host -- command` is
		// exactly that. connhelper runs `ssh … -- docker system dial-stdio`, so
		// without this the feature under test cannot work, not just the probe.
		// /etc/profile would not help: it is only read by login shells.
		"[ -x /usr/bin/docker ] || ln -s \"$(command -v docker)\" /usr/bin/docker",
		"/usr/sbin/sshd",
	}, " && ")
	out, err = inner(t, "sh", "-c", setup)
	require.NoErrorf(t, err, "provisioning sshd: %s", out)

	// A swarm, so there is real state to read back over the tunnel.
	out, err = inner(t, "docker", "swarm", "init", "--advertise-addr", "eth0")
	require.NoErrorf(t, err, "swarm init: %s", out)

	// Poll the actual transport: ssh in and run the very command the connection
	// helper runs. sshd takes a moment to bind after being started.
	// Keep the last failure so a timeout reports WHY ssh never worked rather
	// than just that it did not.
	var lastOut string
	var lastErr error
	deadline = time.Now().Add(60 * time.Second)
	for {
		probe := exec.Command("ssh", "-o", "BatchMode=yes", "localhost", "docker", "version", "--format", "{{.Server.Version}}")
		out, err := probe.CombinedOutput()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			return
		}
		lastOut, lastErr = string(out), err
		require.Truef(t, time.Now().Before(deadline),
			"ssh to the dind never became usable: %v\n%s", lastErr, lastOut)
		time.Sleep(time.Second)
	}
}

// The whole point: a context whose host is ssh:// must produce a working client.
// Before the fix this failed at the ping, because the SDK TCP-dialled the URL.
func TestSSHContextReachesTheDaemon(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh client not available")
	}

	home := isolateHome(t)
	pub, cfg := writeSSHConfig(t, home)
	shimSSH(t, home, cfg)
	startSSHDind(t, pub)

	out, err := hostDocker(t, "context", "create", sshContext,
		"--docker", "host=ssh://root@localhost:"+sshHostPort)
	require.NoErrorf(t, err, "creating ssh context: %s", out)
	t.Cleanup(func() {
		docker.ResetClient()
		_ = exec.Command("docker", "context", "rm", "-f", sshContext).Run()
	})

	// ClientFor pings, so a successful return IS the assertion that the transport
	// works — that ping is exactly what failed before the fix.
	cli, err := docker.ClientFor(sshContext)
	require.NoError(t, err, "ClientFor over ssh:// must succeed")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// And a real API call must return this daemon's state, not something cached
	// from an ambient context.
	info, err := cli.Info(ctx)
	require.NoError(t, err)
	require.True(t, info.Swarm.ControlAvailable, "should be talking to the dind's swarm manager")
}

// A ssh:// host that cannot be reached must fail as a connection error rather
// than hanging or reporting something unrelated to ssh.
func TestUnreachableSSHHostFailsCleanly(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh client not available")
	}

	home := isolateHome(t)
	_, cfg := writeSSHConfig(t, home)
	shimSSH(t, home, cfg)

	const badContext = "swarmcli-ssh-unreachable"
	out, err := hostDocker(t, "context", "create", badContext,
		// Port 1 has nothing on it; ConnectTimeout comes from connhelper.
		"--docker", "host=ssh://root@127.0.0.1:1")
	require.NoErrorf(t, err, "creating context: %s", out)
	t.Cleanup(func() {
		docker.ResetClient()
		_ = exec.Command("docker", "context", "rm", "-f", badContext).Run()
	})

	done := make(chan error, 1)
	go func() {
		_, err := docker.ClientFor(badContext)
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err, "an unreachable ssh host must not report success")
	case <-time.After(90 * time.Second):
		t.Fatal("ClientFor hung on an unreachable ssh host")
	}
}
