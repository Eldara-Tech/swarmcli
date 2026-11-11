// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The regression this feature exists for: `--set 'extraNetworks[0]=x'` used to be
// accepted and then silently do nothing — it landed as a key literally named
// "extraNetworks[0]" that no template reads, so the install succeeded and the
// operator debugged the wrong thing. It must now actually build the list.
func TestMergeValuesSetListIndex(t *testing.T) {
	got, err := MergeValues(map[string]any{}, nil, []string{
		"extraNetworks[0]=ollama-net",
		"extraNetworks[1]=mail-net",
	})
	require.NoError(t, err)
	require.Equal(t, []any{"ollama-net", "mail-net"}, got["extraNetworks"])
	require.NotContains(t, got, "extraNetworks[0]", "must not leak a bracketed key")
}

// Helm's list literal, the ergonomic form for a list of scalars.
func TestMergeValuesSetListLiteral(t *testing.T) {
	got, err := MergeValues(map[string]any{}, nil, []string{"extraNetworks={ollama-net,mail-net}"})
	require.NoError(t, err)
	require.Equal(t, []any{"ollama-net", "mail-net"}, got["extraNetworks"])

	// A comma inside the expression must not be mistaken for a --set separator.
	got, err = MergeValues(map[string]any{}, nil, []string{"a={x,y},b=1"})
	require.NoError(t, err)
	require.Equal(t, []any{"x", "y"}, got["a"])
	require.Equal(t, 1, got["b"])

	empty, err := MergeValues(map[string]any{}, nil, []string{"a={}"})
	require.NoError(t, err)
	require.Equal(t, []any{}, empty["a"])
}

// Elements are type-inferred, and a list replaces a default rather than merging.
func TestMergeValuesSetListReplacesDefault(t *testing.T) {
	defaults := map[string]any{"extraNetworks": []any{"stale-net", "other"}}
	got, err := MergeValues(defaults, nil, []string{"extraNetworks={only-net}"})
	require.NoError(t, err)
	require.Equal(t, []any{"only-net"}, got["extraNetworks"])
	require.Equal(t, []any{"stale-net", "other"}, defaults["extraNetworks"], "defaults must not be mutated")

	mixed, err := MergeValues(map[string]any{}, nil, []string{"a={1,true,x}"})
	require.NoError(t, err)
	require.Equal(t, []any{1, true, "x"}, mixed["a"])
}

// Indexing into a list of maps, Helm-style.
func TestMergeValuesSetListOfMaps(t *testing.T) {
	got, err := MergeValues(map[string]any{}, nil, []string{"servers[0].port=80", "servers[0].host=a", "servers[1].port=443"})
	require.NoError(t, err)
	require.Equal(t, []any{
		map[string]any{"port": 80, "host": "a"},
		map[string]any{"port": 443},
	}, got["servers"])
}

// A sparse index pads with nil rather than failing.
func TestMergeValuesSetListSparseIndex(t *testing.T) {
	got, err := MergeValues(map[string]any{}, nil, []string{"a[2]=x"})
	require.NoError(t, err)
	require.Equal(t, []any{nil, nil, "x"}, got["a"])
}

// Escaped commas survive both the --set split and the list split.
func TestMergeValuesSetEscapedCommas(t *testing.T) {
	got, err := MergeValues(map[string]any{}, nil, []string{`a=x\,y`})
	require.NoError(t, err)
	require.Equal(t, "x,y", got["a"])

	lst, err := MergeValues(map[string]any{}, nil, []string{`a={x\,y,z}`})
	require.NoError(t, err)
	require.Equal(t, []any{"x,y", "z"}, lst["a"])
}

// A malformed path must fail loudly — never land as an unread bracketed key.
func TestMergeValuesSetListErrors(t *testing.T) {
	for _, expr := range []string{
		"a[x]=1",  // non-numeric index
		"a[-1]=1", // negative index
		"a[0=1",   // unbalanced '['
		"a]0]=1",  // unbalanced ']'
		"[0]=1",   // index with no key
		"a..b=1",  // empty segment
		"a[0]x=1", // trailing junk after an index
	} {
		_, err := MergeValues(map[string]any{}, nil, []string{expr})
		require.Error(t, err, "--set %q must be rejected, not silently ignored", expr)
		require.Contains(t, err.Error(), "--set")
	}
}

// --- --set-file (#537) ---

