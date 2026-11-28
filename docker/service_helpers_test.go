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

func TestGetServiceUnpinnedVolumes(t *testing.T) {
	stackLabel := map[string]string{"com.docker.stack.namespace": "st"}
	// vol is a named volume mount as `docker stack deploy` writes one for a
	// volume the stack declares: labelled with the stack, and carrying a
	// DriverConfig only when the stack file names a driver or driver_opts.
	vol := func(source string, dc *mount.Driver) mount.Mount {
		return mount.Mount{Type: mount.TypeVolume, Source: source, Target: "/" + source,
			VolumeOptions: &mount.VolumeOptions{Labels: stackLabel, DriverConfig: dc}}
	}
	svc := func(constraints []string, mounts ...mount.Mount) swarm.Service {
		s := swarm.Service{Spec: swarm.ServiceSpec{
			Annotations:  swarm.Annotations{Name: "st_db", Labels: stackLabel},
			Mode:         swarm.ServiceMode{Replicated: &swarm.ReplicatedService{}},
			TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Mounts: mounts}},
		}}
		if constraints != nil {
			s.Spec.TaskTemplate.Placement = &swarm.Placement{Constraints: constraints}
		}
		return s
	}
	withMode := func(s swarm.Service, mode swarm.ServiceMode) swarm.Service {
		s.Spec.Mode = mode
		return s
	}
	node := func(name string, role swarm.NodeRole, labels map[string]string) swarm.Node {
		return swarm.Node{ID: name,
			Description: swarm.NodeDescription{Hostname: name},
			Spec:        swarm.NodeSpec{Role: role, Labels: labels},
			Status:      swarm.NodeStatus{State: swarm.NodeStateReady}}
	}
	m1 := node("m1", swarm.NodeRoleManager, map[string]string{"db": "true", "pg": "true"})
	m2 := node("m2", swarm.NodeRoleManager, map[string]string{"db": "true"})
	w1 := node("w1", swarm.NodeRoleWorker, nil)
	three := []swarm.Node{m1, m2, w1}
	drained := w1
	drained.Spec.Availability = swarm.NodeAvailabilityDrain
	drained.Status.State = swarm.NodeStateDown

	data := []UnpinnedVolume{{Name: "data"}}
	cases := []struct {
		name  string
		svc   swarm.Service
		nodes []swarm.Node
		want  []UnpinnedVolume
	}{
		{"no container spec", swarm.Service{}, three, nil},
		{"default local volume, several nodes", svc(nil, vol("st_data", nil)), three, data},
		{"single-node swarm", svc(nil, vol("st_data", nil)), three[:1], nil},
		{"pinned by hostname", svc([]string{"node.hostname == w1"}, vol("st_data", nil)), three, nil},
		{"pinned by a label one node carries", svc([]string{"node.labels.pg == true"}, vol("st_data", nil)), three, nil},
		{"a label two nodes carry", svc([]string{"node.labels.db == true"}, vol("st_data", nil)), three, data},
		{"node.role == manager with two managers", svc([]string{"node.role == manager"}, vol("st_data", nil)), three, data},
		{"node.role == manager with one manager", svc([]string{"node.role == manager"}, vol("st_data", nil)), []swarm.Node{m1, w1}, nil},
		{"constraints no node satisfies", svc([]string{"node.labels.none == true"}, vol("st_data", nil)), three, nil},
		{"a drained, down node still counts", svc(nil, vol("st_data", nil)), []swarm.Node{m1, drained}, data},
		{"global", withMode(svc(nil, vol("st_data", nil)), swarm.ServiceMode{Global: &swarm.GlobalService{}}), three, nil},
		{"global job", withMode(svc(nil, vol("st_data", nil)), swarm.ServiceMode{GlobalJob: &swarm.GlobalJob{}}), three, nil},
		{"replicated job", withMode(svc(nil, vol("st_data", nil)), swarm.ServiceMode{ReplicatedJob: &swarm.ReplicatedJob{}}), three, data},
		{"explicit local driver", svc(nil, vol("st_data", &mount.Driver{Name: "local"})), three, data},
		{"local driver binding a host path", svc(nil, vol("st_data", &mount.Driver{
			Options: map[string]string{"type": "none", "o": "bind", "device": "/srv/pg"}})), three, data},
		{"plugin driver", svc(nil, vol("st_data", &mount.Driver{Name: "rexray/ebs"})), three, nil},
		{"bind, tmpfs and anonymous mounts", svc(nil,
			mount.Mount{Type: mount.TypeBind, Source: "/srv", Target: "/srv"},
			mount.Mount{Type: mount.TypeTmpfs, Target: "/tmp"},
			mount.Mount{Type: mount.TypeVolume, Target: "/scratch"},
		), three, nil},
		{"external volume", svc(nil, mount.Mount{Type: mount.TypeVolume, Source: "pgdata", Target: "/pg",
			VolumeOptions: &mount.VolumeOptions{}}), three, []UnpinnedVolume{{Name: "pgdata", DriverUnknown: true}}},
		{"service outside any stack", swarm.Service{Spec: swarm.ServiceSpec{TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: "_cache", Target: "/c"}}},
		}}}, three, []UnpinnedVolume{{Name: "_cache", DriverUnknown: true}}},
		{"a named driver outside any stack keeps its name", swarm.Service{Spec: swarm.ServiceSpec{TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: "_cache", Target: "/c",
				VolumeOptions: &mount.VolumeOptions{DriverConfig: &mount.Driver{Name: "local"}}}}},
		}}}, three, []UnpinnedVolume{{Name: "_cache"}}},
		{"deduplicated and sorted", svc(nil,
			vol("st_data", nil),
			mount.Mount{Type: mount.TypeVolume, Source: "st_data", Target: "/again",
				VolumeOptions: &mount.VolumeOptions{Labels: stackLabel}},
			mount.Mount{Type: mount.TypeVolume, Source: "backups", Target: "/b", VolumeOptions: &mount.VolumeOptions{}},
			vol("st_cache", nil),
		), three, []UnpinnedVolume{{Name: "backups", DriverUnknown: true}, {Name: "cache"}, {Name: "data"}}},
	}
	for _, typ := range []string{"nfs", "nfs4", "cifs", "smb3", "ceph", "tmpfs"} {
		cases = append(cases, struct {
			name  string
			svc   swarm.Service
			nodes []swarm.Node
			want  []UnpinnedVolume
		}{"local driver of type " + typ, svc(nil, vol("st_data", &mount.Driver{
			Name: "local", Options: map[string]string{"type": typ}})), three, nil})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, getServiceUnpinnedVolumes(tc.svc, tc.nodes))
		})
	}
}

