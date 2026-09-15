// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package contexts

import (
	"context"
	"fmt"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// --- State machine tests ---

func TestUpdate_ContextsLoaded_Success(t *testing.T) {
	m := testModel()
	m.Visible = true
	m.SetLoading(true)
	m.Update(ContextsLoadedMsg{
		Contexts: fakeContexts("a", "b"),
	})
	require.False(t, m.IsLoading())
	require.Equal(t, "", m.GetError())
	require.Len(t, m.GetContexts(), 2)
}

func TestUpdate_ContextsLoaded_Error(t *testing.T) {
	m := testModel()
	m.Visible = true
	m.SetLoading(true)
	m.Update(ContextsLoadedMsg{Error: fmt.Errorf("fail")})
	require.False(t, m.IsLoading())
	require.Contains(t, m.GetError(), "fail")
}

func TestUpdate_ContextSwitched_Success(t *testing.T) {
	m := testModel()
	m.Visible = true
	m.SetSwitchPending(true)
	cmd := m.Update(ContextSwitchedMsg{ContextName: "ctx1", Success: true})
	require.False(t, m.IsSwitchPending())
	require.Equal(t, "", m.GetError())
	require.True(t, m.IsLoading()) // triggers reload
	require.NotNil(t, cmd)
}

func TestUpdate_ContextSwitched_Error(t *testing.T) {
	m := testModel()
	m.Visible = true
	m.SetSwitchPending(true)
	m.Update(ContextSwitchedMsg{Success: false, Error: fmt.Errorf("unreachable")})
	require.False(t, m.IsSwitchPending())
	require.Contains(t, m.GetError(), "unreachable")
	require.True(t, m.errorDialogActive)
}

func TestUpdate_ContextExported_Success(t *testing.T) {
	m := testModel()
	m.Update(ContextExportedMsg{
		ContextName: "ctx1",
		FilePath:    "/tmp/ctx1.tar",
		Success:     true,
	})
	require.Equal(t, "", m.GetError())
	require.Contains(t, m.GetSuccess(), "Exported")
}

func TestUpdate_ContextExported_FileExists(t *testing.T) {
	m := testModel()
	m.Update(ContextExportedMsg{
		ContextName: "ctx1",
		FilePath:    "/tmp/ctx1.tar",
		Success:     false,
		Error:       fmt.Errorf("file_exists"),
	})
	require.True(t, m.confirmDialog.Visible)
	require.Equal(t, "export", m.pendingAction)
	require.Equal(t, "ctx1", m.pendingExportContext)
}

func TestUpdate_ContextExported_Error(t *testing.T) {
	m := testModel()
	m.Update(ContextExportedMsg{
		Success: false,
		Error:   fmt.Errorf("disk full"),
	})
	require.Contains(t, m.GetError(), "disk full")
}

func TestUpdate_ContextImported_Success(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	cmd := m.Update(ContextImportedMsg{ContextName: "imported-ctx", Success: true})
	require.False(t, m.fileBrowserActive)
	require.Contains(t, m.GetSuccess(), "imported-ctx")
	require.True(t, m.IsLoading())
	require.NotNil(t, cmd)
}

func TestUpdate_ContextImported_Error(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.Update(ContextImportedMsg{Success: false, Error: fmt.Errorf("bad tar")})
	require.False(t, m.fileBrowserActive)
	require.Contains(t, m.GetError(), "bad tar")
}

func TestUpdate_ContextDeleted_Success(t *testing.T) {
	m := testModel()
	cmd := m.Update(ContextDeletedMsg{ContextName: "old-ctx", Success: true})
	require.Contains(t, m.GetSuccess(), "old-ctx")
	require.True(t, m.IsLoading())
	require.NotNil(t, cmd)
}

func TestUpdate_ContextDeleted_Error(t *testing.T) {
	m := testModel()
	m.Update(ContextDeletedMsg{Success: false, Error: fmt.Errorf("in use")})
	require.Contains(t, m.GetError(), "in use")
}

func TestUpdate_ContextCreated_Success(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createNameInput.SetValue("new-ctx")
	cmd := m.Update(ContextCreatedMsg{ContextName: "new-ctx", Success: true})
	require.False(t, m.createDialogActive)
	require.Contains(t, m.GetSuccess(), "new-ctx")
	require.True(t, m.IsLoading())
	require.NotNil(t, cmd)
}

func TestUpdate_ContextCreated_Error(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.Update(ContextCreatedMsg{Success: false, Error: fmt.Errorf("exists")})
	require.Contains(t, m.GetError(), "exists")
	// Dialog stays open so user can fix
}

func TestUpdate_ContextUpdated_Success(t *testing.T) {
	m := testModel()
	m.editDialogActive = true
	m.editContextName = "ctx1"
	cmd := m.Update(ContextUpdatedMsg{ContextName: "ctx1", Success: true})
	require.False(t, m.editDialogActive)
	require.Equal(t, "", m.editContextName)
	require.Contains(t, m.GetSuccess(), "ctx1")
	require.True(t, m.IsLoading())
	require.NotNil(t, cmd)
	// Nothing moved under the active client, so no reconnect is requested.
	require.NotContains(t, batchMsgs(cmd), ContextChangedNotification{})
}

// A moved endpoint on the active context has to reach the app layer, which is
// what drops the cached client and snapshot built for the old daemon.
func TestUpdate_ContextUpdated_Reconnects(t *testing.T) {
	m := testModel()
	m.editDialogActive = true
	m.editContextName = "ctx1"
	cmd := m.Update(ContextUpdatedMsg{ContextName: "ctx1", Reconnect: true, Success: true})
	require.Contains(t, batchMsgs(cmd), ContextChangedNotification{})
}

func TestUpdate_ContextUpdated_Error(t *testing.T) {
	m := testModel()
	m.editDialogActive = true
	m.Update(ContextUpdatedMsg{Success: false, Error: fmt.Errorf("no access")})
	require.Contains(t, m.GetError(), "no access")
}

func TestUpdate_FilesLoaded_Success(t *testing.T) {
	m := testModel()
	m.importInputActive = true
	m.Update(FilesLoadedMsg{
		Path:  "/tmp",
		Files: []string{"..", "/tmp/ctx.tar"},
	})
	require.True(t, m.fileBrowserActive)
	require.False(t, m.importInputActive)
	require.Equal(t, "/tmp", m.fileBrowserPath)
	require.Len(t, m.fileBrowserFiles, 2)
}

func TestUpdate_FilesLoaded_Error(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.importInputActive = true
	m.Update(FilesLoadedMsg{Error: fmt.Errorf("no such dir")})
	require.Contains(t, m.GetError(), "no such dir")
	require.False(t, m.fileBrowserActive)
	require.False(t, m.importInputActive)
}

func TestUpdate_FilesLoaded_Empty(t *testing.T) {
	m := testModel()
	m.importInputActive = true
	m.Update(FilesLoadedMsg{Path: "/empty", Files: []string{}})
	require.Contains(t, m.GetError(), "No context archive files found")
	require.False(t, m.fileBrowserActive)
}

func TestUpdate_WindowSizeMsg(t *testing.T) {
	m := testModel()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	require.Equal(t, 100, m.viewport.Width)
	require.Equal(t, 30, m.viewport.Height)
	require.True(t, m.ready)
}

func TestUpdate_RefreshTickMsg_Visible(t *testing.T) {
	m := testModel()
	m.Visible = true
	loadContexts(m, fakeContexts("a"))
	cmd := m.Update(RefreshTickMsg{})
	require.NotNil(t, cmd)
}

func TestUpdate_RefreshTickMsg_NotVisible(t *testing.T) {
	m := testModel()
	m.Visible = false
	cmd := m.Update(RefreshTickMsg{})
	// Still returns tick cmd to keep timer running
	require.NotNil(t, cmd)
}

func TestUpdate_RefreshTickMsg_DialogActive(t *testing.T) {
	m := testModel()
	m.Visible = true
	m.createDialogActive = true
	cmd := m.Update(RefreshTickMsg{})
	// Returns tick cmd but does NOT trigger load
	require.NotNil(t, cmd)
}

// --- Confirm dialog tests ---

func TestUpdate_ConfirmDialog_ExportOverwrite(t *testing.T) {
	var exported string
	ops := noopContextOps()
	ops.exportContextWithForceFn = func(name string) (string, error) {
		exported = name
		return "/tmp/" + name + ".tar", nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	m.pendingExportContext = "ctx1"
	m.pendingAction = "export"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	require.IsType(t, ContextExportedMsg{}, msg)
	require.Equal(t, "ctx1", exported)
}

func TestUpdate_ConfirmDialog_Delete(t *testing.T) {
	var deleted string
	ops := noopContextOps()
	ops.deleteContextFn = func(name string) error {
		deleted = name
		return nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	m.pendingDeleteContext = "ctx1"
	m.pendingAction = "delete"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	require.IsType(t, ContextDeletedMsg{}, msg)
	require.Equal(t, "ctx1", deleted)
}

func TestUpdate_ConfirmDialog_Cancelled(t *testing.T) {
	m := testModel()
	m.pendingDeleteContext = "ctx1"
	m.pendingAction = "delete"
	m.confirmDialog.Visible = true
	m.Update(confirmdialog.ResultMsg{Confirmed: false})
	require.False(t, m.confirmDialog.Visible)
	require.Equal(t, "", m.pendingDeleteContext)
	require.Equal(t, "", m.pendingAction)
}

func TestUpdate_ConfirmDialog_EscCloses(t *testing.T) {
	m := testModel()
	m.confirmDialog.Visible = true
	m.pendingAction = "delete"
	m.Update(key("esc"))
	require.False(t, m.confirmDialog.Visible)
	require.Equal(t, "", m.pendingAction)
}

// --- Key routing: main view ---

func TestKey_Enter_SwitchContext(t *testing.T) {
	var switched string
	ops := noopContextOps()
	ops.validateContextFn = func(_ context.Context, name string) error {
		switched = name
		return nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	ctxs := fakeContexts("current", "other")
	ctxs[0].Current = true
	ctxs[1].Current = false
	loadContexts(m, ctxs)
	// Move to non-current context
	m.MoveCursor(1)
	cmd := m.Update(key("enter"))
	require.True(t, m.IsSwitchPending())
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	require.IsType(t, ContextSwitchedMsg{}, msg)
	require.Equal(t, "other", switched)
}

func TestKey_Enter_SwitchContext_Filtered(t *testing.T) {
	var switched string
	ops := noopContextOps()
	ops.validateContextFn = func(_ context.Context, name string) error {
		switched = name
		return nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	// fakeContexts marks the first one current; the filtered row is the third.
	loadContexts(m, fakeContexts("alpha", "bravo", "charlie"))
	m.ApplySearchQuery("charlie")
	cmd := m.Update(key("enter"))
	require.True(t, m.IsSwitchPending())
	require.IsType(t, ContextSwitchedMsg{}, runCmd(cmd))
	require.Equal(t, "charlie", switched)
}

func TestKey_Enter_CurrentContext_NoOp(t *testing.T) {
	m := testModel()
	ctxs := fakeContexts("current")
	ctxs[0].Current = true
	loadContexts(m, ctxs)
	cmd := m.Update(key("enter"))
	require.Nil(t, cmd)
	require.False(t, m.IsSwitchPending())
}

func TestKey_Inspect(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("ctx1"))
	cmd := m.Update(key("i"))
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "inspect", nav.ViewName)
}

// The "?" key is routed by the app, not by this view — see app.Model.openHelp
// and its tests. What the view still owns is the content the app asks it for.
func TestHelpContent(t *testing.T) {
	m := testModel()
	require.NotEmpty(t, m.HelpContent())
}

func TestKey_Export(t *testing.T) {
	var exported string
	ops := noopContextOps()
	ops.checkContextExportExistsFn = func(name string) bool { return false }
	ops.exportContextFn = func(name string) (string, error) {
		exported = name
		return "/tmp/" + name + ".tar", nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	loadContexts(m, fakeContexts("ctx1"))
	cmd := m.Update(key("x"))
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	require.IsType(t, ContextExportedMsg{}, msg)
	require.Equal(t, "ctx1", exported)
}

func TestKey_Import_OpensFileBrowser(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("ctx1"))
	cmd := m.Update(key("m"))
	require.True(t, m.fileBrowserActive)
	require.NotNil(t, cmd)
}

func TestKey_Create_OpensDialog(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("ctx1"))
	cmd := m.Update(key("c"))
	require.True(t, m.createDialogActive)
	require.Equal(t, 0, m.createInputFocus)
	require.NotNil(t, cmd) // textinput.Blink
}

func TestKey_Edit_OpensDialog(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("ctx1"))
	cmd := m.Update(key("e"))
	require.True(t, m.editDialogActive)
	require.Equal(t, "ctx1", m.editContextName)
	require.Equal(t, "desc-ctx1", m.editDescInput.Value())
	require.Equal(t, "tcp://ctx1:2375", m.editHostInput.Value())
	require.NotNil(t, cmd) // textinput.Blink
}

// Docker reserves the name, so the form would never save.
func TestKey_Edit_DefaultContext_Refused(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts(docker.DefaultContextName))
	m.Update(key("e"))
	require.False(t, m.editDialogActive)
	require.True(t, m.errorDialogActive)
	require.Equal(t, docker.ErrDefaultContextImmutable.Error(), m.GetError())
}

