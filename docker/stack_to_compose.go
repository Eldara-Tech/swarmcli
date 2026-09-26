// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ComposeFile represents a Docker Compose file structure
type ComposeFile struct {
	Version  string                    `yaml:"version"`
	Services map[string]ComposeService `yaml:"services"`
	Networks map[string]map[string]any `yaml:"networks,omitempty"`
	Volumes  map[string]map[string]any `yaml:"volumes,omitempty"`
	Secrets  map[string]map[string]any `yaml:"secrets,omitempty"`
	Configs  map[string]map[string]any `yaml:"configs,omitempty"`
}

// ComposeService represents a service in a Docker Compose file
type ComposeService struct {
	Image       string            `yaml:"image,omitempty"`
	Command     any               `yaml:"command,omitempty"` // string or []string
	Entrypoint  any               `yaml:"entrypoint,omitempty"`
	WorkingDir  string            `yaml:"working_dir,omitempty"`
	User        string            `yaml:"user,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Ports       []string          `yaml:"ports,omitempty"`
	Networks    any               `yaml:"networks,omitempty"` // []string or map
	Volumes     []string          `yaml:"volumes,omitempty"`
	Secrets     []map[string]any  `yaml:"secrets,omitempty"`
	Configs     []map[string]any  `yaml:"configs,omitempty"`
	// Container runtime / security settings that round-trip through
	// `docker stack deploy` (see #430).
	Hostname        string            `yaml:"hostname,omitempty"`
	CapAdd          []string          `yaml:"cap_add,omitempty"`
	CapDrop         []string          `yaml:"cap_drop,omitempty"`
	Sysctls         map[string]string `yaml:"sysctls,omitempty"`
	Ulimits         map[string]any    `yaml:"ulimits,omitempty"`
	ExtraHosts      []string          `yaml:"extra_hosts,omitempty"`
	DNS             []string          `yaml:"dns,omitempty"`
	DNSSearch       []string          `yaml:"dns_search,omitempty"`
	DNSOpt          []string          `yaml:"dns_opt,omitempty"`
	ReadOnly        bool              `yaml:"read_only,omitempty"`
	Init            *bool             `yaml:"init,omitempty"`
	StopSignal      string            `yaml:"stop_signal,omitempty"`
	StopGracePeriod string            `yaml:"stop_grace_period,omitempty"`
	Deploy          map[string]any    `yaml:"deploy,omitempty"`
	Healthcheck     *Healthcheck      `yaml:"healthcheck,omitempty"`
	Logging         *Logging          `yaml:"logging,omitempty"`
	Extra           map[string]any    `yaml:",inline,omitempty"` // fallback
}

// Logging is the compose-shaped view of a service's log driver, mirroring the
// Swarm `TaskTemplate.LogDriver` (`*swarm.Driver`). It is used both in
// reconstructed Compose YAML and in stack-inspect JSON. The compose `logging:`
// block carries a `driver:` name and an `options:` map.
type Logging struct {
	Driver  string            `json:"driver,omitempty" yaml:"driver,omitempty"`
	Options map[string]string `json:"options,omitempty" yaml:"options,omitempty"`
}

// Healthcheck is the compose-shaped view of a service healthcheck, used both
// in reconstructed Compose YAML and in stack-inspect JSON. Durations are
// rendered as compose duration strings (e.g. "30s").
type Healthcheck struct {
	Test          []string `json:"test,omitempty" yaml:"test,omitempty"`
	Interval      string   `json:"interval,omitempty" yaml:"interval,omitempty"`
	Timeout       string   `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	StartPeriod   string   `json:"start_period,omitempty" yaml:"start_period,omitempty"`
	StartInterval string   `json:"start_interval,omitempty" yaml:"start_interval,omitempty"`
	Retries       int      `json:"retries,omitempty" yaml:"retries,omitempty"`
	Disable       bool     `json:"disable,omitempty" yaml:"disable,omitempty"`
}

// ServiceInspect represents Docker service inspect output (partial)
type ServiceInspect struct {
	Spec ServiceSpec `json:"Spec"`
}

// ServiceSpec represents the service specification
type ServiceSpec struct {
	Name           string            `json:"Name"`
	Labels         map[string]string `json:"Labels"`
	TaskTemplate   TaskTemplate      `json:"TaskTemplate"`
	Mode           ServiceMode       `json:"Mode"`
	Networks       []NetRef          `json:"Networks"`
	EndpointSpec   *EndpointSpec     `json:"EndpointSpec,omitempty"`
	UpdateConfig   *UpdateConfig     `json:"UpdateConfig,omitempty"`
	RollbackConfig *UpdateConfig     `json:"RollbackConfig,omitempty"`
}

