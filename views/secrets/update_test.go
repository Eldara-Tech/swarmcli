// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package secretsview

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// --- State machine tests ---

func TestSecretsLoaded_TransitionsToReady(t *testing.T) {
	m := testModel()
	require.Equal(t, stateLoading, m.state)
	loadSecrets(m, fakeSecrets("s1", "s2"))
	require.Equal(t, stateReady, m.state)
	require.Len(t, m.secretsList.Items, 2)
}

func TestErrorMsg_TransitionsToError(t *testing.T) {
	m := testModel()
	m.Update(errorMsg(fmt.Errorf("boom")))
	require.Equal(t, stateError, m.state)
	require.True(t, m.errorDialogActive)
}

func TestSecretDeleted_ReloadsSecrets(t *testing.T) {
	called := false
	mock := noopSecretOps()
	mock.listSecretsFn = func(_ context.Context) ([]swarm.Secret, error) {
		called = true
		return nil, nil
	}
	m := testModel(func(m *Model) { m.deps.Secrets = mock })
	cmd := m.Update(secretDeletedMsg{Name: "old"})
	require.NotNil(t, cmd)
	runCmd(cmd)
	require.True(t, called)
}

func TestSecretCreated_AddsToList(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("existing"))
	m.createDialogActive = true

	sec := docker.SecretWithDecodedData{
		Secret: swarm.Secret{
			ID:   "new-id",
			Meta: swarm.Meta{CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Spec: swarm.SecretSpec{Annotations: swarm.Annotations{Name: "new-secret"}},
		},
	}
	m.Update(secretCreatedMsg{Name: "new-secret", Secret: sec})
	require.False(t, m.createDialogActive)
	require.Len(t, m.secretsList.Items, 2)
}

func TestUsedStatusUpdated_SetsUsedFlag(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.Update(usedStatusUpdatedMsg(map[string]bool{"id-s1": true}))
	require.True(t, m.secretsList.Items[0].Used)
	require.True(t, m.secretsList.Items[0].UsedKnown)
}

func TestWindowSizeMsg_UpdatesViewport(t *testing.T) {
	m := testModel()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	require.Equal(t, 120, m.secretsList.Viewport.Width)
	require.Equal(t, 40, m.secretsList.Viewport.Height)
}

func TestTickMsg_WhenReadyAndVisible_Polls(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.visible = true
	cmd := m.Update(TickMsg{Gen: m.pollGen})
	require.NotNil(t, cmd)
}

func TestTickMsg_WhenNotVisible_SchedulesTick(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.visible = false
	cmd := m.Update(TickMsg{Gen: m.pollGen})
	require.NotNil(t, cmd) // returns tickCmd
}

func TestFileBrowserMsg_ActivatesBrowser(t *testing.T) {
	m := testModel()
	m.Update(fileBrowserMsg{Path: "/tmp", Files: []string{"..", "/tmp/a.txt"}})
	require.True(t, m.fileBrowserActive)
	require.Equal(t, "/tmp", m.fileBrowserPath)
	require.Len(t, m.fileBrowserFiles, 2)
}

func TestEditorContentMsg_SetsContent(t *testing.T) {
	m := testModel()
	m.Update(editorContentMsg{Content: "secret-data"})
	require.Equal(t, "secret-data", m.createSecretData)
	require.True(t, m.createDialogActive)
	require.Equal(t, "details-inline", m.createDialogStep)
}

func TestUsedByMsg_ActivatesUsedByView(t *testing.T) {
	m := testModel()
	// Need viewport dimensions set
	m.secretsList.Viewport.Width = 80
	m.secretsList.Viewport.Height = 20
	m.Update(usedByMsg{
		SecretName: "my-secret",
		UsedBy: []usedByItem{
			{StackName: "stack1", ServiceName: "svc1"},
		},
	})
	require.True(t, m.usedByViewActive)
	require.Equal(t, "my-secret", m.usedBySecretName)
	require.Len(t, m.usedByList.Items, 1)
}

// --- Key routing tests ---

func TestKey_N_OpensCreateDialog(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.Update(key("n"))
	require.True(t, m.createDialogActive)
	require.Equal(t, "source", m.createDialogStep)
}

func TestKey_CtrlD_OpensConfirmDialog(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.Update(key("ctrl+d"))
	require.True(t, m.confirmDialog.Visible)
	require.Equal(t, "delete", m.pendingAction)
}

func TestKey_CtrlD_EmptyList_Noop(t *testing.T) {
	m := testModel()
	m.Update(key("ctrl+d"))
	require.False(t, m.confirmDialog.Visible)
}

