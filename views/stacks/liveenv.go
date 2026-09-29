// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"fmt"
	"maps"
	"slices"

	"github.com/Eldara-Tech/swarmcli/v2/charts"

	"gopkg.in/yaml.v3"
)

// liveEmptyEnv returns the variables a stack reconstructed from its running
// services holds empty, for the redeploy of an edit to withhold from the docker
// CLI. The CLI fills an empty environment: value from its own environment, and
// the running services are what an edit starts from, so a value empty there
// must stay empty. Only the reconstruction is read: a name the operator adds in
// the editor is filled as the CLI always fills it.
//
// A variable the docker CLI reads for itself cannot be withheld from it, so an
// empty one refuses the redeploy, as it refuses a chart that declares it empty.
// The names come back sorted and without duplicates.
func liveEmptyEnv(reconstructed string) ([]string, error) {
	// The reconstruction writes environment: as a mapping of strings, and only
	// an empty value reads back as "".
	var doc struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(reconstructed), &doc); err != nil {
		return nil, fmt.Errorf("parse the reconstructed stack: %w", err)
	}
	var names []string
	for _, svc := range slices.Sorted(maps.Keys(doc.Services)) {
		env := doc.Services[svc].Environment
		for _, name := range slices.Sorted(maps.Keys(env)) {
			if env[name] != "" {
				continue
			}
			if charts.CLIReadsEnv(name) {
				return nil, fmt.Errorf("services.%s.environment: '%s' is empty in the running service, and the docker CLI that redeploys the stack reads that variable itself, so it cannot stay empty — give the service a value for it first", svc, name)
			}
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}