// UpdateConfig mirrors the Swarm `UpdateConfig` (used for both the update and
// rollback strategies). Durations (Delay, Monitor) arrive as nanosecond
// integers over the `docker service inspect` CLI JSON.
type UpdateConfig struct {
	Parallelism     uint64  `json:"Parallelism,omitempty"`
	Delay           int64   `json:"Delay,omitempty"`
	FailureAction   string  `json:"FailureAction,omitempty"`
	Monitor         int64   `json:"Monitor,omitempty"`
	MaxFailureRatio float32 `json:"MaxFailureRatio,omitempty"`
	Order           string  `json:"Order,omitempty"`
}

// ServiceMode represents the service mode (replicated or global)
type ServiceMode struct {
	Replicated *struct {
		Replicas *uint64 `json:"Replicas"`
	} `json:"Replicated,omitempty"`
	Global any `json:"Global,omitempty"`
}

// TaskTemplate represents the task template specification
type TaskTemplate struct {
	ContainerSpec *ContainerSpec `json:"ContainerSpec,omitempty"`
	Resources     *Resources     `json:"Resources,omitempty"`
	RestartPolicy *RestartPolicy `json:"RestartPolicy,omitempty"`
	Placement     *Placement     `json:"Placement,omitempty"`
	Networks      []NetRef       `json:"Networks,omitempty"`
	ForceUpdate   uint64         `json:"ForceUpdate,omitempty"`
	LogDriver     *LogDriver     `json:"LogDriver,omitempty"`
}

// LogDriver represents the Swarm `TaskTemplate.LogDriver` (`*swarm.Driver`):
// the logging driver name plus its options. It is nil when the service does
// not pin a log driver (the daemon default is used).
type LogDriver struct {
	Name    string            `json:"Name,omitempty"`
	Options map[string]string `json:"Options,omitempty"`
}

// ContainerSpec represents the container specification
type ContainerSpec struct {
	Image    string            `json:"Image"`
	Args     []string          `json:"Args,omitempty"`
	Command  []string          `json:"Command,omitempty"`
	Env      []string          `json:"Env,omitempty"`
	Dir      string            `json:"Dir,omitempty"`
	User     string            `json:"User,omitempty"`
	Hostname string            `json:"Hostname,omitempty"`
	Labels   map[string]string `json:"Labels,omitempty"`
	Mounts   []Mount           `json:"Mounts,omitempty"`
	Secrets  []SecretRef       `json:"Secrets,omitempty"`
	Configs  []ConfigRef       `json:"Configs,omitempty"`
	// Healthcheck durations arrive as nanosecond integers over the
	// `docker service inspect` CLI JSON.
	Healthcheck *HealthConfigJSON `json:"Healthcheck,omitempty"`
	// Runtime / security fields (see #430). StopGracePeriod is a nanosecond
	// integer; Init is a tri-state pointer (nil = inherit image default).
	CapabilityAdd   []string          `json:"CapabilityAdd,omitempty"`
	CapabilityDrop  []string          `json:"CapabilityDrop,omitempty"`
	Sysctls         map[string]string `json:"Sysctls,omitempty"`
	Ulimits         []Ulimit          `json:"Ulimits,omitempty"`
	Hosts           []string          `json:"Hosts,omitempty"`
	DNSConfig       *DNSConfig        `json:"DNSConfig,omitempty"`
	ReadOnly        bool              `json:"ReadOnly,omitempty"`
	Init            *bool             `json:"Init,omitempty"`
	StopSignal      string            `json:"StopSignal,omitempty"`
	StopGracePeriod int64             `json:"StopGracePeriod,omitempty"`
}

// Ulimit mirrors a Swarm ContainerSpec ulimit entry (go-units `Ulimit`, which
// carries no JSON tags, so the keys are PascalCase).
type Ulimit struct {
	Name string `json:"Name"`
	Hard int64  `json:"Hard"`
	Soft int64  `json:"Soft"`
}

// DNSConfig mirrors the Swarm ContainerSpec.DNSConfig block.
type DNSConfig struct {
	Nameservers []string `json:"Nameservers,omitempty"`
	Search      []string `json:"Search,omitempty"`
	Options     []string `json:"Options,omitempty"`
}

// HealthConfigJSON captures the Healthcheck block of a `docker service inspect`
// ContainerSpec. Durations are nanosecond integers.
type HealthConfigJSON struct {
	Test          []string `json:"Test,omitempty"`
	Interval      int64    `json:"Interval,omitempty"`
	Timeout       int64    `json:"Timeout,omitempty"`
	StartPeriod   int64    `json:"StartPeriod,omitempty"`
	StartInterval int64    `json:"StartInterval,omitempty"`
	Retries       int      `json:"Retries,omitempty"`
}

