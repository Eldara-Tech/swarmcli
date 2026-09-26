// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestFilterLabels_RemovesDockerStackKeys(t *testing.T) {
	input := map[string]string{
		"com.docker.stack.namespace": "mystack",
		"com.docker.stack.image":     "nginx:latest",
		"app":                        "web",
	}
	got := filterLabels(input)
	require.Equal(t, map[string]string{"app": "web"}, got)
}

func TestFilterLabels_KeepsUserLabels(t *testing.T) {
	input := map[string]string{
		"app":     "web",
		"version": "1.0",
	}
	got := filterLabels(input)
	require.Equal(t, map[string]string{"app": "web", "version": "1.0"}, got)
}

func TestFilterLabels_ReturnsNilWhenAllFiltered(t *testing.T) {
	input := map[string]string{
		"com.docker.stack.namespace": "mystack",
	}
	require.Nil(t, filterLabels(input))
}

func TestFilterLabels_ReturnsNilForEmptyMap(t *testing.T) {
	require.Nil(t, filterLabels(map[string]string{}))
}

func TestFilterLabels_NilInput(t *testing.T) {
	require.Nil(t, filterLabels(nil))
}

func TestComposeFile_VolumesExternalTrue(t *testing.T) {
	cf := ComposeFile{
		Version:  "3.8",
		Services: map[string]ComposeService{"web": {Image: "nginx"}},
		Volumes: map[string]map[string]any{
			"mydata": {"external": true},
		},
	}
	out, err := yaml.Marshal(&cf)
	require.NoError(t, err)
	yamlStr := string(out)
	require.Contains(t, yamlStr, "external: true")
}

func TestComposeFile_VolumeWithDriver(t *testing.T) {
	cf := ComposeFile{
		Version:  "3.8",
		Services: map[string]ComposeService{"web": {Image: "nginx", Volumes: []string{"nfs_data:/data"}}},
		Volumes: map[string]map[string]any{
			"nfs_data": {
				"driver":      "local",
				"driver_opts": map[string]string{"type": "nfs", "device": ":/export"},
			},
		},
	}
	out, err := yaml.Marshal(&cf)
	require.NoError(t, err)
	yamlStr := string(out)
	require.Contains(t, yamlStr, "driver: local")
	require.Contains(t, yamlStr, "driver_opts:")
	require.NotContains(t, yamlStr, "external")
}

func TestComposeService_DeployLabelsYAML(t *testing.T) {
	cs := ComposeService{
		Image: "nginx:latest",
		Deploy: map[string]any{
			"replicas": 3,
			"labels": map[string]string{
				"com.example.role": "frontend",
			},
		},
	}

	out, err := yaml.Marshal(cs)
	require.NoError(t, err)
	yamlStr := string(out)

	require.Contains(t, yamlStr, "deploy:")
	require.Contains(t, yamlStr, "labels:")
	require.Contains(t, yamlStr, "com.example.role: frontend")
}

func TestComposeService_ServiceLevelLabelsYAML(t *testing.T) {
	cs := ComposeService{
		Image: "nginx:latest",
		Labels: map[string]string{
			"container.label": "value",
		},
	}

	out, err := yaml.Marshal(cs)
	require.NoError(t, err)
	yamlStr := string(out)

	require.Contains(t, yamlStr, "labels:")
	require.Contains(t, yamlStr, "container.label: value")
	require.NotContains(t, yamlStr, "deploy:")
}

func TestComposeService_BothLabelLevelsYAML(t *testing.T) {
	cs := ComposeService{
		Image: "nginx:latest",
		Labels: map[string]string{
			"container.label": "cval",
		},
		Deploy: map[string]any{
			"replicas": 2,
			"labels": map[string]string{
				"deploy.label": "dval",
			},
		},
	}

	out, err := yaml.Marshal(cs)
	require.NoError(t, err)

	// Unmarshal back to verify structure
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(out, &parsed))

	// Service-level labels
	svcLabels, ok := parsed["labels"].(map[string]any)
	require.True(t, ok, "service-level labels should exist")
	require.Equal(t, "cval", svcLabels["container.label"])

	// Deploy labels
	deploy, ok := parsed["deploy"].(map[string]any)
	require.True(t, ok, "deploy section should exist")
	deployLabels, ok := deploy["labels"].(map[string]any)
	require.True(t, ok, "deploy.labels should exist")
	require.Equal(t, "dval", deployLabels["deploy.label"])
}

