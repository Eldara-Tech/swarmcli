// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package systeminfoview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/telemetry"

	"github.com/stretchr/testify/require"
)

// --- mock ---

type mockClusterInfoOps struct {
	getCurrentContextFn     func() (string, error)
	getContainerCountFn     func() (int, error)
	getServiceCountFn       func() (int, error)
	getSwarmCPUCapacityFn   func() (float64, error)
	getSwarmMemCapacityFn   func() (int64, error)
	getSwarmResourceUsageFn func() (string, string, error)
	getDockerVersionFn      func() (string, error)
}

func (m *mockClusterInfoOps) GetCurrentContext() (string, error) {
	return m.getCurrentContextFn()
}
func (m *mockClusterInfoOps) GetContainerCount() (int, error) {
	return m.getContainerCountFn()
}
func (m *mockClusterInfoOps) GetServiceCount() (int, error) {
	return m.getServiceCountFn()
}
func (m *mockClusterInfoOps) GetSwarmCPUCapacity() (float64, error) {
	return m.getSwarmCPUCapacityFn()
}
func (m *mockClusterInfoOps) GetSwarmMemCapacity() (int64, error) {
	return m.getSwarmMemCapacityFn()
}
func (m *mockClusterInfoOps) GetSwarmResourceUsage() (string, string, error) {
	return m.getSwarmResourceUsageFn()
}
func (m *mockClusterInfoOps) GetDockerVersion() (string, error) {
	return m.getDockerVersionFn()
}

var _ docker.ClusterInfoOps = (*mockClusterInfoOps)(nil)

func noopClusterInfoOps() *mockClusterInfoOps {
	return &mockClusterInfoOps{
		getCurrentContextFn:     func() (string, error) { return "default", nil },
		getContainerCountFn:     func() (int, error) { return 5, nil },
		getServiceCountFn:       func() (int, error) { return 3, nil },
		getSwarmCPUCapacityFn:   func() (float64, error) { return 4.0, nil },
		getSwarmMemCapacityFn:   func() (int64, error) { return 8 * 1024 * 1024 * 1024, nil },
		getSwarmResourceUsageFn: func() (string, string, error) { return "12.5%", "45.3%", nil },
		getDockerVersionFn:      func() (string, error) { return "27.0.0", nil },
	}
}

func testDeps() docker.Deps {
	return docker.Deps{ClusterInfo: noopClusterInfoOps()}
}

// --- Tests ---

func TestNew(t *testing.T) {
	m := New(testDeps(), "1.0.0", "ce")
	require.NotNil(t, m)
	require.Equal(t, "1.0.0", m.version)
	require.Equal(t, "default", m.context)
	require.True(t, m.firstLoad)
}

func TestLoadStatus(t *testing.T) {
	ops := noopClusterInfoOps()
	ops.getCurrentContextFn = func() (string, error) { return "test-ctx", nil }
	ops.getContainerCountFn = func() (int, error) { return 10, nil }
	ops.getServiceCountFn = func() (int, error) { return 5, nil }
	m := New(docker.Deps{ClusterInfo: ops}, "1.0.0", "ce")
	cmd := m.LoadStatus()
	msg := cmd()
	statusMsg, ok := msg.(Msg)
	require.True(t, ok)
	require.Equal(t, "test-ctx", statusMsg.context)
	require.Equal(t, 10, statusMsg.containers)
	require.Equal(t, 5, statusMsg.services)
}

func TestLoadSlowStatus(t *testing.T) {
	ops := noopClusterInfoOps()
	ops.getSwarmResourceUsageFn = func() (string, string, error) { return "25.0%", "50.0%", nil }
	m := New(docker.Deps{ClusterInfo: ops}, "1.0.0", "ce")
	cmd := m.LoadSlowStatus()
	msg := cmd()
	slow, ok := msg.(SlowStatusMsg)
	require.True(t, ok)
	require.Equal(t, "25.0%", slow.cpu)
	require.Equal(t, "50.0%", slow.mem)
}