// The whole point of a separate flag: --set applies inference and comma
// splitting, and both destroy a file. A config.js is full of commas and a
// one-line file may read "true".
func TestApplySetFilesTakesTheContentVerbatim(t *testing.T) {
	const js = "module.exports = { a: 1, b: [2, 3] };\n"
	dst := map[string]any{}
	require.NoError(t, ApplySetFiles(dst, []SetFile{
		{Key: "config", Data: []byte(js)},
		{Key: "flag", Data: []byte("true")},
	}))
	require.Equal(t, js, dst["config"])
	require.Equal(t, "true", dst["flag"], "a file reading \"true\" is a string, not a bool")
}

func TestApplySetFilesWritesANestedKey(t *testing.T) {
	dst := map[string]any{"renovate": map[string]any{"image": "renovate/renovate"}}
	require.NoError(t, ApplySetFiles(dst, []SetFile{{Key: "renovate.config", Data: []byte("x")}}))
	require.Equal(t, map[string]any{"image": "renovate/renovate", "config": "x"}, dst["renovate"])
}

// --set-file is applied after MergeValues, so it wins over a values file and a
// --set naming the same key.
func TestApplySetFilesOverrideOrder(t *testing.T) {
	dst, err := MergeValues(map[string]any{"config": "from defaults"}, nil, []string{"config=from --set"})
	require.NoError(t, err)
	require.NoError(t, ApplySetFiles(dst, []SetFile{{Key: "config", Data: []byte("from --set-file")}}))
	require.Equal(t, "from --set-file", dst["config"])
}

func TestApplySetFilesRejectsAMalformedKey(t *testing.T) {
	for _, key := range []string{"", "a..b", "a[x]", "[0]"} {
		require.Error(t, ApplySetFiles(map[string]any{}, []SetFile{{Key: key, Data: []byte("x")}}),
			"--set-file %q must be rejected", key)
	}
}

// lookupPath is the inverse of setPath, and every path setPath writes it must
// read back — that pairing is what lets a manifest's values/<key> resolve
// against the effective values, whatever put them there.
func TestLookupPathRoundTripsSetPath(t *testing.T) {
	for _, path := range []string{"a", "a.b.c", "a[0]", "a.b[1].c"} {
		steps, err := parseSetPath(path)
		require.NoError(t, err)
		dst := map[string]any{}
		setPath(dst, steps, "x")
		got, ok := lookupPath(dst, steps)
		require.True(t, ok, "lookupPath must find what setPath wrote at %q", path)
		require.Equal(t, "x", got)
	}
}

func TestLookupPathReportsAbsence(t *testing.T) {
	values := map[string]any{"a": map[string]any{"b": "x"}, "list": []any{"one"}}
	for _, path := range []string{
		"missing",   // no such key
		"a.missing", // no such key under an existing map
		"a.b.c",     // hop into a scalar
		"list[1]",   // index past the end
		"a[0]",      // index into a map
		"list.key",  // key of a list
	} {
		steps, err := parseSetPath(path)
		require.NoError(t, err)
		_, ok := lookupPath(values, steps)
		require.False(t, ok, "%q names no value", path)
	}
}

// A key the new chart version adds keeps its default, a key the previous
// release had keeps its previous value — nested keys too — and neither input
// is mutated (#687).
func TestReuseValuesLayersThePreviousReleaseOverTheDefaults(t *testing.T) {
	defaults := map[string]any{
		"replicas": 1,
		"image":    map[string]any{"repo": "nginx", "tag": "1.27"},
		"metrics":  map[string]any{"enabled": false, "port": 9104},
	}
	previous := map[string]any{"replicas": 3, "image": map[string]any{"tag": "1.25"}}

	got := ReuseValues(defaults, previous)
	require.Equal(t, map[string]any{
		"replicas": 3,
		"image":    map[string]any{"repo": "nginx", "tag": "1.25"},
		"metrics":  map[string]any{"enabled": false, "port": 9104},
	}, got)

	got["metrics"].(map[string]any)["enabled"] = true
	got["image"].(map[string]any)["tag"] = "x"
	require.Equal(t, false, defaults["metrics"].(map[string]any)["enabled"])
	require.Equal(t, "1.27", defaults["image"].(map[string]any)["tag"])
	require.Equal(t, map[string]any{"replicas": 3, "image": map[string]any{"tag": "1.25"}}, previous)

	require.Equal(t, defaults, ReuseValues(defaults, nil), "no previous release: the defaults alone")
}
