// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build integration
// +build integration

package docker_test

import (
	"bytes"
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	yamlPkg "gopkg.in/yaml.v3"
)

func TestReconstructStackCompose(t *testing.T) {
	// This test assumes that the demo stack is already deployed
	// (done by test-setup/testenv.sh deploy)
	stackName := "demo"

	yaml, err := docker.ReconstructStackCompose(stackName)
	if err != nil {
		t.Fatalf("Failed to reconstruct stack compose: %v", err)
	}

	if yaml == "" {
		t.Fatal("Reconstructed YAML is empty")
	}

	// Verify YAML contains expected elements
	if !strings.Contains(yaml, "version:") {
		t.Error("Reconstructed YAML missing version field")
	}

	if !strings.Contains(yaml, "services:") {
		t.Error("Reconstructed YAML missing services section")
	}

	// Check for known service names from test-stack.yml
	// (test-stack contains whoami and whoami_single services)
	if !strings.Contains(yaml, "whoami") {
		t.Error("Reconstructed YAML missing expected service 'whoami'")
	}

	// Verify volumes section
	if !strings.Contains(yaml, "volumes:") {
		t.Error("Reconstructed YAML missing volumes section")
	}
	if !strings.Contains(yaml, "whoami_data:") {
		t.Error("Reconstructed YAML missing volume 'whoami_data' (should be stripped of 'demo_' prefix)")
	}

	// Verify networks section
	if !strings.Contains(yaml, "networks:") {
		t.Error("Reconstructed YAML missing networks section")
	}
	if !strings.Contains(yaml, "backend:") {
		t.Error("Reconstructed YAML missing network 'backend' (should be stripped of 'demo_' prefix)")
	}

	// Parse YAML and verify backend network is not marked external
	var composed docker.ComposeFile
	if err := yamlPkg.Unmarshal([]byte(yaml), &composed); err != nil {
		t.Fatalf("Failed to parse reconstructed YAML: %v", err)
	}
	if netProps, ok := composed.Networks["backend"]; ok {
		if _, hasExternal := netProps["external"]; hasExternal {
			t.Error("Stack-managed network 'backend' should not be marked as external")
		}
	} else {
		t.Error("Parsed YAML missing 'backend' network key")
	}

	// Issue #363 regression: a stack declaring ONLY the "default" network
	// (with a service referencing it) must retain both the top-level
	// networks: default key and the service-level networks: [default].
	if _, ok := composed.Networks["default"]; !ok {
		t.Error("Issue #363: reconstructed YAML dropped the 'default' network")
	}
	probe, ok := composed.Services["default_net_probe"]
	if !ok {
		t.Fatal("Issue #363: missing 'default_net_probe' service in reconstructed YAML")
	}
	probeNets, _ := probe.Networks.([]any) // yaml.Unmarshal yields []any
	foundDefault := false
	for _, n := range probeNets {
		if s, _ := n.(string); s == "default" {
			foundDefault = true
		}
	}
	if !foundDefault {
		t.Errorf("Issue #363: 'default_net_probe' lost its networks: [default], got %#v", probe.Networks)
	}

	// Issue #379: whoami_single declares a healthcheck — it must round-trip
	// through reconstruction rather than being silently dropped.
	if !strings.Contains(yaml, "healthcheck:") {
		t.Error("Issue #379: reconstructed YAML missing 'healthcheck:' section")
	}
	single, ok := composed.Services["whoami_single"]
	if !ok {
		t.Fatal("Issue #379: missing 'whoami_single' service in reconstructed YAML")
	}
	if single.Healthcheck == nil {
		t.Error("Issue #379: 'whoami_single' lost its healthcheck during reconstruction")
	} else {
		if single.Healthcheck.Interval != "30s" {
			t.Errorf("Issue #379: healthcheck interval = %q, want \"30s\"", single.Healthcheck.Interval)
		}
		if single.Healthcheck.Retries != 3 {
			t.Errorf("Issue #379: healthcheck retries = %d, want 3", single.Healthcheck.Retries)
		}
		if len(single.Healthcheck.Test) == 0 {
			t.Error("Issue #379: healthcheck test command is empty")
		}
	}

	// Issue #428: whoami_single declares a logging driver — it must round-trip
	// through reconstruction rather than being silently dropped.
	if !strings.Contains(yaml, "logging:") {
		t.Error("Issue #428: reconstructed YAML missing 'logging:' section")
	}
	if single.Logging == nil {
		t.Error("Issue #428: 'whoami_single' lost its logging driver during reconstruction")
	} else {
		if single.Logging.Driver != "json-file" {
			t.Errorf("Issue #428: logging driver = %q, want \"json-file\"", single.Logging.Driver)
		}
		if single.Logging.Options["max-size"] != "10m" {
			t.Errorf("Issue #428: logging option max-size = %q, want \"10m\"", single.Logging.Options["max-size"])
		}
	}

	// Issue #662: whoami's secret and config set no uid/gid/mode, so the
	// defaults `docker stack deploy` filled in must not be echoed back, and
	// no service sets an endpoint mode, so the daemon's "vip" must not be.
	whoami := composed.Services["whoami"]
	wantSecrets := []map[string]any{{"source": "demo_whoami_secret", "target": "/run/secrets/whoami_secret"}}
	if !reflect.DeepEqual(whoami.Secrets, wantSecrets) {
		t.Errorf("Issue #662: whoami secrets = %#v, want %#v", whoami.Secrets, wantSecrets)
	}
	wantConfigs := []map[string]any{{"source": "demo_whoami_config", "target": "/etc/whoami/config"}}
	if !reflect.DeepEqual(whoami.Configs, wantConfigs) {
		t.Errorf("Issue #662: whoami configs = %#v, want %#v", whoami.Configs, wantConfigs)
	}
	if strings.Contains(yaml, "endpoint_mode: vip") {
		t.Error("Issue #662: reconstructed YAML pins the default 'endpoint_mode: vip'")
	}

	t.Logf("Successfully reconstructed stack YAML (%d bytes)", len(yaml))
}