func TestKey_Delete_CurrentContext_Error(t *testing.T) {
	m := testModel()
	ctxs := fakeContexts("current")
	ctxs[0].Current = true
	loadContexts(m, ctxs)
	m.Update(key("ctrl+d"))
	require.Contains(t, m.GetError(), "Cannot delete")
	require.False(t, m.confirmDialog.Visible)
}

func TestKey_Delete_NonCurrentContext(t *testing.T) {
	m := testModel()
	ctxs := fakeContexts("current", "other")
	ctxs[0].Current = true
	ctxs[1].Current = false
	loadContexts(m, ctxs)
	m.MoveCursor(1)
	m.Update(key("ctrl+d"))
	require.True(t, m.confirmDialog.Visible)
	require.Equal(t, "other", m.pendingDeleteContext)
	require.Equal(t, "delete", m.pendingAction)
}

func TestKey_SwitchPending_BlocksInput(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("ctx1"))
	m.SetSwitchPending(true)
	cmd := m.Update(key("i"))
	require.Nil(t, cmd)
}

// --- Sort key tests ---

func TestKey_Sort_Name(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("b", "a"))
	require.Equal(t, SortByName, m.sortField)
	require.True(t, m.sortAscending)
	m.Update(key("N"))
	require.Equal(t, SortByName, m.sortField)
	require.False(t, m.sortAscending) // toggled
}

