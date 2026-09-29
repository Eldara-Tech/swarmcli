// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

//go:build !windows

package stacksview

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/docker"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// scriptedEditor makes $EDITOR a script that appends a line to the file it is
// given, and runs the editor the view starts without a terminal.
func scriptedEditor(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	editor := filepath.Join(dir, "editor")
	require.NoError(t, os.WriteFile(editor, []byte("#!/bin/sh\necho '# edited' >> \"$1\"\n"), 0o700))
	t.Setenv("EDITOR", editor)
	t.Setenv("TMPDIR", dir)
	prev := execProcess
	execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		return func() tea.Msg { return fn(c.Run()) }
	}
	t.Cleanup(func() { execProcess = prev })
}

// An edit redeploys the stack its editor was opened on, even when another
// stack was opened for editing before that editor returned.
func TestAnEditRedeploysTheStackItWasOpenedOn(t *testing.T) {
	scriptedEditor(t)
	fastSpinner(t)
	var deployed []string
	stackMock := noopStackOps()
	stackMock.reconstructStackComposeFn = func(name string) (string, error) {
		return "services:\n  " + name + ":\n    image: nginx\n", nil
	}
	stackMock.deployStackFn = func(name string, _ string, _ docker.DeployOptions) error {
		deployed = append(deployed, name)
		return nil
	}
	m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
	loadStacks(m, fakeStacks("alpha", "beta"))

	m.List.Cursor = 0
	first := m.Update(key("e"))
	m.List.Cursor = 1
	second := m.Update(key("e"))

	runBatch(m.Update(runCmd(first)))
	runBatch(m.Update(runCmd(second)))
	require.Equal(t, []string{"alpha", "beta"}, deployed)
}

// Content a create flow sends to the editor comes back as a new stack's, not as
// an edit, whatever was opened for editing before.
func TestTheCreateEditorReturnsANewStack(t *testing.T) {
	file := filepath.Join(t.TempDir(), "stack.yml")
	require.NoError(t, os.WriteFile(file, []byte("services: {}\n"), 0o600))
	for _, tc := range []struct {
		name string
		open func(m *Model) tea.Cmd
	}{
		{"the create dialog's editor", func(m *Model) tea.Cmd {
			m.createDialogActive = true
			m.createDialogStep = "details-inline"
			m.createInputFocus = 1
			m.createDialogContent = "services: {}\n"
			return m.Update(key("e"))
		}},
		{"a file loaded from the browser", func(m *Model) tea.Cmd {
			m.fileBrowserActive = true
			m.fileBrowserContext = "create"
			m.fileBrowserFiles = []string{file}
			return m.Update(key("enter"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scriptedEditor(t)
			stackMock := noopStackOps()
			stackMock.reconstructStackComposeFn = func(string) (string, error) { return "services: {}\n", nil }
			stackMock.deployStackFn = func(string, string, docker.DeployOptions) error {
				t.Fatal("a new stack's content must not be deployed as an edit")
				return nil
			}
			m := testModel(func(m *Model) { m.deps.Stacks = stackMock })
			loadStacks(m, fakeStacks("prod"))
			m.Update(key("e")) // an edit whose editor never returns

			m.Update(runCmd(tc.open(m)))
			require.True(t, m.createDialogActive)
			require.Contains(t, m.createDialogContent, "# edited")
		})
	}
}
