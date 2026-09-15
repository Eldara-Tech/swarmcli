// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package configsview

import (
	"fmt"
	"testing"
	"time"

	"github.com/Eldara-Tech/swarmcli/v2/charts"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
)

// --- State machine tests ---

func TestUpdate_ConfigsLoaded_SetsReady(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("alpha", "beta"))
	require.Equal(t, stateReady, m.state)
	require.Len(t, m.configs, 2)
	require.Len(t, m.configsList.Items, 2)
}

func TestUpdate_ErrorMsg_ShowsError(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("c1"))
	m.Update(errorMsg(fmt.Errorf("test error")))
	require.True(t, m.errorDialogActive)
}

func TestUpdate_ConfigDeleted_Reloads(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("c1"))
	cmd := m.Update(configDeletedMsg{Name: "c1"})
	require.NotNil(t, cmd)
}

func TestUpdate_ConfigCreated_Reloads(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("c1"))
	cmd := m.Update(configCreatedMsg{Config: swarm.Config{}})
	require.NotNil(t, cmd)
}

func TestUpdate_UsedStatusUpdated(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("c1"))
	m.Update(usedStatusUpdatedMsg{"id-c1": true})
	require.True(t, m.configsList.Items[0].Used)
	require.True(t, m.configsList.Items[0].UsedKnown)
}

func TestUpdate_WindowSizeMsg(t *testing.T) {
	m := testModel()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	require.Equal(t, 120, m.configsList.Viewport.Width)
	require.Equal(t, 40, m.configsList.Viewport.Height)
}

func TestUpdate_TickMsg(t *testing.T) {
	m := testModel()
	m.visible = true
	m.state = stateReady
	cmd := m.Update(TickMsg{Gen: m.pollGen})
	require.NotNil(t, cmd)
}

func TestUpdate_SpinnerTickMsg(t *testing.T) {
	m := testModel()
	old := m.spinner
	m.Update(SpinnerTickMsg(time.Now()))
	require.Equal(t, old+1, m.spinner)
}

func TestUpdate_EditorContentMsg(t *testing.T) {
	m := testModel()
	m.Update(editorContentMsg{Content: "key=value"})
	require.True(t, m.createDialogActive)
	require.Equal(t, "details-inline", m.createDialogStep)
	require.Equal(t, "key=value", m.createConfigData)
}

func TestUpdate_FilesLoadedMsg_Success(t *testing.T) {
	m := testModel()
	m.Update(filesLoadedMsg{Path: "/tmp", Files: []string{"..", "foo.txt"}})
	require.True(t, m.fileBrowserActive)
	require.Equal(t, "/tmp", m.fileBrowserPath)
}

func TestUpdate_FilesLoadedMsg_Error(t *testing.T) {
	m := testModel()
	m.Update(filesLoadedMsg{Path: "/nope", Error: fmt.Errorf("no access")})
	require.False(t, m.fileBrowserActive)
	require.True(t, m.createDialogActive)
	require.Contains(t, m.createDialogError, "no access")
}

func TestUpdate_UsedByMsg_Success(t *testing.T) {
	m := testModel()
	m.configsList.Viewport.Width = 80
	m.configsList.Viewport.Height = 20
	m.Update(usedByMsg{ConfigName: "c1", UsedBy: []usedByItem{{StackName: "s1", ServiceName: "svc1"}}})
	require.True(t, m.usedByViewActive)
	require.Equal(t, "c1", m.usedByConfigName)
}

func TestUpdate_UsedByMsg_Error(t *testing.T) {
	m := testModel()
	m.Update(usedByMsg{ConfigName: "c1", Error: fmt.Errorf("fail")})
	require.True(t, m.errorDialogActive)
}

func TestUpdate_ConfigCreateErrorMsg(t *testing.T) {
	m := testModel()
	m.Update(configCreateErrorMsg{err: fmt.Errorf("dup")})
	require.True(t, m.createDialogActive)
	require.Contains(t, m.createDialogError, "dup")
}

func TestUpdate_ConfigRotated_Reloads(t *testing.T) {
	m := testModel()
	cmd := m.Update(configRotatedMsg{})
	require.NotNil(t, cmd)
}