// Mount represents a mount specification
type Mount struct {
	Type        string `json:"Type"` // bind, volume, tmpfs
	Source      string `json:"Source,omitempty"`
	Target      string `json:"Target,omitempty"`
	ReadOnly    bool   `json:"ReadOnly,omitempty"`
	BindOptions *struct {
		Propagation string `json:"Propagation,omitempty"`
	} `json:"BindOptions,omitempty"`
	VolumeOptions *struct {
		NoCopy       bool              `json:"NoCopy,omitempty"`
		Labels       map[string]string `json:"Labels,omitempty"`
		DriverConfig *struct {
			Name    string            `json:"Name,omitempty"`
			Options map[string]string `json:"Options,omitempty"`
		} `json:"DriverConfig,omitempty"`
	} `json:"VolumeOptions,omitempty"`
	TmpfsOptions *struct {
		SizeBytes int64  `json:"SizeBytes,omitempty"`
		Mode      uint32 `json:"Mode,omitempty"`
	} `json:"TmpfsOptions,omitempty"`
}

// SecretRef represents a secret reference
type SecretRef struct {
	SecretID   string `json:"SecretID"`
	SecretName string `json:"SecretName"`
	File       *struct {
		Name string `json:"Name"`
		UID  string `json:"UID,omitempty"`
		GID  string `json:"GID,omitempty"`
		Mode uint32 `json:"Mode,omitempty"`
	} `json:"File,omitempty"`
}

// ConfigRef represents a config reference
type ConfigRef struct {
	ConfigID   string `json:"ConfigID"`
	ConfigName string `json:"ConfigName"`
	File       *struct {
		Name string `json:"Name"`
		UID  string `json:"UID,omitempty"`
		GID  string `json:"GID,omitempty"`
		Mode uint32 `json:"Mode,omitempty"`
	} `json:"File,omitempty"`
}

// NetRef represents a network reference
type NetRef struct {
	Target  string   `json:"Target"` // network ID
	Aliases []string `json:"Aliases,omitempty"`
}

// Resources represents resource constraints
type Resources struct {
	Limits       *ResourceSpec `json:"Limits,omitempty"`
	Reservations *ResourceSpec `json:"Reservations,omitempty"`
}

// ResourceSpec represents resource specifications. Pids is only ever set on
// the Limits side (Swarm carries no reservation pids).
type ResourceSpec struct {
	NanoCPUs    int64 `json:"NanoCPUs,omitempty"`
	MemoryBytes int64 `json:"MemoryBytes,omitempty"`
	Pids        int64 `json:"Pids,omitempty"`
}

// RestartPolicy represents the restart policy
type RestartPolicy struct {
	Condition   string  `json:"Condition,omitempty"`
	Delay       int64   `json:"Delay,omitempty"`
	MaxAttempts *uint64 `json:"MaxAttempts,omitempty"`
	Window      int64   `json:"Window,omitempty"`
}

// Placement represents placement constraints
type Placement struct {
	Constraints []string `json:"Constraints,omitempty"`
	Preferences []any    `json:"Preferences,omitempty"`
	MaxReplicas *uint64  `json:"MaxReplicas,omitempty"`
}

// EndpointSpec represents the endpoint specification
type EndpointSpec struct {
	Mode  string       `json:"Mode,omitempty"` // vip/dnsrr
	Ports []PortConfig `json:"Ports,omitempty"`
}

// PortConfig represents a port configuration
type PortConfig struct {
	Protocol      string `json:"Protocol,omitempty"` // tcp/udp
	TargetPort    uint32 `json:"TargetPort,omitempty"`
	PublishedPort uint32 `json:"PublishedPort,omitempty"`
	PublishMode   string `json:"PublishMode,omitempty"` // ingress/host
}

// ReconstructStackCompose reconstructs a Docker Compose file from a running stack
func ReconstructStackCompose(stackName string) (string, error) {
	// Get list of services in the stack
	serviceNames, err := getStackServices(stackName)
	if err != nil {
		return "", fmt.Errorf("failed to list services: %w", err)
	}

	if len(serviceNames) == 0 {
		return "", fmt.Errorf("no services found in stack '%s'", stackName)
	}

	// Build network ID to name mapping
	netID2Name, err := dockerNetworkIDToNameMap()
	if err != nil {
		l().Warnf("Could not build network ID->name map: %v", err)
		netID2Name = map[string]string{}
	}

	// Inspect every service, then hand the parsed specs to the pure
	// assembler. Keeping exec out of assembleCompose lets the reconstruction
	// be unit-tested without Docker (see #430).
	var services []ServiceInspect
	for _, fullSvcName := range serviceNames {
		si, err := inspectService(fullSvcName)
		if err != nil {
			l().Warnf("Failed to inspect service %s: %v", fullSvcName, err)
			continue
		}
		services = append(services, *si)
	}

	cf := assembleCompose(services, stackName, netID2Name)

	// Remove empty top-level sections (does NOT suppress a "default"
	// network — see issue #363)
	pruneEmptySections(&cf)

	// Marshal to YAML
	y, err := yaml.Marshal(&cf)
	if err != nil {
		return "", fmt.Errorf("yaml marshal error: %w", err)
	}

	return string(y), nil
}

