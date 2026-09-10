// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/swarm"

	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// --- State machine tests ---

func TestUpdate_Msg_SetsVisibleAndStacks(t *testing.T) {
	m := testModel()
	m.Update(Msg{NodeID: "n1", Stacks: fakeStacks("alpha", "beta")})
	require.True(t, m.Visible)
	require.Len(t, m.List.Items, 2)
	require.Len(t, m.List.Filtered, 2)
}

func TestUpdate_Msg_LaunchesTaskFetches(t *testing.T) {
	fetched := map[string]bool{}
	taskMock := &mockTaskOps{
		getTasksForStackFn: func(name string) ([]docker.TaskEntry, error) {
			fetched[name] = true
			return nil, nil
		},
		getTasksForServiceFn: func(_ string) ([]docker.TaskEntry, error) { return nil, nil },
	}
	m := testModel(func(m *Model) { m.deps.Tasks = taskMock })
	cmd := m.Update(Msg{NodeID: "n1", Stacks: fakeStacks("s1", "s2")})
	require.NotNil(t, cmd)
	// The batched commands include tickCmd + task fetch cmds.
	// We only need to verify the mock was called; the tick produces a time.Timer
	// that we skip. Walk the batch and run only non-tick commands.
	batchMsg := cmd()
	if batch, ok := batchMsg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			// Run the cmd; tick cmds block on time.After, so use a goroutine + timeout
			done := make(chan struct{})
			go func() {
				defer close(done)
				c()
			}()
			select {
			case <-done:
			case <-time.After(50 * time.Millisecond):
				// tick cmd — skip it
			}
		}
	}
	require.True(t, fetched["s1"])
	require.True(t, fetched["s2"])
}

func TestUpdate_TickMsg_WhenVisible(t *testing.T) {
	m := testModel()
	m.Visible = true
	cmd := m.Update(TickMsg{Gen: m.pollGen})
	require.NotNil(t, cmd)
}

func TestUpdate_TickMsg_WhenNotVisible(t *testing.T) {
	m := testModel()
	m.Visible = false
	cmd := m.Update(TickMsg{Gen: m.pollGen})
	// Should still return tick cmd for polling
	require.NotNil(t, cmd)
}

func TestUpdate_RefreshErrorMsg_SetsContent(t *testing.T) {
	m := testModel()
	m.Update(RefreshErrorMsg{Err: fmt.Errorf("timeout")})
	require.True(t, m.Visible)
}

func TestUpdate_StackTasksLoadedMsg_CachesTasks(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("mystack"))
	tasks := []docker.TaskEntry{
		{Name: "mystack_web.1", NodeName: "node1"},
	}
	m.Update(StackTasksLoadedMsg{StackName: "mystack", Tasks: tasks})
	require.Len(t, m.stackTasks["mystack"], 1)
}

func TestUpdate_StackTasksLoadedMsg_ErrorCachesEmpty(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("mystack"))
	m.Update(StackTasksLoadedMsg{StackName: "mystack", Error: fmt.Errorf("fail")})
	require.Empty(t, m.stackTasks["mystack"])
}

func TestUpdate_WindowSizeMsg(t *testing.T) {
	m := testModel()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	require.Equal(t, 120, m.width)
	require.Equal(t, 40, m.height)
	require.True(t, m.ready)
}

func TestUpdate_RemoveErrorMsg_ShowsError(t *testing.T) {
	m := testModel()
	m.Update(RemoveErrorMsg{StackName: "bad", Error: fmt.Errorf("permission denied")})
	require.True(t, m.confirmDialog.Visible)
	require.True(t, m.confirmDialog.ErrorMode)
	require.Contains(t, m.confirmDialog.Message, "bad")
}

func TestUpdate_StackUpdateErrorMsg_ShowsError(t *testing.T) {
	m := testModel()
	m.Update(stackUpdateErrorMsg{StackName: "mystack", Err: fmt.Errorf("invalid yaml")})
	require.True(t, m.confirmDialog.Visible)
	require.True(t, m.confirmDialog.ErrorMode)
	require.Contains(t, m.confirmDialog.Message, "mystack")
}

func TestUpdate_StackCreateErrorMsg_ReturnsToDialog(t *testing.T) {
	m := testModel()
	m.createFileInput.SetValue("somefile.yml")
	m.Update(stackCreateErrorMsg{Err: fmt.Errorf("deployment failed")})
	require.True(t, m.createDialogActive)
	require.Equal(t, "details-file", m.createDialogStep)
	require.Contains(t, m.createDialogError, "deployment failed")
}

func TestUpdate_StackCreateErrorMsg_InlineMode(t *testing.T) {
	m := testModel()
	// No file input value → returns to inline step
	m.Update(stackCreateErrorMsg{Err: fmt.Errorf("bad yaml")})
	require.True(t, m.createDialogActive)
	require.Equal(t, "details-inline", m.createDialogStep)
}