func TestKey_Sort_Description(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("a"))
	m.Update(key("D"))
	require.Equal(t, SortByDescription, m.sortField)
	require.True(t, m.sortAscending)
}

func TestKey_Sort_Endpoint(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("a"))
	m.Update(key("E"))
	require.Equal(t, SortByEndpoint, m.sortField)
	require.True(t, m.sortAscending)
}

func TestKey_Sort_Status(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("a"))
	m.Update(key("S"))
	require.Equal(t, SortByStatus, m.sortField)
	require.True(t, m.sortAscending)
}

// --- Navigation keys ---

func TestKey_UpDown(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("a", "b", "c"))
	require.Equal(t, 0, m.GetCursor())
	m.Update(key("down"))
	require.Equal(t, 1, m.GetCursor())
	m.Update(key("up"))
	require.Equal(t, 0, m.GetCursor())
}

func TestKey_PgUpPgDown(t *testing.T) {
	m := testModel()
	names := make([]string, 20)
	for i := range names {
		names[i] = fmt.Sprintf("ctx%d", i)
	}
	loadContexts(m, fakeContexts(names...))
	m.List.Viewport.Height = 5
	require.Equal(t, 0, m.GetCursor())
	m.Update(key("pgdown"))
	require.Equal(t, 5, m.GetCursor())
	m.Update(key("pgdown"))
	require.Equal(t, 10, m.GetCursor())
	m.Update(key("pgup"))
	require.Equal(t, 5, m.GetCursor())
	m.Update(key("pgup"))
	require.Equal(t, 0, m.GetCursor())
}

