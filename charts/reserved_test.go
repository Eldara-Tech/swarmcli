// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A config or secret a chart declares may not carry a com.swarmcli. label, in
// either shape compose accepts for labels, however the document spells it.
func TestCheckReservedRefusesAReservedLabel(t *testing.T) {
	for _, tc := range []struct{ name, manifest, want string }{
		{"config, mapping", "configs:\n  app:\n    file: ./a\n    labels:\n      com.swarmcli.type: release\n",
			"configs.app.labels: label 'com.swarmcli.type'"},
		{"config, list", "configs:\n  app:\n    file: ./a\n    labels:\n      - com.example=x\n      - com.swarmcli.release=web\n",
			"configs.app.labels: label 'com.swarmcli.release'"},
		{"config, list entry with no value", "configs:\n  app:\n    file: ./a\n    labels: [com.swarmcli.status]\n",
			"configs.app.labels: label 'com.swarmcli.status'"},
		{"secret, mapping", "secrets:\n  key:\n    file: ./k\n    labels:\n      com.swarmcli.owner: x\n",
			"secrets.key.labels: label 'com.swarmcli.owner'"},
		{"secret, list", "secrets:\n  key:\n    file: ./k\n    labels: [\"com.swarmcli.chart=x\"]\n",
			"secrets.key.labels: label 'com.swarmcli.chart'"},
		{"external", "configs:\n  app:\n    external: true\n    labels:\n      com.swarmcli.type: release\n",
			"configs.app.labels: label 'com.swarmcli.type'"},
		{"through an alias", "x-l: &l\n  com.swarmcli.type: release\nconfigs:\n  app:\n    file: ./a\n    labels: *l\n",
			"configs.app.labels: label 'com.swarmcli.type'"},
		{"through a merge key", "x-l: &l\n  com.swarmcli.type: release\nconfigs:\n  app:\n    file: ./a\n    labels:\n      <<: *l\n      tier: web\n",
			"configs.app.labels: label 'com.swarmcli.type'"},
		{"the first of two, in order", "configs:\n  b:\n    labels: {com.swarmcli.type: x}\n  a:\n    labels: {com.swarmcli.revision: x, com.swarmcli.created: x}\n",
			"configs.a.labels: label 'com.swarmcli.created'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckReserved(tc.manifest, "site")
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
			require.Contains(t, err.Error(), "rename or drop the label")
		})
	}
}

// A declared config or secret may not have a name on the swarm that starts
// like a release record's, whichever way compose arrives at that name.
func TestCheckReservedRefusesARecordName(t *testing.T) {
	for _, tc := range []struct{ name, stack, manifest, want string }{
		{"config name:", "site", "configs:\n  app:\n    file: ./a\n    name: swarmcli.release.web.v3\n",
			"configs.app: name 'swarmcli.release.web.v3'"},
		{"secret name:", "site", "secrets:\n  key:\n    file: ./k\n    name: swarmcli.release.web.v3\n",
			"secrets.key: name 'swarmcli.release.web.v3'"},
		{"external, by key", "site", "configs:\n  swarmcli.release.web.v1:\n    external: true\n",
			"configs.swarmcli.release.web.v1: name 'swarmcli.release.web.v1'"},
		{"external, by key, as a mapping", "site", "configs:\n  swarmcli.release.web.v1:\n    external: {}\n",
			"configs.swarmcli.release.web.v1: name 'swarmcli.release.web.v1'"},
		{"external, spelled as YAML 1.1", "site", "configs:\n  swarmcli.release.web.v1:\n    external: yes\n",
			"configs.swarmcli.release.web.v1: name 'swarmcli.release.web.v1'"},
		{"external, spelled as YAML 1.1, scoped", "swarmcli.release.team", "configs:\n  web.v2:\n    external: on\n",
			"configs.web.v2: name 'swarmcli.release.team_web.v2'"},
		{"external, by name:", "site", "configs:\n  app:\n    external: true\n    name: swarmcli.release.web.v1\n",
			"configs.app: name 'swarmcli.release.web.v1'"},
		{"external, by external.name", "site", "secrets:\n  app:\n    external:\n      name: swarmcli.release.web.v1\n",
			"secrets.app: name 'swarmcli.release.web.v1'"},
		{"in another case", "site", "configs:\n  app:\n    file: ./a\n    name: Swarmcli.Release.web.v3\n",
			"configs.app: name 'Swarmcli.Release.web.v3'"},
		{"scoped by a stack in another case", "SwarmCLI.Release.team", "secrets:\n  web.v2:\n    file: ./k\n",
			"secrets.web.v2: name 'SwarmCLI.Release.team_web.v2'"},
		{"scoped by the stack", "swarmcli.release.team", "configs:\n  web.v2:\n    file: ./a\n",
			"configs.web.v2: name 'swarmcli.release.team_web.v2'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckReserved(tc.manifest, tc.stack)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
			require.Contains(t, err.Error(), "rename it")
		})
	}
}

