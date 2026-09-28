// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"path"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// chartFiles is the chart every test below resolves against: one file at the
// top of files/ and one nested, so the recursive shape is always available.
func chartFiles() map[string][]byte {
	return map[string][]byte{
		"files/nginx.conf":  []byte("server { listen 80; }"),
		"files/tls/ca.pem":  []byte("-----BEGIN CERTIFICATE-----"),
		"files/unused.conf": []byte("nothing references this"),
	}
}

func quoted(p string) string { return strconv.Quote(p) }

// The three manifest shapes, one per key that reads a path. Each names exactly
// one path so a refusal can only have come from the key under test.
func configManifest(p string) string {
	return "services:\n  web:\n    image: nginx\nconfigs:\n  site:\n    file: " + quoted(p) + "\n"
}

func secretManifest(p string) string {
	return "services:\n  web:\n    image: nginx\nsecrets:\n  site:\n    file: " + quoted(p) + "\n"
}

func envFileManifest(p string) string {
	return "services:\n  web:\n    image: nginx\n    env_file: " + quoted(p) + "\n"
}

// TestResolveManifestFilesRefusesEveryShapeOnEveryKey is the specification: the
// guard covers three keys, and a test that covers one proves nothing about the
// other two — swarmcli-cd#99 was one key, and this is the same defect three
// times. Dropping any single key from manifestFileRefs fails four subtests.
func TestResolveManifestFilesRefusesEveryShapeOnEveryKey(t *testing.T) {
	keys := []struct {
		name     string
		manifest func(string) string
		errKey   string
	}{
		{"configs.file", configManifest, "configs.site.file"},
		{"secrets.file", secretManifest, "secrets.site.file"},
		{"services.env_file", envFileManifest, "services.web.env_file"},
	}
	shapes := []struct {
		name  string
		path  string
		wants []string
	}{
		{
			name: "absolute",
			path: "/etc/shadow",
			// The absolute case is the one with a working chart behind it, so
			// its message is the entire migration path (R-4): the rule, and the
			// operator-managed replacement by name.
			wants: []string{
				`'/etc/shadow'`, "absolute path", "files/",
				"external: true", "docker config create", "docker secret create",
			},
		},
		{
			name:  "escapes the chart",
			path:  "../../etc/shadow",
			wants: []string{`'../../etc/shadow'`, "escapes the chart", "files/"},
		},
		{
			name:  "well-formed but absent",
			path:  "files/missing.conf",
			wants: []string{`'files/missing.conf'`, "not in the chart", "files/"},
		},
		{
			// Every other check reads the path as written, and the docker CLI
			// reads it only after substituting variables in it.
			name:  "contains a variable",
			path:  "files/${X}/app.conf",
			wants: []string{`'files/${X}/app.conf'`, "contains '$'", "files/"},
		},
		{
			// The likeliest author mistake, so the message has to say files/ and
			// show the corrected path.
			name:  "outside files/",
			path:  "nginx.conf",
			wants: []string{`'nginx.conf'`, "outside files/", `'files/nginx.conf'`},
		},
	}
	for _, k := range keys {
		for _, s := range shapes {
			t.Run(k.name+"/"+s.name, func(t *testing.T) {
				got, err := ResolveManifestFiles(k.manifest(s.path), chartFiles(), nil)
				require.Nil(t, got)
				require.Error(t, err)
				require.Contains(t, err.Error(), k.errKey, "the error must name the offending key")
				for _, want := range s.wants {
					require.Contains(t, err.Error(), want)
				}
			})
		}
	}
}

func TestResolveManifestFilesEscapesFromInsideFiles(t *testing.T) {
	// files/../../etc/shadow cleans to ../etc/shadow: it leaves the chart root
	// even though it started under files/, which is what path.Clean is for.
	_, err := ResolveManifestFiles(configManifest("files/../../etc/shadow"), chartFiles(), nil)
	require.ErrorContains(t, err, `'files/../../etc/shadow' escapes the chart`)

	// files/../nginx.conf stays inside the chart but leaves files/, so it is the
	// outside-files/ refusal and not the escape one.
	_, err = ResolveManifestFiles(configManifest("files/../nginx.conf"), chartFiles(), nil)
	require.ErrorContains(t, err, "is outside files/")
}

