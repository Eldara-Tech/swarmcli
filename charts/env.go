// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"bufio"
	"bytes"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/docker/cli/cli/compose/template"
	"github.com/docker/cli/pkg/kvfile"
	"gopkg.in/yaml.v3"
)

// escapeRule is the remedy every interpolation refusal carries.
const escapeRule = "escape '$' as '$$' for a literal dollar sign"

// cliEnv lists the variables the docker CLI deploying a chart reads for itself,
// matched without regard to case. A chart may not declare one of them empty: the
// CLI would fill that value from its own variable, and withholding the variable
// instead would withhold it from the CLI too.
var cliEnv = []string{
	// PATH finds the CLI's plugins and credential helpers; HOME and USER locate
	// its configuration; TMPDIR is where it and its helpers write; SSH_AUTH_SOCK
	// is the agent an ssh:// context authenticates with.
	"PATH", "HOME", "USER", "TMPDIR", "SSH_AUTH_SOCK",
	// The CLI's own settings, by exact name. A DOCKER_ prefix would also refuse
	// DOCKER_TLS_CERTDIR: "", the docker:dind idiom, which the CLI never reads.
	"DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CONTEXT", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY",
	"DOCKER_TLS", "DOCKER_API_VERSION", "DOCKER_DEFAULT_PLATFORM", "DOCKER_CUSTOM_HEADERS",
	// Where a rootless daemon's socket and XDG-located configuration live.
	"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME",
	// The proxies the CLI's own connections go through, which Go reads in
	// either case.
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
	// The Windows equivalents: the profile and application-data directories the
	// configuration and credential helpers live in, the temporary directories,
	// and what process creation and executable lookup need.
	"USERPROFILE", "APPDATA", "LOCALAPPDATA", "ProgramData", "TEMP", "TMP",
	"SystemRoot", "windir", "ComSpec", "PATHEXT",
}

// CLIReadsEnv reports whether name is one of the variables the docker CLI
// deploying a stack reads for itself (cliEnv), matched without regard to case.
// Withholding such a variable would withhold it from the CLI too, so an empty
// value under that name cannot be kept empty by withholding it.
func CLIReadsEnv(name string) bool {
	return slices.ContainsFunc(cliEnv, func(n string) bool { return strings.EqualFold(n, name) })
}

// EnvLookups returns the variables a rendered manifest would take from the
// environment of the process deploying it, for that process to withhold from the
// docker CLI, and refuses every lookup that withholding cannot serve. It is what
// makes a chart deploy the same stack whoever runs it.
//
// The docker CLI fills a stack from its own environment in three ways (docker/cli
// v28.5.1): it interpolates $VAR and ${VAR} in every value; it fills an
// environment: entry that is null or "" from the variable of that name; and it
// does the same for an env_file: line that is a bare name or ends in '='. So:
//
//   - any interpolation is refused, anywhere in the document;
//   - a bare env-file line is refused, since a lookup is all it can mean;
//   - an empty value for a variable the CLI itself reads (cliEnv) is refused;
//   - every other empty value is returned, and deploys as empty once the caller
//     withholds that name.
//
// It reads the document as the CLI does — decoded, with tags, aliases and merge
// keys already applied (loader.ParseYAML is yaml.v3 into any) — so it sees the
// values the CLI will. A key that is not a string is refused, because it makes
// yaml.v3 decode its whole mapping in a shape nothing here reads into.
//
// files are the chart files the manifest names, as ResolveManifestFiles returns
// them; an env file the manifest names that is not among them is refused. A
// refusal names the compose key that holds the offending value, and the names
// come back sorted and without duplicates.
func EnvLookups(manifest string, files map[string][]byte) ([]string, error) {
	top, err := decodeManifest(manifest)
	if err != nil {
		return nil, err
	}

	var remove []string
	withhold := func(at, name string) error {
		if CLIReadsEnv(name) {
			return fmt.Errorf("%s: '%s' may not be empty, because the docker CLI deploying the chart reads that variable itself — give it a value", at, name)
		}
		remove = append(remove, name)
		return nil
	}

	services, _ := top["services"].(map[string]any)
	for _, name := range slices.Sorted(maps.Keys(services)) {
		svc, _ := services[name].(map[string]any)
		for _, env := range emptyEnv(svc["environment"]) {
			if err := withhold("services."+name+".environment", env); err != nil {
				return nil, err
			}
		}
		key := "services." + name + ".env_file"
		paths, err := envFiles(svc["env_file"], key)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			// Refused rather than skipped: the CLI would read a file this cannot
			// see, such as an absolute path in a revision recorded before the
			// chart-file checks existed.
			data, ok := files[path.Clean(p)]
			if !ok {
				return nil, fmt.Errorf("%s: env_file '%s' is not among the chart's resolved files", key, p)
			}
			var bare []string
			lines, err := kvfile.ParseFromReader(bytes.NewReader(data), func(name string) (string, bool) {
				bare = append(bare, name)
				return "", false
			})
			if err != nil {
				// The parser's message quotes the line, which can hold a value.
				if n := envFileLine(data, false); n > 0 {
					return nil, fmt.Errorf("%s: '%s' line %d is not a valid env-file line", key, p, n)
				}
				return nil, fmt.Errorf("%s: '%s' is not a valid env file", key, p)
			}
			if len(bare) > 0 {
				return nil, fmt.Errorf("%s: '%s' line %d names a variable with no '=', which a chart may not — end it with '=' for an empty value",
					key, p, envFileLine(data, true))
			}
			for _, line := range lines {
				if name, value, _ := strings.Cut(line, "="); value == "" {
					if err := withhold(key+" '"+p+"'", name); err != nil {
						return nil, err
					}
				}
			}
		}
	}

	slices.Sort(remove)
	return slices.Compact(remove), nil
}