// assembleCompose builds the Compose model from already-inspected service
// specs. It performs no I/O, so it is the pure seam that field-coverage tests
// drive directly (see #430): feed it ServiceInspect fixtures and assert on the
// returned ComposeFile.
func assembleCompose(services []ServiceInspect, stackName string, netID2Name map[string]string) ComposeFile {
	cf := ComposeFile{
		Version:  "3.9",
		Services: map[string]ComposeService{},
		Networks: map[string]map[string]any{},
		Volumes:  map[string]map[string]any{},
		Secrets:  map[string]map[string]any{},
		Configs:  map[string]map[string]any{},
	}

	for i := range services {
		si := &services[i]
		key := stripStackPrefix(stackName, si.Spec.Name)
		cf.Services[key] = cf.buildService(si, stackName, netID2Name)
	}

	return cf
}

// declareNet declares a top-level network on the compose file.
func (cf *ComposeFile) declareNet(netName string, external bool) {
	if netName == "" {
		return
	}
	if _, ok := cf.Networks[netName]; !ok {
		props := map[string]any{}
		if external {
			props["external"] = true
		}
		cf.Networks[netName] = props
	}
}

// declareVolume declares a top-level named volume on the compose file.
func (cf *ComposeFile) declareVolume(volName string, m *Mount, external bool) {
	if volName == "" {
		return
	}
	if _, ok := cf.Volumes[volName]; ok {
		return
	}
	vol := map[string]any{}
	if external {
		vol["external"] = true
	}
	if m != nil && m.VolumeOptions != nil && m.VolumeOptions.DriverConfig != nil {
		dc := m.VolumeOptions.DriverConfig
		if dc.Name != "" {
			vol["driver"] = dc.Name
			delete(vol, "external")
		}
		if len(dc.Options) > 0 {
			vol["driver_opts"] = dc.Options
		}
	}
	cf.Volumes[volName] = vol
}

// declareSecretExternal declares a top-level external secret on the compose file.
func (cf *ComposeFile) declareSecretExternal(name string) {
	if name == "" {
		return
	}
	if _, ok := cf.Secrets[name]; !ok {
		cf.Secrets[name] = map[string]any{"external": true}
	}
}

// declareConfigExternal declares a top-level external config on the compose file.
func (cf *ComposeFile) declareConfigExternal(name string) {
	if name == "" {
		return
	}
	if _, ok := cf.Configs[name]; !ok {
		cf.Configs[name] = map[string]any{"external": true}
	}
}

