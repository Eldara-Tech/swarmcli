// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package contexts

import (
	"context"
	"fmt"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// --- mocks ---

type mockContextOps struct {
	listContextsFn               func() ([]docker.ContextInfo, error)
	useContextFn                 func(contextName string) error
	validateContextFn            func(ctx context.Context, contextName string) error
	inspectContextFn             func(contextName string) (string, error)
	exportContextFn              func(contextName string) (string, error)
	exportContextWithForceFn     func(contextName string) (string, error)
	checkContextExportExistsFn   func(contextName string) bool
	deleteContextFn              func(contextName string) error
	importContextFn              func(filePath string) (string, error)
	createContextFn              func(name, dockerHost string) error
	createContextWithTLSFn       func(name, dockerHost, tlsPath string, skipTLSVerify bool) error
	createContextWithCertFilesFn func(name, description, dockerHost, caFile, certFile, keyFile string, skipTLSVerify bool) error
	updateContextEndpointFn      func(name, description, dockerHost string) error
}

func (m *mockContextOps) ListContexts() ([]docker.ContextInfo, error) {
	return m.listContextsFn()
}
func (m *mockContextOps) UseContext(contextName string) error {
	return m.useContextFn(contextName)
}
func (m *mockContextOps) ValidateContext(ctx context.Context, contextName string) error {
	return m.validateContextFn(ctx, contextName)
}
func (m *mockContextOps) InspectContext(contextName string) (string, error) {
	return m.inspectContextFn(contextName)
}
func (m *mockContextOps) ExportContext(contextName string) (string, error) {
	return m.exportContextFn(contextName)
}
func (m *mockContextOps) ExportContextWithForce(contextName string) (string, error) {
	return m.exportContextWithForceFn(contextName)
}
func (m *mockContextOps) CheckContextExportExists(contextName string) bool {
	return m.checkContextExportExistsFn(contextName)
}
func (m *mockContextOps) DeleteContext(contextName string) error {
	return m.deleteContextFn(contextName)
}
func (m *mockContextOps) ImportContext(filePath string) (string, error) {
	return m.importContextFn(filePath)
}
func (m *mockContextOps) CreateContext(name, dockerHost string) error {
	return m.createContextFn(name, dockerHost)
}
func (m *mockContextOps) CreateContextWithTLS(name, dockerHost, tlsPath string, skipTLSVerify bool) error {
	return m.createContextWithTLSFn(name, dockerHost, tlsPath, skipTLSVerify)
}
func (m *mockContextOps) CreateContextWithCertFiles(name, description, dockerHost, caFile, certFile, keyFile string, skipTLSVerify bool) error {
	return m.createContextWithCertFilesFn(name, description, dockerHost, caFile, certFile, keyFile, skipTLSVerify)
}
func (m *mockContextOps) UpdateContextEndpoint(name, description, dockerHost string) error {
	return m.updateContextEndpointFn(name, description, dockerHost)
}

// Verify interface compliance
var _ docker.ContextOps = (*mockContextOps)(nil)

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
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
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

func noopContextOps() *mockContextOps {
	return &mockContextOps{
		listContextsFn:               func() ([]docker.ContextInfo, error) { return nil, nil },
		useContextFn:                 func(_ string) error { return nil },
		validateContextFn:            func(_ context.Context, _ string) error { return nil },
		inspectContextFn:             func(_ string) (string, error) { return "{}", nil },
		exportContextFn:              func(_ string) (string, error) { return "/tmp/test.tar", nil },
		exportContextWithForceFn:     func(_ string) (string, error) { return "/tmp/test.tar", nil },
		checkContextExportExistsFn:   func(_ string) bool { return false },
		deleteContextFn:              func(_ string) error { return nil },
		importContextFn:              func(_ string) (string, error) { return "imported", nil },
		createContextFn:              func(_, _ string) error { return nil },
		createContextWithTLSFn:       func(_, _, _ string, _ bool) error { return nil },
		createContextWithCertFilesFn: func(_, _, _, _, _, _ string, _ bool) error { return nil },
		updateContextEndpointFn:      func(_, _, _ string) error { return nil },
	}
}

// stubContext is a convenience alias to avoid unused-import warnings for
// context.Context in test helpers that satisfy interface constraints.
var _ = context.Background

func testModel(opts ...func(*Model)) *Model {
	m := New()
	m.deps = docker.Deps{Contexts: noopContextOps()}
	// SetContexts calls List.View() which needs RenderItem.
	m.List.RenderItem = func(ctx docker.ContextInfo, selected bool, _ int) string {
		marker := " "
		if selected {
			marker = ">"
		}
		return fmt.Sprintf("%s %s", marker, ctx.Name)
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func fakeContexts(names ...string) []docker.ContextInfo {
	ctxs := make([]docker.ContextInfo, len(names))
	for i, name := range names {
		ctxs[i] = docker.ContextInfo{
			Name:        name,
			Current:     i == 0,
			Description: "desc-" + name,
			DockerHost:  "tcp://" + name + ":2375",
		}
	}
	return ctxs
}

// batchMsgs runs a command and returns every message it produced, flattening a
// tea.Batch so a test can assert on one member of it.
func batchMsgs(cmd tea.Cmd) []tea.Msg {
	msg := runCmd(cmd)
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	msgs := make([]tea.Msg, 0, len(batch))
	for _, c := range batch {
		msgs = append(msgs, runCmd(c))
	}
	return msgs
}

func loadContexts(m *Model, ctxs []docker.ContextInfo) {
	m.Visible = true
	m.ready = true
	m.Update(ContextsLoadedMsg{Contexts: ctxs})
}

// --- Tests ---

func TestNew(t *testing.T) {
	m := New()
	require.False(t, m.Visible)
	require.Equal(t, SortByName, m.sortField)
	require.True(t, m.sortAscending)
	require.Equal(t, 0, m.cursor)
}

func TestName(t *testing.T) {
	m := testModel()
	require.Equal(t, "contexts", m.Name())
}

func TestCapturesInput_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.CapturesInput())
}

func TestCapturesInput_ConfirmVisible(t *testing.T) {
	m := testModel()
	m.confirmDialog.Visible = true
	require.True(t, m.CapturesInput())
}

func TestCapturesInput_ImportInput(t *testing.T) {
	m := testModel()
	m.importInputActive = true
	require.True(t, m.CapturesInput())
}

func TestCapturesInput_FileBrowser(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	require.True(t, m.CapturesInput())
}

func TestCapturesInput_ErrorDialog(t *testing.T) {
	m := testModel()
	m.errorDialogActive = true
	require.True(t, m.CapturesInput())
}

func TestCapturesInput_CreateDialog(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	require.True(t, m.CapturesInput())
}

func TestCapturesInput_CertFileBrowser(t *testing.T) {
	m := testModel()
	m.certFileBrowserActive = true
	require.True(t, m.CapturesInput())
}

func TestCapturesInput_EditDialog(t *testing.T) {
	m := testModel()
	m.editDialogActive = true
	require.True(t, m.CapturesInput())
}

func TestHasActiveFilter_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.HasActiveFilter())
}