func TestUpdate_EditorContentMsg_CreateMode(t *testing.T) {
	m := testModel()
	m.editStackName = "" // create mode
	m.Update(editorContentMsg{Content: "version: '3'\nservices:\n  web:\n    image: nginx"})
	require.True(t, m.createDialogActive)
	require.Equal(t, "details-inline", m.createDialogStep)
	require.Contains(t, m.createDialogContent, "nginx")
}

func TestUpdate_EditorContentMsg_EditMode(t *testing.T) {
	deployed := ""
	stackMock := noopStackOps()
	stackMock.deployStackFn = func(name string, content string) error {
		deployed = name
		return nil
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	m.editStackName = "mystack"
	fastSpinner(t)
	cmd := m.Update(editorContentMsg{Content: "version: '3'", OriginalContent: "version: '2'"})
	require.Equal(t, "", m.editStackName) // cleared
	require.NotNil(t, cmd)
	runBatch(cmd)
	require.Equal(t, "mystack", deployed)
}

func TestUpdate_EditorContentMsg_EditMode_NoChange(t *testing.T) {
	deployed := false
	stackMock := noopStackOps()
	stackMock.deployStackFn = func(name string, content string) error {
		deployed = true
		return nil
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	m.editStackName = "mystack"
	yaml := "version: '3'\nservices:\n  web:\n    image: nginx"
	cmd := m.Update(editorContentMsg{Content: yaml, OriginalContent: yaml})
	require.Equal(t, "", m.editStackName) // cleared
	require.Nil(t, cmd)                   // no redeploy
	require.False(t, deployed)
}

func TestUpdate_FilesLoadedMsg_Success(t *testing.T) {
	m := testModel()
	m.Update(filesLoadedMsg{Path: "/tmp", Files: []string{"..", "foo.yml"}})
	require.True(t, m.fileBrowserActive)
	require.Equal(t, "/tmp", m.fileBrowserPath)
	require.Len(t, m.fileBrowserFiles, 2)
}

func TestUpdate_FilesLoadedMsg_Error(t *testing.T) {
	m := testModel()
	m.Update(filesLoadedMsg{Path: "/nope", Error: fmt.Errorf("no access")})
	require.False(t, m.fileBrowserActive)
	require.True(t, m.createDialogActive)
	require.Contains(t, m.createDialogError, "no access")
}

// --- Key routing tests ---

func TestKey_N_OpensCreateDialog(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.Update(key("n"))
	require.True(t, m.createDialogActive)
	require.Equal(t, "source", m.createDialogStep)
}

func TestKey_C_OpensCreateDialog(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.Update(key("c"))
	require.True(t, m.createDialogActive)
	require.Equal(t, "source", m.createDialogStep)
}

func TestKey_CtrlD_StackDeleteIntent(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("mystack"))
	// ctrl+d dispatches an async chart-ownership check; the dialog is not opened
	// until the resulting StackDeleteIntentMsg is handled.
	cmd := m.Update(key("ctrl+d"))
	require.NotNil(t, cmd)
	require.False(t, m.confirmDialog.Visible)

	// A plain stack gets the generic remove confirmation.
	m.Update(StackDeleteIntentMsg{StackName: "mystack"})
	require.True(t, m.confirmDialog.Visible)
	require.Equal(t, "remove", m.pendingAction)
	require.Contains(t, m.confirmDialog.Message, "mystack")

	// A chart-managed stack warns about the release and points at charts uninstall.
	m.Update(StackDeleteIntentMsg{StackName: "whoami", ChartRelease: "whoami"})
	require.Contains(t, m.confirmDialog.Message, "chart release")
	require.Contains(t, m.confirmDialog.Message, "charts uninstall")
}

// "g" is the way into the charts browser from a stack the operator is already
// looking at. It reuses the same ownership lookup the delete guard makes.
func TestKey_G_JumpsToTheOwningChartRelease(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("whoami"))

	// The lookup is async: nothing navigates until the result arrives.
	require.NotNil(t, m.Update(key("g")))

	msg := runCmd(m.Update(ChartJumpMsg{StackName: "whoami", ChartRelease: "whoami"}))
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok, "expected navigation, got %T", msg)
	require.Equal(t, view.NameCharts, nav.ViewName)
	require.Equal(t, map[string]any{"release": "whoami"}, nav.Payload)
}

// A stack nothing owns must say so rather than opening an empty browser.
func TestKey_G_SaysWhenAStackIsNotChartManaged(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("plain"))

	require.Nil(t, m.Update(ChartJumpMsg{StackName: "plain"}), "no navigation")
	require.Contains(t, m.toastMessage, "not managed by a chart release")
	require.Contains(t, m.toastMessage, "plain")
}

func TestKey_P_TogglesExpand(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.Update(key("p"))
	require.True(t, m.expandedStacks["s1"])
	// Toggle off
	m.Update(key("p"))
	require.False(t, m.expandedStacks["s1"])
}