// buildService renders one inspected service into a ComposeService, declaring
// any top-level networks/volumes/secrets/configs it references on cf.
func (cf *ComposeFile) buildService(si *ServiceInspect, stackName string, netID2Name map[string]string) ComposeService {
	cs := ComposeService{}

	// ServiceSpec.Labels → deploy.labels in Compose
	deployLabels := filterLabels(si.Spec.Labels)

	if si.Spec.TaskTemplate.ContainerSpec != nil {
		cspec := si.Spec.TaskTemplate.ContainerSpec
		cs.Image = stripImageDigest(cspec.Image)

		if cspec.Dir != "" {
			cs.WorkingDir = cspec.Dir
		}
		if cspec.User != "" {
			cs.User = cspec.User
		}
		if cspec.Hostname != "" {
			cs.Hostname = cspec.Hostname
		}
		if env := parseKeyValEnv(cspec.Env); env != nil {
			cs.Environment = env
		}

		// ContainerSpec.Labels → service-level labels in Compose
		if cl := filterLabels(cspec.Labels); len(cl) > 0 {
			cs.Labels = cl
		}

		// Command / Args — escape $ → $$ for Compose variable interpolation
		if len(cspec.Args) > 0 {
			cs.Command = escapeComposeArgs(cspec.Args)
		} else if len(cspec.Command) > 0 {
			cs.Command = escapeComposeArgs(cspec.Command)
		}

		// Mounts -> volumes
		for _, m := range cspec.Mounts {
			if m.Target == "" {
				continue
			}
			ro := ""
			if m.ReadOnly {
				ro = ":ro"
			}
			switch m.Type {
			case "bind":
				if m.Source != "" {
					cs.Volumes = append(cs.Volumes, fmt.Sprintf("%s:%s%s", m.Source, m.Target, ro))
				}
			case "volume":
				src := m.Source
				if src == "" {
					continue // anonymous volume, cannot round-trip
				}
				stripped := stripStackPrefix(stackName, src)
				isExternal := stripped == src // no prefix removed → external
				cf.declareVolume(stripped, &m, isExternal)
				cs.Volumes = append(cs.Volumes, fmt.Sprintf("%s:%s%s", stripped, m.Target, ro))
			case "tmpfs":
				if cs.Extra == nil {
					cs.Extra = map[string]any{}
				}
				tmpfs, _ := cs.Extra["tmpfs"].([]any)
				tmpfs = append(tmpfs, m.Target)
				cs.Extra["tmpfs"] = tmpfs
			}
		}

		// Secrets
		for _, s := range cspec.Secrets {
			if s.SecretName == "" {
				continue
			}
			cf.declareSecretExternal(s.SecretName)
			ref := map[string]any{"source": s.SecretName}
			if s.File != nil {
				// stack deploy defaults the target to the source (#662)
				if s.File.Name != "" && s.File.Name != s.SecretName {
					ref["target"] = s.File.Name
				}
				applyFilePerms(ref, s.File.UID, s.File.GID, s.File.Mode)
			}
			cs.Secrets = append(cs.Secrets, ref)
		}

		// Configs
		for _, c := range cspec.Configs {
			if c.ConfigName == "" {
				continue
			}
			cf.declareConfigExternal(c.ConfigName)
			ref := map[string]any{"source": c.ConfigName}
			if c.File != nil {
				if c.File.Name != "" && c.File.Name != c.ConfigName {
					ref["target"] = c.File.Name
				}
				applyFilePerms(ref, c.File.UID, c.File.GID, c.File.Mode)
			}
			cs.Configs = append(cs.Configs, ref)
		}

		// Healthcheck
		if h := cspec.Healthcheck; h != nil {
			cs.Healthcheck = composeHealthcheck(h.Test, h.Interval, h.Timeout,
				h.StartPeriod, h.StartInterval, h.Retries, true /*escape*/)
		}

		// Runtime / security settings that round-trip through stack deploy (#430).
		if len(cspec.CapabilityAdd) > 0 {
			cs.CapAdd = cspec.CapabilityAdd
		}
		if len(cspec.CapabilityDrop) > 0 {
			cs.CapDrop = cspec.CapabilityDrop
		}
		if len(cspec.Sysctls) > 0 {
			cs.Sysctls = cspec.Sysctls
		}
		if ul := composeUlimits(cspec.Ulimits); len(ul) > 0 {
			cs.Ulimits = ul
		}
		if eh := composeExtraHosts(cspec.Hosts); len(eh) > 0 {
			cs.ExtraHosts = eh
		}
		if dc := cspec.DNSConfig; dc != nil {
			if len(dc.Nameservers) > 0 {
				cs.DNS = dc.Nameservers
			}
			if len(dc.Search) > 0 {
				cs.DNSSearch = dc.Search
			}
			if len(dc.Options) > 0 {
				cs.DNSOpt = dc.Options
			}
		}
		if cspec.ReadOnly {
			cs.ReadOnly = true
		}
		if cspec.Init != nil {
			cs.Init = cspec.Init
		}
		if cspec.StopSignal != "" {
			cs.StopSignal = cspec.StopSignal
		}
		if cspec.StopGracePeriod > 0 {
			cs.StopGracePeriod = time.Duration(cspec.StopGracePeriod).String()
		}
	}

	// Logging driver — TaskTemplate.LogDriver → compose `logging:`
	if ld := si.Spec.TaskTemplate.LogDriver; ld != nil {
		cs.Logging = composeLogging(ld.Name, ld.Options)
	}

	// Ports
	if si.Spec.EndpointSpec != nil {
		for _, p := range si.Spec.EndpointSpec.Ports {
			if p.TargetPort == 0 {
				continue
			}
			cs.Ports = append(cs.Ports, formatPort(p.PublishedPort, p.TargetPort, p.Protocol))
		}
	}

	// Networks
	netRefs := append([]NetRef{}, si.Spec.TaskTemplate.Networks...)
	netRefs = append(netRefs, si.Spec.Networks...)
	netNames := make(map[string]struct{})
	for _, nr := range netRefs {
		nm := netID2Name[nr.Target]
		if nm == "" {
			nm = nr.Target
		}
		if nm == "" {
			continue
		}
		stripped := stripStackPrefix(stackName, nm)
		isExternal := stripped == nm // no prefix removed → external
		netNames[stripped] = struct{}{}
		cf.declareNet(stripped, isExternal)
	}
	if len(netNames) > 0 {
		var nets []string
		for n := range netNames {
			nets = append(nets, n)
		}
		sort.Strings(nets)
		cs.Networks = nets
	}

	// Deploy section
	deploy := make(map[string]any)

	// Mode/replicas
	if si.Spec.Mode.Replicated != nil && si.Spec.Mode.Replicated.Replicas != nil {
		deploy["replicas"] = int(*si.Spec.Mode.Replicated.Replicas)
	} else if si.Spec.Mode.Global != nil {
		deploy["mode"] = "global"
	}

	// Endpoint mode (vip/dnsrr); the daemon reports an unset mode as vip (#662)
	if si.Spec.EndpointSpec != nil && si.Spec.EndpointSpec.Mode != "" && si.Spec.EndpointSpec.Mode != "vip" {
		deploy["endpoint_mode"] = si.Spec.EndpointSpec.Mode
	}

	// Placement
	if pl := si.Spec.TaskTemplate.Placement; pl != nil {
		placement := map[string]any{}
		if len(pl.Constraints) > 0 {
			placement["constraints"] = pl.Constraints
		}
		if pl.MaxReplicas != nil && *pl.MaxReplicas > 0 {
			placement["max_replicas_per_node"] = int(*pl.MaxReplicas)
		}
		if len(placement) > 0 {
			deploy["placement"] = placement
		}
	}

	// Resources
	if si.Spec.TaskTemplate.Resources != nil {
		res := make(map[string]any)
		if si.Spec.TaskTemplate.Resources.Limits != nil {
			lim := make(map[string]any)
			if si.Spec.TaskTemplate.Resources.Limits.NanoCPUs != 0 {
				lim["cpus"] = nanoCPUToCPUString(si.Spec.TaskTemplate.Resources.Limits.NanoCPUs)
			}
			if si.Spec.TaskTemplate.Resources.Limits.MemoryBytes != 0 {
				lim["memory"] = bytesToHuman(si.Spec.TaskTemplate.Resources.Limits.MemoryBytes)
			}
			if si.Spec.TaskTemplate.Resources.Limits.Pids != 0 {
				lim["pids"] = si.Spec.TaskTemplate.Resources.Limits.Pids
			}
			if len(lim) > 0 {
				res["limits"] = lim
			}
		}
		if si.Spec.TaskTemplate.Resources.Reservations != nil {
			resv := make(map[string]any)
			if si.Spec.TaskTemplate.Resources.Reservations.NanoCPUs != 0 {
				resv["cpus"] = nanoCPUToCPUString(si.Spec.TaskTemplate.Resources.Reservations.NanoCPUs)
			}
			if si.Spec.TaskTemplate.Resources.Reservations.MemoryBytes != 0 {
				resv["memory"] = bytesToHuman(si.Spec.TaskTemplate.Resources.Reservations.MemoryBytes)
			}
			if len(resv) > 0 {
				res["reservations"] = resv
			}
		}
		if len(res) > 0 {
			deploy["resources"] = res
		}
	}

	// Restart policy
	if rpSpec := si.Spec.TaskTemplate.RestartPolicy; rpSpec != nil {
		rp := make(map[string]any)
		if rpSpec.Condition != "" {
			rp["condition"] = rpSpec.Condition
		}
		if rpSpec.Delay > 0 {
			rp["delay"] = time.Duration(rpSpec.Delay).String()
		}
		if rpSpec.MaxAttempts != nil && *rpSpec.MaxAttempts > 0 {
			rp["max_attempts"] = int(*rpSpec.MaxAttempts)
		}
		if rpSpec.Window > 0 {
			rp["window"] = time.Duration(rpSpec.Window).String()
		}
		if len(rp) > 0 {
			deploy["restart_policy"] = rp
		}
	}

	// Update / rollback strategy
	if uc := composeUpdateConfig(si.Spec.UpdateConfig); uc != nil {
		deploy["update_config"] = uc
	}
	if rc := composeUpdateConfig(si.Spec.RollbackConfig); rc != nil {
		deploy["rollback_config"] = rc
	}

	if len(deployLabels) > 0 {
		deploy["labels"] = deployLabels
	}

	if len(deploy) > 0 {
		cs.Deploy = deploy
	}

	return cs
}

