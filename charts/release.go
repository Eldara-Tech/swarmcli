// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// maxConfigPayload guards against Docker's per-Config size limit (~500 KB). We
// gzip the release payload before storing; if it still exceeds this it cannot
// be saved as a Config.
const maxConfigPayload = 500 << 10 // 500 KiB

// ConfigMeta is the edition-agnostic view of a stored release Config.
type ConfigMeta struct {
	Name   string
	Labels map[string]string
	// Data is the config payload, set when the Backend's listing already
	// carried it. Docker returns a config's payload in the list response, not
	// only on inspect, so a Backend that passes it through spares allRevisions
	// one inspect per stored revision — which is the entire cost of reading
	// release history. Leaving it nil is valid and costs exactly that inspect.
	Data []byte
}

// ServiceState is a live status line for a release's services, plus the facts
// --wait needs to decide whether the rollout is actually finished.
type ServiceState struct {
	Name     string
	Mode     string
	Replicas string // "running/desired" for replicated, "" otherwise — display only
	Status   string

	// Running counts tasks that are actually running on an active node.
	// Deliberately not derived from Replicas: that string mirrors `docker
	// service ls` and so counts every generation, superseded tasks included, and
	// counts them wherever they sit — a down node's stale task included (see
	// issue #480).
	Running int
	// Desired is the target task count over active nodes.
	Desired int
	// Completed counts tasks that ran to completion, and Job marks a service
	// swarm will not restart after a clean exit. A one-shot init or migration
	// step is *supposed* to end with nothing running, so without these two a
	// finished job reads as a service that never came up (issue #443).
	Completed int
	Job       bool
	// UpdateState is swarm's UpdateStatus.State, empty when the service has
	// never been updated. Empty means "no rollout has ever run" — NOT "the
	// rollout finished", which is why a fresh install cannot rely on it.
	UpdateState string
	// Monitor is UpdateConfig.Monitor: the window after a task is created in
	// which its failure still counts against the rollout.
	Monitor time.Duration
	// NewestTaskAge is how much of that window the newest running task has
	// already lived through, measured from task creation as swarm measures it.
	NewestTaskAge time.Duration
}

// DeployRequest is one deploy.
//
// A struct rather than a parameter list: Backend is implemented outside this
// repository (swarmcli-cd's backend.Backend), so a field added later costs an
// implementation nothing, while widening the parameter list is a breaking
// change to every one of them. This method has been widened twice; it will not
// be widened again.
type DeployRequest struct {
	// Name is the stack, which is also the release name.
	Name string
	// Manifest is the rendered compose document.
	Manifest string
	// Resolve is the --resolve-image mode, empty for the daemon's default.
	Resolve string
	// Files are the chart files the manifest's file: and env_file: keys name,
	// keyed by their chart-relative path. A backend that serves them must make
	// each one readable at exactly that relative path from wherever the
	// manifest is resolved, and must not let a path escape that root.
	//
	// Empty for a manifest that names none, which is every manifest a chart
	// without a files/ directory can produce.
	Files map[string][]byte
}

// Backend abstracts the Docker operations the release engine needs, so the
// lifecycle logic is unit-testable without a live Swarm.
//
// Every method takes a context and is expected to honour it. Release operations
// are the long ones — a --wait deploy legitimately runs for minutes — and the
// daemon is reached over a connection the caller does not hold, so a controller
// being shut down, or retiring the application it is syncing, has no other way to
// stop work already in flight.
type Backend interface {
	DeployStack(ctx context.Context, req DeployRequest) error
	RemoveStack(ctx context.Context, name string) error
	// RefreshSnapshot invalidates the shared Docker state cache after a mutation
	// so subsequent reads (status, convergence polling) do not see stale data.
	RefreshSnapshot(ctx context.Context) error
	CreateConfig(ctx context.Context, name string, data []byte, labels map[string]string) error
	ListConfigs(ctx context.Context) ([]ConfigMeta, error)
	InspectConfig(ctx context.Context, name string) ([]byte, error)
	DeleteConfig(ctx context.Context, name string) error
	StackServices(ctx context.Context, name string) []ServiceState
	StackVolumes(ctx context.Context, name string) ([]string, error)
	RemoveVolume(ctx context.Context, name string) error
	// NetworkScopes returns existing network names mapped to their scope
	// (e.g. "swarm", "local"), used to pre-flight a chart's external networks.
	NetworkScopes(ctx context.Context) (map[string]string, error)
	// CreateOverlayNetwork creates a swarm-scoped network with the given driver
	// and attachability (driver defaults to "overlay" when a chart does not
	// declare one in requirements.yaml).
	CreateOverlayNetwork(ctx context.Context, name, driver string, attachable bool) error
	// RemoveOverlayNetwork removes a network by name, used to roll back networks
	// auto-created for an install whose deploy then failed. A no-op if absent.
	RemoveOverlayNetwork(ctx context.Context, name string) error
	// SecretNames returns the set of existing swarm secret names, used to
	// pre-flight a chart's external secrets (which cannot be auto-created).
	SecretNames(ctx context.Context) (map[string]struct{}, error)
}

// Engine drives release lifecycle operations against a Backend.
type Engine struct {
	Backend Backend
	// now returns the current time; overridable in tests.
	now func() time.Time
}

// NewEngine returns an Engine bound to the live Docker backend.
func NewEngine() *Engine {
	return &Engine{Backend: &dockerBackend{}, now: time.Now}
}

// NewEngineWith returns an Engine bound to a custom backend (used in tests).
func NewEngineWith(b Backend) *Engine {
	return &Engine{Backend: b, now: time.Now}
}