func TestKey_I_InspectsSecret(t *testing.T) {
	inspected := ""
	mock := noopSecretOps()
	mock.inspectSecretFn = func(_ context.Context, nameOrID string) (*docker.SecretWithDecodedData, error) {
		inspected = nameOrID
		return &docker.SecretWithDecodedData{
			Secret: swarm.Secret{
				ID:   "id-s1",
				Spec: swarm.SecretSpec{Annotations: swarm.Annotations{Name: "s1"}},
			},
		}, nil
	}
	m := testModel(func(m *Model) { m.deps.Secrets = mock })
	loadSecrets(m, fakeSecrets("s1"))
	cmd := m.Update(key("i"))
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "inspect", nav.ViewName)
	require.Equal(t, "s1", inspected)
}

func TestKey_U_OpensUsedByView(t *testing.T) {
	mock := noopSecretOps()
	mock.inspectSecretFn = func(_ context.Context, _ string) (*docker.SecretWithDecodedData, error) {
		return &docker.SecretWithDecodedData{
			Secret: swarm.Secret{ID: "id-s1", Spec: swarm.SecretSpec{Annotations: swarm.Annotations{Name: "s1"}}},
		}, nil
	}
	m := testModel(func(m *Model) { m.deps.Secrets = mock })
	loadSecrets(m, fakeSecrets("s1"))
	cmd := m.Update(key("u"))
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	_, ok := msg.(usedByMsg)
	require.True(t, ok)
}

func TestKey_X_NoAction_ShowsError(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.Update(key("x"))
	require.True(t, m.errorDialogActive)
	require.Contains(t, m.err.Error(), "Business Edition")
	require.Contains(t, m.err.Error(), "swarmcli.io/be")
}

// The "?" key is routed by the app, not by this view — see app.Model.openHelp
// and its tests. What the view still owns is the content the app asks it for.
func TestHelpContent(t *testing.T) {
	m := testModel()
	require.NotEmpty(t, m.HelpContent())
}

func TestKey_ErrorDialog_EnterDismisses(t *testing.T) {
	m := testModel()
	m.errorDialogActive = true
	m.err = fmt.Errorf("boom")
	m.state = stateError
	m.Update(key("enter"))
	require.False(t, m.errorDialogActive)
	require.Equal(t, stateReady, m.state)
}

func TestKey_ErrorDialog_EscDismisses(t *testing.T) {
	m := testModel()
	m.errorDialogActive = true
	m.err = fmt.Errorf("boom")
	m.state = stateError
	m.Update(key("esc"))
	require.False(t, m.errorDialogActive)
}

func TestConfirmDialogResult_DeleteConfirmed(t *testing.T) {
	deleted := ""
	mock := noopSecretOps()
	mock.deleteSecretFn = func(_ context.Context, nameOrID string) error {
		deleted = nameOrID
		return nil
	}
	m := testModel(func(m *Model) { m.deps.Secrets = mock })
	loadSecrets(m, fakeSecrets("s1"))

	m.pendingAction = "delete"
	m.secretToDelete = &m.secrets[0]
	m.confirmDialog.Visible = true

	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	_, ok := msg.(secretDeletedMsg)
	require.True(t, ok)
	require.Equal(t, "s1", deleted)
}

func TestConfirmDialogResult_DeleteCancelled(t *testing.T) {
	m := testModel()
	m.pendingAction = "delete"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: false})
	require.Nil(t, cmd)
	require.False(t, m.confirmDialog.Visible)
}

// --- Sort key tests ---

func TestSortKey_N_SortsByName(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("beta", "alpha"))
	require.Equal(t, SortByName, m.sortField)
	require.True(t, m.sortAscending)
	// Press N to toggle descending
	m.Update(key("N"))
	require.Equal(t, SortByName, m.sortField)
	require.False(t, m.sortAscending)
}

func TestSortKey_I_SortsByID(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.Update(key("I"))
	require.Equal(t, SortByID, m.sortField)
	require.True(t, m.sortAscending)
}

func TestSortKey_C_SortsByCreated(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.Update(key("C"))
	require.Equal(t, SortByCreated, m.sortField)
}

func TestSortKey_D_SortsByUpdated(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.Update(key("D"))
	require.Equal(t, SortByUpdated, m.sortField)
}

func TestSortKey_L_SortsByLabels(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.Update(key("L"))
	require.Equal(t, SortByLabels, m.sortField)
}

func TestSortKey_U_SortsByUsed(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.Update(key("U"))
	require.Equal(t, SortByUsed, m.sortField)
}

// --- Create dialog tests ---

func TestCreateDialog_Source_UpDown_TogglesSource(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "source"
	m.createSecretSource = "file"

	m.Update(key("down"))
	require.Equal(t, "inline", m.createSecretSource)
	m.Update(key("up"))
	require.Equal(t, "file", m.createSecretSource)
}

func TestCreateDialog_Source_Enter_GoesToDetailsFile(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "source"
	m.createSecretSource = "file"

	m.Update(key("enter"))
	require.Equal(t, "details-file", m.createDialogStep)
}