func TestUpdate_EditConfigDoneMsg_Changed(t *testing.T) {
	m := testModel()
	old := docker.ConfigWithDecodedData{Config: swarm.Config{Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: "old"}}}}
	new := docker.ConfigWithDecodedData{Config: swarm.Config{Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: "new"}}}}
	m.Update(editConfigDoneMsg{Changed: true, OldConfig: old, NewConfig: new})
	require.Equal(t, "rotate", m.pendingAction)
	require.True(t, m.confirmDialog.Visible)
}

func TestUpdate_EditConfigDoneMsg_NoChange(t *testing.T) {
	m := testModel()
	old := docker.ConfigWithDecodedData{Config: swarm.Config{Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: "c1"}}}}
	cmd := m.Update(editConfigDoneMsg{Changed: false, OldConfig: old, NewConfig: old})
	require.NotNil(t, cmd) // tea.Printf
}

func TestUpdate_EditConfigErrorMsg(t *testing.T) {
	m := testModel()
	m.Update(editConfigErrorMsg{err: fmt.Errorf("edit failed")})
	require.Equal(t, stateError, m.state)
}

func TestUpdate_EditorContentReadyMsg_Error(t *testing.T) {
	m := testModel()
	m.Update(editorContentReadyMsg{Err: fmt.Errorf("name taken")})
	require.True(t, m.createDialogActive)
	require.Equal(t, "details-inline", m.createDialogStep)
	require.Contains(t, m.createDialogError, "name taken")
}

func TestUpdate_FileContentReadyMsg_Error(t *testing.T) {
	m := testModel()
	m.Update(fileContentReadyMsg{FilePath: "/tmp/foo", Err: fmt.Errorf("dup name")})
	require.True(t, m.createDialogActive)
	require.Equal(t, "details-file", m.createDialogStep)
	require.Contains(t, m.createDialogError, "dup name")
}

// --- Key routing tests ---

func TestKey_N_OpensCreateDialog(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("c1"))
	m.Update(key("n"))
	require.True(t, m.createDialogActive)
	require.Equal(t, "source", m.createDialogStep)
}

func TestKey_CtrlD_OpensConfirmDialog(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("myconfig"))
	m.Update(key("ctrl+d"))
	require.True(t, m.confirmDialog.Visible)
	require.Contains(t, m.confirmDialog.Message, "myconfig")
}

func TestKey_I_InspectsConfig(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("myconfig"))
	cmd := m.Update(key("i"))
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "inspect", nav.ViewName)
}

func TestKey_Enter_ViewsRawConfig(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("myconfig"))
	cmd := m.Update(key("enter"))
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "inspect", nav.ViewName)
}

func TestKey_U_OpensUsedByView(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("myconfig"))
	cmd := m.Update(key("u"))
	require.NotNil(t, cmd)
}

// The "?" key is routed by the app, not by this view — see app.Model.openHelp
// and its tests. What the view still owns is the content the app asks it for.
func TestHelpContent(t *testing.T) {
	m := testModel()
	require.NotEmpty(t, m.HelpContent())
}

func TestKey_ErrorDialog_Dismiss(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("c1"))
	m.errorDialogActive = true
	m.err = errorMsg(fmt.Errorf("boom"))
	m.Update(key("enter"))
	require.False(t, m.errorDialogActive)
	require.Equal(t, stateReady, m.state)
}

// --- Sort key tests ---

func TestSortKey_N_Name(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("b", "a"))
	// Default is SortByName+asc, N toggles to desc
	m.Update(key("N"))
	require.Equal(t, SortByName, m.sortField)
	require.False(t, m.sortAscending)
}

func TestSortKey_I_ID(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("a", "b"))
	m.Update(key("I"))
	require.Equal(t, SortByID, m.sortField)
}

func TestSortKey_C_Created(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("a", "b"))
	m.Update(key("C"))
	require.Equal(t, SortByCreated, m.sortField)
}

func TestSortKey_D_Updated(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("a", "b"))
	m.Update(key("D"))
	require.Equal(t, SortByUpdated, m.sortField)
}

func TestSortKey_L_Labels(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("a", "b"))
	m.Update(key("L"))
	require.Equal(t, SortByLabels, m.sortField)
}

