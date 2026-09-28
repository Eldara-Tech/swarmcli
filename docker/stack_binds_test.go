// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// bindManifest is one service with one volumes: entry, so a refusal can only
// have come from the entry under test.
func bindManifest(volumes string) string {
	return "services:\n  app:\n    image: nginx\n    volumes: " + volumes + "\n"
}

// The refusal, in every way a manifest can write a source the loader would have
// resolved against swarmcli's temp directory.
//
// The cases are the shapes rather than a sample of paths: both syntaxes, both
// types that name a host path, and the three prefixes compose infers a bind from
// — `.`, `~` and a drive letter. A relative source is unbindable for the same
// reason in all of them, and covering one proves nothing about the rest.
func TestCheckBindSourcesRefusesASourceThatIsNotAbsolute(t *testing.T) {
	for _, tc := range []struct{ name, volumes, source string }{
		{"a relative path", `["./data:/data"]`, "./data"},
		{"one that climbs", `["../data:/data"]`, "../data"},
		{"read only, like a config would be", `["./nginx.conf:/etc/nginx/nginx.conf:ro"]`, "./nginx.conf"},
		// Expanded by the loader against the HOME of whoever ran swarmcli, so it
		// is absolute by the time anything downstream could object, and names a
		// directory on a machine that does not run the task.
		{"the operator's home", `["~/data:/data"]`, "~/data"},
		{"long syntax", `[{type: bind, source: ./data, target: /data}]`, "./data"},
		// The type that is not a bind and names a host path anyway.
		{"long syntax as a named pipe", `[{type: npipe, source: pipe, target: /data}]`, "pipe"},
		// A drive letter without a separator is relative to that drive's current
		// directory, which is why the loader resolves it like any other.
		{"a drive-relative Windows path", `["C:data:/data"]`, "C:data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkBindSources(bindManifest(tc.volumes))
			require.Error(t, err, "want a source that is not absolute refused")
			require.ErrorContains(t, err, `service 'app'`)
			require.ErrorContains(t, err, tc.source)
			require.ErrorContains(t, err, "must be absolute")
		})
	}
}

// What still deploys. Every entry here either names a path on the node or names
// no host path at all, and the two are told apart by the docker CLI's own parser
// — which is the point of using it: `my.data` is a legal volume name, and a
// guess that "it contains a dot, so it looks like a path" refuses a chart that
// was never wrong.
func TestCheckBindSourcesAcceptsWhatNamesTheNode(t *testing.T) {
	for _, tc := range []struct{ name, volumes string }{
		{"an absolute path", `["/srv/data:/data"]`},
		{"with options", `["/srv/data:/data:ro"]`},
		{"long syntax", `[{type: bind, source: /srv/data, target: /data}]`},
		{"a named volume", `["data:/data"]`},
		{"a named volume with a dot in it", `["my.data:/data"]`},
		{"a named volume, long syntax", `[{type: volume, source: data, target: /data}]`},
		{"an anonymous volume", `["/data"]`},
		{"tmpfs, which has no source", `[{type: tmpfs, target: /tmp}]`},
		{"a Windows path, from whatever OS swarmcli runs on", `['C:\data:/data']`},
		{"a Windows named pipe", `['\\.\pipe\docker_engine:\\.\pipe\docker_engine']`},
		{"a Windows named pipe, long syntax", `[{type: npipe, source: '\\.\pipe\docker_engine', target: '\\.\pipe\docker_engine'}]`},
		{"no volumes at all", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := "services:\n  app:\n    image: nginx\n"
			if tc.volumes != "" {
				manifest = bindManifest(tc.volumes)
			}
			require.NoError(t, checkBindSources(manifest))
		})
	}
}

// The classification itself, which is what the accept table above rests on: an
// entry accepted because it was never seen as a bind proves nothing about the
// guard. These pairs were checked against docker/cli v28.5.1's own parser
// (loader.ParseVolume over internal/volumespec) and agree with it on every spec
// but one — `./a:`, which volumespec rejects outright as an empty section
// between colons, and which is refused here as a relative source instead. Both
// refuse; only the wording differs, so the divergence costs a message and not a
// deploy.
func TestShortSyntaxBindSourceMirrorsCompose(t *testing.T) {
	for _, tc := range []struct {
		spec   string
		source string
		bind   bool
	}{
		{"./data:/d", "./data", true},
		{"~/data:/d", "~/data", true},
		{"/srv:/d", "/srv", true},
		{"/srv/data:/d:ro", "/srv/data", true},
		// A volume name, whatever punctuation it carries: compose reads the
		// source's first character and nothing else.
		{"data:/d", "", false},
		{"my.data:/d", "", false},
		{"vol.1.2:/d", "", false},
		// An anonymous volume — a container path, no host side.
		{"/data", "", false},
		// The drive letter's colon is not a separator.
		{`C:\data:/d`, `C:\data`, true},
		{"C:data:/d", "C:data", true},
		{`\\.\pipe\x:\\.\pipe\y`, `\\.\pipe\x`, true},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			source, bind := shortSyntaxBindSource(tc.spec)
			require.Equal(t, tc.bind, bind)
			require.Equal(t, tc.source, source)
		})
	}
}

// One malformed service must not hide the others. yaml.v3 returns a mapping with
// a single non-string key as map[any]any for the whole mapping, so a guard that
// type-asserts map[string]any sees no services at all here, reports nothing
// wrong, and lets the deploy through — while docker normalises the key and
// deploys the entry.
func TestCheckBindSourcesSeesEverySiblingService(t *testing.T) {
	err := checkBindSources("services:\n" +
		"  1:\n    image: nginx\n" +
		"  app:\n    image: nginx\n    volumes: [\"./data:/data\"]\n")
	require.ErrorContains(t, err, `service 'app'`)
}

// A volumes block, or an entry in one, reached through an alias is read like any
// other: the loader resolves the alias before it resolves the source.
func TestCheckBindSourcesFollowsAliases(t *testing.T) {
	for name, manifest := range map[string]string{
		"an aliased block":      "x-v: &v [\"~/data:/data\"]\nservices:\n  app:\n    image: nginx\n    volumes: *v\n",
		"an aliased short item": "x-b: &b \"~/data:/data\"\nservices:\n  app:\n    image: nginx\n    volumes: [*b]\n",
		"an aliased long item":  "x-m: &m {type: bind, source: ./data, target: /data}\nservices:\n  app:\n    image: nginx\n    volumes: [*m]\n",
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, checkBindSources(manifest), "must be absolute")
		})
	}
}

// A manifest with more than one offender always refuses on the same one, so the
// message an operator fixes against does not depend on map iteration order.
func TestCheckBindSourcesRefusesTheFirstServiceInOrder(t *testing.T) {
	manifest := "services:\n" +
		"  zebra:\n    image: nginx\n    volumes: [\"./z:/z\"]\n" +
		"  alpha:\n    image: nginx\n    volumes: [\"./a:/a\"]\n"
	for range 5 {
		require.ErrorContains(t, checkBindSources(manifest), `service 'alpha'`)
	}
}

// And the refusal is reached by a deploy, before the manifest is written and
// before `docker` is executed: the context named here does not exist, so any
// error about it is an error that ran too late.
func TestDeployStackInContextRefusesARelativeBindSource(t *testing.T) {
	err := DeployStackInContext(context.Background(), "no-such-context", "web",
		bindManifest(`["./data:/data"]`), ResolveImageDefault, nil, DeployOptions{})
	require.ErrorContains(t, err, "bind source")
	require.ErrorContains(t, err, "must be absolute")
}
