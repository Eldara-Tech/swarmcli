// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

// StackService is a lightweight representation of a Swarm service within a stack.
type StackService struct {
	NodeID         string
	StackName      string
	ServiceName    string
	ServiceID      string
	ReplicasOnNode int
	ReplicasTotal  int
}

//
// ─── Internal helpers ───────────────────────────────────────────────────────────
//

// findServiceByName returns a swarm.Service by name, or an error if not found.
func findServiceByName(ctx context.Context, c *client.Client, name string) (*swarm.Service, error) {
	services, err := c.ServiceList(ctx, swarm.ServiceListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	for i := range services {
		if services[i].Spec.Name == name {
			return &services[i], nil
		}
	}
	return nil, fmt.Errorf("service %s not found", name)
}

// updateService applies the given Service.Spec and logs any warnings.
func updateService(ctx context.Context, c *client.Client, svc *swarm.Service) error {
	resp, err := c.ServiceUpdate(ctx, svc.ID, svc.Version, svc.Spec, swarm.ServiceUpdateOptions{
		RegistryAuthFrom: types.RegistryAuthFromSpec,
	})
	if err != nil {
		return fmt.Errorf("updating service %s: %w", svc.Spec.Name, err)
	}
	for _, w := range resp.Warnings {
		l().Warnf("⚠️  Warning for service %s: %s\n", svc.Spec.Name, w)
	}
	return nil
}

// scaleServiceCommon performs the actual scaling given a service struct.
func scaleServiceCommon(ctx context.Context, c *client.Client, svc *swarm.Service, replicas uint64) error {
	if svc.Spec.Mode.Replicated == nil {
		return fmt.Errorf("service %s is not in replicated mode", svc.Spec.Name)
	}
	current := *svc.Spec.Mode.Replicated.Replicas
	if current == replicas {
		return nil // nothing to change
	}
	svc.Spec.Mode.Replicated.Replicas = &replicas
	return updateService(ctx, c, svc)
}

// restartServiceCommon increments ForceUpdate to trigger a rolling restart.
func restartServiceCommon(ctx context.Context, c *client.Client, svc *swarm.Service) error {
	svc.Spec.TaskTemplate.ForceUpdate++
	if err := updateService(ctx, c, svc); err != nil {
		return fmt.Errorf("forcing service update for %s: %w", svc.Spec.Name, err)
	}

	// Log with mode-specific info
	if svc.Spec.Mode.Replicated != nil {
		l().Infof("🔁 Service %s restarted (replicas: %d)\n",
			svc.Spec.Name, *svc.Spec.Mode.Replicated.Replicas)
	} else {
		l().Infof("🔁 Service %s restarted (global mode)\n", svc.Spec.Name)
	}
	return nil
}

//
// ─── Public API ─────────────────────────────────────────────────────────────────
//

// ScaleService updates the replica count of a service by ID.
func ScaleService(ctx context.Context, serviceID string, replicas uint64) error {
	c, err := GetClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	svc, _, err := c.ServiceInspectWithRaw(ctx, serviceID, swarm.ServiceInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect service %s: %w", serviceID, err)
	}
	return scaleServiceCommon(ctx, c, &svc, replicas)
}

// ScaleServiceByName looks up a service by name and scales it.
func ScaleServiceByName(ctx context.Context, serviceName string, replicas uint64) error {
	c, err := GetClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	svc, err := findServiceByName(ctx, c, serviceName)
	if err != nil {
		return err
	}
	return scaleServiceCommon(ctx, c, svc, replicas)
}

// RestartService performs a rolling restart (like `docker service update --force`).
func RestartService(ctx context.Context, serviceName string) error {
	c, err := GetClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	svc, err := findServiceByName(ctx, c, serviceName)
	if err != nil {
		return err
	}
	return restartServiceCommon(ctx, c, svc)
}

// RemoveService removes a service by name.
func RemoveService(ctx context.Context, serviceName string) error {
	c, err := GetClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	svc, err := findServiceByName(ctx, c, serviceName)
	if err != nil {
		return err
	}

	if err := c.ServiceRemove(ctx, svc.ID); err != nil {
		return fmt.Errorf("removing service %s: %w", serviceName, err)
	}

	l().Infof("🗑️  Service %s removed\n", serviceName)
	return nil
}

// RollbackService rolls back a service to its previous configuration.
func RollbackService(ctx context.Context, serviceName string) error {
	c, err := GetClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	svc, err := findServiceByName(ctx, c, serviceName)
	if err != nil {
		return err
	}

	// Check if there's a previous spec to rollback to
	if svc.PreviousSpec == nil {
		return fmt.Errorf("service %s has no previous configuration to rollback to", serviceName)
	}

	// Perform rollback by setting the previous spec as the current spec
	svc.Spec = *svc.PreviousSpec
	resp, err := c.ServiceUpdate(ctx, svc.ID, svc.Version, svc.Spec, swarm.ServiceUpdateOptions{
		RegistryAuthFrom: types.RegistryAuthFromSpec,
		Rollback:         "previous",
	})
	if err != nil {
		return fmt.Errorf("rolling back service %s: %w", serviceName, err)
	}

	for _, w := range resp.Warnings {
		l().Warnf("⚠️  Warning for service %s: %s\n", serviceName, w)
	}

	l().Infof("⏪ Service %s rolled back\n", serviceName)
	return nil
}

type ProgressUpdate struct {
	Replaced int
	Running  int
	Total    int
}

func RestartServiceAndWait(ctx context.Context, serviceName string) error {
	return restartServiceAndWaitInternal(ctx, serviceName, nil)
}

func RestartServiceWithProgress(ctx context.Context, serviceName string, progressCh chan<- ProgressUpdate) error {
	return restartServiceAndWaitInternal(ctx, serviceName, progressCh)
}

func restartServiceAndWaitInternal(ctx context.Context, serviceName string, progressCh chan<- ProgressUpdate) error {
	cli, err := GetClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	svc, err := findServiceByName(ctx, cli, serviceName)
	if err != nil {
		return err
	}
	if svc.Spec.Mode.Replicated == nil {
		return fmt.Errorf("service %s is not in replicated mode", serviceName)
	}

	total := int(*svc.Spec.Mode.Replicated.Replicas)
	l().Infof("🔁 Restarting service %s (replicas: %d)...", serviceName, total)

	// Snapshot old tasks
	oldTasks := map[string]swarm.Task{}
	tasks, err := cli.TaskList(ctx, swarm.TaskListOptions{})
	if err != nil {
		return fmt.Errorf("listing initial tasks: %w", err)
	}
	for _, t := range tasks {
		if t.ServiceID == svc.ID && t.DesiredState == swarm.TaskStateRunning {
			oldTasks[t.ID] = t
		}
	}
	l().Debugf("📦 Snapshot: %d old running tasks for %s", len(oldTasks), serviceName)

	// Trigger rolling restart
	if err := restartServiceCommon(ctx, cli, svc); err != nil {
		return fmt.Errorf("restart trigger: %w", err)
	}

	type slotState struct {
		oldTaskID string
		newTaskID string
	}
	slots := make(map[int]slotState)
	for _, t := range oldTasks {
		slots[t.Slot] = slotState{oldTaskID: t.ID}
	}

	var (
		lastProgress ProgressUpdate
		stableSince  time.Time
		lastActivity time.Time
	)
	lastActivity = time.Now()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s restart: %w", serviceName, ctx.Err())
		default:
		}

		tasks, err := cli.TaskList(ctx, swarm.TaskListOptions{})
		if err != nil {
			return fmt.Errorf("listing tasks: %w", err)
		}

		running := 0
		replaced := 0
		updating := 0

		for _, t := range tasks {
			if t.ServiceID != svc.ID || t.DesiredState != swarm.TaskStateRunning {
				continue
			}

			state := t.Status.State
			slot := t.Slot
			s := slots[slot]

			switch state {
			case swarm.TaskStateRunning:
				running++
				if s.oldTaskID != "" && t.ID != s.oldTaskID {
					s.newTaskID = t.ID
					replaced++
				}
				slots[slot] = s
			case swarm.TaskStatePreparing,
				swarm.TaskStateStarting,
				swarm.TaskStatePending,
				swarm.TaskStateAssigned:
				updating++
			}
		}

		currentProgress := ProgressUpdate{Replaced: replaced, Running: running, Total: total}
		if progressCh != nil && currentProgress != lastProgress {
			trySendProgress(progressCh, currentProgress)
			lastProgress = currentProgress
			lastActivity = time.Now()
			l().Debugf("[Docker] Progress update: %d/%d replaced, %d running", replaced, total, running)
		}

		// Determine stability
		allReplaced := true
		for _, s := range slots {
			if s.newTaskID == "" {
				allReplaced = false
				break
			}
		}

		if allReplaced && running >= total && updating == 0 {
			if stableSince.IsZero() {
				stableSince = time.Now()
			} else if time.Since(stableSince) > 3*time.Second {
				l().Infof("✅ Service %s stable: %d/%d new tasks running", serviceName, replaced, total)
				return nil
			}
		} else {
			stableSince = time.Time{}
		}

		// Adaptive polling — faster while changing, slower when idle
		sleep := 500 * time.Millisecond
		if time.Since(lastActivity) > 5*time.Second {
			sleep = 2 * time.Second
		}
		time.Sleep(sleep)
	}
}

