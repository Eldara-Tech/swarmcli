// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// reservedLabelPrefix is the label namespace release records are written under
// (LabelType and its siblings). Whatever reads those labels takes a config
// carrying them for a record.
const reservedLabelPrefix = "com.swarmcli."

// recordNamePrefix begins every release record's name (releaseConfigName). The
// engine allocates the next record's name when it deploys, so the names it will
// need are not all on the swarm yet.
const recordNamePrefix = "swarmcli.release."

// CheckReserved refuses a manifest that declares a config or secret in the
// space release records occupy: one carrying a label under com.swarmcli., or
// one whose name on the swarm starts with swarmcli.release. stack is the name
// the manifest deploys as, which the docker CLI prefixes to every name the
// manifest does not set itself.
//
// It reads the document through decodeManifest, so it sees the declarations the
// CLI will, and refuses what EnvLookups refuses for the same reasons.
func CheckReserved(manifest, stack string) error {
	top, err := decodeManifest(manifest)
	if err != nil {
		return err
	}
	for _, kind := range []string{"configs", "secrets"} {
		decls, _ := top[kind].(map[string]any)
		// Sorted, so a manifest with more than one offender always refuses on
		// the same one.
		for _, key := range slices.Sorted(maps.Keys(decls)) {
			at := kind + "." + key
			decl, _ := decls[key].(map[string]any)
			if label, ok := reservedLabel(decl["labels"]); ok {
				return fmt.Errorf("%s.labels: label '%s' is under '%s', which is reserved for swarmcli's release records — rename or drop the label",
					at, label, reservedLabelPrefix)
			}
			for _, name := range swarmNames(stack, key, decl) {
				// Swarm keeps config and secret names unique regardless of case.
				if strings.HasPrefix(strings.ToLower(name), recordNamePrefix) {
					return fmt.Errorf("%s: name '%s' starts with '%s', which is reserved for swarmcli's release records — rename it",
						at, name, recordNamePrefix)
				}
			}
		}
	}
	return nil
}

// reservedLabel returns the first label key under reservedLabelPrefix, in either
// shape compose accepts: a mapping, or a list of "key=value" entries.
func reservedLabel(labels any) (string, bool) {
	var keys []string
	switch labels := labels.(type) {
	case map[string]any:
		keys = slices.Collect(maps.Keys(labels))
	case []any:
		for _, item := range labels {
			key, _, _ := strings.Cut(fmt.Sprint(item), "=")
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		if strings.HasPrefix(k, reservedLabelPrefix) {
			return k, true
		}
	}
	return "", false
}

// swarmNames returns the name the docker CLI gives a declared config or secret
// on the swarm (convert.Configs and loadFileObjectConfig, docker/cli v28.5.1):
// its name: if set; an external one's deprecated external.name, or else its
// key; otherwise the key scoped to the stack.
//
// An external: that is neither a boolean nor a mapping, such as yes or on, is a
// string to this decoder and an error to that CLI, but a CLI parsing YAML 1.1
// reads it as a boolean. Both of the names it could mean are returned for it.
//
// A name is read as written. decodeManifest has refused every '$' the CLI would
// substitute, and turning an escaped '$$' into '$' cannot make a name start
// with a prefix that has no '$' in it.
func swarmNames(stack, key string, decl map[string]any) []string {
	name, _ := decl["name"].(string)
	if ext, ok := decl["external"].(map[string]any); ok {
		if n, _ := ext["name"].(string); n != "" {
			name = n
		}
	}
	if name != "" {
		return []string{name}
	}
	switch ext := decl["external"].(type) {
	case nil:
	case bool:
		if ext {
			return []string{key}
		}
	case map[string]any:
		return []string{key}
	default:
		return []string{key, stack + "_" + key}
	}
	return []string{stack + "_" + key}
}
