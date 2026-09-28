// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ValidateStackYAML validates that the provided YAML content is a valid Docker Compose file.
func ValidateStackYAML(content string) error {
	// Parse YAML to ensure it's valid
	var data interface{}
	if err := yaml.Unmarshal([]byte(content), &data); err != nil {
		return fmt.Errorf("invalid YAML syntax: %w", err)
	}

	// Check if it looks like a compose file (should have "services" or "version")
	if m, ok := data.(map[string]interface{}); ok {
		// A valid compose file should have at least "services" or "version"
		hasServices := m["services"] != nil
		hasVersion := m["version"] != nil

		if !hasServices && !hasVersion {
			return fmt.Errorf("invalid compose file: missing 'services' and 'version' keys")
		}

		// If it has services, try to parse them
		if services, ok := m["services"].(map[string]interface{}); ok {
			if len(services) == 0 {
				return fmt.Errorf("invalid compose file: 'services' section is empty")
			}
		}
	} else if data == nil {
		return fmt.Errorf("empty YAML content")
	} else {
		return fmt.Errorf("invalid compose file format")
	}

	return nil
}

// ResolveImage selects how the daemon resolves image tags to digests at deploy
// time, mirroring `docker stack deploy --resolve-image`.
//
// The empty value passes no flag, leaving Docker's own default of "always": the
// manager queries the registry for every service on every deploy. That makes a
// deploy fail when the registry is unreachable even though every node already
// has the image, and it rewrites the spec to repo:tag@sha256:..., so anything
// diffing desired against live sees every service as changed.
//
// "changed" re-resolves only when the compose file's image string differs from
// the com.docker.stack.image label the CLI stashes, which is what a reconciler
// wants. "never" has an open upstream rollout bug (moby#51658).
type ResolveImage string

const (
	ResolveImageDefault ResolveImage = ""
	ResolveImageAlways  ResolveImage = "always"
	ResolveImageChanged ResolveImage = "changed"
	ResolveImageNever   ResolveImage = "never"
)

// Valid reports whether r is a mode the docker CLI accepts.
func (r ResolveImage) Valid() bool {
	switch r {
	case ResolveImageDefault, ResolveImageAlways, ResolveImageChanged, ResolveImageNever:
		return true
	}
	return false
}

// DeployStack deploys a stack with the provided name and YAML content, leaving
// image resolution at Docker's default. See DeployStackResolved.
//
// It is the TUI's entry point, and a Bubble Tea command has no context to
// inherit, so the background one is spelled out here instead of being pushed
// onto every caller. Everything below this takes a context.
func DeployStack(stackName string, yamlContent string) error {
	return DeployStackResolved(context.Background(), stackName, yamlContent, ResolveImageDefault)
}

// DeployStackResolved deploys a stack with an explicit image-resolution mode,
// on whichever context the process is pointed at.
func DeployStackResolved(ctx context.Context, stackName string, yamlContent string, resolve ResolveImage) error {
	ctxName, err := GetDockerContext()
	if err != nil {
		return fmt.Errorf("failed to get docker context: %w", err)
	}
	// nil files: the TUI's raw-editor path deploys a document the operator typed,
	// with no chart behind it to resolve a file: against. No options either: that
	// document is the operator's own, so it keeps the operator's environment.
	return DeployStackInContext(ctx, ctxName, stackName, yamlContent, resolve, nil, DeployOptions{})
}

// DeployOptions are the parts of a deploy only some callers set. The zero value
// deploys exactly as `docker stack deploy` would from the invoking shell.
type DeployOptions struct {
	// UnsetEnv names variables the docker CLI must not inherit from this
	// process. The CLI fills a stack's empty environment values from its own
	// environment, so a name withheld here deploys as the empty value the
	// manifest declared. Compared without regard to case on Windows, where
	// variable names are case-insensitive.
	UnsetEnv []string
}

// credentialEnvPrefixes are the variable families registry credential helpers
// read. Withholding one can change the identity a deploy resolves images with,
// so doing so is logged.
var credentialEnvPrefixes = []string{"AWS_", "GOOGLE_", "CLOUDSDK_", "AZURE_"}