// What the refusals exist to leave alone: ordinary labels and names, the same
// labels anywhere but on a config or secret, and a name that only resembles the
// prefix.
func TestCheckReservedAllowsOrdinaryDeclarations(t *testing.T) {
	for _, tc := range []struct{ name, stack, manifest string }{
		{"empty", "site", ""},
		{"no declarations", "site", "services:\n  web:\n    image: nginx\n"},
		{"other labels", "site", "configs:\n  app:\n    file: ./a\n    labels:\n      com.example.tier: web\n      com.swarmcli: x\n      com.swarmclix.y: z\n"},
		{"list labels", "site", "secrets:\n  key:\n    file: ./k\n    labels: [\"com.example=x\", \"com.swarmcli=y\"]\n"},
		{"a service's labels", "site", "services:\n  web:\n    image: nginx\n    labels:\n      com.swarmcli.type: release\n    deploy:\n      labels:\n        com.swarmcli.type: release\n"},
		{"names that only resemble", "site", "configs:\n  a:\n    name: my.swarmcli.release.x\n  b:\n    external: true\n    name: swarmcli.releases\n  c:\n    name: swarmcli.release\n"},
		{"a stack named swarmcli", "swarmcli", "configs:\n  release.web.v1:\n    file: ./a\n"},
		{"not external, by key", "site", "configs:\n  swarmcli.release.web.v1:\n    external: false\n    file: ./a\n"},
		{"an external in a stack named like a record", "swarmcli.release.team", "configs:\n  app:\n    external: true\n"},
		{"an escaped dollar", "site", "configs:\n  app:\n    file: ./a\n    name: $${X}swarmcli.release.web.v1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, CheckReserved(tc.manifest, tc.stack))
		})
	}
}

// It decodes the manifest as EnvLookups does, so what that refuses — a document
// it cannot read as the CLI would, or a name the CLI would interpolate — is
// refused here too rather than read past.
func TestCheckReservedRefusesWhatDecodingRefuses(t *testing.T) {
	for _, tc := range []struct{ name, manifest, want string }{
		{"not YAML", "configs: [\n", "parse manifest"},
		{"a key that is not a string", "configs:\n  1:\n    file: ./b\n  app:\n    file: ./a\n    labels:\n      com.swarmcli.type: release\n",
			"key '1' is not a string"},
		{"an interpolated name", "configs:\n  app:\n    file: ./a\n    name: ${UNSET_IN_TEST:-swarmcli.release.web.v3}\n",
			"configs.app.name: the value has a '$' that is not escaped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, CheckReserved(tc.manifest, "site"), tc.want)
		})
	}
}

// Every chart deploy passes through the Docker backend's DeployStack, so the
// refusal is there whatever the caller checked first, and a rollback to a
// revision recorded before it is told to upgrade instead.
func TestDockerBackendRefusesAReservedDeclaration(t *testing.T) {
	ctx := context.Background()
	manifest := "services:\n  web:\n    image: nginx\nconfigs:\n  app:\n    file: files/app.conf\n    labels:\n      com.swarmcli.type: release\n"
	const want = "configs.app.labels: label 'com.swarmcli.type'"

	err := NewDockerBackend("no-such-context").DeployStack(ctx, DeployRequest{Name: "web", Manifest: manifest})
	require.ErrorContains(t, err, want)
	require.NotContains(t, err.Error(), "no-such-context")
	require.NotContains(t, err.Error(), "rolling back")

	fb := newFakeBackend()
	chart := ReleaseChart{Name: "c", Version: "1"}
	_, err = NewEngineWith(fb).Install(ctx, "web", chart, nil, manifest, InstallOptions{})
	require.NoError(t, err, "stored as a revision recorded before this check would be")
	_, err = NewEngineWith(realDeploy{fb}).Rollback(ctx, "web", 1, InstallOptions{})
	require.ErrorContains(t, err, want)
	require.ErrorContains(t, err, "upgrade to a chart version that passes this check instead of rolling back to it")

	_, err = NewEngineWith(realDeploy{newFakeBackend()}).Install(ctx, "web", chart, nil, manifest, InstallOptions{})
	require.ErrorContains(t, err, want)
	require.NotContains(t, err.Error(), "rolling back")
}
