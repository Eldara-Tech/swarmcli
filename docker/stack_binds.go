// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// checkBindSources refuses a bind mount whose source is not an absolute path.
//
// A swarm bind mount names a path on whichever node runs the task, so a source
// that is not absolute has no referent at all — and nothing downstream says so.
// The docker CLI resolves every bind source against the directory of the compose
// file it was handed (resolveVolumePaths in cli/compose/loader, docker/cli
// v28.5.1), and the compose file swarmcli hands it is the manifest writeStackTree
// just put in a temporary directory. So `./data:/data` has always meant a path
// under swarmcli's own temp directory — on a node that has never heard of it, and
// which swarmcli deletes the moment the deploy returns. `~/data` differs only in
// being legible: expandUser resolves it against the HOME of whoever ran swarmcli,
// which is not the machine the container runs on either.
//
// This runs before the manifest is written, because once the loader has run the
// source has already been rewritten and the evidence is gone — the same reason
// charts.ResolveManifestFiles reads the document rather than the loaded config.
//
// Every deploy passes through here: charts install, upgrade and rollback, and
// the stacks view's three raw paths. They share the temp directory, so they
// share the defect, so they share the refusal.
//
// An absolute source is left alone. A bind naming a real path on a real node is
// a documented and deliberately privileged thing for a swarm service to do, and
// it is a property of compose rather than a swarmcli defect.
//
// swarmcli-cd refuses the same thing in the same words (compose.checkBindSources)
// so that an operator meeting this in both editions meets one rule.
func checkBindSources(manifest string) error {
	var top map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(manifest), &top); err != nil {
		// A refusal rather than an empty result: this is the guard, and a
		// manifest it cannot parse is one whose volumes it cannot see.
		return fmt.Errorf("parse manifest: %w", err)
	}
	// Walked as yaml.Nodes rather than decoded into map[string]any because one
	// non-string key anywhere in the services mapping makes yaml.v3 hand back a
	// map[any]any for the WHOLE mapping, so a type assertion would hide every
	// sibling service — a guard that quietly stops looking is worse than none.
	section := top["services"]
	var services map[string]yaml.Node
	_ = section.Decode(&services)

	// Sorted, so a manifest with more than one offender always refuses on the
	// same one.
	for _, name := range slices.Sorted(maps.Keys(services)) {
		node := services[name]
		var svc struct {
			Volumes yaml.Node `yaml:"volumes"`
		}
		if err := node.Decode(&svc); err != nil {
			continue
		}
		// A yaml.Node field keeps an alias as it was written, so it is followed
		// first — for the block here and for each entry in bindSource.
		volumes := unalias(&svc.Volumes)
		if volumes.Kind != yaml.SequenceNode {
			continue
		}
		for _, item := range volumes.Content {
			source, ok := bindSource(item)
			if !ok || source == "" || isAbsBindSource(source) {
				continue
			}
			return fmt.Errorf("service '%s': bind source '%s' is relative; a swarm bind mount names a path "+
				"on the node that runs the task, so it must be absolute (or use a named volume)", name, source)
		}
	}
	return nil
}

// bindSource returns the host path one volume entry names, and whether it names
// one at all.
//
// The long form is read by its type:, and two of the six name a host path: bind,
// and npipe. npipe is here because it is what this would otherwise be one word
// away from missing — it converts without complaint. The other four name none:
// volume and cluster take a volume name, image an image reference, and tmpfs no
// source at all.
func bindSource(item *yaml.Node) (string, bool) {
	item = unalias(item)
	switch item.Kind {
	case yaml.ScalarNode:
		var spec string
		if err := item.Decode(&spec); err != nil {
			return "", false
		}
		return shortSyntaxBindSource(spec)
	case yaml.MappingNode:
		var entry struct {
			Type   string `yaml:"type"`
			Source string `yaml:"source"`
		}
		if err := item.Decode(&entry); err != nil {
			return "", false
		}
		if entry.Type != "bind" && entry.Type != "npipe" {
			return "", false
		}
		return entry.Source, true
	}
	return "", false
}