func TestStripStackPrefix(t *testing.T) {
	tests := []struct {
		stack, input, want string
	}{
		{"ubu", "ubu_ubuntu_example_data", "ubuntu_example_data"},
		{"ubu", "external_volume", "external_volume"},
		{"ubu", "ubu_default", "default"},
		{"ubu", "ubu_", ""},
		{"", "anything", "anything"},
		{"ubu", "ubu", "ubu"}, // no underscore → no strip
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			require.Equal(t, tt.want, stripStackPrefix(tt.stack, tt.input))
		})
	}
}

func TestComposeFile_VolumeManagedNotExternal(t *testing.T) {
	cf := ComposeFile{
		Version:  "3.8",
		Services: map[string]ComposeService{"web": {Image: "nginx", Volumes: []string{"mydata:/data"}}},
		Volumes:  map[string]map[string]any{"mydata": {}},
	}
	out, err := yaml.Marshal(&cf)
	require.NoError(t, err)
	require.NotContains(t, string(out), "external")
}

// Issue #363 regression: a stack declaring ONLY the "default" network with
// a service referencing it must survive reconstruction unchanged. The old
// suppression logic (removed) dropped both, causing "Edit Stack no network".
func TestPruneEmptySections_KeepsSoleDefaultNetwork(t *testing.T) {
	cf := ComposeFile{
		Version: "3.9",
		Services: map[string]ComposeService{
			"web": {Image: "nginx", Networks: []string{"default"}},
		},
		Networks: map[string]map[string]any{"default": {"driver": "overlay"}},
	}

	pruneEmptySections(&cf)

	require.NotNil(t, cf.Networks)
	require.Contains(t, cf.Networks, "default")
	require.Equal(t, "overlay", cf.Networks["default"]["driver"])

	out, err := yaml.Marshal(&cf)
	require.NoError(t, err)
	yamlStr := string(out)
	require.Contains(t, yamlStr, "networks:")
	require.Contains(t, yamlStr, "default:")
	require.Contains(t, yamlStr, "driver: overlay")
}

func TestPruneEmptySections_NilsGenuinelyEmpty(t *testing.T) {
	cf := ComposeFile{
		Version:  "3.9",
		Services: map[string]ComposeService{"web": {Image: "nginx"}},
		Networks: map[string]map[string]any{},
		Volumes:  map[string]map[string]any{},
		Secrets:  map[string]map[string]any{},
		Configs:  map[string]map[string]any{},
	}

	pruneEmptySections(&cf)

	require.Nil(t, cf.Networks)
	require.Nil(t, cf.Volumes)
	require.Nil(t, cf.Secrets)
	require.Nil(t, cf.Configs)
}

func TestStripImageDigest(t *testing.T) {
	digest := strings.Repeat("a", 64)
	tests := []struct{ name, in, want string }{
		{"digest pinned", "nginx:1.25@sha256:" + digest, "nginx:1.25"},
		{"no tag with digest", "nginx@sha256:" + digest, "nginx"},
		{"registry port + digest", "reg:5000/app:v2@sha256:" + digest, "reg:5000/app:v2"},
		{"plain tag", "alpine:latest", "alpine:latest"},
		{"no tag no digest", "alpine", "alpine"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, stripImageDigest(tt.in))
		})
	}
}

func TestFormatPort(t *testing.T) {
	tests := []struct {
		name              string
		published, target uint32
		proto, want       string
	}{
		{"tcp default omitted", 8080, 80, "tcp", "8080:80"}, // issue #363 case
		{"empty proto = tcp", 8080, 80, "", "8080:80"},
		{"udp kept", 53, 53, "udp", "53:53/udp"},
		{"sctp kept", 38412, 38412, "sctp", "38412:38412/sctp"},
		{"target only tcp", 0, 80, "tcp", "80"},
		{"target only udp", 0, 514, "udp", "514/udp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, formatPort(tt.published, tt.target, tt.proto))
		})
	}
}

