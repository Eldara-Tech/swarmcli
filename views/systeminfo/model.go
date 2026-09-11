// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package systeminfoview

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/telemetry"

	"github.com/briandowns/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/docker/docker/api/types/swarm"
	"golang.org/x/mod/semver"
)

type Model struct {
	deps docker.Deps

	// We don't need a viewport here, as we will use a fixed size for the content.
	content string

	version   string
	edition   string
	latest    string
	telemetry *telemetry.Client

	context        string
	cpuUsage       string
	memUsage       string
	cpuCapacity    string // Total CPU cores
	memCapacity    string // Total memory
	containerCount int
	serviceCount   int

	// For tracking trends
	prevCPU        float64
	prevMem        float64
	lastUpdate     time.Time
	updateInterval time.Duration

	// Loading state
	loadingCPU bool
	loadingMem bool
	spinner    int
	firstLoad  bool

	// Trend arrow state
	prevCPUTrend  string // "up", "down", or ""
	prevMemTrend  string
	cpuBlinkCount int
	memBlinkCount int
}

const (
	defaultEdition         = "ce"
	versionCheckDisableEnv = "SWARMCLI_DISABLE_VERSION_CHECK"

	// heartbeatInterval re-reports a session that is still open.
	//
	// A day, not minutes. `swarmcli_started` already answers how many installs
	// launched and which releases they run; the only thing this adds is the
	// machine that opened the TUI once and left it open, which `started` alone
	// would count on the day it began and never again. A short interval would
	// multiply every install's request volume for no extra fact, which is the
	// thing this design is most careful to avoid.
	heartbeatInterval = 24 * time.Hour
)

// Create a new instance
func New(deps docker.Deps, version, edition string) *Model {
	// Get initial context synchronously to display immediately
	context, _ := deps.ClusterInfo.GetCurrentContext()
	normalizedEdition := normalizeEdition(edition)

	return &Model{
		deps:           deps,
		content:        content(context, version, "", "", 0, 0),
		version:        version,
		edition:        normalizedEdition,
		telemetry:      telemetry.New(),
		context:        context,
		updateInterval: 8 * time.Second,
		lastUpdate:     time.Now(),
		loadingCPU:     true,
		loadingMem:     true,
		firstLoad:      true,
	}
}

// Latest returns the newest release reported by the version check, or "" if
// none has been observed yet. The app reads it to populate the on-demand
// update notice (the proactive notice is driven by LatestVersionMsg directly).
func (m *Model) Latest() string { return m.latest }

// Init starts the header's timers.
//
// It seeds the resource-usage loop with the first collection rather than with a
// tick, because that loop re-arms its own tick from SlowStatusMsg (update.go):
// arming one here as well would leave two chains running, and the collection is
// a per-container fan-out at the other end of the Docker socket. One chain in,
// one chain out — a round cannot start while a round is running.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.LoadSlowStatus(), m.spinnerTickCmd()}
	if cmd := m.CheckLatestVersion(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// Its own chain, armed once here and re-armed on HeartbeatSentMsg. nil when
	// there is nothing to report, so no timer wakes for no reason.
	if cmd := m.HeartbeatCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}

func (m *Model) CheckLatestVersion() tea.Cmd {
	currentVersion := strings.TrimSpace(m.version)
	currentEdition := normalizeEdition(m.edition)
	if versionCheckDisabled() {
		l().Infow("startup version check disabled", "env", versionCheckDisableEnv)
		return nil
	}

	if currentVersion == "dev" {
		l().Infow("startup version check skipped for dev build")
		return nil
	}

	return func() tea.Msg {
		latestVersion, err := m.checkIn(telemetry.EventStarted, currentVersion, currentEdition)
		if err != nil {
			l().Infow("startup version check failed", "version", currentVersion, "edition", currentEdition, "error", err)
			return NoVersionUpdateMsg{}
		}

		if !shouldShowLatestVersion(currentVersion, latestVersion) {
			return NoVersionUpdateMsg{}
		}

		return LatestVersionMsg{
			LatestVersion: latestVersion,
		}
	}
}

func normalizeEdition(edition string) string {
	normalizedEdition := strings.TrimSpace(strings.ToLower(edition))
	if normalizedEdition == "" {
		return defaultEdition
	}

	return normalizedEdition
}

func versionCheckDisabled() bool {
	disabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(versionCheckDisableEnv)))
	return err == nil && disabled
}

func shouldShowLatestVersion(currentVersion, latestVersion string) bool {
	latestVersion = strings.TrimSpace(latestVersion)
	if latestVersion == "" {
		return false
	}

	if isNewerVersion(currentVersion, latestVersion) {
		return true
	}

	// For dev/non-semver builds, still surface latest stable release.
	currentOK := isSemver(currentVersion)
	latestOK := isSemver(latestVersion)
	if !currentOK && latestOK {
		return true
	}

	return false
}

// checkIn performs the one outbound call a launch makes, through the telemetry
// client, which decides whether it carries an install id or is the plain
// version check (telemetry/client.go).
//
// The HTTP lives there rather than here for layering: this is a view, and a
// view that owns an HTTP client and an identity file is a view that cannot be
// tested without both.
func (m *Model) checkIn(event, currentVersion, edition string) (string, error) {
	client := m.telemetry
	if client == nil {
		client = telemetry.New()
	}
	return client.CheckIn(event, currentVersion, edition, telemetry.ModeTUI)
}

