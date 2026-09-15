// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package configsview

import (
	"context"
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// editWithTempFileCmd creates a temp file pre-populated with `initialData`,
// opens the user's editor, and calls `onDone` with the edited bytes when the
// editor exits successfully. On any error (temp file creation, editor
// execution, or reading back the file), `onErr` is called with the error so
// callers can return appropriate message types.
func editWithTempFileCmd(baseName string, initialData []byte, onDone func([]byte) tea.Msg, onErr func(error) tea.Msg) tea.Cmd {
	l().Infoln("editWithTempFileCmd: started for", baseName)

	tmp, err := os.CreateTemp("", fmt.Sprintf("%s-*.txt", baseName))
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

// openEditorForContentCmd opens the user's editor to edit config content and returns it to the create dialog.
func openEditorForContentCmd(initialData string) tea.Cmd {
	return editWithTempFileCmd("config", []byte(initialData),
		func(newData []byte) tea.Msg {
			return editorContentMsg{Content: string(newData)}
		},
		func(err error) tea.Msg {
			return configCreateErrorMsg{err}
		})
}

// editConfigInEditorCmd creates a tmp file and opens the editor to edit the new config.
func (m *Model) editConfigInEditorCmd(name string) tea.Cmd {
	configOps := m.deps.Configs
	l().Infoln("editConfigInEditorCmd: started")

	ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
	defer cancel()
	cfg, err := configOps.InspectConfig(ctx, name)
	if err != nil {
		l().Infoln("InspectConfig error:", err)
		return func() tea.Msg { return editConfigErrorMsg{err} }
	}
	l().Infoln("InspectConfig OK")

	// Use helper to open editor with existing content and process result
	return editWithTempFileCmd(cfg.Config.Spec.Name, cfg.Data,
		func(newData []byte) tea.Msg {
			// Check if content changed
			if string(newData) == string(cfg.Data) {
				l().Infoln("No changes made to config")
				return editConfigDoneMsg{
					Name:      cfg.Config.Spec.Name,
					Changed:   false,
					OldConfig: *cfg,
					NewConfig: *cfg,
				}
			}

			// Create a new Docker config version with the edited data
			createCtx, createCancel := context.WithTimeout(context.Background(), userActionTimeout)
			defer createCancel()
			newCfg, err := configOps.CreateConfigVersion(createCtx, cfg.Config, newData)
			if err != nil {
				l().Infoln("CreateConfigVersion error:", err)
				return editConfigErrorMsg{err}
			}
			l().Infoln("Config updated:", newCfg.Spec.Name)

			wrapped := docker.ConfigWithDecodedData{
				Config: newCfg,
				Data:   newData,
			}

			return editConfigDoneMsg{
				Name:      newCfg.Spec.Name,
				Changed:   true,
				OldConfig: *cfg,
				NewConfig: wrapped,
			}
		},
		func(err error) tea.Msg {
			return editConfigErrorMsg{err}
		})
}