func TestReconstructStackCompose_RoundTrip(t *testing.T) {
	// Full round-trip: reconstruct demo → deploy as demo_rt → reconstruct demo_rt → compare.
	const srcStack = "demo"
	const rtStack = "demo_rt"

	// 1. Reconstruct the original stack
	yamlStr, err := docker.ReconstructStackCompose(srcStack)
	if err != nil {
		t.Fatalf("Failed to reconstruct source stack: %v", err)
	}
	t.Logf("Reconstructed YAML:\n%s", yamlStr)

	var srcCF docker.ComposeFile
	if err := yamlPkg.Unmarshal([]byte(yamlStr), &srcCF); err != nil {
		t.Fatalf("Failed to parse reconstructed YAML: %v", err)
	}

	// 2. Strip published ports to avoid conflicts with the running demo stack
	for k, svc := range srcCF.Services {
		svc.Ports = nil
		srcCF.Services[k] = svc
	}
	deployYAML, err := yamlPkg.Marshal(&srcCF)
	if err != nil {
		t.Fatalf("Failed to marshal deploy YAML: %v", err)
	}

	// Write to temp file and deploy as a new stack
	tmpFile, err := os.CreateTemp("", "compose-rt-*.yml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	if _, err := tmpFile.Write(deployYAML); err != nil {
		t.Fatalf("Failed to write compose file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close compose file: %v", err)
	}

	// Deploy round-trip stack
	deployCmd := exec.Command("docker", "stack", "deploy", "-c", tmpFile.Name(), rtStack)
	var deployErr bytes.Buffer
	deployCmd.Stderr = &deployErr
	if out, err := deployCmd.Output(); err != nil {
		t.Fatalf("Failed to deploy round-trip stack: %v\nstderr: %s\nstdout: %s", err, deployErr.String(), string(out))
	}

	// Cleanup: remove the round-trip stack when done
	t.Cleanup(func() {
		rmCmd := exec.Command("docker", "stack", "rm", rtStack)
		_ = rmCmd.Run()
		// Wait for removal to complete
		for range 30 {
			check := exec.Command("docker", "stack", "services", rtStack, "--format", "{{.Name}}")
			if out, _ := check.Output(); len(bytes.TrimSpace(out)) == 0 {
				break
			}
			time.Sleep(time.Second)
		}
	})

	// 3. Wait for convergence
	if err := waitForStackConvergence(rtStack, len(srcCF.Services), 60*time.Second); err != nil {
		t.Fatalf("Round-trip stack failed to converge: %v", err)
	}

	// 4. Reconstruct the round-trip stack
	rtYAML, err := docker.ReconstructStackCompose(rtStack)
	if err != nil {
		t.Fatalf("Failed to reconstruct round-trip stack: %v", err)
	}

	var rtCF docker.ComposeFile
	if err := yamlPkg.Unmarshal([]byte(rtYAML), &rtCF); err != nil {
		t.Fatalf("Failed to parse round-trip YAML: %v", err)
	}

	// 5. Compare: volumes
	for volName := range srcCF.Volumes {
		if _, ok := rtCF.Volumes[volName]; !ok {
			t.Errorf("Volume %q present in source but missing from round-trip, got keys: %v",
				volName, mapKeys(rtCF.Volumes))
		}
	}

	// 6. Compare: networks
	// Issue #363: explicitly assert the default network survived the source
	// reconstruction — the loop below would pass vacuously if "default" were
	// dropped from BOTH src and rt.
	if _, ok := srcCF.Networks["default"]; !ok {
		t.Error("Issue #363: source reconstruction dropped the 'default' network")
	}
	for netName := range srcCF.Networks {
		if _, ok := rtCF.Networks[netName]; !ok {
			t.Errorf("Network %q present in source but missing from round-trip, got keys: %v",
				netName, mapKeys(rtCF.Networks))
		}
	}

	// 7. Compare: service volume mounts
	for svcName, srcSvc := range srcCF.Services {
		rtSvc, ok := rtCF.Services[svcName]
		if !ok {
			t.Errorf("Service %q present in source but missing from round-trip", svcName)
			continue
		}
		for _, srcVol := range srcSvc.Volumes {
			found := false
			for _, rtVol := range rtSvc.Volumes {
				if rtVol == srcVol {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Service %q: volume mount %q missing from round-trip, got: %v",
					svcName, srcVol, rtSvc.Volumes)
			}
		}
	}

	t.Logf("Round-trip deploy test passed: volumes=%v, networks=%v",
		mapKeys(rtCF.Volumes), mapKeys(rtCF.Networks))
}