func TestKey_Enter_NavigatesToServices(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("mystack"))
	cmd := m.Update(key("enter"))
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "services", nav.ViewName)
}

func TestKey_I_Inspect(t *testing.T) {
	inspected := ""
	stackMock := noopStackOps()
	stackMock.inspectStackFn = func(name string) (string, error) {
		inspected = name
		return "yaml-content", nil
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	loadStacks(m, fakeStacks("mystack"))
	cmd := m.Update(key("i"))
	msg := runCmd(cmd)
	require.Equal(t, "mystack", inspected)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	require.Equal(t, "inspect", nav.ViewName)
}

func TestKey_I_InspectError(t *testing.T) {
	stackMock := noopStackOps()
	stackMock.inspectStackFn = func(_ string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	loadStacks(m, fakeStacks("mystack"))
	cmd := m.Update(key("i"))
	msg := runCmd(cmd)
	nav, ok := msg.(view.NavigateToMsg)
	require.True(t, ok)
	payload := nav.Payload.(map[string]any)
	require.Contains(t, payload["title"].(string), "inspect failed")
}

// The "?" key is routed by the app, not by this view — see app.Model.openHelp
// and its tests. What the view still owns is the content the app asks it for.
func TestHelpContent(t *testing.T) {
	m := testModel()
	require.NotEmpty(t, m.HelpContent())
}

func TestKey_Esc_ClearsFilter(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("alpha", "beta"))
	m.List.Query = "alpha"
	m.Update(key("esc"))
	require.Equal(t, "", m.List.Query)
}

// --- Sort key tests ---

func TestSortKey_S_Name(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("beta", "alpha"))
	// Default is SortByName+asc; first S toggles to desc
	m.Update(key("S"))
	require.Equal(t, SortByName, m.sortField)
	require.True(t, m.userSetSort)
	require.False(t, m.sortAscending)
	// Press again to toggle back to ascending
	m.Update(key("S"))
	require.True(t, m.sortAscending)
}

func TestSortKey_E_Services(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("a", "b"))
	m.Update(key("E"))
	require.Equal(t, SortByServices, m.sortField)
}

func TestSortKey_T_Tasks(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("a", "b"))
	m.Update(key("T"))
	require.Equal(t, SortByTasks, m.sortField)
}

func TestSortKey_R_Error(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("a", "b"))
	m.Update(key("R"))
	require.Equal(t, SortByError, m.sortField)
}

// --- Confirm dialog result tests ---

func TestConfirmResult_Remove(t *testing.T) {
	removed := ""
	stackMock := noopStackOps()
	stackMock.removeStackFn = func(_ context.Context, name string) error {
		removed = name
		return nil
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	loadStacks(m, fakeStacks("target"))
	m.pendingAction = "remove"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.False(t, m.confirmDialog.Visible)
	runCmd(cmd)
	require.Equal(t, "target", removed)
}

func TestConfirmResult_RemoveWithNetworks(t *testing.T) {
	removedNetworks := ""
	stackMock := noopStackOps()
	stackMock.removeStackFn = func(_ context.Context, _ string) error { return nil }
	stackMock.removeStackNetworksFn = func(_ context.Context, name string) error {
		removedNetworks = name
		return nil
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	loadStacks(m, fakeStacks("target"))
	m.pendingAction = "remove"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true, CheckboxChecked: true})
	runCmd(cmd)
	require.Equal(t, "target", removedNetworks)
}

func TestConfirmResult_Cancelled(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.pendingAction = "remove"
	m.confirmDialog.Visible = true
	m.Update(confirmdialog.ResultMsg{Confirmed: false})
	require.False(t, m.confirmDialog.Visible)
	require.Equal(t, "", m.pendingAction)
}

// --- Create dialog tests ---

func TestCreateDialog_Source_Toggle(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "source"
	m.createStackSource = "file"
	m.Update(key("down"))
	require.Equal(t, "inline", m.createStackSource)
	m.Update(key("up"))
	require.Equal(t, "file", m.createStackSource)
}

func TestCreateDialog_Source_Enter_File(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "source"
	m.createStackSource = "file"
	m.Update(key("enter"))
	require.Equal(t, "details-file", m.createDialogStep)
}

func TestCreateDialog_Source_Enter_Inline(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "source"
	m.createStackSource = "inline"
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
	m.Update(key("tab"))
	require.Equal(t, 0, m.createInputFocus)
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
	require.Contains(t, m.createDialogError, "Stack name cannot be empty")
}

func TestCreateDialog_DetailsFile_EnterEmptyPath(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createNameInput.SetValue("mystack")
	m.createFileInput.SetValue("")
	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "file path")
}

// --- #525: a printable character must never be a hotkey over a focused input ---

// createFileDialog opens the create dialog on details-file with the file input
// focused, the way tabbing there leaves it.
func createFileDialog(t *testing.T) *Model {
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
	m := createFileDialog(t)
	m.createFileInput.SetValue("/etc/docker/certs.d/")
	m.Update(key("f"))
	require.Equal(t, "/etc/docker/certs.d/f", m.createFileInput.Value())
	require.False(t, m.fileBrowserActive, "f must not open the browser")
	require.True(t, m.createDialogActive)
}

