// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

// Package userconfig reads config.yaml, the preferences a person edits by hand,
// and writes a single key back to it when a toggle in the TUI changes one.
//
// The file is the middle of three layers: an environment variable beats it, and
// it beats the built-in default. Machine state — the install identity, the
// update-notice dismissal — stays in files of its own, so a damaged preference
// cannot take either with it.
//
// A write edits the yaml.v3 node tree of the file as it is on disk at that
// moment, so comments, key order and keys this build does not know survive. A
// Go struct is never marshalled over the file: that loses all three.
package userconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"

	"gopkg.in/yaml.v3"
)

// The keys this build reads.
const (
	KeyMouse     = "mouse"
	KeyTelemetry = "telemetry"
)

// ErrUnparseable is a config.yaml that is not a YAML mapping. swarmcli runs on
// defaults rather than on a guess at what it says, and Set refuses to write over
// it: a write would replace whatever the person was in the middle of with the
// part of it this build understood.
var ErrUnparseable = errors.New("config.yaml does not parse")

// Config is what the file says. A field is its zero value when the key is absent
// or its value is unusable, so the caller falls through to its own default.
type Config struct {
	// Mouse is nil unless the file sets a boolean.
	Mouse *bool
	// Telemetry is the value as written, trimmed and lower-cased. The telemetry
	// package reads it with the spellings SWARMCLI_TELEMETRY accepts, and keeps
	// its rule that a value it does not recognise leaves reporting on.
	Telemetry string
}

// keys is every key, in file order, with the comment the first write puts above
// it and the default it shows commented out.
var keys = []struct {
	name, example string
	doc           []string
}{
	{KeyMouse, "true", []string{
		"Mouse support in the lists: true or false. :mouse switches it and saves",
		"it here. SWARMCLI_MOUSE overrides it.",
	}},
	{KeyTelemetry, "on", []string{
		"Usage reporting: on, off (no report, the update check still runs) or none",
		"(no request at all). SWARMCLI_TELEMETRY overrides it.",
	}},
}

const header = `# swarmcli preferences. Every key is optional, and an environment variable
# overrides the key it names. swarmcli writes a setting back by changing that
# key alone, so comments and keys it does not know are kept.
# https://github.com/Eldara-Tech/swarmcli/blob/main/docs/configuration.md#configyaml
`

// renameFn replaces the file with the one just written. Overridable for tests.
var renameFn = os.Rename

// Path is $XDG_CONFIG_HOME/swarmcli/config.yaml, or ~/.config/swarmcli/
// config.yaml when that is unset — on macOS too, where os.UserConfigDir would
// answer ~/Library/Application Support. A relative XDG_CONFIG_HOME is ignored,
// as the XDG specification asks.
func Path() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "swarmcli", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "swarmcli", "config.yaml"), nil
}

var (
	startupOnce   sync.Once
	startupConfig Config
	startupErr    error
)

// Startup is the file as this process first read it, and an error when it could
// not be read or does not parse, in which case Config is empty.
//
// Read once, because what a preference does is settled as swarmcli starts: a
// value a toggle writes back is for the next start, and this session already
// acts on it. Safe to call from any goroutine.
func Startup() (Config, error) {
	startupOnce.Do(func() {
		startupConfig, startupErr = load()
		if startupErr != nil {
			swarmlog.L().Warnw("config.yaml not used, running on defaults", "error", startupErr)
		}
	})
	return startupConfig, startupErr
}

func load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Config{}, nil // no home directory, so no file
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	_, root, err := parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", p, err)
	}

	var c Config
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		switch key.Value {
		case KeyMouse:
			on, ok := parseBool(value)
			if !ok {
				swarmlog.L().Warnw("config.yaml: not true or false, using the default", "key", key.Value, "line", value.Line)
				continue
			}
			c.Mouse = &on
		case KeyTelemetry:
			c.Telemetry = strings.ToLower(strings.TrimSpace(value.Value))
		default:
			swarmlog.L().Infow("config.yaml: unknown key, kept and ignored", "key", key.Value, "line", key.Line)
		}
	}
	return c, nil
}

func parseBool(n *yaml.Node) (value, ok bool) {
	if n.Kind != yaml.ScalarNode {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(n.Value)) {
	case "true", "yes", "on", "1":
		return true, true
	case "false", "no", "off", "0":
		return false, true
	}
	return false, false
}