// InstallOptions tune an install or upgrade.
type InstallOptions struct {
	DryRun     bool
	Wait       bool
	Install    bool // upgrade: create the release if it does not exist
	Timeout    time.Duration
	HistoryMax int // 0 = keep all
	// ResolveImage selects how the daemon resolves image tags at deploy time
	// ("always" | "changed" | "never"); empty leaves Docker's default of
	// "always". See docker.ResolveImage for why "changed" suits automation.
	ResolveImage string
	// Requirements is the chart's parsed requirements.yaml, when present. It
	// drives the external-resource pre-flight (auto-create vs validate-only, the
	// network driver/attachability, and remediation descriptions) and, when set,
	// every external resource the manifest references must be declared in it. Nil
	// falls back to manifest-driven pre-flight (auto-create attachable overlays).
	Requirements *Requirements
	// Files are the chart files the manifest references, already resolved
	// against the chart by the caller that still had one (ResolveManifestFiles).
	// They are recorded on the revision and handed to the deploy.
	//
	// Carried the same way Requirements is, and for the same reason: it is
	// chart-derived data the engine needs, and the engine has no chart. Nil for
	// a manifest that names no file — and nil on a rollback, which is not a
	// gap: a rollback replays the files off the revision it is rolling back to.
	Files map[string][]byte
	// Owner claims the release for a manifest or controller, e.g.
	// "apply/prod-swarm" or "cd/edge". It is recorded on every revision this
	// call writes, as the id half of an OwnerRef naming the release.
	//
	// Empty leaves the revision unowned, which is what an imperative install
	// does and what keeps "never delete anything" the default: only a release
	// stamped with a caller's own owner can ever be a prune candidate, so a
	// release installed by hand or by somebody else's manifest is untouchable
	// no matter what a later prune is asked to do.
	Owner string
}

// Install deploys a freshly rendered manifest as revision 1 of a new release and
// records it. It refuses to install over an existing, non-uninstalled release
// (use upgrade — Phase 2). manifest must already be rendered and validated.
func (e *Engine) Install(ctx context.Context, release string, chart ReleaseChart, values map[string]any, manifest string, opts InstallOptions) (*Release, error) {
	if err := validateReleaseName(release); err != nil {
		return nil, err
	}
	revs, err := e.revisions(ctx, release)
	if err != nil {
		return nil, err
	}
	if cur := currentRevision(revs); cur != nil && cur.Status != StatusUninstalled {
		return nil, fmt.Errorf("release '%s' already exists (revision %d); use upgrade", release, cur.Revision)
	}

	rel := e.newRevision(release, nextRevision(revs), chart, values, manifest, opts.Files)
	return e.deployAndRecord(ctx, rel, opts)
}

// Upgrade deploys a new revision of an existing release. When the release does
// not exist it errors unless opts.Install is set (the `upgrade --install`
// behavior). manifest must already be rendered and validated.
func (e *Engine) Upgrade(ctx context.Context, release string, chart ReleaseChart, values map[string]any, manifest string, opts InstallOptions) (*Release, error) {
	if err := validateReleaseName(release); err != nil {
		return nil, err
	}
	revs, err := e.revisions(ctx, release)
	if err != nil {
		return nil, err
	}
	cur := currentRevision(revs)
	if cur == nil || cur.Status == StatusUninstalled {
		if !opts.Install {
			return nil, fmt.Errorf("release '%s' does not exist; use install or upgrade --install", release)
		}
	}
	rel := e.newRevision(release, nextRevision(revs), chart, values, manifest, opts.Files)
	return e.deployAndRecord(ctx, rel, opts)
}

// Rollback deploys a new revision whose content is copied from a previous
// revision (append-only, mirroring Helm). targetRev must be an existing,
// non-failed revision.
func (e *Engine) Rollback(ctx context.Context, release string, targetRev int, opts InstallOptions) (*Release, error) {
	revs, err := e.revisions(ctx, release)
	if err != nil {
		return nil, err
	}
	if len(revs) == 0 {
		return nil, fmt.Errorf("release '%s' not found", release)
	}
	var target *Release
	for i := range revs {
		if revs[i].Revision == targetRev {
			target = &revs[i]
		}
	}
	if target == nil {
		return nil, fmt.Errorf("release '%s' has no revision %d", release, targetRev)
	}
	if target.Status == StatusFailed {
		return nil, fmt.Errorf("cannot roll back to failed revision %d", targetRev)
	}
	// target.Files, never opts.Files: a rollback is a replay, not a re-resolution.
	// Nothing on this path has a chart — no ChartSource, no re-render, not even a
	// filesystem — so the stored record is the only thing that knows what the
	// manifest being replayed refers to, and deploying it without those bytes
	// deploys a manifest naming files that are gone.
	rel := e.newRevision(release, nextRevision(revs), target.Chart, target.Values, target.Manifest, target.Files)
	return e.deployAndRecord(ctx, rel, opts)
}

// History returns every stored revision of a release, ascending, with derived
// display statuses.
func (e *Engine) History(ctx context.Context, release string) ([]Release, error) {
	revs, err := e.revisions(ctx, release)
	if err != nil {
		return nil, err
	}
	if len(revs) == 0 {
		return nil, fmt.Errorf("release '%s' not found", release)
	}
	return revs, nil
}

// GetRevision returns a specific revision of a release, or the current one when
// rev <= 0.
func (e *Engine) GetRevision(ctx context.Context, release string, rev int) (*Release, error) {
	revs, err := e.revisions(ctx, release)
	if err != nil {
		return nil, err
	}
	if len(revs) == 0 {
		return nil, fmt.Errorf("release '%s' not found", release)
	}
	if rev <= 0 {
		return currentRevision(revs), nil
	}
	for i := range revs {
		if revs[i].Revision == rev {
			return &revs[i], nil
		}
	}
	return nil, fmt.Errorf("release '%s' has no revision %d", release, rev)
}

// newRevision builds an unsaved, deployed-status revision.
//
// files travels with the manifest rather than beside it because the two are one
// document: the manifest's file: keys are meaningless without the bytes they
// name, and a revision recorded without them is one no rollback can replay.
func (e *Engine) newRevision(release string, rev int, chart ReleaseChart, values map[string]any, manifest string, files map[string][]byte) *Release {
	return &Release{
		Name:      release,
		Revision:  rev,
		Status:    StatusDeployed,
		Chart:     chart,
		Values:    values,
		Manifest:  manifest,
		Files:     files,
		Namespace: release,
		Created:   e.now().UTC().Format(time.RFC3339),
	}
}