// waitForStackConvergence waits until all services in the stack have running tasks.
func waitForStackConvergence(stack string, expectedServices int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := exec.Command("docker", "stack", "services", stack, "--format", "{{.Name}} {{.Replicas}}")
		out, err := cmd.Output()
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) < expectedServices {
			time.Sleep(2 * time.Second)
			continue
		}
		allReady := true
		for _, line := range lines {
			// Format: "stack_svc 2/2" — check N/M where N==M
			parts := strings.Fields(line)
			if len(parts) < 2 {
				allReady = false
				break
			}
			replicas := parts[len(parts)-1]
			slash := strings.Index(replicas, "/")
			if slash < 0 || replicas[:slash] != replicas[slash+1:] {
				allReady = false
				break
			}
		}
		if allReady {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("stack %q did not converge within %v", stack, timeout)
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestReconstructStackCompose_NonExistentStack(t *testing.T) {
	stackName := "non-existent-stack-xyz123"

	_, err := docker.ReconstructStackCompose(stackName)
	if err == nil {
		t.Fatal("Expected error for non-existent stack, got nil")
	}

	if !strings.Contains(err.Error(), "no services found") && !strings.Contains(err.Error(), "failed to list services") {
		t.Errorf("Expected 'no services found' or 'failed to list services' error, got: %v", err)
	}
}