func TestResolveManifestFilesResolvesWhatTheManifestNames(t *testing.T) {
	got, err := ResolveManifestFiles(configManifest("files/nginx.conf"), chartFiles(), nil)
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{"files/nginx.conf": []byte("server { listen 80; }")}, got)
}

func TestResolveManifestFilesResolvesANestedFile(t *testing.T) {
	got, err := ResolveManifestFiles(secretManifest("files/tls/ca.pem"), chartFiles(), nil)
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{"files/tls/ca.pem": []byte("-----BEGIN CERTIFICATE-----")}, got)
}

func TestResolveManifestFilesNormalisesTheKey(t *testing.T) {
	// ./files/nginx.conf resolves to the same file the docker CLI would read, so
	// it comes back under the key Chart.Files uses — the key the materialiser
	// writes to.
	got, err := ResolveManifestFiles(configManifest("./files/nginx.conf"), chartFiles(), nil)
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{"files/nginx.conf": []byte("server { listen 80; }")}, got)
}

func TestResolveManifestFilesReturnsOnlyTheReferencedSubset(t *testing.T) {
	manifest := "services:\n" +
		"  web:\n    image: nginx\n    env_file: files/tls/ca.pem\n" +
		"configs:\n  site:\n    file: files/nginx.conf\n"
	got, err := ResolveManifestFiles(manifest, chartFiles(), nil)
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{
		"files/nginx.conf": []byte("server { listen 80; }"),
		"files/tls/ca.pem": []byte("-----BEGIN CERTIFICATE-----"),
	}, got)
	require.NotContains(t, got, "files/unused.conf")
}

func TestResolveManifestFilesReturnsNilWhenNothingIsNamed(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
	}{
		{"no configs, secrets or env_file", "services:\n  web:\n    image: nginx\n"},
		{"external config carries no file", "configs:\n  site:\n    external: true\n"},
		{"driver-backed secret carries no file", "secrets:\n  s:\n    driver: vault\n"},
		{"empty manifest", ""},
		{"services entry is not a map", "services:\n  web: nginx\n"},
		{"services block is a list", "services:\n  - web\n"},
		{"configs entry is not a map", "configs:\n  site: true\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveManifestFiles(tc.manifest, chartFiles(), nil)
			require.NoError(t, err)
			require.Nil(t, got, "a manifest naming no file must yield nil, not an empty map")
		})
	}
}

func TestResolveManifestFilesReadsBothEnvFileShapes(t *testing.T) {
	t.Run("bare string", func(t *testing.T) {
		got, err := ResolveManifestFiles(envFileManifest("files/nginx.conf"), chartFiles(), nil)
		require.NoError(t, err)
		require.Equal(t, map[string][]byte{"files/nginx.conf": []byte("server { listen 80; }")}, got)
	})
	t.Run("list", func(t *testing.T) {
		manifest := "services:\n  web:\n    image: nginx\n    env_file:\n" +
			"      - files/nginx.conf\n      - files/tls/ca.pem\n"
		got, err := ResolveManifestFiles(manifest, chartFiles(), nil)
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.Contains(t, got, "files/nginx.conf")
		require.Contains(t, got, "files/tls/ca.pem")
	})
	t.Run("a bad path anywhere in the list is refused", func(t *testing.T) {
		manifest := "services:\n  web:\n    image: nginx\n    env_file:\n" +
			"      - files/nginx.conf\n      - /etc/shadow\n"
		_, err := ResolveManifestFiles(manifest, chartFiles(), nil)
		require.ErrorContains(t, err, `'/etc/shadow'`)
	})
}

