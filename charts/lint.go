// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// LintSeverity ranks a lint finding. Only LintError fails a lint.
type LintSeverity int

const (
	// LintWarning is advice: the chart works, but something is worth fixing.
	LintWarning LintSeverity = iota
	// LintError means the chart is broken — either outright, or for the engine
	// version it was linted against.
	LintError
)

func (s LintSeverity) String() string {
	if s == LintError {
		return "error"
	}
	return "warning"
}

// LintFinding is one thing lint noticed about a chart.
type LintFinding struct {
	Severity LintSeverity
	Message  string
}

// lintRelease is the placeholder release name a lint render uses. Templates that
// interpolate .Release.Name still need one; nothing is deployed.
const lintRelease = "lint"

// Lint checks a loaded chart against the chart engine named by engine — this
// build's, or one the caller is asking about via --for-version.
//
// files and sets are extra values layered over the chart defaults for the render
// check, exactly as `charts template -f/--set` would: a chart that requires an
// input it deliberately leaves undefaulted (a {{ required }} / {{ fail }} guard)
// cannot render from bare defaults, so linting it needs the same values a real
// install would supply. Pass nil for both to lint against defaults alone.
//
// It reports everything it finds instead of stopping at the first problem: a
// chart author wants the list, not a game of whack-a-mole.
//
// Structural validation already happened in LoadChartDir / LoadChartArchive,
// which refuse a chart with no name, version or templates, or an apiVersion this
// build cannot read. Lint covers what only becomes visible once a chart is
// rendered.
//
// One thing it deliberately cannot do: prove a chart runs on the version it
// declares. This binary carries one engine's behaviour and cannot emulate
// another's — rendering with a real binary of that version is the only thing
// that settles it. See CheckCompatAgainst.
func Lint(ch *Chart, engine string, files [][]byte, sets []string) []LintFinding {
	var out []LintFinding
	add := func(s LintSeverity, format string, a ...any) {
		out = append(out, LintFinding{Severity: s, Message: fmt.Sprintf(format, a...)})
	}

	switch f := CheckCompatAgainst(ch.Metadata, engine); f.Status {
	case CompatIncompatible:
		// Deliberately not CompatFinding.Message: that says "this build
		// provides X", which is a lie under --for-version, where X is a version
		// the caller is asking about and not the one running.
		add(LintError, "Chart.yaml requires swarmcli %s, which %s does not satisfy", f.Required, f.Engine)
	case CompatUnknown:
		if f.Reason != "" {
			add(LintWarning, "%s", f.Reason)
			break
		}
		// Not an error: the field is optional and most charts predate it. But a
		// chart that names no floor gives an operator on an old build nothing to
		// act on, which is the whole problem swarmcliVersion exists to fix.
		add(LintWarning, "Chart.yaml declares no swarmcliVersion: nothing states which swarmcli this chart needs")
	}

	values, err := MergeValues(ch.Values, files, sets)
	if err != nil {
		add(LintError, "%v", err)
		return out // everything below renders from these values
	}
	if err := ValidateValues(ch.Schema, values); err != nil {
		add(LintError, "values.yaml does not satisfy values.schema.json: %v", err)
	}

	ctx := RenderContext{
		Values:  values,
		Release: ReleaseMeta{Name: lintRelease, Namespace: lintRelease, Revision: 1},
		Chart:   ChartMeta{Name: ch.Metadata.Name, Version: ch.Metadata.Version, AppVersion: ch.Metadata.AppVersion},
	}
	manifest, err := Render(ch, ctx)
	if err != nil {
		add(LintError, "render with default values failed: %v", err)
		return out
	}
	lintHealthcheckMonitor(manifest, add)
	lintInlineSecrets(manifest, add)
	if _, err := RenderRequirements(ch, ctx); err != nil {
		add(LintError, "requirements.yaml: %v", err)
	}
	return out
}

// HasErrors reports whether any finding is fatal, i.e. whether the lint failed.
func HasErrors(findings []LintFinding) bool {
	for _, f := range findings {
		if f.Severity == LintError {
			return true
		}
	}
	return false
}

// composeHealthLint is the sliver of the rendered stack that lintHealthcheckMonitor
// reads. Durations stay strings because compose writes them as "30s"/"1m30s".
type composeHealthLint struct {
	Services map[string]struct {
		Healthcheck *struct {
			Test        any    `yaml:"test"`
			Interval    string `yaml:"interval"`
			Retries     *int   `yaml:"retries"`
			StartPeriod string `yaml:"start_period"`
			Disable     bool   `yaml:"disable"`
		} `yaml:"healthcheck"`
		Deploy struct {
			UpdateConfig *struct {
				Monitor string `yaml:"monitor"`
			} `yaml:"update_config"`
		} `yaml:"deploy"`
	} `yaml:"services"`
}

// Docker's own defaults for an unspecified healthcheck field.
const (
	defaultHealthInterval = 30 * time.Second
	defaultHealthRetries  = 3
)