// deployAndRecord deploys a revision's manifest and records it. On DryRun it
// returns the prospective revision without touching Docker. On deploy failure
// it records nothing, leaving the release retryable (no orphaned revision).
func (e *Engine) deployAndRecord(ctx context.Context, rel *Release, opts InstallOptions) (*Release, error) {
	// Stamp before the dry-run return so a plan shows the ownership it would
	// record, and before any mutation so a malformed owner fails for free.
	// A revision is stamped with whoever wrote it, not with whoever wrote the
	// previous one: an upgrade or rollback by a second manifest takes the
	// release over, and the history shows exactly where the handover happened.
	if opts.Owner != "" {
		if err := validateOwnerID(opts.Owner); err != nil {
			rel.Status = StatusFailed
			return rel, err
		}
		rel.Owner = OwnerRef{ID: opts.Owner, Kind: OwnerKindRelease, Name: rel.Name}.String()
	}
	if opts.DryRun {
		return rel, nil
	}
	// Measure the record before anything is mutated. record() runs after the
	// deploy, so an over-limit payload used to fail once the stack was already
	// up — and a release with no record is invisible to list, history, status
	// and rollback, which is worse than not deploying at all. The bytes are
	// thrown away: storeRevision re-encodes per attempt because record() bumps
	// the revision number on a collision, so this is an early refusal and
	// storeRevision's own check remains the one that gates the write.
	if _, err := encodeRevision(rel); err != nil {
		rel.Status = StatusFailed
		return rel, err
	}
	// Validate prerequisites that cannot be auto-created (external secrets and
	// configs) before mutating any swarm state, so a missing one fails fast
	// without leaving auto-created networks behind.
	if err := e.ensureExternalSecretsConfigs(ctx, rel.Manifest, opts.Requirements); err != nil {
		rel.Status = StatusFailed
		return rel, err
	}
	created, err := e.ensureExternalNetworks(ctx, rel.Manifest, opts.Requirements)
	if err != nil {
		rel.Status = StatusFailed
		for _, n := range created {
			_ = e.Backend.RemoveOverlayNetwork(ctx, n)
		}
		return rel, err
	}
	// rel.Files rather than opts.Files: the revision is the one thing every entry
	// point has already agreed on, and on a rollback it is the only one that
	// carries files at all.
	if err := e.Backend.DeployStack(ctx, DeployRequest{
		Name: rel.Name, Manifest: rel.Manifest, Resolve: opts.ResolveImage, Files: rel.Files,
	}); err != nil {
		rel.Status = StatusFailed
		// Roll back networks we auto-created for this install so a failed deploy
		// leaves no trace; pre-existing networks are untouched.
		for _, n := range created {
			_ = e.Backend.RemoveOverlayNetwork(ctx, n)
		}
		// Do not record on failure: a failed deploy must leave no release Config
		// behind, so the release stays retryable (no orphaned "already exists").
		return rel, fmt.Errorf("deploy failed: %w", err)
	}
	// The deploy mutated swarm state; invalidate the shared cache so the
	// convergence poll and any follow-up status read see fresh data.
	_ = e.Backend.RefreshSnapshot(ctx)
	// Persist the networks we auto-created this revision so uninstall can report
	// what it leaves behind (see Uninstall). Networks already present from an
	// earlier revision are not re-created here, so uninstall unions across all
	// revisions.
	rel.ManagedNetworks = created
	if err := e.record(ctx, rel); err != nil {
		return rel, fmt.Errorf("stack '%s' was deployed but recording its release history failed: %w; re-run install/upgrade to reconcile", rel.Name, err)
	}
	if opts.Wait {
		if err := e.waitReady(ctx, rel.Name, opts.Timeout); err != nil {
			return rel, err
		}
	}
	if opts.HistoryMax > 0 {
		e.pruneHistory(ctx, rel.Name, opts.HistoryMax)
	}
	return rel, nil
}

// ensureExternalNetworks makes sure every network the manifest declares
// external exists and is swarm-scoped before the stack is deployed.
//
// When the chart ships a requirements.yaml (req != nil) it is authoritative:
// every external network the manifest references must be declared there, and the
// declaration drives behaviour — autoCreate:true networks are created with the
// declared driver/attachability, autoCreate:false networks are only validated
// (a missing one is a hard error with the declared description, never created).
// Without a requirements.yaml (req == nil) it falls back to the historical
// behaviour: create any missing network as an attachable overlay.
//
// Networks that exist with a non-swarm scope, that cannot be created, or that
// are required-present-but-missing are reported with the manual remediation
// needed to resolve them. ensureExternalNetworks returns the names of networks
// it auto-created, so a caller can roll them back if a later step (the deploy)
// fails.
func (e *Engine) ensureExternalNetworks(ctx context.Context, manifest string, req *Requirements) (created []string, err error) {
	names, err := externalNetworks(manifest)
	if err != nil || len(names) == 0 {
		return nil, err
	}
	// Enforce the requirements.yaml contract first: an undeclared external
	// resource is a chart-authoring error, distinct from one being unavailable.
	if err := requireDeclared(req, names, "network", "networks"); err != nil {
		return nil, err
	}
	scopes, err := e.Backend.NetworkScopes(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking external networks: %w", err)
	}
	var reasons, fixes []string
	for _, name := range names {
		nr, declared := req.network(name) // declared == (req != nil), per the contract check above
		scope, exists := scopes[name]
		switch {
		case exists && scope == "swarm":
			// already usable
		case exists:
			reasons = append(reasons,
				fmt.Sprintf("  %s: a non-swarm (%s) network of this name already exists; remove or rename it", name, scope))
		case declared && !*nr.AutoCreate:
			// Validate-only: the chart depends on a network it must not create.
			reasons = append(reasons, fmt.Sprintf("  %s: does not exist%s", name, describe(nr.Description)))
			fixes = append(fixes, fmt.Sprintf("  docker network create --driver %s%s %s", networkDriver(nr), attachableFlag(nr), name))
		default:
			driver, attachable := "overlay", true
			if declared {
				driver, attachable = networkDriver(nr), *nr.Attachable
			}
			if cerr := e.Backend.CreateOverlayNetwork(ctx, name, driver, attachable); cerr != nil {
				reasons = append(reasons, fmt.Sprintf("  %s: auto-create failed: %v", name, cerr))
				fixes = append(fixes, fmt.Sprintf("  docker network create --driver %s%s %s", driver, attachableFlagBool(attachable), name))
			} else {
				created = append(created, name)
			}
		}
	}
	if len(reasons) == 0 {
		return created, nil
	}
	msg := "external network(s) required by this chart are unavailable:\n" + strings.Join(reasons, "\n")
	if len(fixes) > 0 {
		msg += "\ncreate them on a swarm manager, then retry:\n" + strings.Join(fixes, "\n")
	}
	return created, fmt.Errorf("%s", msg)
}

