// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// envManifest is a service whose environment: block is the given YAML, indented
// under the key.
func envManifest(env string) string {
	return "services:\n  web:\n    image: nginx\n    environment:\n      " +
		strings.ReplaceAll(strings.TrimSpace(env), "\n", "\n      ") + "\n"
}

// envFileChart names files/app.env as the web service's env_file and returns the
// manifest with the file set EnvLookups is handed.
func envFileChart(content string) (string, map[string][]byte) {
	return envFileManifest("files/app.env"), map[string][]byte{"files/app.env": []byte(content)}
}

// Every form of a reference, in every kind of place the docker CLI substitutes:
// a service's environment, a label, a command, a top-level object's name, and
// an extension block no service even uses.
func TestEnvLookupsRefusesInterpolation(t *testing.T) {
	forms := []string{"$FOO", "${FOO}", "${FOO:-fallback}", "${FOO-fallback}", "${FOO:?unset}", "${FOO?unset}"}
	places := []struct {
		name     string
		manifest func(ref string) string
		at       string
	}{
		{"environment", func(ref string) string { return envManifest("GREETING: " + quoted("hi "+ref)) }, "services.web.environment.GREETING"},
		{"label", func(ref string) string {
			return "services:\n  web:\n    image: nginx\n    labels:\n      team: " + quoted(ref) + "\n"
		}, "services.web.labels.team"},
		{"command", func(ref string) string {
			return "services:\n  web:\n    image: nginx\n    command: [\"sh\", \"-c\", " + quoted("echo "+ref) + "]\n"
		}, "services.web.command[2]"},
		{"config name", func(ref string) string {
			return "services:\n  web:\n    image: nginx\nconfigs:\n  site:\n    name: " + quoted("site-"+ref) + "\n    external: true\n"
		}, "configs.site.name"},
		{"extension block", func(ref string) string {
			return "x-defaults:\n  note: " + quoted(ref) + "\nservices:\n  web:\n    image: nginx\n"
		}, "x-defaults.note"},
	}
	for _, p := range places {
		for _, form := range forms {
			t.Run(p.name+"/"+form, func(t *testing.T) {
				got, err := EnvLookups(p.manifest(form), nil)
				require.Nil(t, got)
				require.ErrorContains(t, err, p.at+":")
				require.ErrorContains(t, err, "escape '$' as '$$'")
			})
		}
	}
}

// A '$' the CLI cannot parse fails the deploy there, and is refused here with
// the same remedy rather than passed through to fail later.
func TestEnvLookupsRefusesAnUnparseableDollar(t *testing.T) {
	for _, value := range []string{"costs $ 5", "trailing $", "$1"} {
		t.Run(value, func(t *testing.T) {
			_, err := EnvLookups(envManifest("PRICE: "+quoted(value)), nil)
			require.ErrorContains(t, err, "services.web.environment.PRICE:")
			require.ErrorContains(t, err, "escape '$' as '$$'")
		})
	}
}

// The value is not repeated in the refusal: it can be one an operator supplied
// with --set, and a refusal is printed to terminals and CI logs.
func TestEnvLookupsRefusalDoesNotQuoteTheValue(t *testing.T) {
	_, err := EnvLookups(envManifest(`DB_PASSWORD: "hunter2$x"`), nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "hunter2")
}