func TestCreateDialog_DetailsFile_ShiftF_TypesIntoPath(t *testing.T) {
	m := createFileDialog(t)
	m.Update(key("F"))
	require.Equal(t, "F", m.createFileInput.Value())
	require.False(t, m.fileBrowserActive)
}

func TestCreateDialog_DetailsFile_BrowseKey_OpensFileBrowser(t *testing.T) {
	m := createFileDialog(t)
	cmd := m.Update(key("ctrl+o"))
	require.False(t, m.createDialogActive)
	require.True(t, m.fileBrowserActive)
	require.Equal(t, "create", m.fileBrowserContext)
	require.NotNil(t, cmd)
}

// The chord is unambiguous — there is one path input — so it browses from the
// name field too, where the old "f" hotkey did nothing.
func TestCreateDialog_DetailsFile_BrowseKey_FromNameFocus(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createInputFocus = 0
	m.createNameInput.Focus()
	cmd := m.Update(key("ctrl+o"))
	require.True(t, m.fileBrowserActive)
	require.NotNil(t, cmd)
}

func TestCreateDialog_DetailsFile_RoutesEveryKeyToFocusedInput(t *testing.T) {
	for _, k := range []string{"f", "F", "e", "x", " "} {
		t.Run(k, func(t *testing.T) {
			m := testModel()
			m.createDialogActive = true
			m.createDialogStep = "details-file"
			m.createInputFocus = 0
			m.createNameInput.Focus()

			m.Update(key(k))
			require.Equal(t, k, m.createNameInput.Value(), "focus 0 must type into the name")
			require.Empty(t, m.createFileInput.Value(), "focus 0 must not touch the path")

			m.Update(key("tab"))
			m.Update(key(k))
			require.Equal(t, k, m.createFileInput.Value(), "focus 1 must type into the path")
			require.Equal(t, k, m.createNameInput.Value(), "focus 1 must not touch the name")
		})
	}
}

func TestCreateDialog_DetailsInline_E_TypesIntoName(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createInputFocus = 0
	m.createNameInput.Focus()
	m.Update(key("e"))
	require.Equal(t, "e", m.createNameInput.Value())
	require.True(t, m.createDialogActive, "e at the name field must not open the editor")
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

func TestCreateDialog_DetailsInline_EnterEmptyName(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createNameInput.SetValue("")
	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "Stack name cannot be empty")
}

func TestCreateDialog_DetailsInline_EnterNoContent(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createNameInput.SetValue("mystack")
	m.createDialogContent = ""
	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "YAML content")
}

