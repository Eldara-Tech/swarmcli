// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package secretsview

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

type mockSecretOps struct {
	listSecretsFn            func(ctx context.Context) ([]swarm.Secret, error)
	inspectSecretFn          func(ctx context.Context, nameOrID string) (*docker.SecretWithDecodedData, error)
	createSecretFn           func(ctx context.Context, name string, data []byte, labels map[string]string) (swarm.Secret, error)
	createSecretVersionFn    func(ctx context.Context, baseSecret swarm.Secret, newData []byte) (swarm.Secret, error)
	rotateSecretInServicesFn func(ctx context.Context, oldSec *swarm.Secret, newSec swarm.Secret) error
	deleteSecretFn           func(ctx context.Context, nameOrID string) error
	servicesUsingSecretsFn   func(ctx context.Context) (map[string][]swarm.Service, error)
	// servicesUsingSecretsCalls counts the indexed lookups, so a test can assert
	// the view asks once rather than once per secret.
	servicesUsingSecretsCalls int
}

func (m *mockSecretOps) ListSecrets(ctx context.Context) ([]swarm.Secret, error) {
	return m.listSecretsFn(ctx)
}
func (m *mockSecretOps) InspectSecret(ctx context.Context, nameOrID string) (*docker.SecretWithDecodedData, error) {
	return m.inspectSecretFn(ctx, nameOrID)
}
func (m *mockSecretOps) CreateSecret(ctx context.Context, name string, data []byte, labels map[string]string) (swarm.Secret, error) {
	return m.createSecretFn(ctx, name, data, labels)
}
func (m *mockSecretOps) CreateSecretVersion(ctx context.Context, baseSecret swarm.Secret, newData []byte) (swarm.Secret, error) {
	if m.createSecretVersionFn != nil {
		return m.createSecretVersionFn(ctx, baseSecret, newData)
	}
	panic("CreateSecretVersion not mocked")
}
func (m *mockSecretOps) RotateSecretInServices(ctx context.Context, oldSec *swarm.Secret, newSec swarm.Secret) error {
	if m.rotateSecretInServicesFn != nil {
		return m.rotateSecretInServicesFn(ctx, oldSec, newSec)
	}
	panic("RotateSecretInServices not mocked")
}
func (m *mockSecretOps) DeleteSecret(ctx context.Context, nameOrID string) error {
	return m.deleteSecretFn(ctx, nameOrID)
}
func (m *mockSecretOps) ServicesUsingSecrets(ctx context.Context) (map[string][]swarm.Service, error) {
	m.servicesUsingSecretsCalls++
	if m.servicesUsingSecretsFn != nil {
		return m.servicesUsingSecretsFn(ctx)
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

func noopSecretOps() *mockSecretOps {
	return &mockSecretOps{
		listSecretsFn: func(_ context.Context) ([]swarm.Secret, error) {
			return nil, nil
		},
		inspectSecretFn: func(_ context.Context, _ string) (*docker.SecretWithDecodedData, error) {
			return &docker.SecretWithDecodedData{}, nil
		},
		createSecretFn: func(_ context.Context, _ string, _ []byte, _ map[string]string) (swarm.Secret, error) {
			return swarm.Secret{}, nil
		},
		deleteSecretFn: func(_ context.Context, _ string) error {
			return nil
		},
	}
}

func testModel(opts ...func(*Model)) *Model {
	m := New(80, 24)
	m.deps = docker.Deps{Secrets: noopSecretOps()}
	for _, o := range opts {
		o(m)
	}
	return m
}

func fakeSecrets(names ...string) []docker.SecretWithDecodedData {
	now := time.Now()
	secs := make([]docker.SecretWithDecodedData, len(names))
	for i, name := range names {
		secs[i] = docker.SecretWithDecodedData{
			Secret: swarm.Secret{
				ID: "id-" + name,
				Meta: swarm.Meta{
					Version:   swarm.Version{Index: uint64(i + 1)},
					CreatedAt: now,
					UpdatedAt: now,
				},
				Spec: swarm.SecretSpec{
					Annotations: swarm.Annotations{Name: name},
				},
			},
		}
	}
	return secs
}

func loadSecrets(m *Model, secs []docker.SecretWithDecodedData) {
	m.Update(secretsLoadedMsg(secs))
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
	require.Equal(t, "secrets", m.Name())
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
	// Should contain key bindings for navigate, new, inspect, delete, help
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

func TestValidateSecretName(t *testing.T) {
	require.NoError(t, validateSecretName("my-secret"))
	require.Error(t, validateSecretName(""))
	require.Error(t, validateSecretName("has space"))
	require.Error(t, validateSecretName("has/slash"))
	// swarmcli-be#353: the daemon caps a secret name at 64, and the dialog used
	// to let one through and report the daemon's refusal instead.
	require.Error(t, validateSecretName(strings.Repeat("a", 65)))
}

func TestSelectedSecret_Empty(t *testing.T) {
	m := testModel()
	require.Equal(t, "", m.selectedSecret())
}

func TestSelectedSecret_WithItems(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("alpha", "beta"))
	require.Equal(t, "alpha", m.selectedSecret())
}

func TestFindSecretByName_Found(t *testing.T) {
	m := testModel()
	m.secrets = fakeSecrets("alpha", "beta")
	sec, err := m.findSecretByName("beta")
	require.NoError(t, err)
	require.Equal(t, "beta", sec.Secret.Spec.Name)
}

func TestFindSecretByName_NotFound(t *testing.T) {
	m := testModel()
	m.secrets = fakeSecrets("alpha")
	_, err := m.findSecretByName("missing")
	require.Error(t, err)
}