func TestUpdate_Msg(t *testing.T) {
	m := New(testDeps(), "1.0.0", "ce")
	cmd := m.Update(Msg{
		context:    "ctx",
		cpu:        "10%",
		mem:        "20%",
		containers: 3,
		services:   2,
	})
	require.Equal(t, "ctx", m.context)
	require.Equal(t, 3, m.containerCount)
	require.Equal(t, 2, m.serviceCount)
	// No follow-on command: the counts land, and the resource-usage fan-out is
	// left to the single loop SlowStatusMsg re-arms. Chaining it here gave that
	// loop a second, unsynchronised driver on the app's 5s tick.
	require.Nil(t, cmd)
}

func TestUpdate_SlowStatusMsg(t *testing.T) {
	m := New(testDeps(), "1.0.0", "ce")
	cmd := m.Update(SlowStatusMsg{cpu: "15.0%", mem: "30.0%"})
	require.Equal(t, "15.0%", m.cpuUsage)
	require.Equal(t, "30.0%", m.memUsage)
	require.False(t, m.loadingCPU)
	require.False(t, m.loadingMem)
	require.False(t, m.firstLoad)
	require.NotNil(t, cmd) // tick cmd
}

func TestUpdate_TickMsg(t *testing.T) {
	m := New(testDeps(), "1.0.0", "ce")
	cmd := m.Update(TickMsg{})
	require.NotNil(t, cmd) // LoadSlowStatus
}

func TestUpdate_SpinnerTickMsg(t *testing.T) {
	m := New(testDeps(), "1.0.0", "ce")
	m.loadingCPU = true
	oldSpinner := m.spinner
	cmd := m.Update(SpinnerTickMsg{})
	require.Equal(t, oldSpinner+1, m.spinner)
	require.NotNil(t, cmd) // next spinner tick
}

func TestContent_Format(t *testing.T) {
	result := content("default", "1.0.0", "10%", "20%", 5, 3)
	require.Contains(t, result, "default")
	require.Contains(t, result, "1.0.0")
	require.Contains(t, result, "10%")
	require.Contains(t, result, "20%")
	require.Contains(t, result, "5")
	require.Contains(t, result, "3")
}

func TestUpdateCPUMem_TrendArrows(t *testing.T) {
	m := New(testDeps(), "1.0.0", "ce")
	// First update sets baseline
	m.Update(SlowStatusMsg{cpu: "10.0%", mem: "20.0%"})
	require.Equal(t, float64(10), m.prevCPU)
	require.Equal(t, float64(20), m.prevMem)

	// Second update should detect increase
	m.Update(SlowStatusMsg{cpu: "15.0%", mem: "25.0%"})
	require.Contains(t, m.cpuUsage, "15.0%")
	require.Contains(t, m.memUsage, "25.0%")
	require.Equal(t, "up", m.prevCPUTrend)
	require.Equal(t, "up", m.prevMemTrend)
}

// checkInRequest is what the test server decodes. A superset of both bodies the
// client can send — the telemetry report and the plain version check — so one
// struct can assert which of the two went out.
type checkInRequest struct {
	Event     string `json:"event"`
	InstallID string `json:"install_id"`
	Version   string `json:"version"`
	Edition   string `json:"edition"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Mode      string `json:"mode"`
}

// newCheckInModel builds a model whose one outbound call goes to the test
// server, with HOME redirected so the install identity is written into the
// test's own directory rather than the developer's config.
func newCheckInModel(t *testing.T, version, edition, serverURL string) *Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	m := New(testDeps(), version, edition)
	m.telemetry = &telemetry.Client{URL: serverURL, FallbackVersionURL: serverURL}
	return m
}

func TestCheckLatestVersion_SendsVersionAndEdition(t *testing.T) {
	var received checkInRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		err := json.NewDecoder(r.Body).Decode(&received)
		require.NoError(t, err)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"latestVersion":"1.3.3","message":"An upgrade available"}`))
	}))
	defer server.Close()

	m := newCheckInModel(t, "1.2.2", "ce", server.URL)
	msg := m.CheckLatestVersion()()

	latestMsg, ok := msg.(LatestVersionMsg)
	require.True(t, ok)
	require.Equal(t, "1.3.3", latestMsg.LatestVersion)
	require.Equal(t, "1.2.2", received.Version)
	require.Equal(t, "ce", received.Edition)
}

