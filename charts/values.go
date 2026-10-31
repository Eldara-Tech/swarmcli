// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// MergeValues computes the effective values for a render, applying Helm's
// precedence: chart defaults < each --values file (in order) < --set overrides.
// Maps are deep-merged; scalars and sequences replace.
func MergeValues(defaults map[string]any, files [][]byte, sets []string) (map[string]any, error) {
	out := deepCopyMap(defaults)

	for i, raw := range files {
		parsed := map[string]any{}
		if err := yaml.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("values file #%d: %w", i+1, err)
		}
		out = deepMerge(out, parsed)
	}

	for _, s := range sets {
		if err := applySet(out, s); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// applySet applies one or more comma-separated "a.b.c=value" overrides to dst.
// Values are type-inferred (bool, int, float, else string), and Helm's "{a,b}"
// literal builds a list. Paths may index into lists Helm-style — "a[0]=x",
// "a.b[1].c=y" — growing the list to fit.
//
// Every expression either applies or errors: a --set that silently did nothing
// (as an unsupported "a[0]=x" once did) is the worst outcome, because the install
// succeeds and the operator debugs the wrong thing.
func applySet(dst map[string]any, expr string) error {
	for _, pair := range splitSet(expr) {
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			return fmt.Errorf("--set '%s': expected key=value", pair)
		}
		path := unescapeCommas(strings.TrimSpace(pair[:eq]))
		if path == "" {
			return fmt.Errorf("--set '%s': empty key", pair)
		}
		steps, err := parseSetPath(path)
		if err != nil {
			return fmt.Errorf("--set '%s': %w", pair, err)
		}
		// Every path starts with a map key and dst is non-nil, so setPath walks
		// into dst and mutates it in place.
		setPath(dst, steps, inferValue(strings.TrimSpace(pair[eq+1:])))
	}
	return nil
}

// SetFile is one --set-file: the values key to populate, and the bytes read
// from the path the operator named. The caller does the reading, so nothing in
// this package opens a file it was not handed.
type SetFile struct {
	Key  string
	Data []byte
}

// ApplySetFiles sets each key to its file's contents, verbatim.
//
// It is --set without the parsing: no comma splitting, no "{a,b}" list literal
// and no type inference, because the content is a file the operator wrote and
// every one of those would corrupt it — a config.js full of commas would be cut
// into fragments, and one reading "true" would become a boolean.
//
// Applied after MergeValues, so a --set-file wins over both a values file and a
// --set naming the same key, matching the order the flags are documented in.
func ApplySetFiles(dst map[string]any, sets []SetFile) error {
	for _, sf := range sets {
		if sf.Key == "" {
			return fmt.Errorf("--set-file: empty key")
		}
		steps, err := parseSetPath(sf.Key)
		if err != nil {
			return fmt.Errorf("--set-file '%s': %w", sf.Key, err)
		}
		setPath(dst, steps, string(sf.Data))
	}
	return nil
}

// setStep is one hop of a --set path: a map key, or a list index.
type setStep struct {
	key     string
	index   int
	isIndex bool
}

// parseSetPath splits a dotted path into map-key and list-index hops, so
// "a.b[0].c" walks map a -> map b -> element 0 -> key c. Anything malformed is an
// error rather than a key with brackets in its name that no template ever reads.
func parseSetPath(path string) ([]setStep, error) {
	var steps []setStep
	for _, seg := range strings.Split(path, ".") {
		if seg == "" {
			return nil, fmt.Errorf("empty path segment")
		}
		name, idx, indexed := strings.Cut(seg, "[")
		if name == "" {
			return nil, fmt.Errorf("list index '%s' has no key", seg)
		}
		if strings.ContainsRune(name, ']') {
			return nil, fmt.Errorf("unbalanced ']' in '%s'", seg)
		}
		steps = append(steps, setStep{key: name})
		for rest := "[" + idx; indexed && rest != ""; {
			if rest[0] != '[' {
				return nil, fmt.Errorf("unexpected '%s' after a list index in '%s'", rest, seg)
			}
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, fmt.Errorf("unbalanced '[' in '%s'", seg)
			}
			n, err := strconv.Atoi(rest[1:end])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid list index '%s' in '%s'", rest[1:end], seg)
			}
			steps = append(steps, setStep{index: n, isIndex: true})
			rest = rest[end+1:]
		}
	}
	return steps, nil
}

// splitSet splits on commas that are neither escaped ("\,") nor inside a "{}"
// list literal. Escapes are left intact for inferValue to resolve, so a comma
// inside a list element ("{a\,b}") survives both splits.
func splitSet(s string) []string { return splitCommas(s, true) }