// requireDeclared enforces the requirements.yaml contract: when req is non-nil,
// every external resource name the manifest references must be declared in it.
// It returns a chart-authoring error listing any undeclared names, or nil when
// req is nil (no requirements.yaml — manifest-driven fallback) or all declared.
func requireDeclared(req *Requirements, names []string, kind, key string) error {
	if req == nil {
		return nil
	}
	var undeclared []string
	for _, n := range names {
		var ok bool
		switch key {
		case "networks":
			_, ok = req.network(n)
		case "secrets":
			_, ok = req.secret(n)
		case "configs":
			_, ok = req.config(n)
		}
		if !ok {
			undeclared = append(undeclared, fmt.Sprintf("  %s", n))
		}
	}
	if len(undeclared) == 0 {
		return nil
	}
	return fmt.Errorf("%s(s) the manifest declares external are not declared in %s:\n%s\n"+
		"declare them under %s: in %s (it is authoritative when present)",
		kind, requirementsName, strings.Join(undeclared, "\n"), key, requirementsName)
}

// networkDriver returns the declared driver, defaulting to overlay (defaults are
// normally applied at parse time; this guards direct callers/tests).
func networkDriver(nr *NetworkRequirement) string {
	if nr.Driver == "" {
		return "overlay"
	}
	return nr.Driver
}

func attachableFlag(nr *NetworkRequirement) string {
	return attachableFlagBool(nr.Attachable == nil || *nr.Attachable)
}

func attachableFlagBool(attachable bool) string {
	if attachable {
		return " --attachable"
	}
	return ""
}

// describe renders an optional requirement description as a trailing " (…)"
// clause for remediation messages, or "" when absent.
func describe(desc string) string {
	if desc == "" {
		return ""
	}
	return " (" + desc + ")"
}

// ensureExternalSecretsConfigs verifies that every secret and config the
// manifest declares external already exists on the swarm. Unlike networks,
// these cannot be auto-created — their content is not part of the chart — so a
// missing one is a hard error that lists the `docker secret/config create`
// commands needed to resolve it. A manifest with no external secrets/configs is
// a no-op.
//
// When the chart ships a requirements.yaml (req != nil) it is authoritative:
// every external secret/config the manifest references must be declared there
// (else a hard error), and a declared description enriches the remediation.
func (e *Engine) ensureExternalSecretsConfigs(ctx context.Context, manifest string, req *Requirements) error {
	secrets, err := externalResourceNames(manifest, "secrets")
	if err != nil {
		return err
	}
	configs, err := externalResourceNames(manifest, "configs")
	if err != nil {
		return err
	}
	if len(secrets) == 0 && len(configs) == 0 {
		return nil
	}
	// Enforce the requirements.yaml contract first (chart-authoring error),
	// separately from the existence check below (operator remediation).
	if err := requireDeclared(req, secrets, "secret", "secrets"); err != nil {
		return err
	}
	if err := requireDeclared(req, configs, "config", "configs"); err != nil {
		return err
	}

	var reasons, cmds []string

	if len(secrets) > 0 {
		have, err := e.Backend.SecretNames(ctx)
		if err != nil {
			return fmt.Errorf("checking external secrets: %w", err)
		}
		for _, name := range secrets {
			rr, _ := req.secret(name)
			if _, ok := have[name]; !ok {
				reasons = append(reasons, fmt.Sprintf("  secret '%s' does not exist%s", name, requirementDescription(rr)))
				cmds = append(cmds, fmt.Sprintf("  docker secret create %s <file>", name))
			}
		}
	}

	if len(configs) > 0 {
		metas, err := e.Backend.ListConfigs(ctx)
		if err != nil {
			return fmt.Errorf("checking external configs: %w", err)
		}
		have := make(map[string]struct{}, len(metas))
		for _, m := range metas {
			have[m.Name] = struct{}{}
		}
		for _, name := range configs {
			rr, _ := req.config(name)
			if _, ok := have[name]; !ok {
				reasons = append(reasons, fmt.Sprintf("  config '%s' does not exist%s", name, requirementDescription(rr)))
				cmds = append(cmds, fmt.Sprintf("  docker config create %s <file>", name))
			}
		}
	}

	if len(reasons) == 0 {
		return nil
	}
	return fmt.Errorf("external secret(s)/config(s) required by this chart do not exist:\n%s\n"+
		"create them on a swarm manager, then retry:\n%s",
		strings.Join(reasons, "\n"), strings.Join(cmds, "\n"))
}

// requirementDescription renders a resource requirement's description as a
// trailing " (…)" clause, or "" when there is no requirement or description.
func requirementDescription(rr *ResourceRequirement) string {
	if rr == nil {
		return ""
	}
	return describe(rr.Description)
}

// UninstallResult reports what an uninstall left behind. OrphanedNetworks are
// the external networks swarmcli auto-created for the release that still exist
// after the stack is removed — `docker stack rm` does not remove external
// networks, and swarmcli deliberately leaves them (they may be shared with other
// stacks) and reports them instead.
type UninstallResult struct {
	OrphanedNetworks []string
}

// Uninstall removes the release's stack and its recorded revisions, retaining
// volumes unless purgeVolumes is set. It returns an UninstallResult describing
// the auto-created external networks left in place so the caller can surface
// them; the networks themselves are not removed.
func (e *Engine) Uninstall(ctx context.Context, release string, purgeVolumes bool) (*UninstallResult, error) {
	revs, err := e.revisions(ctx, release)
	if err != nil {
		return nil, err
	}
	if len(revs) == 0 {
		return nil, fmt.Errorf("release '%s' not found", release)
	}

	// Continue cleanup on partial failure rather than aborting: a stranded
	// history Config or volume must not be left behind because an earlier step
	// failed (e.g. a retry where the stack is already gone). Errors are
	// aggregated and returned together.
	var errs []error
	if err := e.Backend.RemoveStack(ctx, release); err != nil {
		errs = append(errs, fmt.Errorf("removing stack: %w", err))
	} else {
		_ = e.Backend.RefreshSnapshot(ctx)
	}

	if purgeVolumes {
		if vols, err := e.Backend.StackVolumes(ctx, release); err != nil {
			errs = append(errs, fmt.Errorf("listing volumes: %w", err))
		} else {
			for _, v := range vols {
				if err := e.Backend.RemoveVolume(ctx, v); err != nil {
					errs = append(errs, fmt.Errorf("removing volume '%s': %w", v, err))
				}
			}
		}
	}

	// Collect the networks swarmcli auto-created across all revisions (a network
	// created in an early revision is not re-created later, so it only appears on
	// that revision's record — union them), keeping only those that still exist.
	result := &UninstallResult{OrphanedNetworks: e.orphanedManagedNetworks(ctx, revs)}

	for _, r := range revs {
		if err := e.Backend.DeleteConfig(ctx, releaseConfigName(release, r.Revision)); err != nil {
			errs = append(errs, fmt.Errorf("deleting release config: %w", err))
		}
	}
	return result, errors.Join(errs...)
}

