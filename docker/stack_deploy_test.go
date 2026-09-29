// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The stack commands shell out, so a cancelled context is the only thing that
// can reach the running `docker` process — and what comes back has to say so.
// Left to itself the dead child reports "signal: killed", which is
// indistinguishable from a daemon that rejected the deploy; a controller being
// shut down would record a failed sync for work it cancelled itself.
func TestStackCommandsReportCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := DeployStackInContext(ctx, "no-such-context", "web", "services:\n  a:\n    image: x\n", ResolveImageDefault, nil, DeployOptions{})
	require.ErrorIs(t, err, context.Canceled)

	require.ErrorIs(t, RemoveStackCLIInContext(ctx, "no-such-context", "web"), context.Canceled)
}

const testManifest = "services:\n  a:\n    image: x\n"

// The layout is the feature: the docker CLI resolves a manifest's file: keys
// against the directory it was handed, so a chart file has to land at exactly
// the relative path the manifest names, parents and all.
func TestWriteStackTreeLaysTheChartOutBesideTheManifest(t *testing.T) {
	dir, manifestPath, err := writeStackTree(map[string][]byte{
		"files/nginx.conf": []byte("server {}"),
		"files/tls/ca.pem": []byte("-----BEGIN-----"),
	}, testManifest)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	require.Equal(t, filepath.Join(dir, "stack.yml"), manifestPath)
	requireFile(t, manifestPath, testManifest)
	requireFile(t, filepath.Join(dir, "files", "nginx.conf"), "server {}")
	requireFile(t, filepath.Join(dir, "files", "tls", "ca.pem"), "-----BEGIN-----")

	requireMode(t, dir, 0o700)
	requireMode(t, filepath.Join(dir, "files"), 0o700)
	requireMode(t, filepath.Join(dir, "files", "tls"), 0o700)

	// And the tree is the unit of cleanup: one call has to take all of it.
	require.NoError(t, os.RemoveAll(dir))
	require.NoDirExists(t, dir)
}

// A manifest naming no files still needs somewhere to be, and nothing else may
// appear beside it — every chart without a files/ directory takes this path.
func TestWriteStackTreeWithNoFilesWritesOnlyTheManifest(t *testing.T) {
	for _, files := range []map[string][]byte{nil, {}} {
		dir, manifestPath, err := writeStackTree(files, testManifest)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })

		requireFile(t, manifestPath, testManifest)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, "stack.yml", entries[0].Name())
	}
}

// The caller resolved these keys against the chart already. This is the second
// check, on the only code that acts on them: a key that escapes here is a write
// outside a temporary directory as the operator, so it is refused by name and
// the half-built tree goes with it.
func TestWriteStackTreeRefusesAKeyThatEscapes(t *testing.T) {
	for name, key := range map[string]string{
		"absolute":     "/etc/cron.d/pwn",
		"parent":       "../../etc/cron.d/pwn",
		"parent below": "files/../../../etc/cron.d/pwn",
	} {
		t.Run(name, func(t *testing.T) {
			dir, manifestPath, err := writeStackTree(map[string][]byte{key: []byte("x")}, testManifest)
			require.ErrorContains(t, err, key)
			require.Empty(t, dir)
			require.Empty(t, manifestPath)
		})
	}
}

// Two keys that name one file must not leave the second's bytes where the
// first's were checked. Here they differ by a "." segment, which every
// filesystem resolves to one path.
func TestWriteStackTreeRefusesTwoKeysForOneFile(t *testing.T) {
	dir, manifestPath, err := writeStackTree(map[string][]byte{
		"files/a.env":   []byte("A=1\n"),
		"files/./a.env": []byte("B=2\n"),
	}, testManifest)
	require.ErrorContains(t, err, "names the same file as another chart file")
	require.Empty(t, dir)
	require.Empty(t, manifestPath)
}

// Here they differ only in case, which names one file on a case-insensitive
// filesystem such as the macOS and Windows defaults.
func TestWriteStackTreeRefusesKeysThatDifferOnlyInCase(t *testing.T) {
	probe := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(probe, "X"), nil, 0o600))
	if _, err := os.Stat(filepath.Join(probe, "x")); err != nil {
		t.Skip("the temporary directory is case-sensitive")
	}
	t.Setenv("TMPDIR", probe)
	_, _, err := writeStackTree(map[string][]byte{
		"files/A.env": []byte("A=1\n"),
		"files/a.env": []byte("B=2\n"),
	}, testManifest)
	require.ErrorContains(t, err, "names the same file as another chart file")
}

// A failed deploy must take the whole tree with it, not just the manifest the
// old temp-file version knew about — otherwise every failure leaks a chart's
// files, readable to nobody but still there.
func TestDeployStackRemovesTheWholeTree(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	// No networks: key, so the failure path does not go looking for orphaned
	// networks to clean up through a daemon this test has no business reaching.
	err := DeployStackInContext(context.Background(), "no-such-context", "web", testManifest,
		ResolveImageDefault, map[string][]byte{"files/nginx.conf": []byte("server {}")}, DeployOptions{})
	require.Error(t, err)

	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func requireFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, want, string(got))
	requireMode(t, path, 0o600)
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, want, info.Mode().Perm(), "%s", path)
}
