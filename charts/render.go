// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"gopkg.in/yaml.v3"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
)

// RenderContext is the data exposed to chart templates, mirroring Helm's
// top-level objects: .Values, .Release, .Chart.
type RenderContext struct {
	Values  map[string]any
	Release ReleaseMeta
	Chart   ChartMeta
}

// ReleaseMeta is the .Release object available to templates.
type ReleaseMeta struct {
	Name      string
	Namespace string
	Revision  int
}

// ChartMeta is the .Chart object available to templates.
type ChartMeta struct {
	Name       string
	Version    string
	AppVersion string
}

// Render evaluates every templates/*.yaml file with text/template + Sprig,
// deep-merges the resulting Compose fragments into a single document, and
// returns it as validated YAML. Files whose names start with "_" (e.g.
// templates/_helpers.tpl) define named templates only and emit no document.
func Render(ch *Chart, ctx RenderContext) (string, error) {
	merged := map[string]any{}

	for _, name := range sortedTemplateNames(ch.Templates) {
		rendered, err := renderOne(ch, name, ctx)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(baseName(name), "_") {
			continue // helper-only file
		}
		if strings.TrimSpace(rendered) == "" {
			continue // empty after rendering (e.g. fully gated by {{ if }})
		}
		frag := map[string]any{}
		if err := yaml.Unmarshal([]byte(rendered), &frag); err != nil {
			return "", fmt.Errorf("template %s produced invalid YAML: %w", name, err)
		}
		merged = deepMerge(merged, frag)
	}

	// The top-level compose `version` key is obsolete for `docker stack deploy`
	// (the engine ignores it) and, because the merged manifest is re-encoded from
	// a map with sorted keys, it would otherwise surface as a confusing trailing
	// line. Drop it so the rendered stack is clean.
	delete(merged, "version")

	if len(merged) == 0 {
		return "", fmt.Errorf("chart '%s' rendered an empty manifest", ch.Metadata.Name)
	}

	// text/template prints '<no value>' for a key the values do not have. Charts
	// test optional keys with {{ if }}, so missingkey=error is not an option, and
	// without this a misspelt key, or values missing a key the chart reads, would
	// deploy an image, a network or a secret named '<no value>' (#687).
	if at, ok := noValue(merged, ""); ok {
		return "", fmt.Errorf("rendered manifest: %s holds '<no value>', which a template prints for a value that is not set — set it, or check the key's spelling in the template", at)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(merged); err != nil {
		return "", fmt.Errorf("encode rendered manifest: %w", err)
	}
	_ = enc.Close()

	out := buf.String()
	if err := docker.ValidateStackYAML(out); err != nil {
		return "", fmt.Errorf("rendered manifest is not a valid Docker stack: %w", err)
	}
	return out, nil
}

// noValue returns the path of the first key or string under v that holds
// text/template's '<no value>', in the dotted form the other manifest refusals
// use.
func noValue(v any, at string) (string, bool) {
	switch v := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(v)) {
			key := k
			if at != "" {
				key = at + "." + k
			}
			if strings.Contains(k, "<no value>") {
				return key, true
			}
			if p, ok := noValue(v[k], key); ok {
				return p, true
			}
		}
	case []any:
		for i, e := range v {
			if p, ok := noValue(e, fmt.Sprintf("%s[%d]", at, i)); ok {
				return p, true
			}
		}
	case string:
		return at, strings.Contains(v, "<no value>")
	}
	return "", false
}

// renderOne parses every template in the chart (so cross-file named templates
// and {{ template }} / {{ include }} references resolve) and executes the named
// one against ctx.
func renderOne(ch *Chart, name string, ctx RenderContext) (string, error) {
	tmpl := template.New(name).Funcs(renderFuncs()).Funcs(extraFuncs())
	// Parse all templates into the set so helpers are available.
	for _, n := range sortedTemplateNames(ch.Templates) {
		if _, err := tmpl.New(n).Parse(ch.Templates[n]); err != nil {
			return "", fmt.Errorf("template %s: parse error: %w", n, err)
		}
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, ctx); err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	return buf.String(), nil
}

// renderString parses all of the chart's templates into a set (so named
// helpers / {{ include }} resolve) plus an extra template `src` under `name`,
// then executes `src` against ctx. Used to render auxiliary files such as
// requirements.yaml with the same engine, functions, and context as the manifest.
func renderString(ch *Chart, name, src string, ctx RenderContext) (string, error) {
	tmpl := template.New(name).Funcs(renderFuncs()).Funcs(extraFuncs())
	for _, n := range sortedTemplateNames(ch.Templates) {
		if _, err := tmpl.New(n).Parse(ch.Templates[n]); err != nil {
			return "", fmt.Errorf("template %s: parse error: %w", n, err)
		}
	}
	if _, err := tmpl.New(name).Parse(src); err != nil {
		return "", fmt.Errorf("template %s: parse error: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, ctx); err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	return buf.String(), nil
}

// RenderRequirements renders the chart's requirements.yaml through the same
// template engine and context as the manifest, then parses the result. This lets
// requirements.yaml reference .Values (e.g. an operator-chosen network name) while
// staying authoritative — the declared names are resolved against the same values
// that produced the manifest. Returns (nil, nil) when the chart ships no
// requirements.yaml.
//
// Templated values must be quoted (name: "{{ .Values.x }}") so requirements.yaml
// still parses as YAML at chart-load time; the real value is resolved here, at the
// install/upgrade pre-flight.
func RenderRequirements(ch *Chart, ctx RenderContext) (*Requirements, error) {
	if ch.RequirementsRaw == nil {
		return nil, nil
	}
	rendered, err := renderString(ch, requirementsName, string(ch.RequirementsRaw), ctx)
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", requirementsName, err)
	}
	req, err := parseRequirements([]byte(rendered))
	if err != nil {
		return nil, err
	}
	return req, nil
}

// renderFuncs returns the Sprig function map with host-reaching helpers removed.
// Charts may come from untrusted repos, so `env`/`expandenv`/`getHostByName`
// are denied to stop a template from exfiltrating host environment or DNS data
// into the rendered manifest (which is stored verbatim in a Docker Config).
func renderFuncs() template.FuncMap {
	fm := sprig.TxtFuncMap()
	for _, name := range []string{"env", "expandenv", "getHostByName"} {
		delete(fm, name)
	}
	return fm
}

// extraFuncs adds Helm-style helpers that Sprig does not provide. "include"
// renders a named template to a string so it can be piped (e.g. into indent).
func extraFuncs() template.FuncMap {
	return template.FuncMap{
		"toYaml": func(v any) (string, error) {
			var b bytes.Buffer
			enc := yaml.NewEncoder(&b)
			enc.SetIndent(2)
			if err := enc.Encode(v); err != nil {
				return "", err
			}
			_ = enc.Close()
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

func sortedTemplateNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