func trySendProgress(ch chan<- ProgressUpdate, v ProgressUpdate) {
	select {
	case ch <- v:
	default:
		// drop silently; UI may be busy
	}
}

// Service Loader Helpers

type ServiceEntry struct {
	StackName      string
	ServiceName    string
	ServiceID      string
	ReplicasOnNode int
	ReplicasTotal  int
	// UpToDate counts the replicas running the service's *current* generation.
	// ReplicasOnNode counts every running task, superseded ones included, so it
	// matches `docker service ls` — which means a start-first rollout reads as
	// fully converged while the outgoing generation is what is actually up. The
	// two together say what one ratio cannot: how many replicas are serving, and
	// how many of those are the version being rolled out (issue #480).
	UpToDate int
	// RollingOut reports an update in flight — updating or paused. It gates the
	// display of UpToDate: outside a rollout a replica short of the current
	// generation is a restart, not a stale version. A rollback is excluded
	// because UpToDate cannot be measured there (see countUpToDateTasks).
	RollingOut bool
	Status     string
	Mode       string
	Image      string
	Ports      string
	// Health is an aggregate health summary for the service's running replicas
	// (e.g. "2/2 healthy"); "" when unknown. The swarm API does not expose
	// container health, so the default loaders leave it empty; it is an
	// extension point populated by a ServiceOps decorator that can reach
	// per-node container state.
	Health string
	// PullProgress summarizes an image pull in flight for this service's tasks
	// (e.g. "pulling · 3/12 layers · 412 MB"); "" when nothing is being pulled or
	// the progress is unavailable. Like Health it is an extension point populated
	// by a ServiceOps decorator that can reach the nodes performing the pull; the
	// services view shows it in place of Status while it is set, since a service
	// whose image is still downloading otherwise reads as a bare "active".
	PullProgress string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func LoadNodeServices(nodeID string) []ServiceEntry {
	snap, err := GetOrRefreshSnapshot()
	if err != nil {
		l().Warnf("failed to get snapshot: %v", err)
		return nil
	}

	var entries []ServiceEntry

	for _, svc := range snap.Services {
		stack, desired := getServiceStackAndDesired(svc, snap)

		// Count tasks running on this node
		onNode := countTasksForNode(svc.ID, nodeID, snap)

		// Skip if the node neither runs a task for this service nor is meant to.
		// Presence cannot be read off onNode alone: that counts running tasks, so
		// a service whose task here is still preparing — or wedged pending on an
		// unsatisfiable constraint — would vanish from the view of the very node
		// an operator opened to find out why.
		if onNode == 0 && !hasIntendedTaskOnNode(svc.ID, nodeID, snap) {
			continue
		}

		entries = append(entries, ServiceEntry{
			StackName:      stack,
			ServiceName:    svc.Spec.Name,
			ServiceID:      svc.ID,
			ReplicasOnNode: onNode,
			ReplicasTotal:  desired,
			UpToDate:       countUpToDateTasks(svc, snap),
			RollingOut:     isRollingOut(svc),
			Status:         getServiceStatus(svc),
			Mode:           getServiceMode(svc),
			Image:          getServiceImage(svc),
			Ports:          getServicePorts(svc),
			CreatedAt:      svc.CreatedAt,
			UpdatedAt:      svc.UpdatedAt,
		})
	}

	sortEntries(entries)
	return entries
}

// LoadStackServices returns a stack's services from the process-wide snapshot.
func LoadStackServices(stackName string) []ServiceEntry {
	snap, err := GetOrRefreshSnapshot()
	if err != nil {
		l().Warnf("failed to get snapshot: %v", err)
		return nil
	}
	return snap.StackServices(stackName)
}

// StackServices returns a stack's services from an already-fetched snapshot, so
// a caller holding a snapshot of a specific swarm can read it without going
// through the process-wide cache.
func (snap *SwarmSnapshot) StackServices(stackName string) []ServiceEntry {
	var entries []ServiceEntry

	for _, svc := range snap.Services {
		stack, desired := getServiceStackAndDesired(svc, snap)
		if stack != stackName {
			continue
		}

		// Count tasks on all nodes
		onNode := countTasksForNode(svc.ID, "", snap)

		entries = append(entries, ServiceEntry{
			StackName:      stack,
			ServiceName:    svc.Spec.Name,
			ServiceID:      svc.ID,
			ReplicasOnNode: onNode,
			ReplicasTotal:  desired,
			UpToDate:       countUpToDateTasks(svc, snap),
			RollingOut:     isRollingOut(svc),
			Status:         getServiceStatus(svc),
			Mode:           getServiceMode(svc),
			Image:          getServiceImage(svc),
			Ports:          getServicePorts(svc),
			CreatedAt:      svc.CreatedAt,
			UpdatedAt:      svc.UpdatedAt,
		})
	}

	sortEntries(entries)
	return entries
}

// LoadAllServices returns every service in the swarm, across all stacks
// (including services with no stack, shown as "-"). Equivalent to
// `docker service ls`.
func LoadAllServices() []ServiceEntry {
	snap, err := GetOrRefreshSnapshot()
	if err != nil {
		l().Warnf("failed to get snapshot: %v", err)
		return nil
	}

	var entries []ServiceEntry

	for _, svc := range snap.Services {
		stack, desired := getServiceStackAndDesired(svc, snap)

		// Count tasks on all nodes
		onNode := countTasksForNode(svc.ID, "", snap)

		entries = append(entries, ServiceEntry{
			StackName:      stack,
			ServiceName:    svc.Spec.Name,
			ServiceID:      svc.ID,
			ReplicasOnNode: onNode,
			ReplicasTotal:  desired,
			UpToDate:       countUpToDateTasks(svc, snap),
			RollingOut:     isRollingOut(svc),
			Status:         getServiceStatus(svc),
			Mode:           getServiceMode(svc),
			Image:          getServiceImage(svc),
			Ports:          getServicePorts(svc),
			CreatedAt:      svc.CreatedAt,
			UpdatedAt:      svc.UpdatedAt,
		})
	}

	sortEntries(entries)
	return entries
}

// --- Helpers ---

// getServiceStackAndDesired returns the stack name and desired replicas for a service
func getServiceStackAndDesired(svc swarm.Service, snap *SwarmSnapshot) (stack string, desired int) {
	stack = svc.Spec.Labels["com.docker.stack.namespace"]
	if stack == "" {
		stack = "-"
	}

	// Shared with the convergence path rather than duplicated: a global service's
	// target is one task per node that can actually run one, so a drained or
	// down node lowers the target instead of making the service read
	// permanently short (issue #480).
	desired = snap.DesiredReplicas(svc)
	return
}

// countTasksForNode counts the tasks of a service that are actually running; if
// nodeID == "", counts across all nodes.
//
// It counts by Status.State, not DesiredState. Tasks are created with
// DesiredState=running and only then walk new → pending → assigned → preparing
// → starting → running, so counting intent reported a task that had never
// started as if it were up. A service wedged on an unsatisfiable placement
// constraint displayed as fully converged forever, which is exactly when the
// operator most needs the truth (issue #480).
//
// Superseded tasks from a rolling update are still counted while they run,
// matching `docker service ls`. Up-to-dateness is the separate question
// countUpToDateTasks answers, and LoadStackConvergence is where --wait asks it.
func countTasksForNode(serviceID, nodeID string, snap *SwarmSnapshot) int {
	count := 0
	for _, t := range snap.Tasks {
		if t.ServiceID != serviceID {
			continue
		}
		if t.Status.State != swarm.TaskStateRunning {
			continue
		}
		if nodeID == "" || t.NodeID == nodeID {
			count++
		}
	}
	return count
}

// hasIntendedTaskOnNode reports whether swarm means to run a task of this
// service on the node, whatever state that task has reached. It is the
// visibility test the node-scoped services view needs: a task still preparing,
// or pending on a constraint it cannot satisfy, is exactly what the operator
// opened that node to see, and counting only running tasks hid it.
func hasIntendedTaskOnNode(serviceID, nodeID string, snap *SwarmSnapshot) bool {
	for _, t := range snap.Tasks {
		if t.ServiceID == serviceID && t.NodeID == nodeID && t.DesiredState == swarm.TaskStateRunning {
			return true
		}
	}
	return false
}

// countUpToDateTasks counts the running tasks belonging to the service's current
// generation — one per slot at most, so a start-first rollout cannot count the
// outgoing task it is replacing.
//
// The current generation is the newest task per slot, by CreatedAt: swarm
// creates a slot's replacement after the task it supersedes, so creation order
// *is* generation order. DesiredState cannot draw the line, because under
// start-first the outgoing task keeps DesiredState=running until its replacement
// is up — which is precisely the window where the count matters.
//
// During an update, a slot the rollout has not reached yet still has its old
// task as its newest, so a slot counts only if that task was created after the
// update started (issue #667). Creation order is generation order here too:
// swarm replaces a slot's task, restarts included, from the spec it now holds.
// A rollback cannot be judged this way — swarmkit keeps the update's StartedAt,
// so both generations in play were created after it — which is why isRollingOut
// leaves the rollback states out.
//
// This is deliberately not taskutil.LatestTasksByServiceKey: that helper prefers
// a task that wants to be running over a newer terminal one, because it picks a
// task worth surfacing an error from. Here that preference would pick the
// outgoing task and report the old generation as current.
func countUpToDateTasks(svc swarm.Service, snap *SwarmSnapshot) int {
	var since time.Time
	if isRollingOut(svc) && svc.UpdateStatus.StartedAt != nil {
		since = *svc.UpdateStatus.StartedAt
	}
	count := 0
	for _, t := range newestTaskPerSlot(svc.ID, snap.Tasks) {
		if t.Status.State == swarm.TaskStateRunning && t.CreatedAt.After(since) {
			count++
		}
	}
	return count
}

// newestTaskPerSlot indexes one service's tasks by replica, keeping the most
// recent task in each — the current generation, by the rule countUpToDateTasks
// documents above. Shared with StackConvergence, which asks the same question of
// the same task list and must not answer it differently.
func newestTaskPerSlot(serviceID string, tasks []swarm.Task) map[string]swarm.Task {
	newest := make(map[string]swarm.Task)
	for _, t := range tasks {
		if t.ServiceID != serviceID {
			continue
		}
		// An unassigned task has neither slot nor node, so it has no identity to
		// be the newest of; it is also, by definition, not running yet.
		key := taskSlotKey(t)
		if key == "" {
			continue
		}
		if cur, seen := newest[key]; !seen || t.CreatedAt.After(cur.CreatedAt) {
			newest[key] = t
		}
	}
	return newest
}

// taskSlotKey identifies the replica a task belongs to: the slot for a
// replicated service, the node for a global one (whose tasks carry no slot).
// Empty when the task has neither, i.e. it has not been assigned yet.
func taskSlotKey(t swarm.Task) string {
	if t.Slot > 0 {
		return strconv.Itoa(t.Slot)
	}
	return t.NodeID
}

// isRollingOut reports an update in flight. The paused state counts: a rollout
// halted by a failing task is the case where an operator most needs to see how
// far it got. The completed states do not — the generation on show is the
// current one — and neither do the rollback states, whose progress
// countUpToDateTasks cannot measure (issue #667).
func isRollingOut(svc swarm.Service) bool {
	if svc.UpdateStatus == nil {
		return false
	}
	switch svc.UpdateStatus.State {
	case swarm.UpdateStateUpdating, swarm.UpdateStatePaused:
		return true
	default:
		return false
	}
}

// sortEntries sorts entries by stack name then service name
func sortEntries(entries []ServiceEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].StackName == entries[j].StackName {
			return entries[i].ServiceName < entries[j].ServiceName
		}
		return entries[i].StackName < entries[j].StackName
	})
}

