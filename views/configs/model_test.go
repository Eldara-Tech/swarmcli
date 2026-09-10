// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package configsview

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/docker"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// --- mocks ---

type mockConfigOps struct {
	listConfigsFn            func(ctx context.Context) ([]swarm.Config, error)
	inspectConfigFn          func(ctx context.Context, nameOrID string) (*docker.ConfigWithDecodedData, error)
	createConfigFn           func(ctx context.Context, name string, data []byte, labels map[string]string) (swarm.Config, error)
	createConfigVersionFn    func(ctx context.Context, baseConfig swarm.Config, newData []byte) (swarm.Config, error)
	rotateConfigInServicesFn func(ctx context.Context, oldCfg *swarm.Config, newCfg swarm.Config) error
	deleteConfigFn           func(ctx context.Context, nameOrID string) error
	servicesUsingConfigsFn   func(ctx context.Context) (map[string][]swarm.Service, error)
	// servicesUsingConfigsCalls counts the indexed lookups, so a test can assert
	// the view asks once rather than once per config.
	servicesUsingConfigsCalls int
}

func (m *mockConfigOps) ListConfigs(ctx context.Context) ([]swarm.Config, error) {
	return m.listConfigsFn(ctx)
}
func (m *mockConfigOps) InspectConfig(ctx context.Context, nameOrID string) (*docker.ConfigWithDecodedData, error) {
	return m.inspectConfigFn(ctx, nameOrID)
}
func (m *mockConfigOps) CreateConfig(ctx context.Context, name string, data []byte, labels map[string]string) (swarm.Config, error) {
	return m.createConfigFn(ctx, name, data, labels)
}
func (m *mockConfigOps) CreateConfigVersion(ctx context.Context, baseConfig swarm.Config, newData []byte) (swarm.Config, error) {
	if m.createConfigVersionFn != nil {
		return m.createConfigVersionFn(ctx, baseConfig, newData)
	}
	panic("CreateConfigVersion not mocked")
}
func (m *mockConfigOps) RotateConfigInServices(ctx context.Context, oldCfg *swarm.Config, newCfg swarm.Config) error {
	if m.rotateConfigInServicesFn != nil {
		return m.rotateConfigInServicesFn(ctx, oldCfg, newCfg)
	}
	panic("RotateConfigInServices not mocked")
}
func (m *mockConfigOps) DeleteConfig(ctx context.Context, nameOrID string) error {
	return m.deleteConfigFn(ctx, nameOrID)
}
func (m *mockConfigOps) ServicesUsingConfigs(ctx context.Context) (map[string][]swarm.Service, error) {
	m.servicesUsingConfigsCalls++
	if m.servicesUsingConfigsFn != nil {
		return m.servicesUsingConfigsFn(ctx)
	}
	return nil, nil
}

// --- helpers ---

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case " ":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	}
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func noopConfigOps() *mockConfigOps {
	return &mockConfigOps{
		listConfigsFn: func(_ context.Context) ([]swarm.Config, error) {
			return nil, nil
		},
		inspectConfigFn: func(_ context.Context, _ string) (*docker.ConfigWithDecodedData, error) {
			return &docker.ConfigWithDecodedData{}, nil
		},
		createConfigFn: func(_ context.Context, _ string, _ []byte, _ map[string]string) (swarm.Config, error) {
			return swarm.Config{}, nil
		},
		deleteConfigFn: func(_ context.Context, _ string) error {
			return nil
		},
	}
}

func testModel(opts ...func(*Model)) *Model {
	m := New(80, 24)
	m.deps = docker.Deps{Configs: noopConfigOps()}
	for _, o := range opts {
		o(m)
	}
	return m
}

func fakeConfigs(names ...string) []docker.ConfigWithDecodedData {
	now := time.Now()
	cfgs := make([]docker.ConfigWithDecodedData, len(names))
	for i, name := range names {
		cfgs[i] = docker.ConfigWithDecodedData{
			Config: swarm.Config{
				ID: "id-" + name,
				Meta: swarm.Meta{
					Version:   swarm.Version{Index: uint64(i + 1)},
					CreatedAt: now,
					UpdatedAt: now,
				},
				Spec: swarm.ConfigSpec{
					Annotations: swarm.Annotations{Name: name},
				},
			},
		}
	}
	return cfgs
}

func loadConfigs(m *Model, cfgs []docker.ConfigWithDecodedData) {
	m.Update(configsLoadedMsg(cfgs))
}

// --- Tests ---

func TestNew(t *testing.T) {
	m := New(80, 24)
	require.Equal(t, 80, m.width)
	require.Equal(t, 24, m.height)
	require.Equal(t, stateLoading, m.state)
	require.True(t, m.visible)
}

func TestName(t *testing.T) {
	m := testModel()
	require.Equal(t, "configs", m.Name())
}

func TestCapturesInput_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.CapturesInput())
}

func TestHasActiveFilter_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.HasActiveFilter())
}

func TestIsSearching_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.IsSearching())
}

func TestIsInUsedByView_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.IsInUsedByView())
}

func TestHasErrors(t *testing.T) {
	m := testModel()
	require.False(t, m.HasErrors())
}

func TestOnEnter_SetsVisible(t *testing.T) {
	m := testModel()
	m.visible = false
	cmd := m.OnEnter()
	require.True(t, m.visible)
	require.NotNil(t, cmd)
}

func TestOnExit_ClearsVisible(t *testing.T) {
	m := testModel()
	m.OnExit()
	require.False(t, m.visible)
}

func TestShortHelpItems_MainView(t *testing.T) {
	m := testModel()
	items := m.ShortHelpItems()
	require.True(t, len(items) > 5)
	keys := make(map[string]bool)
	for _, item := range items {
		keys[item.Key] = true
	}
	require.True(t, keys["n"])
	require.True(t, keys["i"])
	require.True(t, keys["ctrl+d"])
}

func TestShortHelpItems_UsedByView(t *testing.T) {
	m := testModel()
	m.usedByViewActive = true
	items := m.ShortHelpItems()
	keys := make(map[string]bool)
	for _, item := range items {
		keys[item.Key] = true
	}
	require.True(t, keys["Enter"])
	require.True(t, keys["Esc"])
}

func TestValidateConfigName(t *testing.T) {
	require.NoError(t, validateConfigName("my-config"))
	require.Error(t, validateConfigName(""))
	require.Error(t, validateConfigName("has space"))
	require.Error(t, validateConfigName("has/slash"))
	// swarmcli-be#353: the daemon caps a config name at 64, and the dialog used
	// to let one through and report the daemon's refusal instead.
	require.Error(t, validateConfigName(strings.Repeat("a", 65)))
}

func TestSelectedConfig_Empty(t *testing.T) {
	m := testModel()
	require.Equal(t, "", m.selectedConfig())
}

func TestSelectedConfig_WithItems(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("alpha", "beta"))
	require.Equal(t, "alpha", m.selectedConfig())
}

func TestFindConfigByName_Found(t *testing.T) {
	m := testModel()
	m.configs = fakeConfigs("alpha", "beta")
	cfg, err := m.findConfigByName("beta")
	require.NoError(t, err)
	require.Equal(t, "beta", cfg.Config.Spec.Name)
}

func TestFindConfigByName_NotFound(t *testing.T) {
	m := testModel()
	m.configs = fakeConfigs("alpha")
	_, err := m.findConfigByName("missing")
	require.Error(t, err)
}