// DeployStackInContext deploys a stack to an explicitly named Docker context.
//
// `docker stack deploy` has no SDK equivalent, so this shells out; naming the
// context is what keeps the target an argument rather than the session pin the
// whole process shares.
//
// Cancelling ctx kills the child. Nothing else can reach it: the CLI holds its
// own connection to the daemon, so a caller being torn down while the daemon is
// unresponsive would otherwise sit on a `docker stack deploy` that never returns.
//
// files are the chart files the manifest's file: and env_file: keys name, keyed
// by their slash-separated chart-relative path; they are written beside the
// manifest so those keys resolve to them. nil for a manifest that names none.
func DeployStackInContext(ctx context.Context, ctxName, stackName, yamlContent string, resolve ResolveImage, files map[string][]byte, opts DeployOptions) error {
	if ctxName == "" {
		return fmt.Errorf("docker context name is required")
	}
	if !resolve.Valid() {
		return fmt.Errorf("invalid --resolve-image '%s': want always, changed or never", string(resolve))
	}
	// Validate first
	if err := ValidateStackYAML(yamlContent); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	// Before writeStackTree, because the directory it creates is what a bind
	// source that is not absolute would silently resolve against. Unwrapped: the
	// message states the rule and names the replacement, and is all the operator
	// gets.
	if err := checkBindSources(yamlContent); err != nil {
		return err
	}

	if stackName == "" {
		return fmt.Errorf("stack name cannot be empty")
	}

	// Write the manifest and the chart's files into one temporary directory
	dir, manifestPath, err := writeStackTree(files, yamlContent)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(dir)
	}()

	// Execute docker stack deploy command
	args := []string{"--context", ctxName, "stack", "deploy", "-c", manifestPath}
	if resolve != ResolveImageDefault {
		args = append(args, "--resolve-image", string(resolve))
	}
	args = append(args, stackName)
	for _, name := range opts.UnsetEnv {
		if slices.ContainsFunc(credentialEnvPrefixes, func(p string) bool { return strings.HasPrefix(strings.ToUpper(name), p) }) {
			l().Warnf("Deploying stack %q without %q in the docker CLI's environment, because the stack declares it empty; registry authentication for this deploy may use another identity", stackName, name)
		}
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = deployEnv(os.Environ(), opts.UnsetEnv)

	// Capture output for error reporting
	output, err := cmd.CombinedOutput()
	if err != nil {
		// A killed child reports "signal: killed", which reads as a deploy that
		// failed and would send us into the network cleanup below. Report the
		// cancellation itself so a caller shutting down can tell the two apart
		// and does not spend its remaining seconds tidying up.
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("deploying stack '%s': %w", stackName, cerr)
		}
		// If deployment failed, try to clean up any networks that might have been created
		// This handles the case where a network with the wrong type was created
		if networkNames := extractNetworkNames(yamlContent); len(networkNames) > 0 {
			l().Infof("Deployment failed, attempting to clean up any orphaned networks")
			cleanupNetworks(stackName, networkNames)
		}
		return fmt.Errorf("failed to deploy stack: %w\nOutput: %s", err, string(output))
	}

	l().Infof("Stack %q deployed successfully", stackName)
	return nil
}

// deployEnv returns environ without the variables unset names.
func deployEnv(environ, unset []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if slices.ContainsFunc(unset, func(u string) bool { return envNameEqual(name, u) }) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// envNameEqual reports whether two variable names name the same variable on
// this platform.
func envNameEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// writeStackTree materialises one deploy into a fresh temporary directory: the
// compose document at <dir>/stack.yml, and every entry of files at its
// chart-relative path beneath it. It returns the directory and the manifest's
// path; the caller removes the directory. Nothing is left behind on error.
//
// A directory rather than the single temp file this used to be, because
// `docker stack deploy -c <path>` makes filepath.Dir(<path>) the compose working
// directory and resolves every file: and env_file: against it, reading them
// client-side before anything reaches the daemon. With a bare temp file that
// made `file: ./nginx.conf` mean $TMPDIR/nginx.conf; putting the chart's files
// in the same directory, at the paths the manifest uses, is what makes those
// keys mean the chart.
func writeStackTree(files map[string][]byte, manifest string) (dir string, manifestPath string, err error) {
	// MkdirTemp creates it 0o700, which is what this wants: the content passing
	// through here is on its way into a swarm config and is written as the
	// operator, so no other local user should be able to read it — or, worse,
	// swap it out between the write and the `docker` process reading it.
	dir, err = os.MkdirTemp("", "swarmcli-stack-*")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temporary directory: %w", err)
	}
	// A failure below leaves a partial tree, and the caller never learns its path
	// because it only gets a directory back on success, so clean it up here.
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
			dir, manifestPath = "", ""
		}
	}()

	for name, data := range files {
		// Re-check containment even though the caller resolved these keys
		// against the chart. They are chart-authored, and what a bad one buys is
		// a write outside the temporary directory as the operator.
		rel := filepath.FromSlash(name)
		path := filepath.Join(dir, rel)
		if filepath.IsAbs(rel) || !strings.HasPrefix(path, dir+string(os.PathSeparator)) {
			err = fmt.Errorf("refusing chart file '%s': it resolves outside the stack directory", name)
			return
		}
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			err = fmt.Errorf("failed to create directory for chart file '%s': %w", name, err)
			return
		}
		if err = writeNewFile(path, data); errors.Is(err, fs.ErrExist) {
			err = fmt.Errorf("refusing chart file '%s': it names the same file as another chart file on this filesystem", name)
			return
		} else if err != nil {
			err = fmt.Errorf("failed to write chart file '%s': %w", name, err)
			return
		}
	}

	// Last, so no chart file can displace the manifest.
	manifestPath = filepath.Join(dir, "stack.yml")
	if err = os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		err = fmt.Errorf("failed to write stack manifest: %w", err)
		return
	}
	return dir, manifestPath, nil
}