// getServiceStatus returns a human-readable status string for a service
func getServiceStatus(svc swarm.Service) string {
	if svc.UpdateStatus != nil {
		switch svc.UpdateStatus.State {
		case swarm.UpdateStateUpdating:
			return "updating"
		case swarm.UpdateStatePaused:
			return "paused"
		case swarm.UpdateStateCompleted:
			return "updated"
		case swarm.UpdateStateRollbackStarted:
			return "rolling back"
		case swarm.UpdateStateRollbackPaused:
			return "rollback paused"
		case swarm.UpdateStateRollbackCompleted:
			return "rolled back"
		default:
			return string(svc.UpdateStatus.State)
		}
	}
	return "active"
}

// GetServiceLogs fetches and returns the logs from a service
func GetServiceLogs(ctx context.Context, serviceID string) (string, error) {
	client, err := GetClient()
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %w", err)
	}

	logOptions := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     false,
		Timestamps: false,
	}

	reader, err := client.ServiceLogs(ctx, serviceID, logOptions)
	if err != nil {
		// NOTE: This matches a Docker daemon error string. Update if Docker changes the message.
		if strings.Contains(strings.ToLower(err.Error()), "tty service logs only supported with --raw") {
			return getServiceLogsRawCLI(ctx, serviceID)
		}
		return "", fmt.Errorf("failed to get service logs: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()

	logs, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read service logs: %w", err)
	}

	result := stripDockerLogHeaders(logs)
	// Log for debugging
	l().Infof("GetServiceLogs: raw=%d bytes, cleaned=%d bytes", len(logs), len(result))
	return result, nil
}