// --- Error dialog ---

func TestKey_ErrorDialog_EnterClears(t *testing.T) {
	m := testModel()
	m.errorDialogActive = true
	m.SetError("some error")
	m.Update(key("enter"))
	require.False(t, m.errorDialogActive)
	require.Equal(t, "", m.GetError())
}

func TestKey_ErrorDialog_EscClears(t *testing.T) {
	m := testModel()
	m.errorDialogActive = true
	m.SetError("some error")
	m.Update(key("esc"))
	require.False(t, m.errorDialogActive)
	require.Equal(t, "", m.GetError())
}

// --- Create dialog ---

func TestCreateDialog_Esc_Closes(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createNameInput.SetValue("test")
	m.Update(key("esc"))
	require.False(t, m.createDialogActive)
	require.Equal(t, "", m.createNameInput.Value())
}

func TestCreateDialog_Enter_MissingFields(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createNameInput.SetValue("test")
	// Host is empty
	m.Update(key("enter"))
	require.Contains(t, m.GetError(), "required")
}

func TestCreateDialog_Enter_Submit(t *testing.T) {
	var createdName, createdHost string
	ops := noopContextOps()
	// TLS disabled → calls CreateContext, not CreateContextWithCertFiles
	ops.createContextFn = func(name, host string) error {
		createdName = name
		createdHost = host
		return nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	m.createDialogActive = true
	m.createNameInput.SetValue("new-ctx")
	m.createHostInput.SetValue("tcp://host:2375")
	cmd := m.Update(key("enter"))
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	require.IsType(t, ContextCreatedMsg{}, msg)
	require.Equal(t, "new-ctx", createdName)
	require.Equal(t, "tcp://host:2375", createdHost)
}

func TestCreateDialog_Enter_TLS_MissingCerts(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createNameInput.SetValue("test")
	m.createHostInput.SetValue("tcp://host:2376")
	m.createTLSEnabled = true
	m.Update(key("enter"))
	require.Contains(t, m.GetError(), "certificate")
}

func TestCreateDialog_Tab_CyclesFocus(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createInputFocus = 0
	m.Update(key("tab"))
	require.Equal(t, 1, m.createInputFocus)
	m.Update(key("tab"))
	require.Equal(t, 2, m.createInputFocus)
	m.Update(key("tab"))
	require.Equal(t, 3, m.createInputFocus) // TLS checkbox
	// TLS not enabled, so next tab wraps to 0
	m.Update(key("tab"))
	require.Equal(t, 0, m.createInputFocus)
}

func TestCreateDialog_Space_TogglesTLS(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createInputFocus = 3 // TLS checkbox
	require.False(t, m.createTLSEnabled)
	m.Update(key(" "))
	require.True(t, m.createTLSEnabled)
	m.Update(key(" "))
	require.False(t, m.createTLSEnabled)
}

func TestCreateDialog_Enter_ClearsError(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.SetError("previous error")
	m.Update(key("enter"))
	require.Equal(t, "", m.GetError())
}

// --- #525: a printable character must never be a hotkey over a focused input ---

// certDialog opens the create dialog with TLS on and one cert field focused.
func certDialog(t *testing.T, focus int) *Model {
	t.Helper()
	m := testModel()
	m.createDialogActive = true
	m.createTLSEnabled = true
	m.createInputFocus = focus
	m.updateCreateFocus()
	return m
}

// Every certificate path a Docker context needs is under a directory with an
// "f" in it more often than not — /etc/docker/certs.d, fullchain.pem.
func TestCreateDialog_CertFields_F_TypesIntoFocusedInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		focus int
		value func(*Model) string
	}{
		{"ca", 4, func(m *Model) string { return m.createCAInput.Value() }},
		{"cert", 5, func(m *Model) string { return m.createCertInput.Value() }},
		{"key", 6, func(m *Model) string { return m.createKeyInput.Value() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := certDialog(t, tc.focus)
			for _, k := range []string{"f", "u", "l", "l", "F"} {
				m.Update(key(k))
			}
			require.Equal(t, "fullF", tc.value(m))
			require.False(t, m.certFileBrowserActive, "f must not open the cert browser")
			// The other two cert fields must be untouched.
			require.Empty(t, m.createNameInput.Value())
			require.Empty(t, m.createDescInput.Value())
			require.Empty(t, m.createHostInput.Value())
		})
	}
}