// Every loader carries the unpinned volumes, measured against the swarm's nodes,
// so the services view shows them whichever scope it was opened in.
func TestServiceLoaders_CarryUnpinnedVolumes(t *testing.T) {
	t.Cleanup(InvalidateSnapshot)
	stackLabel := map[string]string{"com.docker.stack.namespace": "st"}
	SetSnapshot(&SwarmSnapshot{
		Fetched: time.Now(), // fresh, so the loaders read it rather than the daemon
		Nodes:   []swarm.Node{{ID: "n1"}, {ID: "n2"}},
		Services: []swarm.Service{{
			ID: "svc1",
			Spec: swarm.ServiceSpec{
				Annotations: swarm.Annotations{Name: "st_db", Labels: stackLabel},
				Mode:        swarm.ServiceMode{Replicated: &swarm.ReplicatedService{}},
				TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{
					Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: "st_data", Target: "/data",
						VolumeOptions: &mount.VolumeOptions{Labels: stackLabel}}},
				}},
			},
		}},
		Tasks: []swarm.Task{{
			ID: "t1", ServiceID: "svc1", NodeID: "n1", Slot: 1,
			DesiredState: swarm.TaskStateRunning,
			Status:       swarm.TaskStatus{State: swarm.TaskStateRunning},
		}},
	})

	want := []UnpinnedVolume{{Name: "data"}}
	for name, entries := range map[string][]ServiceEntry{
		"all":   LoadAllServices(),
		"stack": LoadStackServices("st"),
		"node":  LoadNodeServices("n1"),
	} {
		require.Len(t, entries, 1, name)
		require.Equal(t, want, entries[0].UnpinnedVolumes, name)
	}
}