// writeNewFile writes data to path, which must not exist yet. Two chart files
// can name one file — two spellings of one path, or on a case-insensitive
// filesystem two cases of one name — and the deploy must read the bytes that
// were checked, not whichever key happened to be written last.
func writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// RemoveStackCLI tears down a stack via `docker stack rm`, the symmetric
// counterpart to DeployStack. Unlike RemoveStack (services only), this removes
// the stack's services, networks, configs and secrets while leaving volumes
// intact — matching standard Docker stack semantics.
func RemoveStackCLI(ctx context.Context, stackName string) error {
	ctxName, err := GetDockerContext()
	if err != nil {
		return fmt.Errorf("failed to get docker context: %w", err)
	}
	return RemoveStackCLIInContext(ctx, ctxName, stackName)
}

// RemoveStackCLIInContext is RemoveStackCLI against an explicitly named context.
// As with DeployStackInContext, cancelling ctx kills the child.
func RemoveStackCLIInContext(ctx context.Context, ctxName, stackName string) error {
	if ctxName == "" {
		return fmt.Errorf("docker context name is required")
	}
	if stackName == "" {
		return fmt.Errorf("stack name cannot be empty")
	}
	cmd := exec.CommandContext(ctx, "docker", "--context", ctxName, "stack", "rm", stackName)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		// A killed child produces no output to interpret, so decide on the
		// cancellation before reading the message below.
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("removing stack '%s': %w", stackName, cerr)
		}
		// An already-absent stack is success: makes uninstall idempotent so a
		// retry after a partial teardown can still finish cleanup.
		if strings.Contains(string(output), "Nothing found in stack") {
			l().Infof("Stack %q already absent", stackName)
			return nil
		}
		return fmt.Errorf("failed to remove stack: %w\nOutput: %s", err, string(output))
	}
	l().Infof("Stack %q removed", stackName)
	return nil
}

// extractNetworkNames extracts network names from a compose YAML file
func extractNetworkNames(yamlContent string) []string {
	var data map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &data); err != nil {
		return nil
	}

	var networkNames []string
	if networks, ok := data["networks"].(map[string]interface{}); ok {
		for name := range networks {
			networkNames = append(networkNames, name)
		}
	}
	return networkNames
}

// cleanupNetworks attempts to remove networks that may have been created during a failed deployment
func cleanupNetworks(stackName string, networkNames []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, netName := range networkNames {
		// Docker creates networks with the pattern: stackname_networkname
		fullNetworkName := fmt.Sprintf("%s_%s", stackName, netName)
		l().Infof("Attempting to clean up network: %s", fullNetworkName)

		// Try to remove the network (non-forced)
		if err := RemoveNetwork(ctx, fullNetworkName); err != nil {
			l().Debugf("Could not remove network %s: %v (this may be expected if it wasn't created)", fullNetworkName, err)
		} else {
			l().Infof("Successfully cleaned up orphaned network: %s", fullNetworkName)
		}
	}
}

// GetDockerContext returns the Docker context name this session addresses,
// which is what the exec-based stack commands name with `--context`.
//
// It reports the session pin rather than re-reading the config file, so a
// stack shown from one swarm cannot be deployed to another that was made
// current in some other terminal meanwhile. See SessionContext.
func GetDockerContext() (string, error) {
	return SessionContext()
}