// An env_file: reached through an alias is read like any other, so it is
// resolved against the chart and refused when it cannot mean a file in it.
func TestResolveManifestFilesReadsAnAliasedEnvFile(t *testing.T) {
	manifest := func(p string) string {
		return "x-ef: &ef\n  - " + quoted(p) + "\nservices:\n  web:\n    image: nginx\n    env_file: *ef\n"
	}
	got, err := ResolveManifestFiles(manifest("files/nginx.conf"), chartFiles(), nil)
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{"files/nginx.conf": []byte("server { listen 80; }")}, got)

	_, err = ResolveManifestFiles(manifest("/etc/app.env"), chartFiles(), nil)
	require.ErrorContains(t, err, "services.web.env_file: '/etc/app.env' is an absolute path")
}

// An env_file: entry that is not a path is refused rather than passed over: the
// CLI could read a path from it that no check here has seen.
func TestResolveManifestFilesRefusesAnEnvFileEntryThatIsNotAPath(t *testing.T) {
	for want, envFile := range map[string]string{
		"services.web.env_file[1]: an env_file entry must be a path":        "[files/nginx.conf, {path: files/nginx.conf}]",
		"services.web.env_file: env_file must be a path or a list of paths": "{path: files/nginx.conf}",
	} {
		manifest := "services:\n  web:\n    image: nginx\n    env_file: " + envFile + "\n"
		_, err := ResolveManifestFiles(manifest, chartFiles(), nil)
		require.ErrorContains(t, err, want)
	}
}

// TestResolveManifestFilesSeesEntriesBesideAMalformedOne is the load-bearing
// test for walking yaml.Nodes rather than map[string]any: a single non-string
// key makes yaml.v3 hand back a map[any]any for the WHOLE mapping, so a type
// assertion would see no services at all and let /etc/shadow through.
func TestResolveManifestFilesSeesEntriesBesideAMalformedOne(t *testing.T) {
	cases := []string{
		"services:\n  1:\n    image: nginx\n  web:\n    env_file: /etc/shadow\n",
		"services:\n  broken: nginx\n  web:\n    env_file: /etc/shadow\n",
		"configs:\n  1:\n    external: true\n  site:\n    file: /etc/shadow\n",
	}
	for _, manifest := range cases {
		_, err := ResolveManifestFiles(manifest, chartFiles(), nil)
		require.ErrorContains(t, err, `'/etc/shadow'`, manifest)
	}
}

func TestResolveManifestFilesRefusesAManifestItCannotParse(t *testing.T) {
	_, err := ResolveManifestFiles("services:\n  web:\n   - :\n  bad\n", chartFiles(), nil)
	require.ErrorContains(t, err, "parse manifest")
}

// TestResolveManifestFilesRefusesDeterministically pins the sort in
// manifestFileRefs: without it, which of several bad paths is reported depends
// on Go's map iteration order and the error text flakes.
func TestResolveManifestFilesRefusesDeterministically(t *testing.T) {
	manifest := "configs:\n" +
		"  a:\n    file: /etc/shadow\n" +
		"  b:\n    file: ../../etc/passwd\n" +
		"  c:\n    file: nginx.conf\n"
	first, err := ResolveManifestFiles(manifest, chartFiles(), nil)
	require.Nil(t, first)
	require.Error(t, err)
	for range 20 {
		_, again := ResolveManifestFiles(manifest, chartFiles(), nil)
		require.EqualError(t, again, err.Error())
	}
	require.Contains(t, err.Error(), "configs.a.file")
}

func TestResolveManifestFilesRefusesAgainstAChartWithNoFiles(t *testing.T) {
	_, err := ResolveManifestFiles(configManifest("files/nginx.conf"), nil, nil)
	require.ErrorContains(t, err, `'files/nginx.conf' is not in the chart`)
}

// --- values/: the config the operator supplies (#537) ---

const operatorConfig = "module.exports = { platform: 'github' };\n"

func TestResolveManifestFilesResolvesAValue(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		values map[string]any
	}{
		{"top-level key", "values/config", map[string]any{"config": operatorConfig}},
		{
			"nested key", "values/renovate.config",
			map[string]any{"renovate": map[string]any{"config": operatorConfig}},
		},
		{
			// The docker CLI reads ./values/config and values/config as the same
			// file, so both must key the materialised tree identically.
			"normalised", "./values/config", map[string]any{"config": operatorConfig},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveManifestFiles(configManifest(tc.path), chartFiles(), tc.values)
			require.NoError(t, err)
			require.Equal(t, map[string][]byte{path.Clean(tc.path): []byte(operatorConfig)}, got)
		})
	}
}