// parse returns the document and its top-level mapping. A file that is empty or
// holds only comments has no document node to hang a key on, so both are nil;
// an explicit null document becomes an empty mapping. A second document is
// refused: a write would re-encode the first alone and drop it.
func parse(data []byte) (doc, root *yaml.Node, err error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var d yaml.Node
	if err := dec.Decode(&d); err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	var next yaml.Node
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("%w: more than one YAML document", ErrUnparseable)
	}
	if d.Kind == 0 {
		return nil, &yaml.Node{Kind: yaml.MappingNode}, nil
	}
	root = d.Content[0]
	if root.Kind == yaml.ScalarNode && root.ShortTag() == "!!null" {
		*root = yaml.Node{Kind: yaml.MappingNode, HeadComment: root.HeadComment, FootComment: root.FootComment}
	}
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%w: the top level is not a list of 'key: value' lines", ErrUnparseable)
	}
	return &d, root, nil
}

// Set writes key into config.yaml with value, a YAML scalar as it should appear
// in the file, and changes nothing else.
//
// It re-reads the file first, so a hand edit made while swarmcli runs is kept,
// and refuses one that does not parse. The new content goes to a temporary file
// beside the real one, which is then renamed over it: a crash leaves either the
// old file or the new one, never half of either. Through a symlink — a dotfile
// manager's — it writes the file the link points at and leaves the link alone,
// and it keeps the file's mode. The first write creates the file from a
// template that lists every key.
func Set(key, value string) error {
	if !known(key) {
		return fmt.Errorf("unknown config.yaml key '%s'", key)
	}
	p, err := Path()
	if err != nil {
		return err
	}
	target := linkTarget(p)

	info, err := os.Stat(target)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return writeAtomic(target, render(key, value), 0o644)
	}
	if err != nil {
		return err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	out, err := setKey(data, key, value)
	if err != nil {
		return err
	}
	return writeAtomic(target, out, info.Mode().Perm())
}

func known(key string) bool {
	for _, k := range keys {
		if k.name == key {
			return true
		}
	}
	return false
}

// linkTarget is the file a write has to replace: the end of p's symlink chain,
// followed link by link when it dangles so no link in it is replaced, or p
// itself. maxLinkHops bounds a loop.
func linkTarget(p string) string {
	if t, err := filepath.EvalSymlinks(p); err == nil {
		return t
	}
	for range maxLinkHops {
		dest, err := os.Readlink(p)
		if err != nil {
			return p
		}
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(p), dest)
		}
		p = dest
	}
	return p
}

const maxLinkHops = 40

func setKey(data []byte, key, value string) ([]byte, error) {
	doc, root, err := parse(data)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		// Only comments, or nothing: append to the text as written, so the
		// comments stay where the person put them.
		out := append([]byte{}, data...)
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		return append(out, key+": "+value+"\n"...), nil
	}

	found := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			v := root.Content[i+1]
			*v = yaml.Node{Kind: yaml.ScalarNode, Value: value, Anchor: v.Anchor,
				HeadComment: v.HeadComment, LineComment: v.LineComment, FootComment: v.FootComment}
			found = true
		}
	}
	if !found {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Value: value})
	}

	return encode(doc)
}

// encode writes doc out and reads the result back, so that whatever an edit
// meets that setKey did not foresee, a file that would not parse is never the
// one written. The parse error is not wrapped: the file on disk is fine, and
// ErrUnparseable would say otherwise.
func encode(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	err := enc.Encode(doc)
	if cerr := enc.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		_, _, err = parse(buf.Bytes())
	}
	if err != nil {
		return nil, fmt.Errorf("config.yaml left as it was, the edit would not read back: %v", err)
	}
	return buf.Bytes(), nil
}

// render is the first write: every key, commented out at its default, except
// the one being set.
func render(key, value string) []byte {
	var b strings.Builder
	b.WriteString(header)
	for _, k := range keys {
		b.WriteString("\n")
		for _, line := range k.doc {
			b.WriteString("# " + line + "\n")
		}
		if k.name == key {
			b.WriteString(k.name + ": " + value + "\n")
		} else {
			b.WriteString("# " + k.name + ": " + k.example + "\n")
		}
	}
	return []byte(b.String())
}

func writeAtomic(target string, data []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(target), ".config.yaml.*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), mode)
	}
	if err == nil {
		err = renameFn(f.Name(), target)
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}
