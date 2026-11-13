// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/mount"
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

func TestGetServiceAnonVolumes(t *testing.T) {
	withMounts := func(mounts ...mount.Mount) swarm.Service {
		return swarm.Service{Spec: swarm.ServiceSpec{TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{Mounts: mounts},
		}}}
	}
	cases := []struct {
		name string
		svc  swarm.Service
		want []AnonVolume
	}{
		{"no container spec", swarm.Service{}, nil},
		{"no mounts", withMounts(), nil},
		{"named volume", withMounts(mount.Mount{Type: mount.TypeVolume, Source: "st_data", Target: "/data"}), nil},
		{"bind", withMounts(mount.Mount{Type: mount.TypeBind, Source: "/srv", Target: "/data"}), nil},
		{"tmpfs", withMounts(mount.Mount{Type: mount.TypeTmpfs, Target: "/data"}), nil},
		{"volume without a source, path cleaned and sorted", withMounts(
			mount.Mount{Type: mount.TypeVolume, Target: "/var/lib/db/"},
			mount.Mount{Type: mount.TypeVolume, Source: "st_cache", Target: "/cache"},
			mount.Mount{Type: mount.TypeVolume, Target: "/data"},
		), []AnonVolume{{Path: "/data"}, {Path: "/var/lib/db"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, getServiceAnonVolumes(tc.svc))
		})
	}
}

// Every loader carries the spec's anonymous volumes, so the services view shows
// them whichever scope it was opened in.
func TestServiceLoaders_CarryDeclaredAnonVolumes(t *testing.T) {
	t.Cleanup(InvalidateSnapshot)
	SetSnapshot(&SwarmSnapshot{
		Fetched: time.Now(), // fresh, so the loaders read it rather than the daemon
		Nodes:   []swarm.Node{{ID: "n1"}},
		Services: []swarm.Service{{
			ID: "svc1",
			Spec: swarm.ServiceSpec{
				Annotations: swarm.Annotations{
					Name:   "st_db",
					Labels: map[string]string{"com.docker.stack.namespace": "st"},
				},
				TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{
					Mounts: []mount.Mount{{Type: mount.TypeVolume, Target: "/data"}},
				}},
			},
		}},
		Tasks: []swarm.Task{{
			ID: "t1", ServiceID: "svc1", NodeID: "n1", Slot: 1,
			DesiredState: swarm.TaskStateRunning,
			Status:       swarm.TaskStatus{State: swarm.TaskStateRunning},
		}},
	})

	want := []AnonVolume{{Path: "/data"}}
	for name, entries := range map[string][]ServiceEntry{
		"all":   LoadAllServices(),
		"stack": LoadStackServices("st"),
		"node":  LoadNodeServices("n1"),
	} {
		require.Len(t, entries, 1, name)
		require.Equal(t, want, entries[0].AnonVolumes, name)
	}
}