// TestResolveManifestFilesRefusesAValueForASecret is the containment: values are
// persisted in the release record, which is a Docker Config readable by anyone
// with Docker access for every retained revision, so secret material must not
// reach it. env_file: is refused too — it expands into the service environment
// and creates no config object.
func TestResolveManifestFilesRefusesAValueForASecret(t *testing.T) {
	values := map[string]any{"config": operatorConfig}
	cases := []struct {
		name     string
		manifest func(string) string
		errKey   string
		// Each refusal names the replacement for ITS key, not a generic one:
		// docker secret create is no help to an env_file:.
		remedy []string
	}{
		{"secrets.file", secretManifest, "secrets.site.file", []string{"docker secret create", "external: true"}},
		{"services.env_file", envFileManifest, "services.web.env_file", []string{"environment:", "files/"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveManifestFiles(tc.manifest("values/config"), chartFiles(), values)
			require.Nil(t, got)
			require.ErrorContains(t, err, tc.errKey)
			require.ErrorContains(t, err, "only a config's file: may name values/")
			for _, want := range tc.remedy {
				require.ErrorContains(t, err, want)
			}
		})
	}
}

func TestResolveManifestFilesRefusesAValueItCannotUse(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		values map[string]any
		want   string
	}{
		{"absent", "values/config", nil, `'values/config' names no value`},
		{
			// The chart has to default the key so its rotating name renders, so
			// this is what a forgotten --set-file looks like — and deploying an
			// empty config over a working one is the worst available outcome.
			"empty", "values/config",
			map[string]any{"config": ""},
			`'values/config' is empty`,
		},
		{
			"absent under a key that exists", "values/renovate.config",
			map[string]any{"renovate": map[string]any{"other": "x"}},
			`'values/renovate.config' names no value`,
		},
		{
			// Walking into a scalar as if it were a map is absence, not a panic.
			"parent is not a map", "values/renovate.config",
			map[string]any{"renovate": "not a map"},
			`'values/renovate.config' names no value`,
		},
		{
			"a map is not a file", "values/config",
			map[string]any{"config": map[string]any{"a": 1}},
			"is a map[string]interface {}, and only a string can be a file",
		},
		{
			// A number would stringify plausibly, which is exactly why it is
			// refused: it means the chart named the wrong key.
			"a number is not a file", "values/config",
			map[string]any{"config": 42},
			"is a int, and only a string can be a file",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveManifestFiles(configManifest(tc.path), chartFiles(), tc.values)
			require.Nil(t, got)
			require.ErrorContains(t, err, tc.want)
			// Every one of these names the flag that supplies the value.
			require.ErrorContains(t, err, "--set-file")
		})
	}
}

// TestResolveManifestFilesRefusesAnEscapeFromValues pins that values/ is a
// prefix rule and not a prefix strip: a path leaving it is refused exactly as
// one leaving files/ is, so it can never reach a value by walking out and back.
func TestResolveManifestFilesRefusesAnEscapeFromValues(t *testing.T) {
	values := map[string]any{"config": operatorConfig}
	_, err := ResolveManifestFiles(configManifest("values/../../etc/shadow"), chartFiles(), values)
	require.ErrorContains(t, err, "escapes the chart")

	_, err = ResolveManifestFiles(configManifest("values/../nginx.conf"), chartFiles(), values)
	require.ErrorContains(t, err, "is outside files/ and values/")
}

func TestResolveManifestFilesMixesChartFilesAndValues(t *testing.T) {
	manifest := "services:\n  web:\n    image: nginx\n" +
		"configs:\n  site:\n    file: files/nginx.conf\n  app:\n    file: values/config\n"
	got, err := ResolveManifestFiles(manifest, chartFiles(), map[string]any{"config": operatorConfig})
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{
		"files/nginx.conf": []byte("server { listen 80; }"),
		"values/config":    []byte(operatorConfig),
	}, got)
}