func TestComposeFile_NetworkManagedNotExternal(t *testing.T) {
	// Stack-managed networks (prefix was stripped) should not be external
	cf := ComposeFile{
		Version: "3.8",
		Services: map[string]ComposeService{
			"web": {Image: "nginx", Networks: []string{"mynet"}},
		},
		Networks: map[string]map[string]any{"mynet": {}},
	}
	out, err := yaml.Marshal(&cf)
	require.NoError(t, err)
	yamlStr := string(out)
	require.Contains(t, yamlStr, "networks:")
	require.Contains(t, yamlStr, "mynet:")
	require.NotContains(t, yamlStr, "external")
}

func TestComposeFile_NetworkExternalTrue(t *testing.T) {
	// External networks (no prefix stripped) should have external: true
	cf := ComposeFile{
		Version: "3.8",
		Services: map[string]ComposeService{
			"web": {Image: "nginx", Networks: []string{"shared_net"}},
		},
		Networks: map[string]map[string]any{"shared_net": {"external": true}},
	}
	out, err := yaml.Marshal(&cf)
	require.NoError(t, err)
	yamlStr := string(out)
	require.Contains(t, yamlStr, "shared_net:")
	require.Contains(t, yamlStr, "external: true")
}

func TestEscapeComposeInterpolation(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"no dollars", "echo hello", "echo hello"},
		{"single dollar", "echo $HOME", "echo $$HOME"},
		{"double dollar passthrough", "echo $$HOME", "echo $$$$HOME"},
		{"subshell", "sh -c '$(date)'", "sh -c '$$(date)'"},
		{"arithmetic", "i=$((i+1))", "i=$$((i+1))"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, escapeComposeInterpolation(tt.input))
		})
	}
}

func TestEscapeComposeArgs(t *testing.T) {
	in := []string{"sh", "-c", "echo $HOME; date=$(date)"}
	got := escapeComposeArgs(in)
	require.Equal(t, []string{"sh", "-c", "echo $$HOME; date=$$(date)"}, got)
	// original slice must not be modified
	require.Equal(t, "echo $HOME; date=$(date)", in[2])
}

func TestComposeHealthcheck_FullSpec(t *testing.T) {
	hc := composeHealthcheck(
		[]string{"CMD", "curl", "-f", "http://localhost"},
		90*int64(time.Second), 10*int64(time.Second),
		40*int64(time.Second), 5*int64(time.Second), 3, false)
	require.NotNil(t, hc)
	require.Equal(t, []string{"CMD", "curl", "-f", "http://localhost"}, hc.Test)
	require.Equal(t, "1m30s", hc.Interval)
	require.Equal(t, "10s", hc.Timeout)
	require.Equal(t, "40s", hc.StartPeriod)
	require.Equal(t, "5s", hc.StartInterval)
	require.Equal(t, 3, hc.Retries)
	require.False(t, hc.Disable)
}

func TestComposeHealthcheck_Disabled(t *testing.T) {
	hc := composeHealthcheck([]string{"NONE"}, 0, 0, 0, 0, 0, false)
	require.NotNil(t, hc)
	require.True(t, hc.Disable)
	require.Empty(t, hc.Test)
	require.Empty(t, hc.Interval)
}

func TestComposeHealthcheck_InheritReturnsNil(t *testing.T) {
	require.Nil(t, composeHealthcheck(nil, 0, 0, 0, 0, 0, false))
	require.Nil(t, composeHealthcheck([]string{}, 0, 0, 0, 0, 0, false))
}

func TestComposeHealthcheck_Escapes(t *testing.T) {
	hc := composeHealthcheck(
		[]string{"CMD-SHELL", "test $VAR = 1"}, int64(time.Second), 0, 0, 0, 0, true)
	require.NotNil(t, hc)
	require.Equal(t, []string{"CMD-SHELL", "test $$VAR = 1"}, hc.Test)
}

func TestComposeService_HealthcheckYAML(t *testing.T) {
	cs := ComposeService{
		Image: "nginx:latest",
		Healthcheck: &Healthcheck{
			Test:     []string{"CMD", "true"},
			Interval: "30s",
			Timeout:  "5s",
			Retries:  3,
		},
	}
	out, err := yaml.Marshal(&cs)
	require.NoError(t, err)
	yamlStr := string(out)
	require.Contains(t, yamlStr, "healthcheck:")
	require.Contains(t, yamlStr, "test:")
	require.Contains(t, yamlStr, "interval: 30s")
	require.Contains(t, yamlStr, "retries: 3")
}

