// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"fmt"
	"iter"
	"maps"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// chartFilesRule is the sentence every refusal below carries.
//
// Refusing an absolute path breaks a chart shape that works today, and there is
// no compatibility flag and no deprecation window, so the error text is the
// whole of the migration path. One that only said "refused" would turn a
// breaking change into a support question.
const chartFilesRule = "a chart may only read files it ships in " + filesDir + "/"

// valuesDir is the one other directory a manifest may name: not a directory on
// anyone's disk, but the effective values, materialised as files.
//
// It exists because a config the OPERATOR supplies had no mechanism at all.
// Compose gives a config its content only through file:, there is no content:
// field to inline with (docker/cli v28.5.1 config_schema_v3.9.json is
// name/file/external/labels/template_driver, additionalProperties: false), and a
// chart naming a path outside itself is exactly what chartFilesRule refuses —
// rightly, since the docker CLI reads it as the invoking operator.
//
// The asymmetry that makes this safe is that the OPERATOR names the path, on
// their own command line (--set-file), and the chart names only a values key. A
// path the operator typed carries the authority they already have; a path a
// chart chose does not. See issue #537.
const valuesDir = "values"

// valuesFilesRule is the sentence every refusal of a values/ path carries.
const valuesFilesRule = valuesDir + "/ names a value, which the operator supplies (--set-file <key>=<path>, or a values file)"

// ResolveManifestFiles returns the files a rendered manifest names, and refuses
// every path that cannot mean either a file in the chart or one of values.
//
// Exactly three compose keys read a path — a config's file:, a secret's file:
// and a service's env_file: — and the docker CLI resolves all three against the
// directory of the compose file it was handed, then reads them itself,
// client-side and as the invoking user, before anything reaches the daemon. In
// docker/cli v28.5.1 that is absPath in cli/compose/loader/loader.go for all
// three, then fileObjectConfig in cli/compose/convert/compose.go for the two
// file: keys and parseEnvFile in cli/compose/loader/envfile.go for env_file:.
//
// swarmcli writes that compose file to a temp directory. So an unguarded
// relative path has always meant "a file in the system temp directory" — which
// any local user can plant, against a temp name predictable enough to know when
// to try — and an absolute one has always meant itself, read as the operator
// into a swarm config that anyone with Docker access can read.
//
// This therefore refuses on the way past, in order: a path containing '$',
// which the CLI would interpolate before reading it; one that is absolute; one
// that escapes the chart; and one that is neither in the chart's files/ nor in
// values/. None of it depends on where the chart was loaded from. Trusting a
// local-path chart with an absolute path would grant the most privilege to
// vendoring a repository chart to disk, which is the workflow that most obscures
// a chart's origin.
//
// A files/ path comes back keyed exactly as Chart.Files keys it; a values/ path
// comes back keyed by the path the manifest wrote, carrying the value's bytes.
// Both are materialised beside the manifest at that relative path — which is
// what makes file: mean the chart, or the operator's own file, and nothing else.
// Only the referenced subset is returned: it is persisted in the release record
// so a rollback replays the bytes the original deploy sent, and that record has
// a hard size ceiling it shares with the manifest.
//
// A manifest naming no file at all yields a nil map and no error, which is every
// manifest a chart without a files/ directory can produce.
func ResolveManifestFiles(manifest string, files map[string][]byte, values map[string]any) (map[string][]byte, error) {
	refs, err := manifestFileRefs(manifest)
	if err != nil {
		return nil, err
	}
	var out map[string][]byte
	for _, ref := range refs {
		key, data, err := ref.resolve(files, values)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = map[string][]byte{}
		}
		out[key] = data
	}
	return out, nil
}

// fileRef is one path a rendered manifest asks the docker CLI to read, carrying
// the compose key that asked for it so a refusal can name both.
type fileRef struct {
	key  string // "configs.site.file", "services.web.env_file"
	path string // the path exactly as the manifest wrote it
}