// contextArgs prefixes a docker CLI invocation with the session's context.
//
// Without it these commands follow ~/.docker/config.json, which is neither the
// context the SDK client is connected to nor the one $DOCKER_CONTEXT names —
// so a stack read here could describe a different swarm from the one it was
// selected in (#611).
func contextArgs(extra ...string) []string {
	args := make([]string, 0, 2+len(extra))
	if ctxName, err := SessionContext(); err == nil && strings.TrimSpace(ctxName) != "" {
		args = append(args, "--context", ctxName)
	}
	return append(args, extra...)
}

// stackServicesArgs builds the argument list for listing a stack's services.
func stackServicesArgs(stackName string) []string {
	return contextArgs("stack", "services", stackName, "--format", "{{.Name}}")
}

// serviceInspectArgs builds the argument list for inspecting one service.
func serviceInspectArgs(serviceName string) []string {
	return contextArgs("service", "inspect", serviceName)
}

// networkListArgs builds the argument list for the id-to-name network listing.
func networkListArgs() []string {
	return contextArgs("network", "ls", "--no-trunc", "--format", "{{.ID}}\t{{.Name}}")
}

// getStackServices returns the list of service names for a stack
func getStackServices(stackName string) ([]string, error) {
	cmd := exec.Command("docker", stackServicesArgs(stackName)...)
	var out bytes.Buffer
	var errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("docker stack services failed: %v\n%s", err, errb.String())
	}

	var serviceNames []string
	for _, ln := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			serviceNames = append(serviceNames, ln)
		}
	}
	sort.Strings(serviceNames)
	return serviceNames, nil
}