// orphanedManagedNetworks returns the sorted, de-duplicated set of networks
// swarmcli auto-created for the release (recorded per-revision) that still exist
// on the swarm. A failure to query existing networks degrades to reporting the
// recorded union (better to over-report a cleanup hint than to hide it).
func (e *Engine) orphanedManagedNetworks(ctx context.Context, revs []Release) []string {
	seen := map[string]struct{}{}
	var managed []string
	for _, r := range revs {
		for _, n := range r.ManagedNetworks {
			if _, ok := seen[n]; !ok {
				seen[n] = struct{}{}
				managed = append(managed, n)
			}
		}
	}
	if len(managed) == 0 {
		return nil
	}
	scopes, err := e.Backend.NetworkScopes(ctx)
	if err != nil {
		sort.Strings(managed)
		return managed
	}
	var still []string
	for _, n := range managed {
		if _, ok := scopes[n]; ok {
			still = append(still, n)
		}
	}
	sort.Strings(still)
	return still
}

// List returns the current (highest) revision of every release.
func (e *Engine) List(ctx context.Context) ([]Release, error) {
	byRelease, err := e.allRevisions(ctx)
	if err != nil {
		return nil, err
	}
	var out []Release
	for _, revs := range byRelease {
		if cur := currentRevision(revs); cur != nil && cur.Status != StatusUninstalled {
			out = append(out, *cur)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AllRevisions returns every stored revision, grouped by release name and
// ascending within each group, with display statuses derived the same way
// History derives them.
//
// It exists for a caller that needs the current revision of every release AND
// the history of any of them — a browser view, where List followed by a
// History per expanded release would decode every stored revision twice.
// Unlike List, nothing is filtered: a release whose current revision is
// uninstalled is present, because a caller asking for all revisions is asking
// about the record, not about what is deployed.
func (e *Engine) AllRevisions(ctx context.Context) (map[string][]Release, error) {
	return e.allRevisions(ctx)
}

// Status returns the current release plus live service states for its stack.
func (e *Engine) Status(ctx context.Context, release string) (*Release, []ServiceState, error) {
	revs, err := e.revisions(ctx, release)
	if err != nil {
		return nil, nil, err
	}
	cur := currentRevision(revs)
	if cur == nil {
		return nil, nil, fmt.Errorf("release '%s' not found", release)
	}
	return cur, e.Backend.StackServices(ctx, release), nil
}

// --- helpers ---

// revisions returns all stored revisions for one release, ascending.
func (e *Engine) revisions(ctx context.Context, release string) ([]Release, error) {
	all, err := e.allRevisions(ctx)
	if err != nil {
		return nil, err
	}
	return all[release], nil
}

// allRevisions loads every release revision, grouped by release name (ascending).
func (e *Engine) allRevisions(ctx context.Context) (map[string][]Release, error) {
	metas, err := e.Backend.ListConfigs(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]Release{}
	for _, m := range metas {
		if m.Labels[LabelType] != TypeRelease {
			continue
		}
		data := m.Data
		if data == nil {
			d, err := e.Backend.InspectConfig(ctx, m.Name)
			if err != nil {
				return nil, fmt.Errorf("read release config '%s': %w", m.Name, err)
			}
			data = d
		}
		rel, err := decodeRelease(data)
		if err != nil {
			return nil, fmt.Errorf("decode release config '%s': %w", m.Name, err)
		}
		out[rel.Name] = append(out[rel.Name], *rel)
	}
	for name := range out {
		revs := out[name]
		sort.Slice(revs, func(i, j int) bool { return revs[i].Revision < revs[j].Revision })
		out[name] = deriveStatuses(revs)
	}
	return out, nil
}

// deriveStatuses computes display statuses from the immutable append-only log:
// the highest revision keeps its stored status; every lower revision that was
// deployed becomes "superseded".
func deriveStatuses(revs []Release) []Release {
	if len(revs) == 0 {
		return revs
	}
	top := len(revs) - 1
	for i := range revs {
		if i != top && revs[i].Status == StatusDeployed {
			revs[i].Status = StatusSuperseded
		}
	}
	return revs
}

func currentRevision(revs []Release) *Release {
	if len(revs) == 0 {
		return nil
	}
	r := revs[len(revs)-1]
	return &r
}

func nextRevision(revs []Release) int {
	if len(revs) == 0 {
		return 1
	}
	return revs[len(revs)-1].Revision + 1
}

// maxRecordRetries bounds the TOCTOU retry when a concurrent install/upgrade
// claims the same revision number.
const maxRecordRetries = 5

// record stores a release revision as a gzipped, labeled Docker Config. The
// revision number is computed before deploy, so a concurrent install/upgrade
// can claim the same name; CreateConfig is atomic on the name, so on collision
// we re-read the history and retry with the next free revision, keeping the
// append-only log consistent.
func (e *Engine) record(ctx context.Context, rel *Release) error {
	for attempt := 0; attempt <= maxRecordRetries; attempt++ {
		err := e.storeRevision(ctx, rel)
		if err == nil || !isAlreadyExists(err) {
			return err
		}
		revs, rerr := e.revisions(ctx, rel.Name)
		if rerr != nil {
			return err // surface the original collision error
		}
		rel.Revision = nextRevision(revs)
	}
	return fmt.Errorf("could not allocate a free revision for release '%s' after %d attempts", rel.Name, maxRecordRetries)
}

// isAlreadyExists reports whether err is a Docker "config already exists"
// conflict (the name was claimed concurrently).
func isAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}

// releaseFiles is Release.Files as it is stored: base64 in YAML, one string per
// file, rather than what yaml.v3 does with a []byte left to itself.
//
// yaml.v3 has no special case for []byte. It sees a slice of integers and
// writes a sequence node per byte — "files/a.conf:\n    - 104\n    - 101" —
// which is roughly 13 characters of YAML for every byte of content, and gzip
// only partly undoes it because what it then compresses is decimal digits
// rather than the content's own redundancy. Measured through this exact path
// (yaml.Marshal then gzipBytes) with 100 KiB of nginx-style config: 1,378,425
// bytes of YAML and 18,365 gzipped, against 136,823 and 5,784 for base64 —
// where gzipping the content on its own is 4,356. For incompressible content, a
// certificate or a keyring, the integer sequence costs ~1.7x the raw size
// gzipped (172,006 for 100 KiB) where base64 costs ~1.006x (103,090).
//
// That matters because the budget is not notional: the record is one Docker
// Config, maxConfigPayload is measured after gzip, and a chart's files spend
// that budget alongside the rendered manifest they belong to. Every byte the
// encoding wastes is a byte of chart content that cannot ship.
//
// It is fixed here rather than later because this is a stored format. A record
// written under one encoding has to be readable by whatever reads it next —
// history, status, rollback — so changing it after releases exist in the field
// means either a migration or a reader that understands both. There is no
// version marker in the payload to hang that off.
//
// Nil and empty both marshal to nil, so `omitempty` still elides the key and a
// release with no files stores exactly the bytes it stored before this type
// existed; both decode back to nil, so a rollback of such a release deploys no
// files rather than an empty map. encoding/json needs none of this — it already
// encodes []byte as base64 — so Release's JSON shape is untouched.
type releaseFiles map[string][]byte

func (f releaseFiles) MarshalYAML() (any, error) {
	if len(f) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(f))
	for name, body := range f {
		out[name] = base64.StdEncoding.EncodeToString(body)
	}
	return out, nil
}

// UnmarshalYAML decodes the stored form, and refuses a value that is not
// base64 instead of yielding empty content for it. Silence there would be the
// worst possible failure: a corrupt record would read as "this release shipped
// an empty file", and a rollback would deploy that emptiness over a config that
// was fine.
func (f *releaseFiles) UnmarshalYAML(value *yaml.Node) error {
	var encoded map[string]string
	if err := value.Decode(&encoded); err != nil {
		return err
	}
	if len(encoded) == 0 {
		*f = nil
		return nil
	}
	out := make(releaseFiles, len(encoded))
	for name, body := range encoded {
		raw, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return fmt.Errorf("release file '%s': %w", name, err)
		}
		out[name] = raw
	}
	*f = out
	return nil
}