func TestSortKey_U_Used(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("a", "b"))
	m.Update(key("U"))
	require.Equal(t, SortByUsed, m.sortField)
}

// --- Confirm dialog result tests ---

func TestConfirmResult_Delete(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("target"))
	m.pendingAction = "delete"
	m.configToDelete = &m.configs[0]
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.False(t, m.confirmDialog.Visible)
	require.NotNil(t, cmd)
}

func TestEditBlockedForChartConfig(t *testing.T) {
	m := testModel()
	owned := docker.ConfigWithDecodedData{Config: swarm.Config{
		ID: "id-rel",
		Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{
			Name: "whoami.v1",
			Labels: map[string]string{
				charts.LabelType:    charts.TypeRelease,
				charts.LabelRelease: "whoami",
			},
		}},
	}}
	loadConfigs(m, []docker.ConfigWithDecodedData{owned})

	// Pressing <e> on a chart-owned config opens a dismiss-only info popup
	// explaining why, rather than launching the editor.
	m.Update(key("e"))
	require.True(t, m.confirmDialog.Visible)
	require.True(t, m.confirmDialog.InfoMode)
	require.Contains(t, m.confirmDialog.Message, "chart release")
	require.Contains(t, m.confirmDialog.Message, "charts upgrade")

	// It must render as dismiss-only: the footer reads "Close", not a y/n
	// prompt whose keys do nothing in info mode (PR #415 review feedback).
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	rendered := m.View()
	require.Contains(t, rendered, "Close")
	require.NotContains(t, rendered, "Yes")

	// Dismissing it clears the info mode so a later confirm dialog still works.
	m.Update(confirmdialog.ResultMsg{Confirmed: false})
	require.False(t, m.confirmDialog.InfoMode)
}

func TestDeleteConfirmPrompt(t *testing.T) {
	plain := &docker.ConfigWithDecodedData{Config: swarm.Config{Spec: swarm.ConfigSpec{
		Annotations: swarm.Annotations{Name: "plain"},
	}}}
	require.Equal(t, "Delete config plain?", deleteConfirmPrompt("plain", plain))

	// nil config falls back to the plain prompt.
	require.Equal(t, "Delete config gone?", deleteConfirmPrompt("gone", nil))

	// A config owned by a chart release gets the warning naming the release.
	owned := &docker.ConfigWithDecodedData{Config: swarm.Config{Spec: swarm.ConfigSpec{
		Annotations: swarm.Annotations{Name: "whoami.v1", Labels: map[string]string{
			charts.LabelType:    charts.TypeRelease,
			charts.LabelRelease: "whoami",
		}},
	}}}
	got := deleteConfirmPrompt("whoami.v1", owned)
	require.Contains(t, got, "chart release \"whoami\"")
	require.Contains(t, got, "charts uninstall")
}

func TestConfirmResult_Cancelled(t *testing.T) {
	m := testModel()
	m.pendingAction = "delete"
	m.confirmDialog.Visible = true
	m.Update(confirmdialog.ResultMsg{Confirmed: false})
	require.False(t, m.confirmDialog.Visible)
	require.Equal(t, "", m.pendingAction)
}

// --- Create dialog key tests ---

func TestCreateDialog_Source_Toggle(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "source"
	m.createConfigSource = "file"
	m.Update(key("down"))
	require.Equal(t, "inline", m.createConfigSource)
	m.Update(key("up"))
	require.Equal(t, "file", m.createConfigSource)
}

func TestCreateDialog_Source_Enter_File(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "source"
	m.createConfigSource = "file"
	m.Update(key("enter"))
	require.Equal(t, "details-file", m.createDialogStep)
}

func TestCreateDialog_Source_Enter_Inline(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "source"
	m.createConfigSource = "inline"
	m.Update(key("enter"))
	require.Equal(t, "details-inline", m.createDialogStep)
}

func TestCreateDialog_Source_Esc(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "source"
	m.Update(key("esc"))
	require.False(t, m.createDialogActive)
}

func TestCreateDialog_DetailsFile_Tab(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createInputFocus = 0
	m.Update(key("tab"))
	require.Equal(t, 1, m.createInputFocus)
}