func TestComposeService_HealthcheckOmittedWhenNil(t *testing.T) {
	cs := ComposeService{Image: "nginx:latest"}
	out, err := yaml.Marshal(&cs)
	require.NoError(t, err)
	require.NotContains(t, string(out), "healthcheck")
}

// Issue #428: a service deployed with a logging driver+options must round-trip
// into the compose `logging:` block.
func TestComposeLogging_DriverAndOptions(t *testing.T) {
	lg := composeLogging("loki:latest", map[string]string{
		"loki-url": "http://loki:3100/loki/api/v1/push",
	})
	require.NotNil(t, lg)
	require.Equal(t, "loki:latest", lg.Driver)
	require.Equal(t, "http://loki:3100/loki/api/v1/push", lg.Options["loki-url"])
}

func TestComposeLogging_DriverOnly(t *testing.T) {
	lg := composeLogging("json-file", nil)
	require.NotNil(t, lg)
	require.Equal(t, "json-file", lg.Driver)
	require.Empty(t, lg.Options)
}

func TestComposeLogging_OptionsOnly(t *testing.T) {
	lg := composeLogging("", map[string]string{"max-size": "10m"})
	require.NotNil(t, lg)
	require.Empty(t, lg.Driver)
	require.Equal(t, "10m", lg.Options["max-size"])
}

func TestComposeLogging_NoDriverReturnsNil(t *testing.T) {
	require.Nil(t, composeLogging("", nil))
	require.Nil(t, composeLogging("", map[string]string{}))
}

func TestComposeService_LoggingYAML(t *testing.T) {
	cs := ComposeService{
		Image: "nginx:latest",
		Logging: &Logging{
			Driver:  "loki:latest",
			Options: map[string]string{"loki-url": "http://loki:3100/loki/api/v1/push"},
		},
	}
	out, err := yaml.Marshal(&cs)
	require.NoError(t, err)
	yamlStr := string(out)
	require.Contains(t, yamlStr, "logging:")
	require.Contains(t, yamlStr, "driver: loki:latest")
	require.Contains(t, yamlStr, "options:")
	require.Contains(t, yamlStr, "loki-url: http://loki:3100/loki/api/v1/push")
}

func TestComposeService_LoggingOmittedWhenNil(t *testing.T) {
	cs := ComposeService{Image: "nginx:latest"}
	out, err := yaml.Marshal(&cs)
	require.NoError(t, err)
	require.NotContains(t, string(out), "logging")
}

// Capture side: the `docker service inspect` JSON LogDriver block must
// unmarshal into TaskTemplate.LogDriver (issue #428).
func TestServiceInspect_CapturesLogDriver(t *testing.T) {
	raw := []byte(`{
		"Spec": {
			"Name": "web",
			"TaskTemplate": {
				"LogDriver": {
					"Name": "loki:latest",
					"Options": {"loki-url": "http://loki:3100/loki/api/v1/push"}
				}
			}
		}
	}`)
	var si ServiceInspect
	require.NoError(t, json.Unmarshal(raw, &si))
	require.NotNil(t, si.Spec.TaskTemplate.LogDriver)
	require.Equal(t, "loki:latest", si.Spec.TaskTemplate.LogDriver.Name)
	require.Equal(t, "http://loki:3100/loki/api/v1/push",
		si.Spec.TaskTemplate.LogDriver.Options["loki-url"])
}

func TestServiceInspect_NoLogDriverIsNil(t *testing.T) {
	raw := []byte(`{"Spec":{"Name":"web","TaskTemplate":{}}}`)
	var si ServiceInspect
	require.NoError(t, json.Unmarshal(raw, &si))
	require.Nil(t, si.Spec.TaskTemplate.LogDriver)
}

// --- Issue #430: field-coverage of the reconstruction round-trip path ---

