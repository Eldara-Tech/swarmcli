// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/Eldara-Tech/swarmcli/v2/charts"

	"gopkg.in/yaml.v3"
)

// liveEmptyEnv returns the variables the redeploy of an edited stack must
// withhold from the docker CLI, which fills an environment: value that is null
// or "" from its own environment. reconstructed is the stack as rebuilt from
// its running services, which the edit started from, and edited is the edit.
// A name is withheld when a running service holds it with no value and the edit
// still declares it so: the running services are the source of truth. The edit
// is read as a whole, since withholding covers the whole deploy — a service
// renamed in the editor keeps its empty values empty, and a ${NAME}, or an empty
// NAME, that the edit adds elsewhere is empty too.
//
// A variable the docker CLI reads for itself cannot be withheld from it. When
// this environment gives one a value, the CLI would fill it, so the redeploy is
// refused, as a chart that declares it empty is; otherwise nothing fills it.
// The names come back sorted and without duplicates.
func liveEmptyEnv(reconstructed, edited string) ([]string, error) {
	live, err := emptyEnvByService(reconstructed)
	if err != nil {
		return nil, fmt.Errorf("parse the reconstructed stack: %w", err)
	}
	kept, err := emptyEnvByService(edited)
	if err != nil {
		return nil, fmt.Errorf("parse the edited stack: %w", err)
	}
	keptNames := slices.Concat(slices.Collect(maps.Values(kept))...)
	var names []string
	for _, svc := range slices.Sorted(maps.Keys(live)) {
		for _, name := range live[svc] {
			if !slices.Contains(keptNames, name) {
				continue
			}
			if charts.CLIReadsEnv(name) {
				if cliFills(name) {
					return nil, fmt.Errorf("services.%s.environment: '%s' has no value in the running service, and the docker CLI that redeploys the stack would fill it from its own '%s', which it reads itself — give it a value or remove it", svc, name, name)
				}
				continue
			}
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// emptyEnvByService returns, for each service of a compose document, the names
// its environment: declares with no value (charts.EmptyEnv).
func emptyEnvByService(doc string) (map[string][]string, error) {
	var d struct {
		Services map[string]struct {
			Environment any `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(doc), &d); err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(d.Services))
	for svc, s := range d.Services {
		out[svc] = charts.EmptyEnv(s.Environment)
	}
	return out, nil
}

// cliFills reports whether the docker CLI would change an empty value of name:
// its lookup matches the name exactly, and a value it finds empty changes
// nothing.
func cliFills(name string) bool {
	return slices.ContainsFunc(os.Environ(), func(kv string) bool {
		n, v, _ := strings.Cut(kv, "=")
		return n == name && v != ""
	})
}