// swarmDefaultMonitor is what UpdateConfig.Monitor is when a compose file sets
// none. Same fact as defaultStabilityWindow in release.go, which is the window
// --wait has to sit through; naming it separately here keeps the lint readable
// without a second copy of the number.
const swarmDefaultMonitor = defaultStabilityWindow

// lintHealthcheckMonitor warns when a service cannot fail its healthcheck before
// swarm stops watching.
//
// Swarm counts a task failure against a rollout only if it happens within
// UpdateConfig.Monitor of the task being created. A container that goes
// unhealthy after that window does NOT trigger update_config.failure_action —
// the rollout is reported completed and the task quietly restart-loops. So if
// monitor is shorter than the healthcheck's own worst case, a broken deploy can
// report success. Kubernetes has no analogue of this, so it reliably surprises
// people arriving from there.
//
// An unset monitor is the common way to hit this, not an exemption from it:
// swarm's default is 5s, shorter than almost any healthcheck's worst case. The
// rule originally skipped that case, which meant it fired on nothing at all.
//
// The figure is a floor, not a budget. Swarm starts the window when it CREATES
// the task, so pulling the image happens inside it too — a large first pull on
// a cold node can consume the whole window before the container even starts.
// Pull time cannot be known from the manifest (it depends on image size,
// registry and how warm the node is), so it is named in the finding rather than
// guessed at with a constant: a fabricated allowance would be wrong for every
// chart in a different direction, and would re-fire the rule on charts whose
// monitors were set from this very formula.
//
// Warning, not error: a short monitor is legitimate when the operator wants a
// fast rollout and is watching by other means.
func lintHealthcheckMonitor(manifest string, add func(LintSeverity, string, ...any)) {
	var doc composeHealthLint
	if err := yaml.Unmarshal([]byte(manifest), &doc); err != nil {
		return // the manifest already rendered; shape problems are not lint's business here
	}

	// Map iteration is random; sort so findings do not shuffle between runs.
	names := make([]string, 0, len(doc.Services))
	for name := range doc.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		svc := doc.Services[name]
		hc := svc.Healthcheck
		if hc == nil || hc.Disable || isHealthcheckNone(hc.Test) {
			continue
		}
		monitor, declared := swarmDefaultMonitor, false
		if svc.Deploy.UpdateConfig != nil && svc.Deploy.UpdateConfig.Monitor != "" {
			d, err := time.ParseDuration(svc.Deploy.UpdateConfig.Monitor)
			if err != nil {
				continue
			}
			monitor, declared = d, true
		}

		interval := defaultHealthInterval
		if hc.Interval != "" {
			if d, err := time.ParseDuration(hc.Interval); err == nil {
				interval = d
			}
		}
		retries := defaultHealthRetries
		if hc.Retries != nil {
			retries = *hc.Retries
		}
		var startPeriod time.Duration
		if hc.StartPeriod != "" {
			if d, err := time.ParseDuration(hc.StartPeriod); err == nil {
				startPeriod = d
			}
		}
		if retries < 0 {
			continue
		}

		needed := startPeriod + time.Duration(retries)*interval
		if monitor >= needed {
			continue
		}
		had := fmt.Sprintf("deploy.update_config.monitor (%s)", monitor)
		if !declared {
			had = fmt.Sprintf("no deploy.update_config.monitor, so swarm's %s default applies, which", monitor)
		}
		add(LintWarning,
			"service %q: %s is shorter than the healthcheck needs to fail (%s = start_period %s + interval %s x retries %d); a container that goes unhealthy after the monitor window does not fail the rollout, so a broken deploy reports success. Set deploy.update_config.monitor to at least %s — and higher if the image is large, since swarm starts the window when it creates the task, so the pull runs inside it and %s does not account for it",
			name, had, needed, startPeriod, interval, retries, needed, needed)
	}
}

// isHealthcheckNone reports the compose "disable the image healthcheck" spelling,
// test: ["NONE"] or test: NONE.
func isHealthcheckNone(test any) bool {
	switch v := test.(type) {
	case string:
		return v == "NONE"
	case []any:
		return len(v) > 0 && v[0] == "NONE"
	}
	return false
}

// secretishKey matches an environment variable name that names a credential.
//
// It is deliberately a rule about the key rather than the value: what makes
// DB_PASSWORD: hunter2 wrong is the key, and no amount of looking at "hunter2"
// establishes that. Value-shaped detection — entropy, token prefixes — fires on
// image digests and generated identifiers and would make the rule noise. The one
// value-shaped exception is a PEM private key, which is unambiguous wherever it
// turns up.
var secretishKey = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|credential)`)

// pemPrivateKey is the opening line of any PEM-armoured private key. A
// CERTIFICATE block is deliberately not matched: it is public material and
// inlining one is legitimate.
var pemPrivateKey = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`)