// encodeRevision is the record exactly as it would be stored: marshalled,
// gzipped, and refused if it does not fit a Docker Config.
//
// It is called twice per deploy on purpose — once by deployAndRecord before it
// mutates anything, once by storeRevision for the write — because the two
// answers can legitimately differ: record() bumps rel.Revision when a
// concurrent actor claims the number, which changes the payload. Encoding once
// and reusing the bytes would store a payload naming a revision the Config is
// not.
func encodeRevision(rel *Release) ([]byte, error) {
	payload, err := yaml.Marshal(rel)
	if err != nil {
		return nil, err
	}
	gz, err := gzipBytes(payload)
	if err != nil {
		return nil, err
	}
	if len(gz) > maxConfigPayload {
		return nil, oversizeRecord(rel, len(gz))
	}
	return gz, nil
}

// oversizeRecord explains a release that will not fit its Config.
//
// It itemises the manifest and every file the manifest names, because gzipped
// total is the one number an operator cannot act on: the budget is spent by
// uncompressed content they choose, and until this refusal moved ahead of the
// deploy nobody had to be told what to shrink — the stack was already up.
func oversizeRecord(rel *Release, gz int) error {
	var b strings.Builder
	fmt.Fprintf(&b, "release '%s' revision %d cannot be recorded: its payload is %d bytes gzipped, exceeding the %d-byte Docker Config limit\n",
		rel.Name, rel.Revision, gz, maxConfigPayload)
	fmt.Fprintf(&b, "  manifest: %d bytes\n", len(rel.Manifest))
	for _, name := range slices.Sorted(maps.Keys(rel.Files)) {
		fmt.Fprintf(&b, "  %s: %d bytes\n", name, len(rel.Files[name]))
	}
	b.WriteString("shrink the manifest or the chart files it references (sizes shown uncompressed), " +
		"or keep large content out of the chart: create it with docker config create / docker secret create " +
		"and reference it with external: true")
	return errors.New(b.String())
}

// storeRevision writes a single release revision as a gzipped, labeled Config.
func (e *Engine) storeRevision(ctx context.Context, rel *Release) error {
	gz, err := encodeRevision(rel)
	if err != nil {
		return err
	}
	labels := map[string]string{
		LabelType:         TypeRelease,
		LabelRelease:      rel.Name,
		LabelChart:        rel.Chart.Name,
		LabelChartVersion: rel.Chart.Version,
		LabelRevision:     strconv.Itoa(rel.Revision),
		LabelStatus:       rel.Status,
		LabelCreated:      rel.Created,
	}
	// Absent rather than empty when unowned: a prune filtering on the label's
	// presence must not match a release that merely carries a blank one.
	if rel.Owner != "" {
		labels[LabelOwner] = rel.Owner
	}
	return e.Backend.CreateConfig(ctx, releaseConfigName(rel.Name, rel.Revision), gz, labels)
}

// PruneAction is the keep/delete decision for one revision in a prune.
type PruneAction struct {
	Revision int
	Delete   bool
	Current  bool // the live (highest) revision; never deleted
}

// PruneResult reports what a prune did (or, for a dry run, would do) for one
// release. Actions are ascending by revision.
type PruneResult struct {
	Release string
	Actions []PruneAction
}

// Deleted returns the revision numbers Prune removed (or would remove).
func (r PruneResult) Deleted() []int {
	var out []int
	for _, a := range r.Actions {
		if a.Delete {
			out = append(out, a.Revision)
		}
	}
	return out
}

// planPrune decides, for revisions sorted ascending, which to delete to retain
// the newest keep. keep <= 0 keeps everything. The highest (current) revision is
// always retained regardless of keep, so the live release is never destroyed.
func planPrune(revs []Release, keep int) []PruneAction {
	actions := make([]PruneAction, len(revs))
	top := len(revs) - 1
	for i, r := range revs {
		current := i == top
		del := keep > 0 && i < len(revs)-keep && !current
		actions[i] = PruneAction{Revision: r.Revision, Delete: del, Current: current}
	}
	return actions
}