// resolve returns the key this reference contributes to the resolved file set
// and the bytes behind it, or the error that refuses it. Every message names the
// offending path and states the rule; the absolute one additionally names its
// replacements, because it is the only refusal with a working chart behind it.
func (r fileRef) resolve(files map[string][]byte, values map[string]any) (string, []byte, error) {
	// First, because every check below reads the path as written, and the docker
	// CLI reads it only after interpolating it.
	if strings.Contains(r.path, "$") {
		return "", nil, fmt.Errorf("%s: '%s' contains '$', and a chart names its files literally — %s", r.key, r.path, chartFilesRule)
	}
	if path.IsAbs(r.path) {
		return "", nil, fmt.Errorf(
			"%s: '%s' is an absolute path, and %s — copy it into the chart's %s/ and reference it by that path, "+
				"or let the operator supply it (--set-file <key>=%s, referenced as %s/<key>), "+
				"or keep it operator-managed by creating it outside the chart "+
				"(docker config create <name> <path>, or docker secret create) and referencing it with external: true",
			r.key, r.path, chartFilesRule, filesDir, r.path, valuesDir)
	}
	// The value came from YAML, which is slash-separated whatever the host is,
	// so this is path and not filepath. Clean resolves every interior "..", so a
	// leading one afterwards is the complete test for leaving the chart root.
	clean := path.Clean(r.path)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", nil, fmt.Errorf("%s: '%s' escapes the chart, and %s", r.key, r.path, chartFilesRule)
	}
	if key, ok := strings.CutPrefix(clean, valuesDir+"/"); ok {
		data, err := r.resolveValue(key, values)
		return clean, data, err
	}
	if !strings.HasPrefix(clean, filesDir+"/") {
		return "", nil, fmt.Errorf("%s: '%s' is outside %s/ and %s/, and %s — reference it as '%s'",
			r.key, r.path, filesDir, valuesDir, chartFilesRule, filesDir+"/"+clean)
	}
	if _, ok := files[clean]; !ok {
		return "", nil, fmt.Errorf("%s: '%s' is not in the chart, and %s", r.key, r.path, chartFilesRule)
	}
	return clean, files[clean], nil
}

// resolveValue returns the bytes of the value a values/ path names.
//
// Only a config's file: may name one, and the two refusals are not symmetry for
// its own sake. A values/ path is content the operator supplied through values,
// and values are persisted in the release record — a Docker Config readable by
// anyone with Docker access, for every retained revision, not just the current
// one. Routing a SECRET's material through that would put it in the one place a
// secret exists to keep it out of, so a secret stays what it has always been:
// created outside the chart, referenced with external: true. env_file: is
// refused for a different reason — it is expanded into the service's
// environment, where docker inspect shows it, and it creates no config object,
// so none of the rotation this mechanism exists for applies to it.
func (r fileRef) resolveValue(key string, values map[string]any) ([]byte, error) {
	if !strings.HasPrefix(r.key, "configs.") {
		// Each refusal names its own replacement: a secret's is an external
		// secret, and env_file:'s is the environment: block it expands into
		// anyway, or a file the chart ships.
		remedy := "put them in the service's environment: block, or ship the file in " + filesDir + "/"
		if strings.HasPrefix(r.key, "secrets.") {
			remedy = "create it outside the chart (docker secret create <name> <path>) " +
				"and reference it with external: true"
		}
		return nil, fmt.Errorf("%s: '%s' — only a config's file: may name %s/, because a value is stored in the "+
			"release record and is as readable as any config — %s",
			r.key, r.path, valuesDir, remedy)
	}
	steps, err := parseSetPath(key)
	if err != nil {
		return nil, fmt.Errorf("%s: '%s' is not a values path: %w", r.key, r.path, err)
	}
	v, ok := lookupPath(values, steps)
	if !ok {
		return nil, fmt.Errorf("%s: '%s' names no value, and %s", r.key, r.path, valuesFilesRule)
	}
	// Deliberately not a stringified number or a marshalled map: a config's
	// content is a file the operator wrote, and anything else here is a chart
	// naming the wrong key — which should say so rather than deploy a plausible
	// rendering of the wrong thing.
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("%s: '%s' is a %T, and only a string can be a file — %s", r.key, r.path, v, valuesFilesRule)
	}
	// An empty value is unsupplied, not empty content. A chart referencing
	// values/<key> has to default that key in values.yaml — to "", since a
	// template computing the config's rotating name from it must render before
	// anything here runs — so the operator who forgets --set-file arrives with
	// exactly this, and the alternative to refusing is deploying an empty config
	// over a working one. A config that must genuinely be empty can be a newline.
	if s == "" {
		return nil, fmt.Errorf("%s: '%s' is empty, and %s", r.key, r.path, valuesFilesRule)
	}
	return []byte(s), nil
}

