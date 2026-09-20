// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package stacksview

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
)

// editWithTempFileCmd creates a temp file pre-populated with `initialData`,
// opens the user's editor, and calls `onDone` with the edited bytes when the
// editor exits successfully. On any error (temp file creation, editor
// execution, or reading back the file), `onErr` is called with the error so
// callers can return appropriate message types.
func editWithTempFileCmd(baseName string, initialData []byte, onDone func([]byte) tea.Msg, onErr func(error) tea.Msg) tea.Cmd {
	l().Infoln("editWithTempFileCmd: started for", baseName)

	tmp, err := os.CreateTemp("", fmt.Sprintf("%s-*.yml", baseName))
	if err != nil {
		l().Infoln("CreateTemp error:", err)
		return func() tea.Msg { return onErr(fmt.Errorf("failed to create temp file: %w", err)) }
	}
	// Ensure file is closed; we'll remove it in the ExecProcess callback
	defer func(tmp *os.File) {
		_ = tmp.Close()
	}(tmp)

	if len(initialData) > 0 {
		if _, err := tmp.Write(initialData); err != nil {
			l().Infoln("Write temp error:", err)
			return func() tea.Msg { return onErr(fmt.Errorf("failed to write temp file: %w", err)) }
		}
	}

	l().Infoln("Created temp file:", tmp.Name())

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano"
	}

	l().Infoln("Invoking editor:", editor, tmp.Name())
	parts := strings.Fields(editor)
	cmdArgs := append(parts[1:], tmp.Name())
	cmd := exec.Command(parts[0], cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return view.ExecProcess(cmd, func(err error) tea.Msg {
		// Clean up temp file
		defer func(name string) {
			_ = os.Remove(name)
		}(tmp.Name())

		if err != nil {
			l().Infoln("Editor process error:", err)
			return onErr(fmt.Errorf("editor failed: %w", err))
		}

		l().Infoln("Editor closed, reading back from temp")
		newData, err := os.ReadFile(tmp.Name())
		if err != nil {
			l().Infoln("ReadFile error:", err)
			return onErr(fmt.Errorf("failed to read edited file: %w", err))
		}

		l().Infoln("Read new data, length:", len(newData))
		return onDone(newData)
	})
}

// openEditorForStackCmd opens the user's editor to edit stack YAML and returns it to the create dialog.
func openEditorForStackCmd(initialData string) tea.Cmd {
	return editWithTempFileCmd("stack", []byte(initialData),
		func(newData []byte) tea.Msg {
			return editorContentMsg{Content: string(newData), OriginalContent: initialData}
		},
		func(err error) tea.Msg {
			return stackCreateErrorMsg{err}
		})
}