func TestCreateDialog_CertFields_BrowseKey_OpensCertBrowser(t *testing.T) {
	for _, tc := range []struct {
		name   string
		focus  int
		target string
	}{
		{"ca", 4, "ca"},
		{"cert", 5, "cert"},
		{"key", 6, "key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := certDialog(t, tc.focus)
			cmd := m.Update(key("ctrl+o"))
			require.True(t, m.certFileBrowserActive)
			require.Equal(t, tc.target, m.certFileTarget)
			require.NotNil(t, cmd)
		})
	}
}

// Unlike the other dialogs this one has three path inputs, so the chord has no
// unambiguous target away from them and must do nothing.
func TestCreateDialog_BrowseKey_NoOpAwayFromCertFields(t *testing.T) {
	for focus := range 4 {
		m := certDialog(t, focus)
		m.Update(key("ctrl+o"))
		require.False(t, m.certFileBrowserActive, "focus %d must not open the cert browser", focus)
	}

	// Nor with TLS off, where the cert fields are skipped by tab entirely.
	m := testModel()
	m.createDialogActive = true
	m.createInputFocus = 4
	m.Update(key("ctrl+o"))
	require.False(t, m.certFileBrowserActive)
}

func TestCreateDialog_RoutesEveryKeyToFocusedInput(t *testing.T) {
	value := map[int]func(*Model) string{
		0: func(m *Model) string { return m.createNameInput.Value() },
		1: func(m *Model) string { return m.createDescInput.Value() },
		2: func(m *Model) string { return m.createHostInput.Value() },
		4: func(m *Model) string { return m.createCAInput.Value() },
		5: func(m *Model) string { return m.createCertInput.Value() },
		6: func(m *Model) string { return m.createKeyInput.Value() },
	}
	for _, k := range []string{"f", "F", "e", "x"} {
		t.Run(k, func(t *testing.T) {
			for _, focus := range []int{0, 1, 2, 4, 5, 6} {
				m := certDialog(t, focus)
				m.Update(key(k))
				require.Equal(t, k, value[focus](m), "focus %d must receive %q", focus, k)
			}
		})
	}
}

// --- Edit dialog ---