// inspectService returns the inspect data for a service
func inspectService(serviceName string) (*ServiceInspect, error) {
	cmd := exec.Command("docker", serviceInspectArgs(serviceName)...)
	var out bytes.Buffer
	var errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("docker service inspect failed: %v\n%s", err, errb.String())
	}

	var arr []ServiceInspect
	if err := json.Unmarshal(out.Bytes(), &arr); err != nil || len(arr) == 0 {
		return nil, fmt.Errorf("failed to parse service inspect json: %w", err)
	}
	return &arr[0], nil
}

// dockerNetworkIDToNameMap builds a map of network IDs to names
func dockerNetworkIDToNameMap() (map[string]string, error) {
	cmd := exec.Command("docker", networkListArgs()...)
	var out bytes.Buffer
	var errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("docker network ls failed: %v\n%s", err, errb.String())
	}

	m := make(map[string]string)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		parts := strings.SplitN(ln, "\t", 2)
		if len(parts) == 2 {
			m[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return m, nil
}

// parseKeyValEnv parses environment variables in KEY=VALUE format
func parseKeyValEnv(env []string) map[string]string {
	m := make(map[string]string)
	for _, kv := range env {
		if kv == "" {
			continue
		}
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 1 {
			m[parts[0]] = ""
		} else {
			m[parts[0]] = parts[1]
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// filterLabels returns a copy of labels with Docker-internal keys removed.
// Returns nil if no user labels remain.
func filterLabels(labels map[string]string) map[string]string {
	out := make(map[string]string)
	for k, v := range labels {
		if !strings.HasPrefix(k, "com.docker.stack.") {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// escapeComposeInterpolation escapes $ as $$ so that Compose does not
// attempt variable interpolation on reconstructed command strings.
func escapeComposeInterpolation(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}

// composeHealthcheck builds the compose-shaped Healthcheck from a service's
// healthcheck spec. Durations are raw nanoseconds (as Docker reports them) and
// are rendered as compose duration strings (e.g. "30s"). Returns nil when the
// spec carries nothing meaningful, i.e. the service inherits the image's
// healthcheck. When escape is true, Test elements are escaped for Compose
// variable interpolation (used in the reconstructed Compose YAML).
func composeHealthcheck(test []string, intervalNs, timeoutNs, startPeriodNs,
	startIntervalNs int64, retries int, escape bool) *Healthcheck {

	disabled := len(test) == 1 && test[0] == "NONE"
	if !disabled && len(test) == 0 && intervalNs == 0 && timeoutNs == 0 &&
		startPeriodNs == 0 && startIntervalNs == 0 && retries == 0 {
		return nil // inherit image healthcheck — nothing specified
	}

	hc := &Healthcheck{}
	if disabled {
		hc.Disable = true
		return hc
	}
	if escape {
		hc.Test = escapeComposeArgs(test)
	} else {
		hc.Test = test
	}
	if intervalNs > 0 {
		hc.Interval = time.Duration(intervalNs).String()
	}
	if timeoutNs > 0 {
		hc.Timeout = time.Duration(timeoutNs).String()
	}
	if startPeriodNs > 0 {
		hc.StartPeriod = time.Duration(startPeriodNs).String()
	}
	if startIntervalNs > 0 {
		hc.StartInterval = time.Duration(startIntervalNs).String()
	}
	hc.Retries = retries
	return hc
}

// composeUpdateConfig renders a Swarm UpdateConfig into the compose-shaped
// deploy.update_config / deploy.rollback_config map. Durations (Delay, Monitor)
// arrive as nanoseconds and render as compose duration strings. Returns nil
// when nothing meaningful is set so no block is emitted (the spec is nil when
// the service pins no update/rollback strategy).
func composeUpdateConfig(uc *UpdateConfig) map[string]any {
	if uc == nil {
		return nil
	}
	m := map[string]any{}
	if uc.Parallelism != 0 {
		m["parallelism"] = int(uc.Parallelism)
	}
	if uc.Delay > 0 {
		m["delay"] = time.Duration(uc.Delay).String()
	}
	if uc.FailureAction != "" {
		m["failure_action"] = uc.FailureAction
	}
	if uc.Monitor > 0 {
		m["monitor"] = time.Duration(uc.Monitor).String()
	}
	if uc.MaxFailureRatio != 0 {
		m["max_failure_ratio"] = uc.MaxFailureRatio
	}
	if uc.Order != "" {
		m["order"] = uc.Order
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// composeLogging builds the compose-shaped Logging block from a service's log
// driver name and options. Returns nil when the service pins no log driver
// (Swarm reports LogDriver: nil / empty), so the daemon default is used and no
// `logging:` block is emitted. A driver with no name but with options still
// round-trips its options.
func composeLogging(name string, options map[string]string) *Logging {
	if name == "" && len(options) == 0 {
		return nil
	}
	return &Logging{
		Driver:  name,
		Options: options,
	}
}

// composeUlimits converts Swarm ContainerSpec ulimit entries into the compose
// `ulimits:` map (name -> {soft, hard}). Returns nil when there are none.
func composeUlimits(ulimits []Ulimit) map[string]any {
	if len(ulimits) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, u := range ulimits {
		if u.Name == "" {
			continue
		}
		out[u.Name] = map[string]any{"soft": u.Soft, "hard": u.Hard}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// composeExtraHosts converts Swarm ContainerSpec.Hosts entries (host-file
// order, "IP_address canonical_hostname [aliases...]") into compose
// extra_hosts entries ("hostname:IP"), emitting one entry per hostname. The
// reorder is required — emitting the raw Swarm string would corrupt the entry
// on the next deploy.
func composeExtraHosts(hosts []string) []string {
	var out []string
	for _, h := range hosts {
		fields := strings.Fields(h)
		if len(fields) < 2 {
			continue
		}
		ip := fields[0]
		for _, name := range fields[1:] {
			out = append(out, name+":"+ip)
		}
	}
	return out
}

// fileMode is a secret/config target mode, rendered in octal (0o400) rather
// than as the decimal Swarm reports (256); yaml.v3, the compose loader's parser,
// decodes it back to the same bits. Not 0400: YAML 1.2 reads that as decimal.
type fileMode uint32

// MarshalYAML renders the mode as an octal !!int scalar.
func (m fileMode) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("0o%o", uint32(m))}, nil
}

// applyFilePerms adds the compose secret/config target uid/gid/mode keys to a
// reference map when set. mode is the raw permission bits as Swarm reports them.
// `docker stack deploy` fills unset keys with "0"/"0"/0o444, so those are
// omitted: the next deploy writes them back unchanged (#662).
func applyFilePerms(ref map[string]any, uid, gid string, mode uint32) {
	if uid != "" && uid != "0" {
		ref["uid"] = uid
	}
	if gid != "" && gid != "0" {
		ref["gid"] = gid
	}
	if mode != 0 && mode != 0o444 {
		ref["mode"] = fileMode(mode)
	}
}

// escapeComposeArgs applies escapeComposeInterpolation to each element.
func escapeComposeArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = escapeComposeInterpolation(a)
	}
	return out
}

// stripImageDigest removes a trailing "@sha256:<digest>" pin so the
// reconstructed image is "repo:tag" rather than "repo:tag@sha256:...".
// Docker resolves and pins the digest at deploy time; the original
// Compose file only carried "repo:tag". See issue #363.
func stripImageDigest(image string) string {
	before, _, _ := strings.Cut(image, "@sha256:")
	return before
}

// formatPort renders a Compose short-syntax port mapping. The "/proto"
// suffix is omitted when proto is "tcp" (the Compose default) and kept
// for udp/sctp. See issue #363.
func formatPort(published, target uint32, proto string) string {
	if proto == "" {
		proto = "tcp"
	}
	base := fmt.Sprintf("%d", target)
	if published != 0 {
		base = fmt.Sprintf("%d:%d", published, target)
	}
	if proto == "tcp" {
		return base
	}
	return base + "/" + proto
}

// pruneEmptySections nils out empty top-level map sections so they are
// omitted from the marshaled Compose YAML (the structs use omitempty).
// It does NOT suppress a "default" network — see issue #363.
func pruneEmptySections(cf *ComposeFile) {
	if len(cf.Networks) == 0 {
		cf.Networks = nil
	}
	if len(cf.Volumes) == 0 {
		cf.Volumes = nil
	}
	if len(cf.Secrets) == 0 {
		cf.Secrets = nil
	}
	if len(cf.Configs) == 0 {
		cf.Configs = nil
	}
}

// stripStackPrefix removes the "<stack>_" prefix from a resource name
func stripStackPrefix(stack, full string) string {
	prefix := stack + "_"
	if strings.HasPrefix(full, prefix) {
		return strings.TrimPrefix(full, prefix)
	}
	return full
}

// nanoCPUToCPUString converts nano CPUs to CPU string
func nanoCPUToCPUString(n int64) string {
	f := float64(n) / 1e9
	s := strconv.FormatFloat(f, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// bytesToHuman converts bytes to human-readable format
func bytesToHuman(b int64) string {
	abs := b
	if abs < 0 {
		abs = -abs
	}
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
	)
	switch {
	case abs >= GiB && abs%GiB == 0:
		return fmt.Sprintf("%dG", b/GiB)
	case abs >= MiB && abs%MiB == 0:
		return fmt.Sprintf("%dM", b/MiB)
	case abs >= KiB && abs%KiB == 0:
		return fmt.Sprintf("%dK", b/KiB)
	default:
		return fmt.Sprintf("%d", b)
	}
}
