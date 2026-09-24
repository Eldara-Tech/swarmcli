// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"strings"
	"testing"

	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

func TestGetServiceMode_Replicated(t *testing.T) {
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{}},
		},
	}
	require.Equal(t, "replicated", getServiceMode(svc))
}

func TestGetServiceMode_Global(t *testing.T) {
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			Mode: swarm.ServiceMode{Global: &swarm.GlobalService{}},
		},
	}
	require.Equal(t, "global", getServiceMode(svc))
}

func TestGetServiceMode_Neither(t *testing.T) {
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			Mode: swarm.ServiceMode{},
		},
	}
	require.Equal(t, "-", getServiceMode(svc))
}

// Named as `docker service ls` names them.
func TestGetServiceMode_Jobs(t *testing.T) {
	require.Equal(t, "replicated job", getServiceMode(swarm.Service{Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{ReplicatedJob: &swarm.ReplicatedJob{}}}}))
	require.Equal(t, "global job", getServiceMode(swarm.Service{Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{GlobalJob: &swarm.GlobalJob{}}}}))
}

func TestGetServiceImage_Basic(t *testing.T) {
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{Image: "nginx:latest"},
			},
		},
	}
	require.Equal(t, "nginx:latest", getServiceImage(svc))
}

func TestGetServiceImage_StripDigest(t *testing.T) {
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{Image: "nginx@sha256:abc123"},
			},
		},
	}
	require.Equal(t, "nginx", getServiceImage(svc))
}

func TestGetServiceImage_LongNameNotTruncated(t *testing.T) {
	long := strings.Repeat("a", 60)
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{Image: long},
			},
		},
	}
	require.Equal(t, long, getServiceImage(svc))
}

func TestGetServiceImage_NilContainerSpec(t *testing.T) {
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			TaskTemplate: swarm.TaskSpec{ContainerSpec: nil},
		},
	}
	require.Equal(t, "-", getServiceImage(svc))
}

func TestGetServicePorts_NoPorts(t *testing.T) {
	svc := swarm.Service{}
	require.Equal(t, "-", getServicePorts(svc))
}

func TestGetServicePorts_SinglePort(t *testing.T) {
	svc := swarm.Service{
		Endpoint: swarm.Endpoint{
			Ports: []swarm.PortConfig{
				{PublishedPort: 8080, TargetPort: 80, Protocol: "tcp"},
			},
		},
	}
	require.Equal(t, "*:8080->80/tcp", getServicePorts(svc))
}

func TestGetServicePorts_MultiplePorts(t *testing.T) {
	svc := swarm.Service{
		Endpoint: swarm.Endpoint{
			Ports: []swarm.PortConfig{
				{PublishedPort: 8080, TargetPort: 80, Protocol: "tcp"},
				{PublishedPort: 8443, TargetPort: 443, Protocol: "tcp"},
			},
		},
	}
	require.Equal(t, "*:8080->80/tcp,*:8443->443/tcp", getServicePorts(svc))
}

func TestGetServicePorts_SkipUnpublished(t *testing.T) {
	svc := swarm.Service{
		Endpoint: swarm.Endpoint{
			Ports: []swarm.PortConfig{
				{PublishedPort: 0, TargetPort: 80, Protocol: "tcp"},
				{PublishedPort: 8080, TargetPort: 80, Protocol: "tcp"},
			},
		},
	}
	require.Equal(t, "*:8080->80/tcp", getServicePorts(svc))
}

func TestGetServicePorts_DefaultProtocol(t *testing.T) {
	svc := swarm.Service{
		Endpoint: swarm.Endpoint{
			Ports: []swarm.PortConfig{
				{PublishedPort: 8080, TargetPort: 80, Protocol: ""},
			},
		},
	}
	require.Equal(t, "*:8080->80/tcp", getServicePorts(svc))
}