func TestCreateDialog_Source_Enter_GoesToDetailsInline(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "source"
	m.createSecretSource = "inline"

	m.Update(key("enter"))
	require.Equal(t, "details-inline", m.createDialogStep)
}

func TestCreateDialog_Source_Esc_Closes(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "source"

	m.Update(key("esc"))
	require.False(t, m.createDialogActive)
}

func TestCreateDialog_DetailsFile_Tab_CyclesFocus(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createInputFocus = 0

	m.Update(key("tab"))
	require.Equal(t, 1, m.createInputFocus)
	m.Update(key("tab"))
	require.Equal(t, 2, m.createInputFocus)
	m.Update(key("tab"))
	require.Equal(t, 3, m.createInputFocus)
	m.Update(key("tab"))
	require.Equal(t, 0, m.createInputFocus)
}

func TestCreateDialog_DetailsFile_Enter_EmptyName_ShowsError(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createNameInput.SetValue("")

	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "empty")
}

func TestCreateDialog_DetailsFile_Enter_InvalidName_ShowsError(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createNameInput.SetValue("bad/name")

	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "may contain only")
}

// swarmcli-be#353: a name the daemon would refuse for its length used to reach
// the daemon, which reported it after the dialog had already closed.
func TestCreateDialog_DetailsFile_Enter_OverlongName_ShowsError(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createNameInput.SetValue(strings.Repeat("a", 65))

	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "64 characters or fewer")
}

func TestCreateDialog_DetailsFile_Enter_NoPath_ShowsError(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createNameInput.SetValue("valid-name")
	m.createFileInput.SetValue("")

	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "file path")
}

func TestCreateDialog_DetailsInline_Enter_NoContent_ShowsError(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createNameInput.SetValue("valid-name")
	m.createSecretData = ""

	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "content")
}

func TestCreateDialog_DetailsFile_Esc_Closes(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"

	m.Update(key("esc"))
	require.False(t, m.createDialogActive)
}

func TestCreateDialog_DetailsInline_Esc_Closes(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"

	m.Update(key("esc"))
	require.False(t, m.createDialogActive)
}

// #628: the default must stay off. With encoding on, the stored secret is
// base64 *of* the credential and nothing decodes it again — a consumer reading
// /run/secrets/<name> gets bytes that are printable and plausible enough to
// survive every check but a prefix comparison.
func TestCreateDialog_EncodeDefaultsOff(t *testing.T) {
	require.False(t, testModel().createEncodeSecret)
}

func TestCreateDialog_DetailsFile_Space_TogglesEncode(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createInputFocus = 3
	initial := m.createEncodeSecret

	m.Update(key(" "))
	require.NotEqual(t, initial, m.createEncodeSecret)
}

// --- #525: a printable character must never be a hotkey over a focused input ---

func secretFileDialog(t *testing.T) *Model {
	t.Helper()
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createInputFocus = 0
	m.createNameInput.Focus()
	m.Update(key("tab"))
	require.Equal(t, 1, m.createInputFocus)
	return m
}

func TestCreateDialog_DetailsFile_F_TypesIntoPath(t *testing.T) {
	m := secretFileDialog(t)
	m.createFileInput.SetValue("/etc/ssl/")
	m.Update(key("f"))
	m.Update(key("F"))
	require.Equal(t, "/etc/ssl/fF", m.createFileInput.Value())
	require.False(t, m.fileBrowserActive, "f must not open the browser")
}

func TestCreateDialog_DetailsFile_BrowseKey_OpensFileBrowser(t *testing.T) {
	m := secretFileDialog(t)
	cmd := m.Update(key("ctrl+o"))
	require.False(t, m.createDialogActive)
	require.True(t, m.fileBrowserActive)
	require.NotNil(t, cmd)
}

func TestCreateDialog_DetailsFile_BrowseKey_FromNameFocus(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createInputFocus = 0
	m.createNameInput.Focus()
	require.NotNil(t, m.Update(key("ctrl+o")))
	require.True(t, m.fileBrowserActive)
}