// Prune deletes superseded revisions of one release beyond the keep window,
// always retaining the current (highest) revision. keep <= 0 keeps everything.
// On a dry run no Config is touched. Deletion failures are aggregated and
// returned, but pruning of the remaining revisions continues.
func (e *Engine) Prune(ctx context.Context, release string, keep int, dryRun bool) (PruneResult, error) {
	revs, err := e.revisions(ctx, release)
	if err != nil {
		return PruneResult{}, err
	}
	if len(revs) == 0 {
		return PruneResult{}, fmt.Errorf("release '%s' not found", release)
	}
	return e.pruneRevs(ctx, release, revs, keep, dryRun)
}

// pruneRevs applies the retention plan to a release whose revisions are already
// loaded (ascending, non-empty). Splitting this out lets PruneAll prune from the
// single allRevisions load instead of re-reading every config per release.
func (e *Engine) pruneRevs(ctx context.Context, release string, revs []Release, keep int, dryRun bool) (PruneResult, error) {
	res := PruneResult{Release: release, Actions: planPrune(revs, keep)}
	if dryRun {
		return res, nil
	}
	var errs []error
	for _, a := range res.Actions {
		if !a.Delete {
			continue
		}
		if derr := e.Backend.DeleteConfig(ctx, releaseConfigName(release, a.Revision)); derr != nil {
			errs = append(errs, fmt.Errorf("deleting %s: %w", releaseConfigName(release, a.Revision), derr))
		}
	}
	return res, errors.Join(errs...)
}

// PruneAll prunes every release to the keep window, returning one result per
// release (sorted by name). Per-release errors are aggregated; a failing release
// does not stop the others.
func (e *Engine) PruneAll(ctx context.Context, keep int, dryRun bool) ([]PruneResult, error) {
	byRelease, err := e.allRevisions(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(byRelease))
	for name := range byRelease {
		names = append(names, name)
	}
	sort.Strings(names)
	var results []PruneResult
	var errs []error
	for _, name := range names {
		res, perr := e.pruneRevs(ctx, name, byRelease[name], keep, dryRun)
		if perr != nil {
			errs = append(errs, perr)
		}
		results = append(results, res)
	}
	return results, errors.Join(errs...)
}

// pruneHistory trims a release's history to keep revisions after a successful
// deploy. It is best-effort: deletion errors are ignored because the deploy
// itself already succeeded and recorded. Retention logic is shared with Prune
// via planPrune, so the keep <= 0 / current-revision guarantees hold here too.
func (e *Engine) pruneHistory(ctx context.Context, release string, keep int) {
	revs, err := e.revisions(ctx, release)
	if err != nil {
		return
	}
	for _, a := range planPrune(revs, keep) {
		if a.Delete {
			_ = e.Backend.DeleteConfig(ctx, releaseConfigName(release, a.Revision))
		}
	}
}

// waitPollInterval is how often waitReady re-reads service state. A variable so
// tests can shrink it; a real sleep in a unit test is otherwise unavoidable
// because the stability window is wall-clock.
var waitPollInterval = 2 * time.Second

// defaultStabilityWindow mirrors the Docker CLI's own default monitor period.
// Reaching the target replica count is necessary but not sufficient: a task that
// starts and then dies inside UpdateConfig.Monitor counts as a rollout failure,
// so returning at first parity reports success for a service that is about to
// crash-loop.
const defaultStabilityWindow = 5 * time.Second

// waitReady polls until every service in the release is running its target task
// count and has held it for the stability window, or the timeout elapses.
func (e *Engine) waitReady(ctx context.Context, release string, timeout time.Duration) error {
	return e.waitWave(ctx, []string{release}, timeout)
}