// decodeManifest decodes a rendered manifest as the docker CLI reads it and
// refuses one that interpolates or has a key that is not a string, which
// EnvLookups describes. What it returns holds only string-keyed mappings, so a
// type assertion on a section cannot miss a sibling.
//
// It also refuses a merge key and a key given twice in one mapping (see
// refuseAmbiguousKeys), so the document it decodes is the one the CLI decodes,
// whichever YAML library that CLI was built with.
func decodeManifest(manifest string) (map[string]any, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(manifest), &root); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := refuseAmbiguousKeys(&root); err != nil {
		return nil, err
	}
	var doc any
	if err := root.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := refuseInterpolation(doc, ""); err != nil {
		return nil, err
	}
	top, ok := doc.(map[string]any)
	if !ok && doc != nil {
		return nil, fmt.Errorf("parse manifest: the top level must be a mapping")
	}
	return top, nil
}

// refuseAmbiguousKeys refuses the first merge key ('<<') under n, and the first
// key a mapping gives twice, naming its line. YAML libraries resolve both
// differently: yaml.v3, which this package and docker/cli v28 use, lets a key
// written out win over a merged one, where gopkg.in/yaml.v2, which earlier
// docker/cli versions use, lets whichever comes last win. A key reached through
// an alias is compared by the text it stands for.
func refuseAmbiguousKeys(n *yaml.Node) error {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if err := refuseAmbiguousKeys(c); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		seen := make(map[string]bool, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			text := k.Value
			if k.Kind == yaml.AliasNode && k.Alias != nil {
				text = k.Alias.Value
			}
			if text == "<<" || k.ShortTag() == "!!merge" {
				return fmt.Errorf("line %d: a chart manifest may not use a merge key ('<<') — write the merged keys out", k.Line)
			}
			if seen[text] {
				return fmt.Errorf("line %d: key '%s' is given twice in one mapping, which a chart manifest may not do — keep one", k.Line, text)
			}
			seen[text] = true
			if err := refuseAmbiguousKeys(n.Content[i+1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// refuseInterpolation refuses the first value under v that the docker CLI would
// interpolate, naming it by its key path, at. Keys are not interpolated, so only
// values are read; mappings are read in key order so a manifest with more than
// one offender always refuses on the same one.
func refuseInterpolation(v any, at string) error {
	switch v := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(v)) {
			key := k
			if at != "" {
				key = at + "." + k
			}
			if err := refuseInterpolation(v[k], key); err != nil {
				return err
			}
		}
	case map[any]any:
		where := at
		if where == "" {
			where = "the top level"
		}
		var keys []string
		for k := range v {
			if _, ok := k.(string); !ok {
				keys = append(keys, fmt.Sprint(k))
			}
		}
		// Every key decoded to a string, so what made the mapping decode this
		// way is a tag on one of them.
		if len(keys) == 0 {
			return fmt.Errorf("%s: a key carries a tag, which a chart manifest may not use — remove it", where)
		}
		slices.Sort(keys)
		return fmt.Errorf("%s: key '%s' is not a string, which a chart manifest may not use — quote it", where, keys[0])
	case []any:
		for i, e := range v {
			if err := refuseInterpolation(e, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	case string:
		// The CLI's own substitution, with a mapping that answers nothing and
		// notes what it was asked: a name it asks for is a reference, and an
		// error is a '$' it cannot parse.
		var refs []string
		_, err := template.Substitute(v, func(name string) (string, bool) {
			refs = append(refs, name)
			return "", false
		})
		if len(refs) > 0 || err != nil {
			// The value itself is not quoted: it may be one an operator supplied.
			return fmt.Errorf("%s: the value has a '$' that is not escaped, and a chart may not refer to the environment it is deployed from — %s", at, escapeRule)
		}
	}
	return nil
}

// emptyEnv returns the names a service's environment: block declares with no
// value — null or "" — in either shape compose accepts.
func emptyEnv(env any) []string {
	var names []string
	switch env := env.(type) {
	case map[string]any:
		for _, name := range slices.Sorted(maps.Keys(env)) {
			if v := env[name]; v == nil || v == "" {
				names = append(names, name)
			}
		}
	case []any:
		for _, item := range env {
			// "NAME" and "NAME=" are both empty; only "NAME=value" is not.
			entry, _ := item.(string)
			if name, value, _ := strings.Cut(entry, "="); name != "" && value == "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// envFiles returns the paths a service's env_file: names, in either shape
// compose accepts: one string, or a list of them. Any other shape is refused
// rather than passed over, since the CLI could read a path from it that no
// check here has seen. at is the key path of the env_file: itself.
func envFiles(v any, at string) ([]string, error) {
	switch v := v.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{v}, nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			p, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d]: an env_file entry must be a path", at, i)
			}
			out = append(out, p)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s: env_file must be a path or a list of paths", at)
}

// envFileLine returns the number of the first line of an env file that, parsed
// on its own, the parser rejects — or, with bare, that names a variable with no
// '='. It is 0 when no single line is at fault. Refusals name the line rather
// than quote it, because a line can hold a value.
func envFileLine(data []byte, bare bool) int {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		named := false
		_, err := kvfile.ParseFromReader(bytes.NewReader(sc.Bytes()), func(string) (string, bool) {
			named = true
			return "", false
		})
		if err != nil || (bare && named) {
			return n
		}
	}
	return 0
}