// BuildRawLogArgs constructs the docker CLI arguments for "service logs --raw".
// Extra flags (e.g. "--follow", "--details", "--tail", "100") are inserted before the serviceID.
func BuildRawLogArgs(ctxName, serviceID string, extra ...string) []string {
	args := make([]string, 0, 8+len(extra))
	if strings.TrimSpace(ctxName) != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "service", "logs", "--raw")
	args = append(args, extra...)
	args = append(args, serviceID)
	return args
}

func getServiceLogsRawCLI(ctx context.Context, serviceID string) (string, error) {
	ctxName, err := GetContextFromEnv()
	if err != nil {
		return "", fmt.Errorf("failed to determine docker context: %w", err)
	}

	args := BuildRawLogArgs(ctxName, serviceID)

	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("failed to get service logs via docker CLI: %w: %s", err, msg)
		}
		return "", fmt.Errorf("failed to get service logs via docker CLI: %w", err)
	}

	return stdout.String(), nil
}

// stripDockerLogHeaders decodes Docker's multiplexed log stream.
//
// Docker uses an 8-byte header per frame:
//
//	[stream(1)][0][0][0][size(4, big-endian)] + payload
//
// IMPORTANT: This is NOT line-based. The header bytes can legitimately contain
// '\n', so splitting on newlines can drop content for certain payload sizes.
func stripDockerLogHeaders(logs []byte) string {
	if len(logs) == 0 {
		return ""
	}

	// If it doesn't look like a multiplexed stream, return as-is.
	if len(logs) < 8 {
		return string(logs)
	}
	stream := logs[0]
	isMultiplexed := (logs[1] == 0 && logs[2] == 0 && logs[3] == 0) && (stream == 0 || stream == 1 || stream == 2)
	if !isMultiplexed {
		return string(logs)
	}

	var out bytes.Buffer
	pos := 0
	for len(logs)-pos >= 8 {
		h := logs[pos : pos+8]
		sz := int(binary.BigEndian.Uint32(h[4:8]))
		pos += 8
		if sz < 0 || len(logs)-pos < sz {
			// Not a valid frame; fallback to raw to avoid returning empty.
			return string(logs)
		}
		out.Write(logs[pos : pos+sz])
		pos += sz
	}

	return out.String()
}