func TestEditDialog_Esc_Closes(t *testing.T) {
	m := testModel()
	m.editDialogActive = true
	m.editContextName = "ctx1"
	m.editDescInput.SetValue("new desc")
	m.Update(key("esc"))
	require.False(t, m.editDialogActive)
	require.Equal(t, "", m.editContextName)
}

// editing opens the dialog on the first context and applies edits to it.
func editing(t *testing.T, ops *mockContextOps, edit func(m *Model)) tea.Msg {
	t.Helper()
	m := testModel(func(m *Model) { m.deps.Contexts = ops })
	loadContexts(m, fakeContexts("ctx1", "ctx2"))
	m.Update(key("e"))
	edit(m)
	return runCmd(m.Update(key("enter")))
}

func TestEditDialog_Enter_Submit(t *testing.T) {
	var updatedName, updatedDesc, updatedHost string
	ops := noopContextOps()
	ops.updateContextEndpointFn = func(name, desc, host string) error {
		updatedName, updatedDesc, updatedHost = name, desc, host
		return nil
	}
	msg := editing(t, ops, func(m *Model) {
		m.editDescInput.SetValue("new desc")
		m.editHostInput.SetValue("tcp://10.0.0.7:2376")
	})
	require.IsType(t, ContextUpdatedMsg{}, msg)
	require.Equal(t, "ctx1", updatedName)
	require.Equal(t, "new desc", updatedDesc)
	require.Equal(t, "tcp://10.0.0.7:2376", updatedHost)
	// fakeContexts marks the first context current, and its endpoint moved.
	require.True(t, msg.(ContextUpdatedMsg).Reconnect)
}

// An unchanged host must not reach Docker: passing --docker replaces the whole
// endpoint and resets the TLS material stored for it.
func TestEditDialog_Enter_DescriptionOnly_LeavesEndpointAlone(t *testing.T) {
	var updatedHost string
	called := false
	ops := noopContextOps()
	ops.updateContextEndpointFn = func(_, _, host string) error {
		called, updatedHost = true, host
		return nil
	}
	msg := editing(t, ops, func(m *Model) { m.editDescInput.SetValue("new desc") })
	require.True(t, called)
	require.Equal(t, "", updatedHost)
	require.False(t, msg.(ContextUpdatedMsg).Reconnect)
}

// Docker ignores an empty --description, so reporting success would be a lie.
func TestEditDialog_Enter_ClearDescription_Refused(t *testing.T) {
	ops := noopContextOps()
	ops.updateContextEndpointFn = func(_, _, _ string) error {
		t.Fatal("update must not be attempted")
		return nil
	}
	m := testModel(func(m *Model) { m.deps.Contexts = ops })
	loadContexts(m, fakeContexts("ctx1"))
	m.Update(key("e"))
	m.editDescInput.SetValue("")
	require.Nil(t, m.Update(key("enter")))
	require.Contains(t, m.GetError(), "cannot clear a description")
	require.True(t, m.editDialogActive)
}

func TestEditDialog_Enter_HostRequired(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("ctx1"))
	m.Update(key("e"))
	m.editHostInput.SetValue("")
	require.Nil(t, m.Update(key("enter")))
	require.Contains(t, m.GetError(), "Host is required")
	require.True(t, m.editDialogActive)
}

func TestEditDialog_Enter_NoChanges_Closes(t *testing.T) {
	ops := noopContextOps()
	ops.updateContextEndpointFn = func(_, _, _ string) error {
		t.Fatal("update must not be attempted")
		return nil
	}
	m := testModel(func(m *Model) { m.deps.Contexts = ops })
	loadContexts(m, fakeContexts("ctx1"))
	m.Update(key("e"))
	require.Nil(t, m.Update(key("enter")))
	require.False(t, m.editDialogActive)
}

func TestEditDialog_Tab_MovesFocus(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("ctx1"))
	m.Update(key("e"))
	require.Equal(t, 0, m.editInputFocus)
	m.Update(key("tab"))
	require.Equal(t, 1, m.editInputFocus)
	require.True(t, m.editHostInput.Focused())
	m.Update(key("tab"))
	require.Equal(t, 0, m.editInputFocus)
	require.True(t, m.editDescInput.Focused())
}

func TestEditDialog_Enter_ClearsError(t *testing.T) {
	m := testModel()
	m.editDialogActive = true
	m.SetError("old error")
	m.Update(key("enter"))
	require.Equal(t, "", m.GetError())
}

// --- Import input dialog ---

func TestImportInput_Esc_Closes(t *testing.T) {
	m := testModel()
	m.importInputActive = true
	m.importInput.SetValue("/some/path")
	m.Update(key("esc"))
	require.False(t, m.importInputActive)
}

