// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/swarm"
)

// getServiceMode returns the mode as `docker service ls` names it.
func getServiceMode(svc swarm.Service) string {
	switch {
	case svc.Spec.Mode.Replicated != nil:
		return "replicated"
	case svc.Spec.Mode.Global != nil:
		return "global"
	case svc.Spec.Mode.ReplicatedJob != nil:
		return "replicated job"
	case svc.Spec.Mode.GlobalJob != nil:
		return "global job"
	}
	return "-"
}

// getServiceAnonVolumes returns the paths at which svc's spec mounts a volume
// with no source — the long form's `type: volume` without one, or a short
// `- /data` — which Docker meets with a new anonymous volume for every task.
// Unconfirmed: the spec says so, no node has been asked.
func getServiceAnonVolumes(svc swarm.Service) []AnonVolume {
	cs := svc.Spec.TaskTemplate.ContainerSpec
	if cs == nil {
		return nil
	}
	var out []AnonVolume
	for _, m := range cs.Mounts {
		if m.Type == mount.TypeVolume && m.Source == "" {
			out = append(out, AnonVolume{Path: path.Clean(m.Target)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// sharedLocalTypes are the driver_opts `type`s with which the local driver mounts
// storage every node can reach, or none keeps (tmpfs): a task that moves either
// finds the same data or never had any to lose.
var sharedLocalTypes = map[string]bool{"nfs": true, "nfs4": true, "cifs": true, "smb3": true, "ceph": true, "tmpfs": true}

// getServiceUnpinnedVolumes returns the named volumes, sorted, that svc's tasks
// keep on the node running them while its placement constraints admit more than
// one of the given nodes. Every node counts, drained and down ones included: a
// constraint that only holds while a node is out of service does not pin
// anything. A global service runs one task per node and never moves one, so it
// has none.
func getServiceUnpinnedVolumes(svc swarm.Service, nodes []swarm.Node) []UnpinnedVolume {
	cs := svc.Spec.TaskTemplate.ContainerSpec
	if cs == nil || svc.Spec.Mode.Global != nil || svc.Spec.Mode.GlobalJob != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []UnpinnedVolume
	for _, m := range cs.Mounts {
		if m.Type != mount.TypeVolume || m.Source == "" || seen[m.Source] {
			continue
		}
		seen[m.Source] = true
		if v, ok := nodeLocalVolume(m); ok {
			out = append(out, v)
		}
	}
	if len(out) == 0 || eligibleNodeCount(svc, nodes) <= 1 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// nodeLocalVolume returns the named volume m mounts, and whether that volume
// keeps its data on the node that created it. `docker stack deploy` labels a
// volume the stack declares and names its driver only when the stack file does,
// so an unnamed driver there is the local one; a volume without the label
// (external, or mounted by a service outside any stack) may already exist with
// any driver.
func nodeLocalVolume(m mount.Mount) (UnpinnedVolume, bool) {
	var vo mount.VolumeOptions
	if m.VolumeOptions != nil {
		vo = *m.VolumeOptions
	}
	stack := vo.Labels["com.docker.stack.namespace"]
	v := UnpinnedVolume{Name: m.Source}
	if stack != "" {
		v.Name = stripStackPrefix(stack, m.Source)
	}
	if dc := vo.DriverConfig; dc != nil {
		local := dc.Name == "" || dc.Name == "local"
		return v, local && !sharedLocalTypes[dc.Options["type"]]
	}
	v.DriverUnknown = stack == ""
	return v, true
}

// getServiceImage returns the image name without the digest
func getServiceImage(svc swarm.Service) string {
	if svc.Spec.TaskTemplate.ContainerSpec == nil {
		return "-"
	}
	image := svc.Spec.TaskTemplate.ContainerSpec.Image
	// Strip digest if present
	if idx := strings.Index(image, "@sha256:"); idx != -1 {
		image = image[:idx]
	}
	return image
}

// getServicePorts returns a comma-separated list of published ports
func getServicePorts(svc swarm.Service) string {
	if len(svc.Endpoint.Ports) == 0 {
		return "-"
	}

	var ports []string
	for _, port := range svc.Endpoint.Ports {
		var portStr string
		if port.PublishedPort == 0 {
			continue
		}
		// Always show *: prefix for published ports (both ingress and host mode)
		portStr = fmt.Sprintf("*:%d", port.PublishedPort)
		// Always show target port if available
		if port.TargetPort != 0 {
			portStr += fmt.Sprintf("->%d", port.TargetPort)
		}
		// Always append protocol
		protocol := string(port.Protocol)
		if protocol == "" {
			protocol = "tcp"
		}
		portStr += "/" + protocol
		ports = append(ports, portStr)
	}

	if len(ports) == 0 {
		return "-"
	}

	return strings.Join(ports, ",")
}