// TestReconstruct_DropsRichSpec is the fat-fixture coverage guard: one
// `docker service inspect`-shaped JSON exercising every newly-supported field
// is fed through the pure assembler, and each field is asserted to survive.
// It exercises BOTH halves of the path — capture (JSON → hand-rolled structs)
// and render (structs → compose) — so it catches parse-drops and render-drops
// alike. Before the #430 fix this fails; after it passes.
func TestReconstruct_DropsRichSpec(t *testing.T) {
	const raw = `{
	  "Spec": {
	    "Name": "demo_web",
	    "Labels": {"com.docker.stack.namespace": "demo", "tier": "frontend"},
	    "TaskTemplate": {
	      "ContainerSpec": {
	        "Image": "nginx:1.27@sha256:deadbeef",
	        "Hostname": "web1",
	        "CapabilityAdd": ["NET_ADMIN"],
	        "CapabilityDrop": ["CHOWN"],
	        "Sysctls": {"net.core.somaxconn": "1024"},
	        "Ulimits": [{"Name": "nofile", "Soft": 20000, "Hard": 40000}],
	        "Hosts": ["10.0.0.5 db.internal db"],
	        "DNSConfig": {"Nameservers": ["8.8.8.8"], "Search": ["corp.local"], "Options": ["ndots:2"]},
	        "ReadOnly": true,
	        "Init": true,
	        "StopSignal": "SIGTERM",
	        "StopGracePeriod": 30000000000,
	        "Secrets": [
	          {"SecretName": "demo_api_key", "File": {"Name": "api_key", "UID": "100", "GID": "101", "Mode": 256}}
	        ]
	      },
	      "Resources": {"Limits": {"NanoCPUs": 500000000, "MemoryBytes": 268435456, "Pids": 100}},
	      "RestartPolicy": {"Condition": "on-failure", "Delay": 5000000000, "MaxAttempts": 3, "Window": 120000000000},
	      "Placement": {"Constraints": ["node.role==worker"], "MaxReplicas": 2},
	      "LogDriver": {"Name": "json-file", "Options": {"max-size": "10m"}}
	    },
	    "Mode": {"Replicated": {"Replicas": 3}},
	    "EndpointSpec": {"Mode": "dnsrr", "Ports": [{"Protocol": "tcp", "TargetPort": 80, "PublishedPort": 8080}]},
	    "UpdateConfig": {"Parallelism": 2, "Delay": 10000000000, "FailureAction": "rollback", "Monitor": 60000000000, "MaxFailureRatio": 0.25, "Order": "start-first"},
	    "RollbackConfig": {"Parallelism": 1, "Order": "stop-first"}
	  }
	}`

	var si ServiceInspect
	require.NoError(t, json.Unmarshal([]byte(raw), &si))

	cf := assembleCompose([]ServiceInspect{si}, "demo", nil)
	cs, ok := cf.Services["web"]
	require.True(t, ok, "service should be keyed by its stack-stripped name")

	// Container-level fields
	require.Equal(t, "nginx:1.27", cs.Image) // digest stripped (#363)
	require.Equal(t, "web1", cs.Hostname)
	require.Equal(t, []string{"NET_ADMIN"}, cs.CapAdd)
	require.Equal(t, []string{"CHOWN"}, cs.CapDrop)
	require.Equal(t, "1024", cs.Sysctls["net.core.somaxconn"])
	require.Equal(t, map[string]any{"soft": int64(20000), "hard": int64(40000)}, cs.Ulimits["nofile"])
	require.Equal(t, []string{"db.internal:10.0.0.5", "db:10.0.0.5"}, cs.ExtraHosts)
	require.Equal(t, []string{"8.8.8.8"}, cs.DNS)
	require.Equal(t, []string{"corp.local"}, cs.DNSSearch)
	require.Equal(t, []string{"ndots:2"}, cs.DNSOpt)
	require.True(t, cs.ReadOnly)
	require.NotNil(t, cs.Init)
	require.True(t, *cs.Init)
	require.Equal(t, "SIGTERM", cs.StopSignal)
	require.Equal(t, "30s", cs.StopGracePeriod)

	// Logging driver (folded in from #428/#429 — now covered by the guard).
	require.NotNil(t, cs.Logging)
	require.Equal(t, "json-file", cs.Logging.Driver)
	require.Equal(t, "10m", cs.Logging.Options["max-size"])

	// Secret target permissions (mode 256 == 0o400)
	require.Len(t, cs.Secrets, 1)
	require.Equal(t, "demo_api_key", cs.Secrets[0]["source"])
	require.Equal(t, "api_key", cs.Secrets[0]["target"])
	require.Equal(t, "100", cs.Secrets[0]["uid"])
	require.Equal(t, "101", cs.Secrets[0]["gid"])
	require.Equal(t, fileMode(0o400), cs.Secrets[0]["mode"])

	// Deploy block
	dep := cs.Deploy
	require.Equal(t, 3, dep["replicas"])
	require.Equal(t, "dnsrr", dep["endpoint_mode"])

	placement := dep["placement"].(map[string]any)
	require.Equal(t, []string{"node.role==worker"}, placement["constraints"])
	require.Equal(t, 2, placement["max_replicas_per_node"])

	limits := dep["resources"].(map[string]any)["limits"].(map[string]any)
	require.Equal(t, int64(100), limits["pids"])

	rp := dep["restart_policy"].(map[string]any)
	require.Equal(t, "on-failure", rp["condition"])
	require.Equal(t, "5s", rp["delay"])
	require.Equal(t, 3, rp["max_attempts"])
	require.Equal(t, "2m0s", rp["window"])

	uc := dep["update_config"].(map[string]any)
	require.Equal(t, 2, uc["parallelism"])
	require.Equal(t, "10s", uc["delay"])
	require.Equal(t, "rollback", uc["failure_action"])
	require.Equal(t, "1m0s", uc["monitor"])
	require.Equal(t, float32(0.25), uc["max_failure_ratio"])
	require.Equal(t, "start-first", uc["order"])

	rc := dep["rollback_config"].(map[string]any)
	require.Equal(t, 1, rc["parallelism"])
	require.Equal(t, "stop-first", rc["order"])

	// Service-level labels are filtered to user keys → deploy.labels.
	require.Equal(t, map[string]string{"tier": "frontend"}, dep["labels"])

	// The whole file still marshals to YAML carrying the new keys.
	out, err := yaml.Marshal(&cf)
	require.NoError(t, err)
	y := string(out)
	for _, want := range []string{
		"hostname: web1", "cap_add:", "cap_drop:", "sysctls:", "ulimits:",
		"extra_hosts:", "dns:", "dns_search:", "dns_opt:", "read_only: true",
		"stop_signal: SIGTERM", "stop_grace_period: 30s",
		"endpoint_mode: dnsrr", "max_replicas_per_node:", "pids:",
		"delay: 5s", "window: 2m0s", "update_config:", "rollback_config:",
		"mode: 0o400", "uid:", "gid:", "logging:", "driver: json-file",
	} {
		require.Contains(t, y, want, "reconstructed YAML missing %q", want)
	}

	// The octal mode decodes back to the same bits.
	var back ComposeFile
	require.NoError(t, yaml.Unmarshal(out, &back))
	require.Equal(t, 256, back.Services["web"].Secrets[0]["mode"])
}

