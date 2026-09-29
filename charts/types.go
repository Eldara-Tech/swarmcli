// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

// Package charts implements a Helm-inspired package manager for Docker Swarm.
//
// A chart is a versioned package of Docker Stack (Compose) templates plus
// default values and metadata. Installing a chart produces a release: a Docker
// stack whose revision history is stored append-only in Docker Configs. The
// package is pure Go (no Bubble Tea / TUI) so it can back both the
// non-interactive CLI and, later, a TUI browser view.
package charts

// Label keys applied to release-history Docker Configs. They mirror the
// scheme documented in issue #413 and let list/status/history queries filter
// Configs by release without an external database.
const (
	LabelType         = "com.swarmcli.type"          // always "release"
	LabelRelease      = "com.swarmcli.release"       // release name
	LabelChart        = "com.swarmcli.chart"         // chart name
	LabelChartVersion = "com.swarmcli.chart.version" // chart SemVer
	LabelRevision     = "com.swarmcli.revision"      // revision number
	LabelStatus       = "com.swarmcli.status"        // see Status* constants
	LabelCreated      = "com.swarmcli.created"       // RFC3339 timestamp
	// LabelOwner is the only optional one: "<id>:<kind>/<name>" (see OwnerRef),
	// absent entirely when nothing claimed the release.
	LabelOwner = "com.swarmcli.owner"

	TypeRelease = "release"
)

// stackNamespaceLabel is the label a stack deploy puts on every service,
// network, config and secret it creates. A release record is written directly,
// never by a stack deploy, so it never carries one.
const stackNamespaceLabel = "com.docker.stack.namespace"

// IsReleaseRecord reports whether a config's labels mark it as a release
// record: typed as one, and not created by a stack deploy. Every reader of
// release records in this module applies it, so a config a stack declares is
// never one to them, whatever else it is labelled.
func IsReleaseRecord(labels map[string]string) bool {
	_, stacked := labels[stackNamespaceLabel]
	return labels[LabelType] == TypeRelease && !stacked
}

// Release status values, following Helm's deploy/superseded/failed lifecycle.
const (
	StatusPendingInstall = "pending-install"
	StatusDeployed       = "deployed"
	StatusSuperseded     = "superseded"
	StatusFailed         = "failed"
	StatusUninstalled    = "uninstalled"
)

// Chartfile is the parsed Chart.yaml metadata.
type Chartfile struct {
	APIVersion string `yaml:"apiVersion"`
	Name       string `yaml:"name" json:"name"`
	Version    string `yaml:"version" json:"version"`
	AppVersion string `yaml:"appVersion,omitempty" json:"appVersion,omitempty"`
	// SwarmcliVersion is a SemVer constraint on the chart engine this chart
	// needs, e.g. ">= 1.13.0". Optional; absent means any. It constrains the
	// engine's version rather than the running binary's — see buildinfo.go.
	SwarmcliVersion string       `yaml:"swarmcliVersion,omitempty"`
	Description     string       `yaml:"description,omitempty"`
	Maintainers     []Maintainer `yaml:"maintainers,omitempty"`
	// Dependencies are parsed but not resolved in Phase 1 (subcharts are Phase 3).
	Dependencies []Dependency `yaml:"dependencies,omitempty"`
}

// Maintainer identifies a chart maintainer.
type Maintainer struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email,omitempty"`
	URL   string `yaml:"url,omitempty"`
}

// Dependency is a declared subchart requirement (resolution deferred to Phase 3).
type Dependency struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	Repository string `yaml:"repository,omitempty"`
}

// Chart is a loaded chart: its metadata, default values, optional values
// schema, and raw template sources keyed by their path under templates/.
type Chart struct {
	Metadata        Chartfile
	Values          map[string]any    // parsed values.yaml (defaults)
	ValuesRaw       []byte            // raw values.yaml bytes, nil if absent (preserves comments/order)
	Schema          []byte            // raw values.schema.json, nil if absent
	Templates       map[string]string // template path -> source, e.g. "templates/stack.yaml"
	Readme          string            // README.md, empty if absent
	Requirements    *Requirements     // parsed requirements.yaml (raw, unrendered), nil if absent
	RequirementsRaw []byte            // raw requirements.yaml bytes, nil if absent; re-rendered with values at pre-flight
	// Files are the chart's files/ directory, keyed by path relative to the
	// chart root — "files/nginx.conf", "files/tls/ca.pem". Nil for a chart
	// without a files/ directory, which is every chart written before it
	// existed.
	//
	// It is a directory rather than Helm's "everything that is not a known
	// member" because a chart's file set ends up inside a swarm config, and a
	// rule that sweeps up whatever happens to be lying beside values.yaml is a
	// rule that ships an editor backup to the swarm: values.yaml.bak, .env, a
	// vim swap file, a stray .git. An explicit directory is one rule, both
	// loaders implement it identically, and a chart author can see what they
	// are shipping by listing one directory.
	//
	// Templates are not here; they are in Templates. Unlike templates/, which
	// is deliberately flat, files/ is read recursively — files/tls/ca.pem is an
	// obvious shape and there is no reason to forbid it.
	Files map[string][]byte
}

