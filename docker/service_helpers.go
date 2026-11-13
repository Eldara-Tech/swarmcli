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