func TestCreateDialog_DetailsFile_Esc(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.Update(key("esc"))
	require.False(t, m.createDialogActive)
}

func TestCreateDialog_DetailsFile_EnterEmptyName(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createNameInput.SetValue("")
	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "name")
}

func TestCreateDialog_DetailsInline_Esc(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.Update(key("esc"))
	require.False(t, m.createDialogActive)
}

func TestCreateDialog_DetailsInline_Tab(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createInputFocus = 0
	m.Update(key("tab"))
	require.Equal(t, 1, m.createInputFocus)
}

// --- Used-by view key tests ---

func TestUsedByView_Esc_Returns(t *testing.T) {
	m := testModel()
	m.usedByViewActive = true
	m.usedByList.Viewport.Width = 80
	m.usedByList.Viewport.Height = 20
	m.Update(key("esc"))
	require.False(t, m.usedByViewActive)
}

// --- File browser key tests ---

func TestFileBrowser_Esc(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.Update(key("esc"))
	require.False(t, m.fileBrowserActive)
	require.True(t, m.createDialogActive)
}

func TestFileBrowser_UpDown(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserFiles = []string{"..", "file1.txt", "file2.txt"}
	m.fileBrowserCursor = 0
	m.Update(key("down"))
	require.Equal(t, 1, m.fileBrowserCursor)
	m.Update(key("up"))
	require.Equal(t, 0, m.fileBrowserCursor)
}

// --- #525: a printable character must never be a hotkey over a focused input ---

func configFileDialog(t *testing.T) *Model {
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
	m := configFileDialog(t)
	m.createFileInput.SetValue("~/config/")
	m.Update(key("f"))
	m.Update(key("F"))
	require.Equal(t, "~/config/fF", m.createFileInput.Value())
	require.False(t, m.fileBrowserActive, "f must not open the browser")
}