// Both dialog steps hold an encode toggle at focus 3 and route every other key
// to the focused input. The toggle must keep working and must not swallow a
// space typed into a field.
func TestCreateDialog_RoutesEveryKeyToFocusedInput(t *testing.T) {
	steps := map[string][]int{
		"details-file":   {0, 1, 2},
		"details-inline": {0, 2},
	}
	value := map[int]func(*Model) string{
		0: func(m *Model) string { return m.createNameInput.Value() },
		1: func(m *Model) string { return m.createFileInput.Value() },
		2: func(m *Model) string { return m.createLabelsInput.Value() },
	}
	focusInput := map[int]func(*Model){
		0: func(m *Model) { m.createNameInput.Focus() },
		1: func(m *Model) { m.createFileInput.Focus() },
		2: func(m *Model) { m.createLabelsInput.Focus() },
	}

	for step, focuses := range steps {
		for _, k := range []string{"f", "F", "e", "x", " "} {
			t.Run(step+"/"+k, func(t *testing.T) {
				for _, focus := range focuses {
					m := testModel()
					m.createDialogActive = true
					m.createDialogStep = step
					m.createInputFocus = focus
					focusInput[focus](m)
					m.Update(key(k))
					require.Equal(t, k, value[focus](m), "focus %d must receive %q", focus, k)
				}

				// Focus 3 is the encode toggle, not an input.
				m := testModel()
				m.createDialogActive = true
				m.createDialogStep = step
				m.createInputFocus = 3
				before := m.createEncodeSecret
				m.Update(key(k))
				if k == " " {
					require.NotEqual(t, before, m.createEncodeSecret, "space must toggle encode")
				} else {
					require.Equal(t, before, m.createEncodeSecret)
				}
				require.Empty(t, m.createNameInput.Value())
				require.Empty(t, m.createFileInput.Value())
				require.Empty(t, m.createLabelsInput.Value())
			})
		}
	}
}

func TestCreateDialog_DetailsInline_E_OpensEditorAtContent(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createInputFocus = 1
	cmd := m.Update(key("e"))
	require.False(t, m.createDialogActive)
	require.NotNil(t, cmd)
}

// --- UsedBy view key tests ---

func TestUsedByView_Esc_ClosesView(t *testing.T) {
	m := testModel()
	m.usedByViewActive = true
	m.usedBySecretName = "test"
	m.Update(key("esc"))
	require.False(t, m.usedByViewActive)
}

func TestUsedByView_Enter_NavigatesToServices(t *testing.T) {
	m := testModel()
	m.usedByViewActive = true
	m.usedBySecretName = "test"
	// Set up list with items
	m.usedByList.Items = []usedByItem{{StackName: "mystack", ServiceName: "svc1"}}
	m.usedByList.Filtered = m.usedByList.Items
	m.usedByList.Cursor = 0

	cmd := m.Update(key("enter"))
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, view.NameServices, nav.ViewName)
}

// --- File browser key tests ---

func TestFileBrowser_Esc_ReturnsToCreateDialog(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.Update(key("esc"))
	require.False(t, m.fileBrowserActive)
	require.True(t, m.createDialogActive)
}

func TestFileBrowser_UpDown_MovesCursor(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserFiles = []string{"..", "/tmp/a/", "/tmp/b.txt"}
	m.fileBrowserCursor = 0

	m.Update(key("down"))
	require.Equal(t, 1, m.fileBrowserCursor)
	m.Update(key("down"))
	require.Equal(t, 2, m.fileBrowserCursor)
	m.Update(key("up"))
	require.Equal(t, 1, m.fileBrowserCursor)
}

func TestFileBrowser_Enter_File_SelectsPath(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserFiles = []string{"/tmp/secret.txt"}
	m.fileBrowserPath = "/tmp"
	m.fileBrowserCursor = 0

	m.Update(key("enter"))
	require.False(t, m.fileBrowserActive)
	require.True(t, m.createDialogActive)
	require.Equal(t, "/tmp/secret.txt", m.createSecretPath)
}

// --- parseLabels tests ---

func TestParseLabels_Empty(t *testing.T) {
	labels, err := parseLabels("")
	require.NoError(t, err)
	require.Empty(t, labels)
}

func TestParseLabels_Valid(t *testing.T) {
	labels, err := parseLabels("a=b,c=d")
	require.NoError(t, err)
	require.Equal(t, "b", labels["a"])
	require.Equal(t, "d", labels["c"])
}

func TestParseLabels_Invalid(t *testing.T) {
	_, err := parseLabels("noequals")
	require.Error(t, err)
}

func TestParseLabels_EmptyKey(t *testing.T) {
	_, err := parseLabels("=value")
	require.Error(t, err)
}

func TestTickMsg_WhenPollingInFlight_SkipsCheck(t *testing.T) {
	m := testModel()
	loadSecrets(m, fakeSecrets("s1"))
	m.visible = true
	m.polling.Store(true)
	cmd := m.Update(TickMsg{Gen: m.pollGen})
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	_, isTickMsg := msg.(TickMsg)
	require.True(t, isTickMsg, "expected TickMsg from tickCmd, got %T", msg)
}

// --- SpinnerTick ---

func TestSpinnerTick_AdvancesSpinner(t *testing.T) {
	m := testModel()
	oldSpinner := m.spinner
	m.Update(SpinnerTickMsg(time.Now()))
	require.Equal(t, oldSpinner+1, m.spinner)
}