// HeartbeatCmd re-reports a session that is still open, once a day.
//
// Its own chain, re-armed by the caller on each HeartbeatMsg, and deliberately
// not folded into the 8-second resource tick: that one is a Docker fan-out and
// this one is a network call, so sharing a timer would tie the cheapest thing
// in the view to the most expensive.
//
// Returns nil when there is nothing to report, so a build with the version
// check disabled — or a dev build — arms no timer at all rather than waking
// once a day to decide it has nothing to do.
func (m *Model) HeartbeatCmd() tea.Cmd {
	if versionCheckDisabled() || strings.TrimSpace(m.version) == "dev" || !telemetry.Enabled() {
		return nil
	}

	return tea.Tick(heartbeatInterval, func(time.Time) tea.Msg { return HeartbeatMsg{} })
}

// SendHeartbeat reports the still-open session. Its answer is discarded: the
// update notice is a startup thing, and raising one a day later under somebody
// who has been working in the TUI since yesterday would be an interruption
// rather than news.
func (m *Model) SendHeartbeat() tea.Cmd {
	currentVersion := strings.TrimSpace(m.version)
	currentEdition := normalizeEdition(m.edition)

	return func() tea.Msg {
		if _, err := m.checkIn(telemetry.EventHeartbeat, currentVersion, currentEdition); err != nil {
			l().Infow("heartbeat failed", "error", err)
		}
		return HeartbeatSentMsg{}
	}
}

func isNewerVersion(currentVersion, latestVersion string) bool {
	cmp, ok := compareVersionStrings(latestVersion, currentVersion)
	return ok && cmp > 0
}

func compareVersionStrings(a, b string) (int, bool) {
	normalizedA, ok := normalizeSemver(a)
	if !ok {
		return 0, false
	}
	normalizedB, ok := normalizeSemver(b)
	if !ok {
		return 0, false
	}

	return semver.Compare(normalizedA, normalizedB), true
}

func isSemver(version string) bool {
	_, ok := normalizeSemver(version)
	return ok
}

func normalizeSemver(version string) (string, bool) {
	normalized := strings.TrimSpace(version)
	if normalized == "" {
		return "", false
	}

	if strings.HasPrefix(normalized, "V") {
		normalized = "v" + normalized[1:]
	} else if !strings.HasPrefix(normalized, "v") {
		normalized = "v" + normalized
	}

	if !semver.IsValid(normalized) {
		return "", false
	}

	return normalized, true
}

func (m *Model) tickCmd() tea.Cmd {
	return tea.Tick(m.updateInterval, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

func (m *Model) spinnerTickCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return SpinnerTickMsg(t)
	})
}

func (m *Model) LoadStatus() tea.Cmd {
	clusterInfo := m.deps.ClusterInfo
	snapOps := m.deps.Snapshot
	return func() tea.Msg {
		context, _ := clusterInfo.GetCurrentContext()

		// Parallelize container and service counts.
		var containers, services int
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); containers, _ = clusterInfo.GetContainerCount() }()
		go func() { defer wg.Done(); services, _ = clusterInfo.GetServiceCount() }()
		wg.Wait()

		// Derive capacity from cached snapshot — avoids two extra NodeList API calls.
		var cpuCapacity float64
		var memCapacity int64
		if snapOps != nil {
			if snap := snapOps.GetSnapshot(); snap != nil {
				for _, node := range snap.Nodes {
					if node.Status.State == swarm.NodeStateReady {
						cpuCapacity += float64(node.Description.Resources.NanoCPUs) / 1e9
						memCapacity += node.Description.Resources.MemoryBytes
					}
				}
			}
		}

		cpuCapStr := "-- cores"
		if cpuCapacity > 0 {
			cpuCapStr = fmt.Sprintf("%.0f cores", cpuCapacity)
		}

		memCapStr := "--- GB"
		if memCapacity > 0 {
			memCapStr = fmt.Sprintf("%.0f GB", float64(memCapacity)/1024/1024/1024)
		}

		spinnerMarker := spinner.CharSets[14][0]
		return Msg{
			context:     context,
			cpu:         spinnerMarker,
			mem:         spinnerMarker,
			cpuCapacity: cpuCapStr,
			memCapacity: memCapStr,
			containers:  containers,
			services:    services,
		}
	}
}

func (m *Model) LoadSlowStatus() tea.Cmd {
	clusterInfo := m.deps.ClusterInfo
	return func() tea.Msg {
		l().Info("LoadSlowStatus: Starting background stats collection")

		// Single pass: one ContainerList + one ContainerStats per container
		// instead of two separate passes for CPU and memory.
		cpu, mem, err := clusterInfo.GetSwarmResourceUsage()
		if err != nil {
			l().Error("LoadSlowStatus: GetSwarmResourceUsage failed: %v", err)
		}
		if cpu == "" {
			cpu = "N/A"
		}
		if mem == "" {
			mem = "N/A"
		}
		l().Info("LoadSlowStatus: CPU=%s MEM=%s", cpu, mem)

		return SlowStatusMsg{
			cpu: cpu,
			mem: mem,
		}
	}
}