// An escaped dollar is what a chart writes for a literal one, and the CLI turns
// it into exactly that.
func TestEnvLookupsLeavesEscapedDollarsAlone(t *testing.T) {
	manifest := envManifest(`
SHELL_REF: "$$HOME"
SUBSHELL: "$$(cat /run/secrets/token)"
HTPASSWD: "admin:$$apr1$$abc$$def"
PLAIN: bar`)
	got, err := EnvLookups(manifest, nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

// Keys are not substituted, so a '$' in one is not a reference.
func TestEnvLookupsIgnoresKeys(t *testing.T) {
	got, err := EnvLookups("services:\n  web:\n    image: nginx\n    labels:\n      \"a$b\": x\n", nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

// Every spelling of an empty value, in both shapes compose accepts, comes back
// to be withheld — and one with a value does not.
func TestEnvLookupsReturnsEmptyValuesToWithhold(t *testing.T) {
	for name, tc := range map[string]struct {
		env  string
		want []string
	}{
		"list, bare name":   {"- FOO\n- KEEP=1", []string{"FOO"}},
		"list, trailing =":  {"- FOO=\n- KEEP=1", []string{"FOO"}},
		"map, null":         {"FOO:\nKEEP: 1", []string{"FOO"}},
		"map, tilde":        {"FOO: ~\nKEEP: 1", []string{"FOO"}},
		"map, empty string": {"FOO: \"\"\nKEEP: 1", []string{"FOO"}},
		// Not a variable the CLI reads, so withheld rather than refused: the
		// docker:dind idiom must keep working.
		"DOCKER_TLS_CERTDIR": {"DOCKER_TLS_CERTDIR: \"\"", []string{"DOCKER_TLS_CERTDIR"}},
		"several, sorted":    {"ZED:\nALPHA: \"\"", []string{"ALPHA", "ZED"}},
		"values only":        {"FOO: bar\nNUM: 0\nOFF: false", nil},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := EnvLookups(envManifest(tc.env), nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// An environment block, an entry or a value reached through an anchor is still
// read, as is one merged in: the CLI resolves them all before it looks anything
// up. Each arrives by one route only, so no route can stand in for another.
func TestEnvLookupsFollowsAnchorsAndMerges(t *testing.T) {
	manifest := "x-env: &env\n  BLOCK_ALIAS: \"\"\n" +
		"x-entries: &entries\n  ENTRY_MERGE: \"\"\n" +
		"x-base: &base\n  environment:\n    SERVICE_MERGE: \"\"\n" +
		"x-empty: &empty \"\"\n" +
		"services:\n" +
		"  a:\n    image: nginx\n    environment: *env\n" +
		"  b:\n    <<: *base\n    image: nginx\n" +
		"  c:\n    image: nginx\n    environment:\n      <<: *entries\n      VALUE_ALIAS: *empty\n"
	got, err := EnvLookups(manifest, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"BLOCK_ALIAS", "ENTRY_MERGE", "SERVICE_MERGE", "VALUE_ALIAS"}, got)
}

// The docker CLI reads the decoded document, so this does too: a tag, a key
// anchor or a merge key means here exactly what it means there.
func TestEnvLookupsReadsTheDecodedDocument(t *testing.T) {
	t.Run("a binary value that decodes to a reference is refused", func(t *testing.T) {
		// JEZPTw== is "$FOO".
		_, err := EnvLookups(envManifest("A: !!binary JEZPTw=="), nil)
		require.ErrorContains(t, err, "services.web.environment.A:")
	})
	t.Run("a key anchor used as a value is refused", func(t *testing.T) {
		manifest := "x-k:\n  &a \"${FOO}\": 1\n" + envManifest("GREETING: *a")
		_, err := EnvLookups(manifest, nil)
		require.ErrorContains(t, err, "services.web.environment.GREETING:")
	})
	t.Run("a merged reference is refused where it is used", func(t *testing.T) {
		manifest := "x-base: &base\n  GREETING: \"${FOO}\"\n" + envManifest("<<: *base")
		_, err := EnvLookups(manifest, nil)
		require.ErrorContains(t, err, "services.web.environment.GREETING:")
	})
	for name, env := range map[string]string{
		"a tagged empty value":        `FOO: !x ""`,
		"a binary empty value":        `FOO: !!binary ""`,
		"a tagged empty list entry":   `- !x "FOO="`,
		"a merged empty value":        "<<: {FOO: \"\"}",
		"an aliased empty list entry": "- *e",
	} {
		t.Run(name+" is withheld", func(t *testing.T) {
			manifest := "x-e: &e FOO\n" + envManifest(env)
			got, err := EnvLookups(manifest, nil)
			require.NoError(t, err)
			require.Equal(t, []string{"FOO"}, got)
		})
	}
	t.Run("an aliased env_file is read", func(t *testing.T) {
		manifest := "x-ef: &ef\n  - files/app.env\n" +
			"services:\n  web:\n    image: nginx\n    env_file: *ef\n"
		got, err := EnvLookups(manifest, map[string][]byte{"files/app.env": []byte("FOO=\n")})
		require.NoError(t, err)
		require.Equal(t, []string{"FOO"}, got)

		_, err = EnvLookups(manifest, nil)
		require.ErrorContains(t, err, "env_file 'files/app.env' is not among the chart's resolved files")
	})
}

// A mapping with a key that is not a string decodes differently from every other
// mapping, so it is refused rather than read around.
func TestEnvLookupsRefusesANonStringKey(t *testing.T) {
	_, err := EnvLookups("services:\n  web:\n    image: nginx\n    labels:\n      8080: x\n      team: \"${FOO}\"\n", nil)
	require.ErrorContains(t, err, "services.web.labels: key '8080' is not a string")
	_, err = EnvLookups("1: x\nservices:\n  web:\n    image: nginx\n", nil)
	require.ErrorContains(t, err, "the top level: key '1' is not a string")
}

// A variable the CLI itself reads can be neither passed through nor withheld,
// so declaring it empty is refused — in either shape, in any case.
func TestEnvLookupsRefusesAnEmptyCLIVariable(t *testing.T) {
	for name, tc := range map[string]struct{ env, variable string }{
		"PATH empty string":        {`PATH: ""`, "PATH"},
		"lower case":               {`path: ""`, "path"},
		"DOCKER_HOST null":         {"DOCKER_HOST:", "DOCKER_HOST"},
		"HTTPS_PROXY list":         {"- HTTPS_PROXY", "HTTPS_PROXY"},
		"DOCKER_CUSTOM_HEADERS":    {`DOCKER_CUSTOM_HEADERS: ""`, "DOCKER_CUSTOM_HEADERS"},
		"windows name, other case": {"- systemroot=", "systemroot"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := EnvLookups(envManifest(tc.env), nil)
			require.Nil(t, got)
			require.ErrorContains(t, err, "services.web.environment: '"+tc.variable+"' may not be empty")
		})
	}
}

func TestEnvLookupsReadsEnvFiles(t *testing.T) {
	t.Run("an empty line is withheld", func(t *testing.T) {
		manifest, files := envFileChart("# comment\nFOO=\nKEEP=1\n")
		got, err := EnvLookups(manifest, files)
		require.NoError(t, err)
		require.Equal(t, []string{"FOO"}, got)
	})
	t.Run("a bare name is refused", func(t *testing.T) {
		manifest, files := envFileChart("KEEP=1\nFOO\n")
		got, err := EnvLookups(manifest, files)
		require.Nil(t, got)
		require.ErrorContains(t, err, "services.web.env_file: 'files/app.env' has a line 'FOO' with no '='")
		require.ErrorContains(t, err, "write 'FOO=' for an empty value")
	})
	t.Run("an empty CLI variable is refused", func(t *testing.T) {
		manifest, files := envFileChart("Path=\n")
		got, err := EnvLookups(manifest, files)
		require.Nil(t, got)
		require.ErrorContains(t, err, "services.web.env_file 'files/app.env': 'Path' may not be empty")
	})
	t.Run("a name empty in both places comes back once", func(t *testing.T) {
		manifest, files := envFileChart("FOO=\n")
		manifest = strings.Replace(manifest, "    env_file:", "    environment:\n      FOO: \"\"\n    env_file:", 1)
		got, err := EnvLookups(manifest, files)
		require.NoError(t, err)
		require.Equal(t, []string{"FOO"}, got)
	})
	t.Run("the path is matched as ResolveManifestFiles keys it", func(t *testing.T) {
		manifest := envFileManifest("./files/app.env")
		got, err := EnvLookups(manifest, map[string][]byte{"files/app.env": []byte("FOO=\n")})
		require.NoError(t, err)
		require.Equal(t, []string{"FOO"}, got)
	})
	t.Run("a file not among the resolved files is refused", func(t *testing.T) {
		for _, p := range []string{"files/app.env", "/etc/app.env"} {
			got, err := EnvLookups(envFileManifest(p), map[string][]byte{"files/other.env": []byte("A=1\n")})
			require.Nil(t, got)
			require.ErrorContains(t, err, "services.web.env_file: env_file '"+p+"' is not among the chart's resolved files")
		}
	})
	t.Run("an empty file is still a resolved one", func(t *testing.T) {
		manifest, files := envFileChart("")
		got, err := EnvLookups(manifest, files)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("a file the CLI would reject is refused by line number", func(t *testing.T) {
		manifest, files := envFileChart("OK=1\nBAD KEY=hunter2\n")
		_, err := EnvLookups(manifest, files)
		require.ErrorContains(t, err, "services.web.env_file: 'files/app.env' line 2 is not a valid env-file line")
		require.NotContains(t, err.Error(), "hunter2")
		require.NotContains(t, err.Error(), "BAD KEY")

		// A line too long to read at all is no line's fault in particular.
		manifest, files = envFileChart("LONG=" + strings.Repeat("x", 70000) + "\n")
		_, err = EnvLookups(manifest, files)
		require.ErrorContains(t, err, "services.web.env_file: 'files/app.env' is not a valid env file")
	})
}

// A config's or a secret's file: is content, never environment, so a line in it
// that would be a bare name in an env file means nothing here.
func TestEnvLookupsReadsOnlyEnvFiles(t *testing.T) {
	got, err := EnvLookups(configManifest("files/nginx.conf"), map[string][]byte{"files/nginx.conf": []byte("FOO\n")})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestEnvLookupsRefusesAManifestItCannotParse(t *testing.T) {
	for _, manifest := range []string{
		"services: [",
		"- not\n- a mapping\n",
		// The docker CLI cannot decode this either.
		"x-s: &s text\n" + envManifest("<<: *s\nFOO: \"\""),
	} {
		_, err := EnvLookups(manifest, nil)
		require.ErrorContains(t, err, "parse manifest")
	}
}

// A shape compose itself rejects names nothing to withhold here; the deploy
// refuses it against the real schema.
func TestEnvLookupsLeavesMalformedShapesToTheDeploy(t *testing.T) {
	for name, manifest := range map[string]string{
		"a service that is not a mapping":   "services:\n  web: nginx\n",
		"a list entry that is not a string": envManifest("- {FOO: \"\"}"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := EnvLookups(manifest, nil)
			require.NoError(t, err)
			require.Empty(t, got)
		})
	}
}

// A rollback replays a stored manifest that no render re-reads, so the deploy
// itself is where it is refused. This goes through the real backend, not a fake
// one: a fake would bypass the very check under test. The context does not
// exist, so an error about it would mean the check ran too late.
func TestDockerBackendRefusesAnInterpolatingManifest(t *testing.T) {
	err := NewDockerBackend("no-such-context").DeployStack(context.Background(), DeployRequest{
		Name:     "web",
		Manifest: envManifest(`GREETING: "${FOO}"`),
	})
	require.ErrorContains(t, err, "services.web.environment.GREETING:")
	require.ErrorContains(t, err, "upgrade to a chart version that passes it")
	require.NotContains(t, err.Error(), "no-such-context")
}

// A stored revision can name an env file outside the chart, from before the
// chart-file checks; replaying it would have the CLI read that path, so the
// deploy refuses it with the same hint as any other stored revision.
func TestDockerBackendRefusesAnEnvFileItWasNotHanded(t *testing.T) {
	err := NewDockerBackend("no-such-context").DeployStack(context.Background(), DeployRequest{
		Name:     "web",
		Manifest: envFileManifest("/etc/app.env"),
	})
	require.ErrorContains(t, err, "services.web.env_file: env_file '/etc/app.env' is not among the chart's resolved files")
	require.ErrorContains(t, err, "upgrade to a chart version that passes it")
	require.NotContains(t, err.Error(), "no-such-context")
}

// The names EnvLookups returns reach the docker CLI withheld, and nothing else
// does: a stand-in `docker` on PATH records the environment it was started with.
func TestDockerBackendWithholdsEmptyValuesFromTheCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in docker is a shell script")
	}
	dir := t.TempDir()
	envOut := filepath.Join(dir, "env")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"),
		[]byte("#!/bin/sh\nenv > \""+envOut+"\"\n"), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FOO", "from-the-shell")
	t.Setenv("BAR", "from-the-shell")
	t.Setenv("KEEP", "from-the-shell")

	manifest, files := envFileChart("BAR=\n")
	manifest = strings.Replace(manifest, "    env_file:", "    environment:\n      FOO: \"\"\n    env_file:", 1)
	require.NoError(t, NewDockerBackend("stand-in").DeployStack(context.Background(), DeployRequest{
		Name: "web", Manifest: manifest, Files: files,
	}))

	got, err := os.ReadFile(envOut)
	require.NoError(t, err)
	env := "\n" + string(got)
	require.NotContains(t, env, "\nFOO=")
	require.NotContains(t, env, "\nBAR=")
	require.Contains(t, env, "\nKEEP=from-the-shell\n")
}
