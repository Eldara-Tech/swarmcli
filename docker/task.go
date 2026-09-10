// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/swarm"
)

// TaskEntry represents a task in a human-readable format
type TaskEntry struct {
	ID   string
	Name string
	// Slot is the replica this task belongs to, so two tasks of the same slot
	// read as one replica being replaced rather than as two replicas. Zero for a
	// global service, whose tasks carry no slot and are identified by node.
	Slot         int
	ServiceName  string
	Image        string
	NodeName     string
	ContainerID  string
	DesiredState string
	// State is the swarm task state on its own ("running", "shutdown",
	// "failed", …). CurrentState carries the same state for display, with a
	// relative timestamp appended; this field is what a caller deciding on the
	// task's meaning reads, so nobody has to parse the display string. It is
	// the only signal that separates a replica swarm stopped on purpose (an
	// update, a scale-down, a drained node — "shutdown") from one that died
	// ("failed"): the container's own docker-ps state is "exited" for both.
	State        string
	CurrentState string
	Error        string
	Ports        string
	// Health is the container-level health status (e.g. "healthy",
	// "unhealthy", "starting"); "" when the container has no healthcheck or
	// the status is unknown. The swarm task snapshot does not carry it, so the
	// default loaders leave it empty; it is an extension point populated by a
	// TaskOps decorator that can reach per-node container state.
	Health string
	// ContainerState is the container's live lifecycle state as reported by the
	// on-node agent (e.g. "running", "restarting", "exited", "dead"); "" by
	// default. Like Health it is an extension point populated by a TaskOps
	// decorator. Unlike State (the swarm task state) it reflects the
	// container's `docker ps` state, which the remote Swarm API cannot report;
	// the services view shows it as a fallback when Health is empty so container
	// errors surface even for images without a healthcheck — but only while the
	// task could still be running, since a task that is over has an exited
	// container by definition.
	ContainerState string
	// PullProgress summarizes the image pull the task's node is currently
	// performing for it (e.g. "pulling · 3/12 layers · 412 MB"); "" when nothing
	// is being pulled or the progress is unavailable. A task whose image is still
	// downloading sits in "preparing" with no further detail — the Swarm API
	// carries no pull progress at all — so the default loaders leave this empty;
	// it is an extension point populated by a TaskOps decorator that can reach
	// the node performing the pull.
	PullProgress string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// StatusText is the task's status cell: the live image-pull summary when a
// decorator supplied one (a pulling task otherwise shows only a bare
// "preparing"), else the swarm task state.
func (t TaskEntry) StatusText() string {
	if t.PullProgress != "" {
		return t.PullProgress
	}
	return t.CurrentState
}

// GetTasksForStack returns all tasks for services in the given stack
func GetTasksForStack(stackName string) ([]TaskEntry, error) {
	snap := GetSnapshot()
	if snap == nil {
		// No cached snapshot, try to refresh
		var err error
		snap, err = RefreshSnapshot()
		if err != nil {
			return nil, fmt.Errorf("failed to refresh snapshot: %w", err)
		}
	}

	var tasks []TaskEntry

	// Get all services for this stack
	stackServices := make(map[string]swarm.Service)
	for _, svc := range snap.Services {
		if svc.Spec.Labels["com.docker.stack.namespace"] == stackName {
			stackServices[svc.ID] = svc
		}
	}

	// Get nodes map for lookup
	nodesMap := make(map[string]string)
	for _, node := range snap.Nodes {
		nodesMap[node.ID] = node.Description.Hostname
	}

	// Filter tasks for this stack's services and sort by service name then created time
	for _, task := range snap.Tasks {
		if svc, ok := stackServices[task.ServiceID]; ok {
			nodeName := nodesMap[task.NodeID]
			if nodeName == "" {
				if len(task.NodeID) >= 12 {
					nodeName = task.NodeID[:12]
				} else {
					nodeName = task.NodeID
				}
			}

			// Extract image name (without registry/tag details for cleaner display)
			image := ""
			if task.Spec.ContainerSpec != nil {
				imageParts := strings.Split(task.Spec.ContainerSpec.Image, "@")
				if len(imageParts) > 0 {
					image = imageParts[0]
				}
				if strings.Contains(image, ":") {
					parts := strings.Split(image, ":")
					if len(parts) > 1 {
						image = parts[0] + ":" + strings.Split(parts[1], "@")[0]
					}
				}
			}

			// Format current state with timestamp
			currentState := string(task.Status.State)
			if !task.Status.Timestamp.IsZero() {
				duration := time.Since(task.Status.Timestamp)
				currentState = fmt.Sprintf("%s %s", currentState, formatTaskDuration(duration))
			}

			// Get error message if any
			errorMsg := ""
			if task.Status.Err != "" {
				errorMsg = task.Status.Err
				// Truncate long error messages
				if len(errorMsg) > 50 {
					errorMsg = errorMsg[:47] + "…"
				}
			}

			id := task.ID
			if len(id) > 12 {
				id = id[:12]
			}
			containerID := ""
			if task.Status.ContainerStatus != nil {
				containerID = task.Status.ContainerStatus.ContainerID
			}
			tasks = append(tasks, TaskEntry{
				ID:           id,
				Name:         fmt.Sprintf("%s.%d", svc.Spec.Name, task.Slot),
				Slot:         task.Slot,
				ServiceName:  svc.Spec.Name,
				Image:        image,
				NodeName:     nodeName,
				ContainerID:  containerID,
				DesiredState: string(task.DesiredState),
				State:        string(task.Status.State),
				CurrentState: currentState,
				Error:        errorMsg,
				Ports:        "", // Ports are typically on service level, not task level
				CreatedAt:    task.CreatedAt,
				UpdatedAt:    task.UpdatedAt,
			})
		}
	}

	// Sort tasks: by service name, then by created time (newest first for each service)
	sortTasksByServiceAndTime(tasks)

	return tasks, nil
}

func sortTasksByServiceAndTime(tasks []TaskEntry) {
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].ServiceName != tasks[j].ServiceName {
			return tasks[i].ServiceName < tasks[j].ServiceName
		}
		// Same service: sort by created time descending (newest first)
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
}