// manifestFileRefs returns every path the manifest asks the docker CLI to read,
// in a stable order so a manifest with more than one bad path always refuses on
// the same one.
//
// The manifest is walked as yaml.Nodes rather than decoded into map[string]any
// because a single non-string key anywhere in a mapping makes yaml.v3 hand back
// a map[any]any for the WHOLE mapping — so one "services: {1: …}" entry would
// make every sibling service invisible to a type assertion, and a guard that
// quietly stops looking is worse than no guard. A node map coerces the key to a
// string, and decoding each entry on its own means one malformed entry cannot
// hide the others.
//
// Anything that is still not the shape compose describes names no path here and
// is left to the deploy, which validates the manifest against the real schema.
func manifestFileRefs(manifest string) ([]fileRef, error) {
	var top map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(manifest), &top); err != nil {
		// A refusal rather than an empty result: this is the guard, and a
		// manifest it cannot parse is one whose file: keys it cannot see.
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	var refs []fileRef
	for _, section := range []string{"configs", "secrets"} {
		for name, node := range entries(top[section]) {
			var entry struct {
				File string `yaml:"file"`
			}
			// An entry with no file: — external: true, or driver-backed — is
			// indistinguishable from file: "" here, and neither names anything.
			if err := node.Decode(&entry); err != nil || entry.File == "" {
				continue
			}
			refs = append(refs, fileRef{key: section + "." + name + ".file", path: entry.File})
		}
	}

	for name, node := range entries(top["services"]) {
		var entry struct {
			EnvFile yaml.Node `yaml:"env_file"`
		}
		if err := node.Decode(&entry); err != nil {
			continue
		}
		// env_file is the one of the three compose gives two shapes: a bare
		// string, or a list of them. Both are read. A yaml.Node field keeps an
		// alias as it was written, so it is followed first.
		// An entry that is not a string is refused rather than passed over: the
		// CLI could read a path from it that no check here has seen.
		envFile := unalias(&entry.EnvFile)
		key := "services." + name + ".env_file"
		switch envFile.Kind {
		case yaml.SequenceNode:
			for i, item := range envFile.Content {
				var p string
				if err := item.Decode(&p); err != nil {
					return nil, fmt.Errorf("%s[%d]: an env_file entry must be a path", key, i)
				}
				if p != "" {
					refs = append(refs, fileRef{key: key, path: p})
				}
			}
		default:
			var p string
			if err := envFile.Decode(&p); err != nil {
				return nil, fmt.Errorf("%s: env_file must be a path or a list of paths", key)
			}
			if p != "" {
				refs = append(refs, fileRef{key: key, path: p})
			}
		}
	}
	return refs, nil
}

// entries yields the named entries of one top-level compose block, sorted by
// name. A block that is absent, or present but not a mapping, yields nothing.
func entries(node yaml.Node) iter.Seq2[string, yaml.Node] {
	var block map[string]yaml.Node
	_ = node.Decode(&block)
	names := slices.Sorted(maps.Keys(block))
	return func(yield func(string, yaml.Node) bool) {
		for _, name := range names {
			if !yield(name, block[name]) {
				return
			}
		}
	}
}

// unalias returns the node an alias refers to, or n itself.
func unalias(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode {
		return n.Alias
	}
	return n
}