// TestReconstruct_OmitsDeployDefaults is the #662 regression: `docker stack
// deploy` fills unset target/uid/gid/mode with source/"0"/"0"/0o444 and the
// daemon reports an unset endpoint mode as "vip", so none may be echoed back.
func TestReconstruct_OmitsDeployDefaults(t *testing.T) {
	const raw = `{
	  "Spec": {
	    "Name": "ci_runner",
	    "TaskTemplate": {
	      "ContainerSpec": {
	        "Image": "gitlab/gitlab-runner:latest",
	        "Secrets": [
	          {"SecretName": "gitlab-runner-cache-access-key", "File": {"Name": "gitlab-runner-cache-access-key", "UID": "0", "GID": "0", "Mode": 292}}
	        ],
	        "Configs": [
	          {"ConfigName": "runner-config", "File": {"Name": "runner-config", "UID": "0", "GID": "0", "Mode": 292}}
	        ]
	      }
	    },
	    "EndpointSpec": {"Mode": "vip"}
	  }
	}`

	var si ServiceInspect
	require.NoError(t, json.Unmarshal([]byte(raw), &si))

	cs := assembleCompose([]ServiceInspect{si}, "ci", nil).Services["runner"]
	require.Equal(t, []map[string]any{{"source": "gitlab-runner-cache-access-key"}}, cs.Secrets)
	require.Equal(t, []map[string]any{{"source": "runner-config"}}, cs.Configs)
	require.NotContains(t, cs.Deploy, "endpoint_mode")
}