// splitCommas splits s on unescaped commas, ignoring those nested in "{}" when
// braces is set. Nested list literals are not supported.
func splitCommas(s string, braces bool) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && s[i+1] == ',':
			cur.WriteString(`\,`)
			i++
		case braces && c == '{':
			depth++
			cur.WriteByte(c)
		case braces && c == '}':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case c == ',' && depth == 0:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(parts, cur.String())
}

// unescapeCommas resolves "\," to a literal comma.
func unescapeCommas(s string) string { return strings.ReplaceAll(s, `\,`, ",") }

// inferValue converts a --set value into a scalar, or into a list when written
// Helm-style as "{a,b,c}" ("{}" is the empty list).
func inferValue(s string) any {
	if len(s) >= 2 && strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		inner := s[1 : len(s)-1]
		if strings.TrimSpace(inner) == "" {
			return []any{}
		}
		parts := splitCommas(inner, false)
		out := make([]any, 0, len(parts))
		for _, p := range parts {
			out = append(out, inferScalar(unescapeCommas(strings.TrimSpace(p))))
		}
		return out
	}
	return inferScalar(unescapeCommas(s))
}

// lookupPath reads the value at the given hops, reporting whether every hop
// existed. The inverse of setPath, and the reason a manifest's values/<key> is
// resolved against the effective values rather than against the --set-file
// flags: an upgrade with --reuse-values, a values file carrying the content
// inline, and a rollback replaying a stored record all reach the value the same
// way, and none of them has a flag to consult.
//
// A hop into something that is not the container the step wants — a key of a
// list, an index of a map — is absent rather than an error, which is what
// "names no value" means to the caller.
func lookupPath(cur any, steps []setStep) (any, bool) {
	for _, s := range steps {
		if s.isIndex {
			lst, ok := cur.([]any)
			if !ok || s.index >= len(lst) {
				return nil, false
			}
			cur = lst[s.index]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[s.key]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// setPath assigns val at the given hops, creating maps and lists along the way and
// returning the (possibly new) container. A value of the wrong shape for the next
// hop is replaced — an intermediate scalar becomes a map, as before — and a list
// grows to fit the index, padding with nil.
func setPath(cur any, steps []setStep, val any) any {
	if len(steps) == 0 {
		return val
	}
	if s := steps[0]; s.isIndex {
		lst, _ := cur.([]any)
		for len(lst) <= s.index {
			lst = append(lst, nil)
		}
		lst[s.index] = setPath(lst[s.index], steps[1:], val)
		return lst
	}
	m, _ := cur.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	m[steps[0].key] = setPath(m[steps[0].key], steps[1:], val)
	return m
}

// inferScalar converts a --set string into bool, int, float64, or string.
func inferScalar(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	case "null", "nil":
		return nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return int(i)
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// ReuseValues returns a previous release's values layered over a chart's
// defaults: the base `upgrade --reuse-values` merges its overrides onto. The
// defaults stay underneath rather than being replaced, so a key the new chart
// version adds keeps its default (#687), and previous wins wherever both have a
// key. Neither argument is mutated.
func ReuseValues(defaults, previous map[string]any) map[string]any {
	return deepMerge(deepCopyMap(defaults), deepCopyMap(previous))
}

// deepMerge recursively merges src into dst and returns dst. Nested maps merge;
// every other type (scalars, sequences) replaces the destination value.
func deepMerge(dst, src map[string]any) map[string]any {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				dst[k] = deepMerge(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
	return dst
}

// deepCopyMap clones a values map so merges never mutate a chart's defaults.
func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		cp := make([]any, len(t))
		for i, e := range t {
			cp[i] = deepCopyValue(e)
		}
		return cp
	default:
		return v
	}
}

// ValidateValues checks merged values against a chart's values.schema.json
// (JSON Schema). A nil/empty schema is a no-op.
func ValidateValues(schema []byte, values map[string]any) error {
	if len(bytes.TrimSpace(schema)) == 0 {
		return nil
	}
	compiler := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return fmt.Errorf("values.schema.json is not valid JSON: %w", err)
	}
	if err := compiler.AddResource("values.schema.json", doc); err != nil {
		return fmt.Errorf("values.schema.json: %w", err)
	}
	sch, err := compiler.Compile("values.schema.json")
	if err != nil {
		return fmt.Errorf("values.schema.json: %w", err)
	}
	if err := sch.Validate(normalizeForSchema(values)); err != nil {
		return fmt.Errorf("values failed schema validation: %w", err)
	}
	return nil
}

// normalizeForSchema converts YAML-decoded values (map[string]any) into the
// any-keyed shape the JSON Schema validator expects.
func normalizeForSchema(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = normalizeForSchema(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalizeForSchema(e)
		}
		return out
	case int:
		return float64(t)
	default:
		return v
	}
}