func TestCreateDialog_DetailsFile_BrowseKey_OpensFileBrowser(t *testing.T) {
	m := configFileDialog(t)
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

// The labels field is reachable in details-file — picking a file with an
// invalid labels value lands there — and "f" used to be inserted into the path
// the browser had just filled in, silently corrupting it.
func TestCreateDialog_DetailsFile_LabelsFocus_KeepsPathIntact(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createNameInput.SetValue("myconfig")
	m.createLabelsInput.SetValue("bad-format")

	// Pick a file in the browser: labels fail to parse, so focus lands on them.
	// Opening the browser hides the dialog, which is what routes keys to it.
	m.createDialogActive = false
	m.fileBrowserActive = true
	m.fileBrowserFiles = []string{"/tmp/app.conf"}
	m.fileBrowserCursor = 0
	m.Update(key("enter"))
	require.Equal(t, 2, m.createInputFocus, "a labels parse failure must focus the labels field")
	require.Equal(t, "/tmp/app.conf", m.createFileInput.Value())

	m.Update(key("f"))
	require.Equal(t, "bad-formatf", m.createLabelsInput.Value(), "f must reach the labels field")
	require.Equal(t, "/tmp/app.conf", m.createFileInput.Value(), "the chosen path must not be touched")
}

func TestCreateDialog_DetailsFile_RoutesEveryKeyToFocusedInput(t *testing.T) {
	for _, k := range []string{"f", "F", "e", "x", " "} {
		t.Run(k, func(t *testing.T) {
			for _, tc := range []struct {
				focus int
				value func(*Model) string
			}{
				{0, func(m *Model) string { return m.createNameInput.Value() }},
				{1, func(m *Model) string { return m.createFileInput.Value() }},
				{2, func(m *Model) string { return m.createLabelsInput.Value() }},
			} {
				m := testModel()
				m.createDialogActive = true
				m.createDialogStep = "details-file"
				m.createInputFocus = tc.focus
				switch tc.focus {
				case 0:
					m.createNameInput.Focus()
				case 1:
					m.createFileInput.Focus()
				case 2:
					m.createLabelsInput.Focus()
				}
				m.Update(key(k))
				require.Equal(t, k, tc.value(m), "focus %d must receive %q", tc.focus, k)
			}
		})
	}
}

func TestCreateDialog_DetailsInline_E_TypesIntoFocusedInput(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createInputFocus = 2
	m.createLabelsInput.Focus()
	m.Update(key("e"))
	require.Equal(t, "e", m.createLabelsInput.Value())
	require.True(t, m.createDialogActive, "e at the labels field must not open the editor")
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
	_, err := parseLabels("bad-format")
	require.Error(t, err)
}

// --- Help content ---

func TestTickMsg_WhenPollingInFlight_SkipsCheck(t *testing.T) {
	m := testModel()
	loadConfigs(m, fakeConfigs("c1"))
	m.visible = true
	m.polling.Store(true)
	cmd := m.Update(TickMsg{Gen: m.pollGen})
	require.NotNil(t, cmd)
	// With polling in flight the TickMsg handler should only return tickCmd,
	// not batch(checkConfigsCmd, tickCmd). Executing the returned cmd should
	// produce a TickMsg (from tickCmd), not a PollRetryMsg or configsLoadedMsg.
	msg := runCmd(cmd)
	_, isTickMsg := msg.(TickMsg)
	require.True(t, isTickMsg, "expected TickMsg from tickCmd, got %T", msg)
}

func TestGetConfigsHelpContent(t *testing.T) {
	cats := GetConfigsHelpContent()
	require.True(t, len(cats) >= 3)
	require.Equal(t, "General", cats[0].Title)
	require.Equal(t, "View", cats[1].Title)
	require.Equal(t, "Navigation", cats[2].Title)
}

// --- Row clicks ---

func TestClickRow_SelectsTheLine(t *testing.T) {
	m := testModel()
	readyConfigs(m, 120, "a", "b", "c", "d")
	require.True(t, m.ClickRow(2))
	require.Equal(t, "c", m.selectedConfig())
}

func TestClickRow_CountsFromTheScrolledWindow(t *testing.T) {
	m := testModel()
	readyConfigs(m, 120, "a", "b", "c", "d")
	m.configsList.Viewport.YOffset = 1
	require.True(t, m.ClickRow(1))
	require.Equal(t, "c", m.selectedConfig())
}

func TestClickRow_Filtered(t *testing.T) {
	m := testModel()
	readyConfigs(m, 120, "db-1", "web-1", "web-2")
	m.ApplySearchQuery("web")
	require.True(t, m.ClickRow(1))
	require.Equal(t, "web-2", m.selectedConfig())
}

func TestClickRow_PastTheLastRow(t *testing.T) {
	m := testModel()
	readyConfigs(m, 120, "a", "b", "c")
	m.Update(key("down"))
	require.False(t, m.ClickRow(3))
	require.Equal(t, "b", m.selectedConfig())
}

func TestClickRow_ResetsTheColumnScroll(t *testing.T) {
	m := testModel()
	readyConfigs(m, 60, longConfigName, "tls")
	m.Update(key("right"))
	m.Update(key("right"))
	scrolled := m.configsList.RenderItem(m.configsList.Filtered[0], true, 0)

	require.True(t, m.ClickRow(1))
	require.True(t, m.ClickRow(0))

	require.NotEqual(t, scrolled, m.configsList.RenderItem(m.configsList.Filtered[0], true, 0),
		"a click that moves the cursor resets the scroll offset, as the cursor keys do")
}

// While the UsedBy list is open it is what is on screen, so the click lands
// there, and Enter then opens the stack that was clicked.
func TestClickRow_UsedByList(t *testing.T) {
	m := testModel()
	readyConfigs(m, 120, "a", "b")
	m.usedByViewActive = true
	m.usedByConfigName = "a"
	m.usedByList.Items = []usedByItem{{StackName: "stack1", ServiceName: "svc1"}, {StackName: "stack2", ServiceName: "svc2"}}
	m.usedByList.Filtered = m.usedByList.Items

	require.True(t, m.ClickRow(1))
	require.Equal(t, 1, m.usedByList.Cursor)
	require.Equal(t, "a", m.selectedConfig(), "the main list stays where it was")

	nav, ok := runCmd(m.Update(key("enter"))).(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "stack2", nav.Payload.(map[string]interface{})["stackName"])
}