func TestComposeUpdateConfig_FullAndNil(t *testing.T) {
	require.Nil(t, composeUpdateConfig(nil))
	require.Nil(t, composeUpdateConfig(&UpdateConfig{}))
	uc := composeUpdateConfig(&UpdateConfig{
		Parallelism: 2, Delay: int64(10 * time.Second), FailureAction: "pause",
		Monitor: int64(time.Minute), MaxFailureRatio: 0.5, Order: "start-first",
	})
	require.Equal(t, 2, uc["parallelism"])
	require.Equal(t, "10s", uc["delay"])
	require.Equal(t, "pause", uc["failure_action"])
	require.Equal(t, "1m0s", uc["monitor"])
	require.Equal(t, float32(0.5), uc["max_failure_ratio"])
	require.Equal(t, "start-first", uc["order"])
}

func TestComposeExtraHosts_Reorder(t *testing.T) {
	// Swarm host-file order "IP host [aliases]" → compose "host:IP" per name.
	got := composeExtraHosts([]string{"10.0.0.1 db.local db", "192.168.0.2 cache"})
	require.Equal(t, []string{"db.local:10.0.0.1", "db:10.0.0.1", "cache:192.168.0.2"}, got)
	require.Nil(t, composeExtraHosts(nil))
	require.Nil(t, composeExtraHosts([]string{"malformed"})) // no hostname → skipped
}

func TestComposeUlimits_MapShape(t *testing.T) {
	require.Nil(t, composeUlimits(nil))
	got := composeUlimits([]Ulimit{{Name: "nofile", Soft: 1000, Hard: 2000}, {Name: ""}})
	require.Equal(t, map[string]any{"nofile": map[string]any{"soft": int64(1000), "hard": int64(2000)}}, got)
}

func TestApplyFilePerms(t *testing.T) {
	ref := map[string]any{}
	applyFilePerms(ref, "100", "101", 0o400)
	require.Equal(t, map[string]any{"uid": "100", "gid": "101", "mode": fileMode(0o400)}, ref)
	defaults := map[string]any{}
	applyFilePerms(defaults, "0", "0", 0o444) // what `docker stack deploy` fills in
	require.Empty(t, defaults)
	bare := map[string]any{}
	applyFilePerms(bare, "", "", 0)
	require.Empty(t, bare)
}

// Capture side: the new rich ContainerSpec / ServiceSpec fields must unmarshal
// from `docker service inspect` JSON (mirrors the parse-drop class of #430).
func TestServiceInspect_CapturesRichContainerSpec(t *testing.T) {
	raw := []byte(`{"Spec":{"TaskTemplate":{"ContainerSpec":{
		"CapabilityAdd":["NET_ADMIN"],"Sysctls":{"k":"v"},
		"Ulimits":[{"Name":"nofile","Soft":1,"Hard":2}],
		"Hosts":["1.2.3.4 h"],"DNSConfig":{"Nameservers":["8.8.8.8"]},
		"ReadOnly":true,"Init":true,"StopSignal":"SIGINT","StopGracePeriod":5000000000}}}}`)
	var si ServiceInspect
	require.NoError(t, json.Unmarshal(raw, &si))
	cspec := si.Spec.TaskTemplate.ContainerSpec
	require.Equal(t, []string{"NET_ADMIN"}, cspec.CapabilityAdd)
	require.Equal(t, "v", cspec.Sysctls["k"])
	require.Equal(t, []Ulimit{{Name: "nofile", Soft: 1, Hard: 2}}, cspec.Ulimits)
	require.Equal(t, []string{"1.2.3.4 h"}, cspec.Hosts)
	require.Equal(t, []string{"8.8.8.8"}, cspec.DNSConfig.Nameservers)
	require.True(t, cspec.ReadOnly)
	require.NotNil(t, cspec.Init)
	require.Equal(t, "SIGINT", cspec.StopSignal)
	require.Equal(t, int64(5000000000), cspec.StopGracePeriod)
}

func TestServiceInspect_CapturesUpdateConfigAndEndpointMode(t *testing.T) {
	raw := []byte(`{"Spec":{"UpdateConfig":{"Parallelism":2,"Order":"start-first"},
		"EndpointSpec":{"Mode":"dnsrr"}}}`)
	var si ServiceInspect
	require.NoError(t, json.Unmarshal(raw, &si))
	require.NotNil(t, si.Spec.UpdateConfig)
	require.Equal(t, uint64(2), si.Spec.UpdateConfig.Parallelism)
	require.Equal(t, "start-first", si.Spec.UpdateConfig.Order)
	require.NotNil(t, si.Spec.EndpointSpec)
	require.Equal(t, "dnsrr", si.Spec.EndpointSpec.Mode)
}
