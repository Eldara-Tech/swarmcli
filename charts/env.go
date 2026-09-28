// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
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
	"DOCKER_TLS", "DOCKER_API_VERSION", "DOCKER_DEFAULT_PLATFORM",
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
// files are the chart files the manifest names, as ResolveManifestFiles returns
// them. A refusal names the compose key that holds the offending value, and the
// names come back sorted and without duplicates.
func EnvLookups(manifest string, files map[string][]byte) ([]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(manifest), &doc); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := refuseInterpolation(&doc, ""); err != nil {
		return nil, err
	}

	var remove []string
	withhold := func(at, name string) error {
		if slices.ContainsFunc(cliEnv, func(n string) bool { return strings.EqualFold(n, name) }) {
			return fmt.Errorf("%s: '%s' may not be empty, because the docker CLI deploying the chart reads that variable itself — give it a value", at, name)
		}
		remove = append(remove, name)
		return nil
	}

	var top map[string]yaml.Node
	_ = yaml.Unmarshal([]byte(manifest), &top)
	for name, node := range entries(top["services"]) {
		var svc struct {
			Environment yaml.Node `yaml:"environment"`
		}
		if err := node.Decode(&svc); err != nil {
			continue
		}
		for _, env := range emptyEnv(svc.Environment) {
			if err := withhold("services."+name+".environment", env); err != nil {
				return nil, err
			}
		}
	}

	refs, err := manifestFileRefs(manifest)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if !strings.HasPrefix(ref.key, "services.") {
			continue // a config's or a secret's file: is content, not environment
		}
		var bare []string
		lines, err := kvfile.ParseFromReader(bytes.NewReader(files[path.Clean(ref.path)]), func(name string) (string, bool) {
			bare = append(bare, name)
			return "", false
		})
		if err != nil {
			return nil, fmt.Errorf("%s: '%s': %w", ref.key, ref.path, err)
		}
		if len(bare) > 0 {
			return nil, fmt.Errorf("%s: '%s' has a line '%s' with no '=', which a chart may not — write '%s=' for an empty value",
				ref.key, ref.path, bare[0], bare[0])
		}
		for _, line := range lines {
			if name, value, _ := strings.Cut(line, "="); value == "" {
				if err := withhold(ref.key+" '"+ref.path+"'", name); err != nil {
					return nil, err
				}
			}
		}
	}

	slices.Sort(remove)
	return slices.Compact(remove), nil
}

// refuseInterpolation refuses the first value under n that the docker CLI would
// interpolate, naming it by its key path, at. Keys are not interpolated, so only
// values are read; an alias repeats a node already read where its anchor is.
func refuseInterpolation(n *yaml.Node, at string) error {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			if err := refuseInterpolation(c, at); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			if err := refuseInterpolation(c, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			if at != "" {
				key = at + "." + key
			}
			if err := refuseInterpolation(n.Content[i+1], key); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		// The CLI's own substitution, with a mapping that answers nothing and
		// notes what it was asked: a name it asks for is a reference, and an
		// error is a '$' it cannot parse.
		var refs []string
		_, err := template.Substitute(n.Value, func(name string) (string, bool) {
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
func emptyEnv(env yaml.Node) []string {
	n := unalias(&env)
	var names []string
	switch n.Kind {
	case yaml.MappingNode:
		// Decoded rather than walked, so a merge key contributes its entries and
		// a non-string key comes back as the string compose reads it as.
		var m map[string]yaml.Node
		if err := n.Decode(&m); err != nil {
			return nil
		}
		for _, name := range slices.Sorted(maps.Keys(m)) {
			v := m[name]
			if v := unalias(&v); v.ShortTag() == "!!null" || (v.ShortTag() == "!!str" && v.Value == "") {
				names = append(names, name)
			}
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			var entry string
			if err := item.Decode(&entry); err != nil {
				continue
			}
			// "NAME" and "NAME=" are both empty; only "NAME=value" is not.
			if name, value, _ := strings.Cut(entry, "="); name != "" && value == "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// unalias returns the node an alias refers to, or n itself.
func unalias(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode {
		return n.Alias
	}
	return n
}