// shortSyntaxBindSource returns the host side of a `source:target[:opts]` entry,
// and whether that entry is a bind at all.
//
// Compose decides that from the source's prefix alone and nowhere else
// (isFilePath in docker/cli's internal/volumespec, reached from populateType):
// `data:/d` is a named volume and `./data:/d` is a bind, and `my.data:/d` is a
// named volume too — a dot elsewhere in the name is not a path, and a guess that
// "it looks like one" would refuse a chart that was never wrong.
//
// The rules are mirrored rather than imported because volumespec is internal to
// docker/cli and reaching it means cli/compose/loader, whose own dependencies
// this module deliberately does not carry (see the pin in go.mod). They are two:
// the prefix test below, and the fact that a drive letter's colon is not a
// separator — `C:\data:/d` has three colons and only the last two divide fields.
//
// An entry the parser would reject names nothing here. loader.Load refuses it
// during the deploy a moment later, with a better message than this could give.
func shortSyntaxBindSource(spec string) (string, bool) {
	// Two characters or fewer is a container path and nothing else, exactly as
	// volumespec.Parse special-cases it.
	if len(spec) <= 2 {
		return "", false
	}
	from := 0
	if isWindowsDrive(spec) {
		from = 2
	}
	i := strings.IndexByte(spec[from:], ':')
	if i < 0 {
		// No separator: an anonymous volume, which has no host side.
		return "", false
	}
	source := spec[:from+i]
	if source == "" || !isFilePath(source) {
		return "", false
	}
	return source, true
}

// isFilePath mirrors volumespec.isFilePath: the prefixes compose reads as "this
// source is a path on a host" rather than as a volume name.
func isFilePath(source string) bool {
	switch source[0] {
	case '.', '/', '~':
		return true
	}
	// A Windows named pipe or UNC path, then a drive letter.
	return strings.HasPrefix(source, `\\`) || isWindowsDrive(source)
}

// isWindowsDrive reports whether s opens with a drive letter and its colon.
func isWindowsDrive(s string) bool {
	if len(s) < 2 || s[1] != ':' {
		return false
	}
	c := s[0] | 0x20
	return c >= 'a' && c <= 'z'
}

// isAbsBindSource reports whether a bind source is absolute, by the same test the
// loader applies before rewriting one — path.IsAbs, then a Windows-absolute check
// (isAbs in cli/compose/loader/windows_path.go). Sharing the test is what makes
// this refuse exactly the sources the loader would have resolved against
// swarmcli's temp directory, and nothing else.
//
// A Windows path is absolute here whatever the operator's own OS, because the
// path names the node that runs the task rather than the machine swarmcli runs
// on: a Linux workstation deploying to a Windows node writes C:\data, where
// filepath.IsAbs would say no.
//
// `~/data` is deliberately not absolute. expandUser rewrites it first, against
// the HOME of whoever ran swarmcli, so a test applied after that point would pass
// it while it named a directory on the wrong machine entirely.
func isAbsBindSource(source string) bool {
	if path.IsAbs(source) {
		return true
	}
	// A UNC path or a Windows named pipe (\\.\pipe\docker_engine). Not parsed
	// further: a malformed one is refused by the daemon, and guessing at where
	// the host and share end is how this would start disagreeing with the CLI.
	if strings.HasPrefix(source, `\\`) {
		return true
	}
	// A drive letter, which is absolute only with a separator after it: C:\data
	// names the drive's root, while C:data names the drive's current directory
	// and is resolved against the temp directory like any other relative path.
	return isWindowsDrive(source) && len(source) > 2 && (source[2] == '\\' || source[2] == '/')
}

// unalias returns the node an alias refers to, or n itself.
func unalias(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode {
		return n.Alias
	}
	return n
}