func TestIsSearching_Default(t *testing.T) {
	m := testModel()
	require.False(t, m.IsSearching())
}

func TestHasErrors(t *testing.T) {
	m := testModel()
	require.False(t, m.HasErrors())
}

func TestOnEnter_NoContexts(t *testing.T) {
	m := testModel()
	cmd := m.OnEnter()
	require.True(t, m.Visible)
	require.True(t, m.IsLoading())
	require.NotNil(t, cmd)
}

func TestOnEnter_WithContexts(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("ctx1"))
	m.Visible = false
	cmd := m.OnEnter()
	require.True(t, m.Visible)
	require.False(t, m.IsLoading(), "no reload needed")
	require.NotNil(t, cmd, "but the poll chain is armed here on every entry")
}

func TestOnExit(t *testing.T) {
	m := testModel()
	m.Visible = true
	m.OnExit()
	require.False(t, m.Visible)
}

func TestSetSize(t *testing.T) {
	m := testModel()
	m.SetSize(120, 40)
	require.Equal(t, 120, m.viewport.Width)
	require.Equal(t, 40, m.viewport.Height)
	require.True(t, m.ready)
}

func TestGetSetContexts(t *testing.T) {
	m := testModel()
	ctxs := fakeContexts("a", "b")
	m.SetContexts(ctxs)
	got := m.GetContexts()
	require.Len(t, got, 2)
	require.Equal(t, "a", got[0].Name)
}

func TestMoveCursor(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("a", "b", "c"))
	require.Equal(t, 0, m.GetCursor())
	m.MoveCursor(1)
	require.Equal(t, 1, m.GetCursor())
	m.MoveCursor(-5)
	require.Equal(t, 0, m.GetCursor())
}

func TestGetSelectedContext(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("ctx1", "ctx2"))
	ctx, ok := m.GetSelectedContext()
	require.True(t, ok)
	require.Equal(t, "ctx1", ctx.Name)
}

// The cursor indexes the filtered rows, so a filter must not change which
// context the highlighted row resolves to.
func TestGetSelectedContext_Filtered(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("alpha", "bravo", "charlie"))
	m.ApplySearchQuery("charlie")
	ctx, ok := m.GetSelectedContext()
	require.True(t, ok)
	require.Equal(t, "charlie", ctx.Name)
}

func TestGetSelectedContext_Empty(t *testing.T) {
	m := testModel()
	_, ok := m.GetSelectedContext()
	require.False(t, ok)
}

func TestSetGetLoading(t *testing.T) {
	m := testModel()
	require.False(t, m.IsLoading())
	m.SetLoading(true)
	require.True(t, m.IsLoading())
}

func TestSetGetError(t *testing.T) {
	m := testModel()
	m.SetError("boom")
	require.Equal(t, "boom", m.GetError())
}

func TestSetGetSuccess(t *testing.T) {
	m := testModel()
	m.SetSuccess("done")
	require.Equal(t, "done", m.GetSuccess())
}

func TestSwitchPending(t *testing.T) {
	m := testModel()
	require.False(t, m.IsSwitchPending())
	m.SetSwitchPending(true)
	require.True(t, m.IsSwitchPending())
}

func TestShortHelpItems(t *testing.T) {
	m := testModel()
	items := m.ShortHelpItems()
	keys := make(map[string]bool)
	for _, item := range items {
		keys[item.Key] = true
	}
	require.True(t, keys["Enter"])
	require.True(t, keys["i"])
	require.True(t, keys["x"])
	require.True(t, keys["m"])
	require.True(t, keys["c"])
	require.True(t, keys["ctrl+d"])
	require.True(t, keys["e"])
}

func TestGetContextsHelpContent(t *testing.T) {
	cats := GetContextsHelpContent()
	require.True(t, len(cats) >= 3)
	require.Equal(t, "General", cats[0].Title)
	require.Equal(t, "View", cats[1].Title)
	require.Equal(t, "Navigation", cats[2].Title)
}

func TestUpdateCreateFocus(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createInputFocus = 2
	m.updateCreateFocus()
	// Host input should be focused (index 2)
	require.True(t, m.createHostInput.Focused())
	require.False(t, m.createNameInput.Focused())
}