// Requirements is the parsed, defaulted requirements.yaml: the external
// networks/secrets/configs a chart needs. It is optional — a chart without it
// falls back to manifest-driven pre-flight. When present it is authoritative:
// every external resource the rendered manifest references must be declared.
type Requirements struct {
	Networks []NetworkRequirement  `yaml:"networks"`
	Secrets  []ResourceRequirement `yaml:"secrets"`
	Configs  []ResourceRequirement `yaml:"configs"`
}

// NetworkRequirement declares one external network a chart needs. AutoCreate and
// Attachable are pointers so an omitted key defaults to true (preserving the
// historical auto-create-as-attachable-overlay behaviour) while an explicit
// false is distinguishable. After parseRequirements they are always non-nil and
// Driver is non-empty.
type NetworkRequirement struct {
	Name        string `yaml:"name"`
	Driver      string `yaml:"driver"`     // default "overlay"
	Attachable  *bool  `yaml:"attachable"` // default true
	AutoCreate  *bool  `yaml:"autoCreate"` // default true; false => validate-only
	Description string `yaml:"description"`
}

// ResourceRequirement declares one external secret or config a chart needs.
// These cannot be auto-created; Description enriches the remediation message.
type ResourceRequirement struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// RepoEntry is a configured chart repository (name -> index URL).
type RepoEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Index is the parsed index.yaml of a chart repository, mapping a chart name to
// its available versions (Helm repository index format, subset).
type Index struct {
	APIVersion string                  `yaml:"apiVersion"`
	Entries    map[string][]IndexEntry `yaml:"entries"`
}

// IndexEntry describes one published chart version within an Index.
type IndexEntry struct {
	Name        string   `yaml:"name"`
	Version     string   `yaml:"version"`
	AppVersion  string   `yaml:"appVersion,omitempty"`
	Description string   `yaml:"description,omitempty"`
	URLs        []string `yaml:"urls"` // tarball download URLs (absolute or index-relative)
	Digest      string   `yaml:"digest,omitempty"`
}

// Release is the payload stored (gzipped) inside a release-history Config. It
// fully describes one deployed revision so it can be inspected or rolled back.
type Release struct {
	Name      string         `yaml:"release" json:"release"`
	Revision  int            `yaml:"revision" json:"revision"`
	Status    string         `yaml:"status" json:"status"`
	Chart     ReleaseChart   `yaml:"chart" json:"chart"`
	Values    map[string]any `yaml:"values" json:"values"`
	Manifest  string         `yaml:"manifest" json:"manifest"` // rendered Compose document
	Created   string         `yaml:"created" json:"created"`   // RFC3339
	Namespace string         `yaml:"namespace" json:"namespace"`
	// ManagedNetworks are the external networks swarmcli auto-created for this
	// revision. Persisted so uninstall can report what it left behind (it does
	// not remove them — they may be shared). Omitted for revisions that created
	// none and for records written before this field existed.
	ManagedNetworks []string `yaml:"managedNetworks,omitempty" json:"managedNetworks,omitempty"`
	// Owner is the stamp naming whichever manifest or controller produced this
	// revision, encoded as "<id>:<kind>/<name>" (see OwnerRef). Empty for a
	// release nothing claimed — an imperative install, or an apply from a file
	// declaring no owner — and an unowned release is never a prune candidate.
	Owner string `yaml:"owner,omitempty" json:"owner,omitempty"`
	// Files are the chart files this revision's manifest references, keyed by
	// their chart-relative path, so that a rollback deploys what the original
	// deploy did. Rollback has no chart in scope — it replays a stored manifest,
	// with no ChartSource, no re-render and no filesystem — so anything not here
	// is gone, and the manifest it replays names files that do not exist.
	//
	// The referenced subset, not the chart's whole files/ tree: this record is a
	// Docker Config with a hard size ceiling it shares with the manifest, so an
	// asset the manifest never names must not spend it. Absent for a manifest
	// naming none, which is every manifest a chart without a files/ directory
	// can produce, and for records written before this field existed.
	//
	// releaseFiles, not a bare map[string][]byte, only so that YAML stores the
	// bytes as base64 rather than as a sequence of decimal integers — see its
	// doc comment. The two types are assignable both ways, so nothing that
	// produces or consumes a file set has to know about it.
	Files releaseFiles `yaml:"files,omitempty" json:"files,omitempty"`
}

// ReleaseChart is the chart reference recorded in a Release.
type ReleaseChart struct {
	Name       string `yaml:"name" json:"name"`
	Version    string `yaml:"version" json:"version"`
	AppVersion string `yaml:"appVersion,omitempty" json:"appVersion,omitempty"`
}