// waitWave polls until every named release has converged, or the timeout elapses.
//
// One deadline covers the whole wave rather than one per release. The releases in
// a wave were deployed one after another without waiting, so their rollouts
// overlap; charging the wave the sum of what it spends in parallel would make the
// timeout mean something different depending on how many releases happened to
// share a wave.
//
// It returns early with an error when swarm reports any of them wedged
// ("paused" / "rollback_paused" / "rollback_completed"): swarm will not
// continue from any of them on its own — the paused pair needs a human, and a
// completed rollback is a deploy that already failed and was undone — so
// waiting out the timeout only delays the same answer.
//
// A cancelled ctx ends the wait with the context's own error rather than running
// on to the timeout. This is the longest thing a release operation does, and the
// timeout it would otherwise serve out defaults to five minutes — far longer than
// the grace period a caller being shut down has to work with.
func (e *Engine) waitWave(ctx context.Context, releases []string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := e.now().Add(timeout)
	// Reused across polls rather than reallocated: this loop runs for as long as
	// a rollout takes, which is minutes.
	pending := make([]string, 0, len(releases))
	for {
		pending = pending[:0]
		for _, release := range releases {
			switch c := Rollup(e.Backend.StackServices(ctx, release)); c.Phase {
			case PhaseWedged:
				// Named individually, and the same message a single-release wait
				// has always produced: the release that wedged is the one to go
				// and look at, not the group it was in.
				return fmt.Errorf("release '%s': %s", release, c.Reason)
			case PhaseConverged:
			default:
				pending = append(pending, release)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		if !e.now().Before(deadline) {
			return fmt.Errorf("timed out after %s waiting for %s to converge", timeout, releaseList(pending))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitPollInterval):
		}
	}
}

// releaseList renders the releases a wait gave up on, singular or plural. One
// release produces exactly the phrase this wait produced before it could take
// more than one, so a caller matching on that text — and the integration suite
// does — is unaffected.
func releaseList(releases []string) string {
	if len(releases) == 1 {
		return fmt.Sprintf("release '%s'", releases[0])
	}
	quoted := make([]string, 0, len(releases))
	for _, r := range releases {
		quoted = append(quoted, fmt.Sprintf("'%s'", r))
	}
	return "releases " + strings.Join(quoted, ", ")
}

// Phase is how far a rollout has got.
type Phase string

const (
	// PhaseConverged means every task is up and has outlived the window in
	// which swarm would still hold its failure against the rollout.
	PhaseConverged Phase = "converged"
	// PhaseProgressing means not yet — and, so far, not a failure either.
	PhaseProgressing Phase = "progressing"
	// PhaseWedged means swarm has given up and will not continue on its own.
	PhaseWedged Phase = "wedged"
)

// Convergence is a phase and, when it is not converged, why.
//
// The reason is written for display: it is the sentence a status view or an API
// response puts next to the phase, so it says what is outstanding rather than
// naming an internal state.
type Convergence struct {
	Phase  Phase  `json:"phase"`
	Reason string `json:"reason,omitempty"`
}

// Convergence classifies one service.
//
// Every rule here has been wrong once, which is why interpreting a ServiceState
// belongs to this package rather than to each caller that holds one.
func (s ServiceState) Convergence() Convergence {
	// A one-shot's task terminates inside the monitor window by design, and
	// swarmkit counts any task that leaves RUNNING in that window as an update
	// failure — a restart policy of none is not special-cased (its updater's
	// failureTriggersAction). So the default failure_action pauses the rollout of
	// every job that has ever run a second time, and the UpdateStatus that
	// results says nothing about the job: a migration that exited 0 and one that
	// exited 3 both leave the service "paused". Judge a job by its own task.
	state := s.UpdateState
	if s.Job && state == "paused" {
		if up := s.Running + s.Completed; up < s.Desired {
			return Convergence{PhaseWedged, fmt.Sprintf("the one-shot task did not complete (%d/%d); swarm paused the rollout", up, s.Desired)}
		}
		// Not "converged" outright: the checks below still apply, and this is
		// only the removal of an UpdateStatus that was never evidence.
		state = ""
	}
	switch state {
	// Swarm never rolls back a rollback, so a paused rollout needs a human;
	// waiting it out only delays the report.
	case "paused":
		return Convergence{PhaseWedged, "update paused after a task failure; swarm will not continue without intervention"}
	case "rollback_paused":
		return Convergence{PhaseWedged, "rollback paused; swarm never rolls back a rollback, so this needs manual recovery"}
	// A finished rollback is terminal too, and it is a FAILED deploy: swarmkit
	// restores the previous spec before it marks the rollback complete, so the
	// tasks that reach parity here are running exactly what the deploy set out
	// to replace. Falling through to the parity check reported that as success,
	// which is how a rolled-back release passed --wait and let a pipeline gating
	// on it go green (issue #526).
	case "rollback_completed":
		return Convergence{PhaseWedged, "update failed and was rolled back; the previous spec is running"}
	// A rolling update in flight is not converged even if the task count
	// already reads N/N: the count may still be the outgoing generation.
	case "updating":
		return Convergence{PhaseProgressing, "rolling update in progress"}
	case "rollback_started":
		return Convergence{PhaseProgressing, "rolling back"}
	}
	// Parity is measured from the actual running count, not the Replicas display
	// string. That string counts every generation on any node, so it can reach
	// parity on tasks this package would not accept — which made --wait return
	// early on a fresh install, where UpdateState is empty and the in-flight arm
	// above cannot fire either (issue #473).
	//
	// A job's task ends Complete and is never replaced, so requiring a running
	// task would report a step that succeeded as one that never came up. A
	// completed task fills its slot; a failed one ends in state Failed and is
	// not counted, so a broken job still blocks (issue #443).
	if up := s.Running + s.Completed; up < s.Desired {
		return Convergence{PhaseProgressing, fmt.Sprintf("%d/%d tasks running", up, s.Desired)}
	}
	// Parity is necessary but not sufficient: a task that starts and then dies
	// inside the monitor window is a rollout failure, so hold until swarm's own
	// window has closed. Losing parity needs no special case — the replacement
	// task is newer, so the window it has left grows back on its own.
	if left := s.stabilityRemaining(); left > 0 {
		return Convergence{PhaseProgressing, fmt.Sprintf("%s of the stability window remains", left.Round(time.Millisecond))}
	}
	return Convergence{Phase: PhaseConverged}
}

// stabilityRemaining is how much of the monitor window this service still has
// outstanding; <= 0 means it has held parity for as long as swarm itself would
// watch.
//
// The remainder is measured from TASK CREATION, not from the moment parity was
// reached. Swarm starts the monitor when it creates the task, and a task only
// reports running once its healthcheck passes, so start_period and the checks
// that followed have already burned part of the window — for a service with a
// long start_period, usually all of it. Restarting a full window at parity
// instead made --wait sit for the monitor period on top of the time the service
// took to come up, which is strictly more conservative than swarm and turns an
// honest `monitor: 8m` into an eight-minute install.
//
// A service declaring no monitor uses the CLI default, matching swarm's own.
func (s ServiceState) stabilityRemaining() time.Duration {
	window := s.Monitor
	if window < defaultStabilityWindow {
		window = defaultStabilityWindow
	}
	return window - s.NewestTaskAge
}

// Rollup reduces a release's services to one answer: the worst phase wins, and
// the reason names the service that produced it.
//
// No services is progressing rather than converged. A release whose stack
// reports nothing has not finished coming up — and reporting it converged would
// let --wait return the instant a deploy was accepted.
func Rollup(states []ServiceState) Convergence {
	if len(states) == 0 {
		return Convergence{PhaseProgressing, "no services are running yet"}
	}
	worst := Convergence{Phase: PhaseConverged}
	for _, s := range states {
		c := s.Convergence()
		if phaseRank(c.Phase) > phaseRank(worst.Phase) {
			worst = Convergence{c.Phase, fmt.Sprintf("service '%s': %s", s.Name, c.Reason)}
		}
	}
	return worst
}

// phaseRank orders phases by how much they should worry the reader, so that one
// wedged service is not masked by another that is merely slow.
func phaseRank(p Phase) int {
	switch p {
	case PhaseWedged:
		return 2
	case PhaseProgressing:
		return 1
	default:
		return 0
	}
}

func releaseConfigName(release string, rev int) string {
	return fmt.Sprintf("swarmcli.release.%s.v%d", release, rev)
}

func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeRelease(gz []byte) (*Release, error) {
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	raw, err := io.ReadAll(io.LimitReader(r, maxChartFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxChartFileSize {
		return nil, fmt.Errorf("release payload exceeds %d bytes", maxChartFileSize)
	}
	var rel Release
	if err := yaml.Unmarshal(raw, &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// validateReleaseName mirrors Docker stack naming constraints.
func validateReleaseName(name string) error {
	if name == "" {
		return fmt.Errorf("release name is required")
	}
	if !isPlainName(name) {
		return fmt.Errorf("invalid release name '%s': use letters, digits, '-', '_', '.'", name)
	}
	return nil
}

// isPlainName reports whether name is made only of letters, digits, '-', '_' and
// '.'. A release name and a repository name are constrained for unrelated reasons
// — Docker stack naming for one, a path component and a chart reference for the
// other — but to the same charset, and one spelling of it is enough.
func isPlainName(name string) bool {
	for _, r := range name {
		if !(r == '-' || r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