func TestCheckLatestVersion_NoMessageWhenNotNewer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"latestVersion":"1.2.2","message":"same"}`))
	}))
	defer server.Close()

	m := newCheckInModel(t, "1.2.2", "ce", server.URL)
	_, ok := m.CheckLatestVersion()().(NoVersionUpdateMsg)
	require.True(t, ok)
}

func TestCheckLatestVersion_FailureReturnsNoUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	m := newCheckInModel(t, "1.2.2", "ce", server.URL)
	_, ok := m.CheckLatestVersion()().(NoVersionUpdateMsg)
	require.True(t, ok)
}

func TestCheckLatestVersion_DevBuildSkipsCheck(t *testing.T) {
	m := New(testDeps(), "dev", "ce")
	require.Nil(t, m.CheckLatestVersion())
}

func TestCheckLatestVersion_UsesOverriddenEdition(t *testing.T) {
	var received checkInRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewDecoder(r.Body).Decode(&received)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"latestVersion":"1.3.3","message":"upgrade"}`))
	}))
	defer server.Close()

	m := newCheckInModel(t, "1.2.2", "be", server.URL)
	msg := m.CheckLatestVersion()()
	_, ok := msg.(LatestVersionMsg)
	require.True(t, ok)
	require.Equal(t, "be", received.Edition)
}

func TestCheckLatestVersion_DisabledReturnsNilCmd(t *testing.T) {
	t.Setenv(versionCheckDisableEnv, "true")

	m := New(testDeps(), "1.2.2", "ce")
	require.Nil(t, m.CheckLatestVersion())
}

func TestIsNewerVersion(t *testing.T) {
	require.True(t, isNewerVersion("1.2.2", "1.2.3"))
	require.True(t, isNewerVersion("v1.2.2", "1.3.0"))
	require.True(t, isNewerVersion("1.3.0-rc.1", "1.3.0"))
	require.True(t, isNewerVersion("1.3.0-beta.1", "1.3.0-rc.1"))
	require.False(t, isNewerVersion("1.2.2", "1.2.2"))
	require.False(t, isNewerVersion("1.2.2", "1.2.1"))
	require.False(t, isNewerVersion("1.3.0", "1.3.0-rc.1"))
	require.False(t, isNewerVersion("dev", "1.2.3"))
}

func TestUpdate_LatestVersionMsg(t *testing.T) {
	m := New(testDeps(), "1.2.2", "ce")
	cmd := m.Update(LatestVersionMsg{LatestVersion: "1.3.3"})
	require.Nil(t, cmd)
	require.Equal(t, "1.3.3", m.latest)
	require.Contains(t, m.content, "1.2.2")
	require.Contains(t, m.content, "v1.3.3")
	require.Contains(t, m.content, "⚡")
}

// Regression: long version+latest combinations must render in the fixed
// systeminfo height without lipgloss wrapping the value onto a new line.
func TestView_VersionLineDoesNotWrap_LongVersions(t *testing.T) {
	cases := []struct {
		name    string
		version string
		latest  string
	}{
		{"snapshot build", "1.4.6-next", "1.5.0"},
		{"rc build", "v1.5.0-rc.1", "1.5.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(testDeps(), tc.version, "ce")
			m.Update(LatestVersionMsg{LatestVersion: tc.latest})

			rendered := m.View()
			lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
			require.Equal(t, Height, len(lines),
				"systeminfo must render exactly %d lines (got %d) for version %q latest %q:\n%s",
				Height, len(lines), tc.version, tc.latest, rendered)
		})
	}
}