func TestImportInput_Enter_SubmitsPath(t *testing.T) {
	m := testModel()
	m.importInputActive = true
	m.importInput.SetValue("/tmp")
	cmd := m.Update(key("enter"))
	require.NotNil(t, cmd) // LoadFilesCmd
}

// --- File browser ---

func TestFileBrowser_Navigation(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserFiles = []string{"..", "/tmp/a/", "/tmp/b.tar", "/tmp/c.tar"}
	m.fileBrowserCursor = 0
	m.Update(key("down"))
	require.Equal(t, 1, m.fileBrowserCursor)
	m.Update(key("down"))
	require.Equal(t, 2, m.fileBrowserCursor)
	m.Update(key("up"))
	require.Equal(t, 1, m.fileBrowserCursor)
}

func TestFileBrowser_Esc_Closes(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserFiles = []string{".."}
	m.Update(key("esc"))
	require.False(t, m.fileBrowserActive)
}

func TestFileBrowser_Enter_SelectsTarFile(t *testing.T) {
	var importedPath string
	ops := noopContextOps()
	ops.importContextFn = func(path string) (string, error) {
		importedPath = path
		return "ctx-name", nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	m.fileBrowserActive = true
	m.fileBrowserPath = "/tmp"
	m.fileBrowserFiles = []string{"/tmp/ctx.tar"}
	m.fileBrowserCursor = 0
	cmd := m.Update(key("enter"))
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	require.IsType(t, ContextImportedMsg{}, msg)
	require.Equal(t, "/tmp/ctx.tar", importedPath)
	require.False(t, m.fileBrowserActive)
}

func TestFileBrowser_Enter_NavigatesDirectory(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserPath = "/tmp"
	m.fileBrowserFiles = []string{"..", "/tmp/subdir/"}
	m.fileBrowserCursor = 1
	cmd := m.Update(key("enter"))
	require.NotNil(t, cmd) // LoadFilesCmd for subdir
	require.Equal(t, "/tmp/subdir", m.fileBrowserPath)
}

func TestFileBrowser_Enter_ParentDir(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserPath = "/tmp/sub"
	m.fileBrowserFiles = []string{"..", "/tmp/sub/file.tar"}
	m.fileBrowserCursor = 0
	cmd := m.Update(key("enter"))
	require.NotNil(t, cmd) // LoadFilesCmd for parent
	require.Equal(t, "/tmp", m.fileBrowserPath)
}

func TestFileBrowser_PgUpPgDown(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	files := make([]string, 25)
	for i := range files {
		files[i] = fmt.Sprintf("/tmp/file%d.tar", i)
	}
	m.fileBrowserFiles = files
	m.fileBrowserCursor = 0
	m.Update(key("pgdown"))
	require.Equal(t, 10, m.fileBrowserCursor)
	m.Update(key("pgup"))
	require.Equal(t, 0, m.fileBrowserCursor)
}

// --- Cert file browser ---

func TestCertFileBrowser_Navigation(t *testing.T) {
	m := testModel()
	m.certFileBrowserActive = true
	m.fileBrowserFiles = []string{"..", "/home/ca.pem", "/home/cert.pem"}
	m.fileBrowserCursor = 0
	m.Update(key("down"))
	require.Equal(t, 1, m.fileBrowserCursor)
	m.Update(key("up"))
	require.Equal(t, 0, m.fileBrowserCursor)
}

func TestCertFileBrowser_Esc_Closes(t *testing.T) {
	m := testModel()
	m.certFileBrowserActive = true
	m.certFileTarget = "ca"
	m.Update(key("esc"))
	require.False(t, m.certFileBrowserActive)
	require.Equal(t, "", m.certFileTarget)
}

func TestCertFileBrowser_Enter_SelectsFile(t *testing.T) {
	m := testModel()
	m.certFileBrowserActive = true
	m.certFileTarget = "ca"
	m.fileBrowserPath = "/home"
	m.fileBrowserFiles = []string{"/home/ca.pem"}
	m.fileBrowserCursor = 0
	m.Update(key("enter"))
	require.False(t, m.certFileBrowserActive)
	require.Equal(t, "/home/ca.pem", m.createCAInput.Value())
}

func TestCertFileBrowser_Enter_SelectsCert(t *testing.T) {
	m := testModel()
	m.certFileBrowserActive = true
	m.certFileTarget = "cert"
	m.fileBrowserPath = "/home"
	m.fileBrowserFiles = []string{"/home/cert.pem"}
	m.fileBrowserCursor = 0
	m.Update(key("enter"))
	require.Equal(t, "/home/cert.pem", m.createCertInput.Value())
}

func TestCertFileBrowser_Enter_SelectsKey(t *testing.T) {
	m := testModel()
	m.certFileBrowserActive = true
	m.certFileTarget = "key"
	m.fileBrowserPath = "/home"
	m.fileBrowserFiles = []string{"/home/key.pem"}
	m.fileBrowserCursor = 0
	m.Update(key("enter"))
	require.Equal(t, "/home/key.pem", m.createKeyInput.Value())
}

func TestCertFileBrowser_Enter_NavigatesDir(t *testing.T) {
	m := testModel()
	m.certFileBrowserActive = true
	m.certFileTarget = "ca"
	m.fileBrowserPath = "/home"
	m.fileBrowserFiles = []string{"/home/certs/"}
	m.fileBrowserCursor = 0
	cmd := m.Update(key("enter"))
	require.True(t, m.certFileBrowserActive) // stays open
	require.NotNil(t, cmd)                   // LoadCertFilesCmd
	require.Equal(t, "/home/certs", m.fileBrowserPath)
}

// --- Cmd correctness ---

func TestLoadContextsCmd(t *testing.T) {
	called := false
	ops := noopContextOps()
	ops.listContextsFn = func() ([]docker.ContextInfo, error) {
		called = true
		return fakeContexts("a"), nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	cmd := m.loadContextsCmd()
	msg := runCmd(cmd)
	require.True(t, called)
	loaded, ok := msg.(ContextsLoadedMsg)
	require.True(t, ok)
	require.Nil(t, loaded.Error)
	require.Len(t, loaded.Contexts, 1)
}

func TestSwitchContextCmd(t *testing.T) {
	var validated string
	ops := noopContextOps()
	ops.validateContextFn = func(_ context.Context, name string) error {
		validated = name
		return nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	cmd := m.switchContextCmd("target")
	msg := runCmd(cmd)
	require.Equal(t, "target", validated)
	switched, ok := msg.(ContextSwitchedMsg)
	require.True(t, ok)
	require.True(t, switched.Success)
}

func TestDeleteContextCmd(t *testing.T) {
	var deleted string
	ops := noopContextOps()
	ops.deleteContextFn = func(name string) error {
		deleted = name
		return nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	cmd := m.deleteContextCmd("ctx1")
	msg := runCmd(cmd)
	require.Equal(t, "ctx1", deleted)
	del, ok := msg.(ContextDeletedMsg)
	require.True(t, ok)
	require.True(t, del.Success)
}

func TestExportContextCmd_FileExists(t *testing.T) {
	ops := noopContextOps()
	ops.checkContextExportExistsFn = func(_ string) bool { return true }
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	cmd := m.exportContextCmd("ctx1")
	msg := runCmd(cmd)
	exp, ok := msg.(ContextExportedMsg)
	require.True(t, ok)
	require.False(t, exp.Success)
	require.Equal(t, "file_exists", exp.Error.Error())
}

func TestExportContextCmd_Success(t *testing.T) {
	ops := noopContextOps()
	ops.checkContextExportExistsFn = func(_ string) bool { return false }
	ops.exportContextFn = func(name string) (string, error) {
		return "/tmp/" + name + ".tar", nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	cmd := m.exportContextCmd("ctx1")
	msg := runCmd(cmd)
	exp, ok := msg.(ContextExportedMsg)
	require.True(t, ok)
	require.True(t, exp.Success)
	require.Equal(t, "/tmp/ctx1.tar", exp.FilePath)
}

func TestInspectContextCmd(t *testing.T) {
	ops := noopContextOps()
	ops.inspectContextFn = func(name string) (string, error) {
		return `{"name":"` + name + `"}`, nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	cmd := m.inspectContextCmd("ctx1")
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "inspect", nav.ViewName)
}

func TestImportContextCmd(t *testing.T) {
	var importedPath string
	ops := noopContextOps()
	ops.importContextFn = func(path string) (string, error) {
		importedPath = path
		return "imported-ctx", nil
	}
	m := testModel(func(m *Model) {
		m.deps.Contexts = ops
	})
	cmd := m.importContextCmd("/tmp/ctx.tar")
	msg := runCmd(cmd)
	require.Equal(t, "/tmp/ctx.tar", importedPath)
	imp, ok := msg.(ContextImportedMsg)
	require.True(t, ok)
	require.True(t, imp.Success)
	require.Equal(t, "imported-ctx", imp.ContextName)
}

// --- Esc clears filter ---

func TestKey_Esc_ClearsFilter(t *testing.T) {
	m := testModel()
	loadContexts(m, fakeContexts("a", "b"))
	m.List.Query = "a"
	m.Update(key("esc"))
	require.Equal(t, "", m.List.Query)
}