// GetTasksForService returns all tasks for a specific service ID from the cached snapshot.
func GetTasksForService(serviceID string) ([]TaskEntry, error) {
	snap := GetSnapshot()
	if snap == nil {
		return nil, fmt.Errorf("no snapshot available")
	}

	var tasks []TaskEntry

	// Build nodes map for hostname lookup
	nodesMap := make(map[string]string)
	for _, node := range snap.Nodes {
		nodesMap[node.ID] = node.Description.Hostname
	}

	// Filter tasks for this service and sort by created time
	for _, task := range snap.Tasks {
		if task.ServiceID == serviceID {
			nodeName := nodesMap[task.NodeID]
			if nodeName == "" {
				if len(task.NodeID) >= 12 {
					nodeName = task.NodeID[:12]
				} else {
					nodeName = task.NodeID
				}
			}

			// Extract image name (without registry/tag details for cleaner display)
			image := ""
			if task.Spec.ContainerSpec != nil {
				imageParts := strings.Split(task.Spec.ContainerSpec.Image, "@")
				if len(imageParts) > 0 {
					image = imageParts[0]
				}
				if strings.Contains(image, ":") {
					parts := strings.Split(image, ":")
					if len(parts) > 1 {
						image = parts[0] + ":" + strings.Split(parts[1], "@")[0]
					}
				}
			}

			// Format current state with timestamp
			currentState := string(task.Status.State)
			if !task.Status.Timestamp.IsZero() {
				duration := time.Since(task.Status.Timestamp)
				currentState = fmt.Sprintf("%s %s", currentState, formatTaskDuration(duration))
			}

			// Get error message if any
			errorMsg := ""
			if task.Status.Err != "" {
				errorMsg = task.Status.Err
				// Truncate long error messages
				if len(errorMsg) > 50 {
					errorMsg = errorMsg[:47] + "…"
				}
			}

			// Get service name from snapshot
			var serviceName string
			for _, svc := range snap.Services {
				if svc.ID == serviceID {
					// Guard against nil Spec (defensive)
					if svc.Spec.Name != "" {
						serviceName = svc.Spec.Name
					}
					break
				}
			}

			id := task.ID
			if len(id) > 12 {
				id = id[:12]
			}
			containerID := ""
			if task.Status.ContainerStatus != nil {
				containerID = task.Status.ContainerStatus.ContainerID
			}
			tasks = append(tasks, TaskEntry{
				ID:           id,
				Name:         fmt.Sprintf("%s.%d", serviceName, task.Slot),
				Slot:         task.Slot,
				ServiceName:  serviceName,
				Image:        image,
				NodeName:     nodeName,
				ContainerID:  containerID,
				DesiredState: string(task.DesiredState),
				State:        string(task.Status.State),
				CurrentState: currentState,
				Error:        errorMsg,
				Ports:        "", // Ports are typically on service level, not task level
				CreatedAt:    task.CreatedAt,
				UpdatedAt:    task.UpdatedAt,
			})
		}
	}

	// Sort tasks by created time (newest first)
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})

	return tasks, nil
}

func formatTaskDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d seconds ago", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	} else {
		days := int(d.Hours() / 24)
		if days < 7 {
			return fmt.Sprintf("%d days ago", days)
		} else if days < 30 {
			weeks := days / 7
			return fmt.Sprintf("%d weeks ago", weeks)
		} else {
			months := days / 30
			return fmt.Sprintf("%d months ago", months)
		}
	}
}