// GetServiceTaskDiagnostics returns a human-readable summary of tasks for a service.
// This is useful when a service produces no logs (e.g., image pull errors).
func GetServiceTaskDiagnostics(ctx context.Context, serviceID string) (string, error) {
	cli, err := GetClient()
	if err != nil {
		return "", fmt.Errorf("docker client: %w", err)
	}

	tasks, err := cli.TaskList(ctx, swarm.TaskListOptions{})
	if err != nil {
		return "", fmt.Errorf("listing tasks: %w", err)
	}

	lines := make([]string, 0, 4)
	for _, t := range tasks {
		if t.ServiceID != serviceID {
			continue
		}
		errStr := strings.TrimSpace(t.Status.Err)
		msgStr := strings.TrimSpace(t.Status.Message)
		id := t.ID
		if len(id) > 12 {
			id = id[:12]
		}
		line := fmt.Sprintf(
			"task=%s slot=%d desired=%s state=%s err=%q msg=%q",
			id,
			t.Slot,
			t.DesiredState,
			t.Status.State,
			errStr,
			msgStr,
		)
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return "(no tasks found for service)", nil
	}
	return strings.Join(lines, "\n"), nil
}

// CreateService creates a service with the given spec and returns the service ID
func CreateService(ctx context.Context, spec swarm.ServiceSpec) (string, error) {
	client, err := GetClient()
	if err != nil {
		return "", fmt.Errorf("failed to get Docker client: %w", err)
	}

	resp, err := client.ServiceCreate(ctx, spec, swarm.ServiceCreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create service: %w", err)
	}

	return resp.ID, nil
}
