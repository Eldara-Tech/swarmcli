// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"fmt"
	"strings"

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