func TestCreateDialog_DetailsInline_EnterValidation(t *testing.T) {
	stackMock := noopStackOps()
	stackMock.validateStackYAMLFn = func(_ string) error {
		return fmt.Errorf("invalid services")
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createNameInput.SetValue("mystack")
	m.createDialogContent = "bad yaml"
	m.Update(key("enter"))
	require.Contains(t, m.createDialogError, "invalid services")
}

func TestCreateDialog_DetailsInline_EnterDeploys(t *testing.T) {
	deployed := ""
	stackMock := noopStackOps()
	stackMock.validateStackYAMLFn = func(_ string) error { return nil }
	stackMock.deployStackFn = func(name string, _ string) error {
		deployed = name
		return nil
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createNameInput.SetValue("mystack")
	m.createDialogContent = "version: '3'\nservices:\n  web:\n    image: nginx"
	fastSpinner(t)
	cmd := m.Update(key("enter"))
	require.False(t, m.createDialogActive)
	runBatch(cmd)
	require.Equal(t, "mystack", deployed)
}

func TestCreateDialog_EnterWithError_ClearsError(t *testing.T) {
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createDialogError = "previous error"
	m.Update(key("enter"))
	require.Equal(t, "", m.createDialogError)
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
	m.fileBrowserFiles = []string{"..", "file1.yml", "file2.yml"}
	m.fileBrowserCursor = 0
	m.Update(key("down"))
	require.Equal(t, 1, m.fileBrowserCursor)
	m.Update(key("up"))
	require.Equal(t, 0, m.fileBrowserCursor)
	// Don't go below 0
	m.Update(key("up"))
	require.Equal(t, 0, m.fileBrowserCursor)
}

func TestFileBrowser_PgUpDown(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	files := make([]string, 20)
	for i := range files {
		files[i] = fmt.Sprintf("file%d.yml", i)
	}
	m.fileBrowserFiles = files
	m.fileBrowserCursor = 0
	m.Update(key("pgdown"))
	require.Equal(t, 10, m.fileBrowserCursor)
	m.Update(key("pgup"))
	require.Equal(t, 0, m.fileBrowserCursor)
}

func TestFileBrowser_Enter_ParentDir(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserFiles = []string{".."}
	m.fileBrowserPath = "/home/user"
	m.fileBrowserCursor = 0
	cmd := m.Update(key("enter"))
	// Should return a loadFilesCmd for parent dir
	require.NotNil(t, cmd)
}

func TestFileBrowser_Enter_Directory(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserFiles = []string{"/tmp/subdir/"}
	m.fileBrowserCursor = 0
	cmd := m.Update(key("enter"))
	require.NotNil(t, cmd)
}

func TestRemoveStack_Timeout_ReturnsRemoveError(t *testing.T) {
	stackMock := noopStackOps()
	stackMock.removeStackFn = func(_ context.Context, _ string) error {
		return context.DeadlineExceeded
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	loadStacks(m, fakeStacks("target"))
	m.pendingAction = "remove"
	m.confirmDialog.Visible = true
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.NotNil(t, cmd)
	msg := runCmd(cmd)
	_, ok := msg.(RemoveErrorMsg)
	require.True(t, ok, "expected RemoveErrorMsg, got %T", msg)
}

// --- Task navigation tests ---

func TestTaskNavigation_DownIntoTasks(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.expandedStacks["s1"] = true
	m.stackTasks["s1"] = []docker.TaskEntry{
		{Name: "s1_web.1"}, {Name: "s1_web.2"},
	}
	m.selectedTaskIndex = -1
	m.Update(key("down"))
	require.Equal(t, 0, m.selectedTaskIndex)
	m.Update(key("down"))
	require.Equal(t, 1, m.selectedTaskIndex)
}

func TestTaskNavigation_UpFromTasks(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.expandedStacks["s1"] = true
	m.stackTasks["s1"] = []docker.TaskEntry{{Name: "s1_web.1"}}
	m.selectedTaskIndex = 0
	m.Update(key("up"))
	require.Equal(t, -1, m.selectedTaskIndex)
}

// --- Horizontal scroll tests ---

func TestHorizontalScroll_Right(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.stackTasks["s1"] = []docker.TaskEntry{
		{DesiredState: "running", Error: "something went wrong"},
	}
	m.Update(key("right"))
	require.Equal(t, 5, m.errorScrollOffset)
}

func TestHorizontalScroll_Left(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("s1"))
	m.errorScrollOffset = 10
	m.Update(key("left"))
	require.Equal(t, 5, m.errorScrollOffset)
}

// --- Save dialog tests ---

func TestKey_S_OpensSaveDialog(t *testing.T) {
	m := testModel()
	loadStacks(m, fakeStacks("mystack"))
	m.Update(key("s"))
	require.True(t, m.saveDialogActive)
	require.Equal(t, "mystack", m.saveStackName)
	require.Equal(t, "mystack.yml", m.saveFileInput.Value())
}

func TestKey_S_NoStacks_NoDialog(t *testing.T) {
	m := testModel()
	m.Update(key("s"))
	require.False(t, m.saveDialogActive)
}

func TestSaveDialog_Esc_Closes(t *testing.T) {
	m := testModel()
	m.saveDialogActive = true
	m.saveStackName = "mystack"
	m.saveDialogError = "some error"
	m.Update(key("esc"))
	require.False(t, m.saveDialogActive)
	require.Equal(t, "", m.saveDialogError)
}

func TestSaveDialog_Enter_EmptyPath(t *testing.T) {
	m := testModel()
	m.saveDialogActive = true
	m.saveStackName = "mystack"
	m.saveFileInput.SetValue("")
	m.Update(key("enter"))
	require.Contains(t, m.saveDialogError, "File path cannot be empty")
	require.True(t, m.saveDialogActive)
}

func TestSaveDialog_EnterWithError_ClearsError(t *testing.T) {
	m := testModel()
	m.saveDialogActive = true
	m.saveDialogError = "previous error"
	m.Update(key("enter"))
	require.Equal(t, "", m.saveDialogError)
	require.True(t, m.saveDialogActive)
}

func TestSaveDialog_BrowseKey_OpensFileBrowser(t *testing.T) {
	m := testModel()
	m.saveDialogActive = true
	m.saveStackName = "mystack"
	m.saveFileInput.SetValue("mystack.yml")
	cmd := m.Update(key("ctrl+o"))
	require.False(t, m.saveDialogActive)
	require.True(t, m.fileBrowserActive)
	require.Equal(t, "save", m.fileBrowserContext)
	require.NotNil(t, cmd)
}

// The save dialog never had a focus guard at all, so "f" was unreachable in its
// only field — and its placeholder ("./my-stack.yml") invites relative paths.
func TestSaveDialog_F_TypesIntoPath(t *testing.T) {
	m := testModel()
	m.saveDialogActive = true
	m.saveStackName = "mystack"
	m.saveFileInput.Focus()
	m.saveFileInput.SetValue("./con")
	m.Update(key("f"))
	m.Update(key("F"))
	require.Equal(t, "./confF", m.saveFileInput.Value())
	require.True(t, m.saveDialogActive)
	require.False(t, m.fileBrowserActive)
}

func TestStackSavedMsg_ShowsSuccess(t *testing.T) {
	m := testModel()
	m.saveDialogActive = true
	m.Update(stackSavedMsg{Path: "/tmp/mystack.yml"})
	require.False(t, m.saveDialogActive)
	require.True(t, m.confirmDialog.Visible)
	require.True(t, m.confirmDialog.InfoMode)
	require.False(t, m.confirmDialog.ErrorMode)
	require.Contains(t, m.confirmDialog.Message, "/tmp/mystack.yml")
}

func TestStackSaveErrorMsg_ReturnsToDialog(t *testing.T) {
	m := testModel()
	m.Update(stackSaveErrorMsg{Err: fmt.Errorf("permission denied")})
	require.True(t, m.saveDialogActive)
	require.Contains(t, m.saveDialogError, "permission denied")
}

func TestConfirmResult_SaveOverwrite_Confirmed(t *testing.T) {
	reconstructed := false
	stackMock := noopStackOps()
	stackMock.reconstructStackComposeFn = func(_ string) (string, error) {
		reconstructed = true
		return "version: '3'", nil
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	loadStacks(m, fakeStacks("mystack"))
	m.pendingAction = "save-overwrite"
	m.confirmDialog.Visible = true
	m.saveStackName = "mystack"
	m.saveFileInput.SetValue("/tmp/mystack.yml")
	cmd := m.Update(confirmdialog.ResultMsg{Confirmed: true})
	require.False(t, m.confirmDialog.Visible)
	require.Equal(t, "", m.pendingAction)
	require.NotNil(t, cmd)
	runCmd(cmd)
	require.True(t, reconstructed)
}

func TestConfirmResult_SaveOverwrite_Cancelled(t *testing.T) {
	m := testModel()
	m.pendingAction = "save-overwrite"
	m.confirmDialog.Visible = true
	m.saveStackName = "mystack"
	m.Update(confirmdialog.ResultMsg{Confirmed: false})
	require.False(t, m.confirmDialog.Visible)
	require.True(t, m.saveDialogActive)
	require.Equal(t, "", m.pendingAction)
}

func TestFileBrowser_Esc_ReturnsSaveDialog(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserContext = "save"
	m.fileBrowserPath = "/tmp"
	m.saveFileInput.SetValue("mystack.yml")
	m.saveStackName = "mystack"
	m.Update(key("esc"))
	require.False(t, m.fileBrowserActive)
	require.True(t, m.saveDialogActive)
	require.Equal(t, "/tmp/mystack.yml", m.saveFileInput.Value())
}

func TestFileBrowser_Esc_ReturnsCreateDialog(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserContext = "create"
	m.Update(key("esc"))
	require.False(t, m.fileBrowserActive)
	require.True(t, m.createDialogActive)
}

func TestSaveStackToFileCmd_Success(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := tmpDir + "/test-stack.yml"

	stackMock := noopStackOps()
	stackMock.reconstructStackComposeFn = func(_ string) (string, error) {
		return "version: '3'\nservices:\n  web:\n    image: nginx\n", nil
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	cmd := m.saveStackToFileCmd("mystack", filePath)
	msg := runCmd(cmd)
	saved, ok := msg.(stackSavedMsg)
	require.True(t, ok)
	require.Equal(t, filePath, saved.Path)

	// Verify file was written
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Contains(t, string(data), "nginx")
}

func TestSaveStackToFileCmd_ReconstructError(t *testing.T) {
	stackMock := noopStackOps()
	stackMock.reconstructStackComposeFn = func(_ string) (string, error) {
		return "", fmt.Errorf("stack not found")
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	cmd := m.saveStackToFileCmd("mystack", "/tmp/test.yml")
	msg := runCmd(cmd)
	errMsg, ok := msg.(stackSaveErrorMsg)
	require.True(t, ok)
	require.Contains(t, errMsg.Err.Error(), "stack not found")
}

func TestSaveStackToFileCmd_WriteError(t *testing.T) {
	stackMock := noopStackOps()
	stackMock.reconstructStackComposeFn = func(_ string) (string, error) {
		return "version: '3'", nil
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	// Use an invalid path to trigger write error
	cmd := m.saveStackToFileCmd("mystack", "/proc/0/nonexistent/test.yml")
	msg := runCmd(cmd)
	errMsg, ok := msg.(stackSaveErrorMsg)
	require.True(t, ok)
	require.Error(t, errMsg.Err)
}

func TestFilesLoadedMsg_Error_SaveContext(t *testing.T) {
	m := testModel()
	m.fileBrowserContext = "save"
	m.Update(filesLoadedMsg{Path: "/nope", Error: fmt.Errorf("no access")})
	require.False(t, m.fileBrowserActive)
	require.True(t, m.saveDialogActive)
	require.Contains(t, m.saveDialogError, "no access")
}

func TestFileBrowser_Enter_SaveHere(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserContext = "save"
	m.fileBrowserPath = "/tmp/mydir"
	m.fileBrowserFiles = []string{"..", saveDirSentinel, "/tmp/mydir/sub/"}
	m.fileBrowserCursor = 1 // [Save here]
	m.saveFileInput.SetValue("mystack.yml")
	m.saveStackName = "mystack"
	m.Update(key("enter"))
	require.False(t, m.fileBrowserActive)
	require.True(t, m.saveDialogActive)
	require.Equal(t, "/tmp/mydir/mystack.yml", m.saveFileInput.Value())
}

func TestFilesLoadedMsg_SaveContext_InjectsSentinel(t *testing.T) {
	m := testModel()
	m.fileBrowserContext = "save"
	m.Update(filesLoadedMsg{Path: "/tmp", Files: []string{"..", "/tmp/sub/", "/tmp/file.yml"}})
	require.True(t, m.fileBrowserActive)
	require.Equal(t, []string{"..", saveDirSentinel, "/tmp/sub/", "/tmp/file.yml"}, m.fileBrowserFiles)
}

func TestFilesLoadedMsg_CreateContext_NoSentinel(t *testing.T) {
	m := testModel()
	m.fileBrowserContext = "create"
	m.Update(filesLoadedMsg{Path: "/tmp", Files: []string{"..", "/tmp/sub/"}})
	require.True(t, m.fileBrowserActive)
	require.Equal(t, []string{"..", "/tmp/sub/"}, m.fileBrowserFiles)
}

func TestFileBrowser_Enter_File_SaveContext(t *testing.T) {
	m := testModel()
	m.fileBrowserActive = true
	m.fileBrowserContext = "save"
	m.fileBrowserFiles = []string{"/tmp/existing-file.yml"}
	m.fileBrowserCursor = 0
	m.saveFileInput.SetValue("mystack.yml")
	m.saveStackName = "mystack"
	m.Update(key("enter"))
	require.False(t, m.fileBrowserActive)
	require.True(t, m.saveDialogActive)
	require.Equal(t, "/tmp/mystack.yml", m.saveFileInput.Value())
}

// --- Deploy progress tests (#444) ---

func TestDeploy_EditMode_SetsDeployingState(t *testing.T) {
	fastSpinner(t)
	m := testModel()
	m.editStackName = "mystack"
	cmd := m.Update(editorContentMsg{Content: "version: '3'", OriginalContent: "version: '2'"})
	require.NotNil(t, cmd)
	require.True(t, m.deploying)
	require.Equal(t, "mystack", m.deployingStack)
	require.False(t, m.deployStartedAt.IsZero())
}

func TestDeploy_CreateInline_SetsDeployingState(t *testing.T) {
	fastSpinner(t)
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createNameInput.SetValue("mystack")
	m.createDialogContent = "version: '3'\nservices:\n  web:\n    image: nginx"
	m.Update(key("enter"))
	require.True(t, m.deploying)
	require.Equal(t, "mystack", m.deployingStack)
}

func TestDeploy_CreateFromFile_SetsDeployingState(t *testing.T) {
	fastSpinner(t)
	composePath := filepath.Join(t.TempDir(), "compose.yml")
	require.NoError(t, os.WriteFile(composePath, []byte("services:\n  web:\n    image: nginx\n"), 0o600))

	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-file"
	m.createNameInput.SetValue("mystack")
	m.createFileInput.SetValue(composePath)
	m.Update(key("enter"))
	require.True(t, m.deploying)
	require.Equal(t, "mystack", m.deployingStack)
}

func TestDeploy_Success_EmitsStackDeployedMsg(t *testing.T) {
	fastSpinner(t)
	m := testModel()
	m.createDialogActive = true
	m.createDialogStep = "details-inline"
	m.createNameInput.SetValue("mystack")
	m.createDialogContent = "version: '3'\nservices:\n  web:\n    image: nginx"
	cmd := m.Update(key("enter"))

	deployed, ok := firstOfType[stackDeployedMsg](runBatch(cmd))
	require.True(t, ok, "deploy cmd must report success with stackDeployedMsg")
	require.Equal(t, "mystack", deployed.StackName)
}

func TestUpdate_StackDeployedMsg_ClearsDeployingAndToasts(t *testing.T) {
	fastSpinner(t)
	m := testModel()
	m.beginDeploy("mystack")
	m.Update(stackDeployedMsg{StackName: "mystack"})
	require.False(t, m.deploying)
	require.Equal(t, "", m.deployingStack)
	require.Contains(t, m.toastMessage, "mystack")
	require.True(t, time.Now().Before(m.toastUntil))
}

// Guards the blank-list regression: the create paths used to return a Msg with
// no Stacks, which emptied the list until the next poll.
func TestUpdate_StackDeployedMsg_ReloadsStacks(t *testing.T) {
	fastSpinner(t)
	snapMock := noopSnapshotOps()
	snap := snapshotWithStacks("alpha", "beta")
	snapMock.getSnapshotFn = func() *docker.SwarmSnapshot { return snap }
	m := testModel(func(m *Model) { m.deps.Snapshot = snapMock })
	m.beginDeploy("alpha")

	cmd := m.Update(stackDeployedMsg{StackName: "alpha"})
	loaded, ok := firstOfType[Msg](runBatch(cmd))
	require.True(t, ok, "deploy success must reload the stack list")
	require.Len(t, loaded.Stacks, 2)
}

func TestUpdate_StackCreateErrorMsg_ClearsDeploying(t *testing.T) {
	m := testModel()
	m.beginDeploy("mystack")
	m.Update(stackCreateErrorMsg{Err: fmt.Errorf("deployment failed")})
	require.False(t, m.deploying)
	require.True(t, m.createDialogActive)
}

func TestUpdate_StackUpdateErrorMsg_ClearsDeploying(t *testing.T) {
	m := testModel()
	m.beginDeploy("mystack")
	m.Update(stackUpdateErrorMsg{StackName: "mystack", Err: fmt.Errorf("invalid yaml")})
	require.False(t, m.deploying)
	require.True(t, m.confirmDialog.Visible)
}

func TestUpdate_SpinnerTickMsg_ReArmsWhileDeploying(t *testing.T) {
	fastSpinner(t)
	m := testModel()
	m.beginDeploy("mystack")
	cmd := m.Update(SpinnerTickMsg(time.Now()))
	require.Equal(t, 1, m.spinner)
	require.NotNil(t, cmd)
}

func TestUpdate_SpinnerTickMsg_StopsWhenIdle(t *testing.T) {
	m := testModel()
	cmd := m.Update(SpinnerTickMsg(time.Now()))
	require.Equal(t, 1, m.spinner)
	require.Nil(t, cmd, "the animation must not re-arm at rest")
}

func TestUpdate_SpinnerTickMsg_ReArmsWhileToastVisible(t *testing.T) {
	fastSpinner(t)
	m := testModel()
	m.showToast("deployed")
	cmd := m.Update(SpinnerTickMsg(time.Now()))
	require.NotNil(t, cmd, "the toast needs redraws to disappear on time")
}

// A deploy raises Docker events, which the app turns into a plain Msg. That must
// not be mistaken for the deploy finishing.
func TestUpdate_Msg_DoesNotClearDeploying(t *testing.T) {
	m := testModel()
	m.beginDeploy("mystack")
	m.Update(Msg{NodeID: "node1", Stacks: fakeStacks("mystack")})
	require.True(t, m.deploying)
	require.Equal(t, "mystack", m.deployingStack)
}

// stackSnapshotWithInitTasks puts one run-to-completion service in a stack, with
// the PR #633 review's task history: newest run in the given state, a failed
// attempt a minute before it.
func stackSnapshotWithInitTasks(newest swarm.TaskState, errText string) *docker.SwarmSnapshot {
	now := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	mk := func(state swarm.TaskState, ts time.Time, msg string) swarm.Task {
		return swarm.Task{
			ServiceID: "svc0", Slot: 1, DesiredState: swarm.TaskStateShutdown,
			Status: swarm.TaskStatus{State: state, Timestamp: ts, Err: msg},
		}
	}
	snap := snapshotWithStacks("mystack")
	snap.Tasks = []swarm.Task{
		mk(newest, now, errText),
		mk(swarm.TaskStateFailed, now.Add(-time.Minute), "task: non-zero exit (1)"),
	}
	return snap
}

func stacksModelWithSnapshot(snap *docker.SwarmSnapshot) *Model {
	return testModel(func(m *Model) {
		ops := noopSnapshotOps()
		ops.getSnapshotFn = func() *docker.SwarmSnapshot { return snap }
		m.deps.Snapshot = ops
	})
}

// The stack badge reads the same rule as the services view's ERROR column, so a
// stack cannot claim to be failing while the only service in it reports a clean
// run (PR #633 review).
func TestStackErrors_CompletedRunClearsTheEarlierFailure(t *testing.T) {
	m := stacksModelWithSnapshot(stackSnapshotWithInitTasks(swarm.TaskStateComplete, ""))
	loadStacks(m, fakeStacks("mystack"))

	require.False(t, m.stackHasError["mystack"], "the stack's only service completed its newest run")
	require.Empty(t, m.stackErrorText["mystack"])
}

// The control: an unsuperseded failure still badges the stack.
func TestStackErrors_NewestFailureIsReported(t *testing.T) {
	m := stacksModelWithSnapshot(stackSnapshotWithInitTasks(swarm.TaskStateFailed, "task: non-zero exit (2)"))
	loadStacks(m, fakeStacks("mystack"))

	require.True(t, m.stackHasError["mystack"])
	require.Equal(t, "task: non-zero exit (2)", m.stackErrorText["mystack"])
}