// runtimeRef is a value that names a variable rather than carrying one: an
// escaped $$VAR/$${VAR}, which reaches the service as the literal reference. An
// unescaped $VAR/${VAR} matches too, but template and every deploy refuse it, so
// reporting it is not this rule's job.
var runtimeRef = regexp.MustCompile(`^\$+\{?[A-Za-z_][A-Za-z0-9_]*\}?$`)

// envPair is one entry of a service's environment: block.
type envPair struct{ key, value string }

// lintInlineSecrets warns when a service's environment: block carries credential
// material as a literal.
//
// Such a value is stored verbatim twice over — in the release record Config and
// in the swarm service spec — and both are readable by anyone with Docker
// access, so the credential is disclosed to every operator who can run `docker
// service inspect`. The convention charts are expected to follow instead is an
// external Docker secret read from /run/secrets/<name>, either through an image's
// *_FILE variable or in the service's own command.
//
// A warning, not an error, and deliberately so. The engine cannot tell a
// credential from a value that merely reads like one, and refusing the deploy on
// a name-matching heuristic would break third-party charts over a guess. It also
// fires on the *shape* rather than the rendered value: a chart whose
// DB_PASSWORD defaults to "" still warns, because the key is what will carry the
// credential once an operator supplies one, and that is precisely the chart worth
// warning its author about.
//
// Scope is environment: alone. command:, entrypoint: and healthcheck.test: are
// not scanned, because the sanctioned pattern — reading /run/secrets/<name> in a
// command wrapper — lives there, and a rule that flagged it would fire on every
// chart that does the right thing.
func lintInlineSecrets(manifest string, add func(LintSeverity, string, ...any)) {
	var top map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(manifest), &top); err != nil {
		return // the manifest already rendered; shape problems are not lint's business here
	}
	services, ok := top["services"]
	if !ok {
		return
	}
	for name, svc := range entries(services) {
		var s struct {
			Environment yaml.Node `yaml:"environment"`
		}
		if err := svc.Decode(&s); err != nil {
			continue
		}
		for _, kv := range envPairs(s.Environment) {
			reason, bad := inlineSecret(kv.key, kv.value)
			if !bad {
				continue
			}
			// The PEM rule fires before the _FILE exemption, so the key named
			// here may already end in _FILE and appending another would name a
			// variable no image reads.
			fileVar := kv.key
			if !strings.HasSuffix(strings.ToUpper(fileVar), "_FILE") {
				fileVar += "_FILE"
			}
			add(LintWarning,
				"services.%s.environment.%s %s. The manifest is stored verbatim in the release record and in the service spec, both readable by anyone with Docker access — reference an external Docker secret instead and read it from /run/secrets/<name>, through the image's %s variable or in the service's command",
				name, kv.key, reason, fileVar)
		}
	}
}

// inlineSecret reports whether an environment entry carries credential material,
// and why.
//
// The value is examined but never named in the finding it returns. A lint
// warning is printed to a terminal and scraped into CI logs, so a rule that
// quoted the secret it found would disclose it further than the manifest that
// prompted the warning did.
func inlineSecret(key, value string) (string, bool) {
	// A value that only names where the material is is not material, whatever
	// the key is called: an escaped $${VAR} stays a reference in the service's
	// environment, and /run/secrets/... is already the convention this rule
	// exists to ask for.
	if runtimeRef.MatchString(value) || strings.HasPrefix(value, "/run/secrets/") {
		return "", false
	}
	// Checked before the _FILE exemption below, because a *_FILE variable
	// holding a PEM block is not naming a path — it is the key material itself,
	// under a name chosen to suggest otherwise.
	if pemPrivateKey.MatchString(value) {
		return "inlines a PEM private key", true
	}
	if strings.HasSuffix(strings.ToUpper(key), "_FILE") {
		return "", false
	}
	if secretishKey.MatchString(key) {
		return "names a credential", true
	}
	return "", false
}

// envPairs flattens an environment: block into key/value pairs. Compose accepts
// both a mapping and a KEY=value sequence and a chart may render either, so both
// are read.
//
// It walks yaml.Nodes rather than decoding into a map because an environment
// block legitimately mixes scalar types — PORT: 8080 next to HOST: db — and
// decoding into map[string]string fails on the whole block at the first integer,
// taking every sibling with it. A node's raw .Value is the scalar text whatever
// the type, and works for a non-string key too.
func envPairs(node yaml.Node) []envPair {
	var out []envPair
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			k, v := node.Content[i], node.Content[i+1]
			if v.Kind != yaml.ScalarNode {
				continue
			}
			out = append(out, envPair{key: k.Value, value: v.Value})
		}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				continue
			}
			// "KEY" with no "=" passes the host's own value through at deploy
			// time; there is no literal in the manifest to warn about.
			if k, v, ok := strings.Cut(item.Value, "="); ok {
				out = append(out, envPair{key: k, value: v})
			}
		}
	}
	return out
}
