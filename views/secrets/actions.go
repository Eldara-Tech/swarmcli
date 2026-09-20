// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package secretsview

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/Eldara-Tech/swarmcli/v2/core/primitives/hash"
	"github.com/Eldara-Tech/swarmcli/v2/docker"
	inspectview "github.com/Eldara-Tech/swarmcli/v2/views/inspect"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/docker/docker/api/types/swarm"
)

// --- Async commands ---

func (m *Model) loadSecretsCmd() tea.Cmd {
	secretOps := m.deps.Secrets
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
		defer cancel()
		secs, err := secretOps.ListSecrets(ctx)
		if err != nil {
			return errorMsg(fmt.Errorf("failed to list secrets: %w", err))
		}

		wrapped := make([]docker.SecretWithDecodedData, len(secs))
		for i, s := range secs {
			wrapped[i] = docker.SecretWithDecodedData{Secret: s, Data: nil}
		}
		return secretsLoadedMsg(wrapped)
	}
}

// computeSecretUsedCmd checks which secrets are used by services in background
// and returns a usedStatusUpdatedMsg containing a map[id]bool.
//
// One indexed lookup, not one service listing per secret. This runs after every
// load and after every poll that sees a change, so the per-secret version cost a
// full ServiceList times the number of secrets on the swarm each time.
func (m *Model) computeSecretUsedCmd(secs []docker.SecretWithDecodedData) tea.Cmd {
	secretOps := m.deps.Secrets
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
		defer cancel()

		usedBy, err := secretOps.ServicesUsingSecrets(ctx)
		if err != nil {
			l().Errorf("computeSecretUsedCmd: ServicesUsingSecrets failed: %v", err)
		}

		usedMap := make(map[string]bool, len(secs))
		for _, s := range secs {
			usedMap[s.Secret.ID] = len(usedBy[s.Secret.ID]) > 0
		}
		return usedStatusUpdatedMsg(usedMap)
	}
}

// checkSecretsCmd checks if secrets have changed and returns update message if so
func (m *Model) checkSecretsCmd(lastHash uint64) tea.Cmd {
	secretOps := m.deps.Secrets
	return func() tea.Msg {
		if !m.polling.CompareAndSwap(false, true) {
			l().Info("checkSecretsCmd: skipped, previous poll still in flight")
			return PollRetryMsg{}
		}
		defer m.polling.Store(false)

		l().Info("checkSecretsCmd: Polling for secret changes")

		ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
		defer cancel()
		secs, err := secretOps.ListSecrets(ctx)
		if err != nil {
			l().Errorf("checkSecretsCmd: ListSecrets failed: %v", err)
			return PollRetryMsg{}
		}

		wrapped := make([]docker.SecretWithDecodedData, len(secs))
		for i, s := range secs {
			wrapped[i] = docker.SecretWithDecodedData{Secret: s, Data: nil}
		}

		// Create a stable hash based only on ID and Version (not timestamps)
		type stableSecret struct {
			ID      string
			Version uint64
			Name    string
		}
		stableSecrets := make([]stableSecret, len(secs))
		for i, s := range secs {
			stableSecrets[i] = stableSecret{
				ID:      s.ID,
				Version: s.Version.Index,
				Name:    s.Spec.Name,
			}
		}

		newHash, err := hash.Compute(stableSecrets)
		if err != nil {
			l().Errorf("checkSecretsCmd: Error computing hash: %v", err)
			// Schedule next poll even on error
			return PollRetryMsg{}
		}

		l().Infof("checkSecretsCmd: lastHash=%s, newHash=%s, secretCount=%d",
			hash.Fmt(lastHash), hash.Fmt(newHash), len(wrapped))

		// Only return update message if something changed
		if newHash != lastHash {
			l().Info("checkSecretsCmd: Change detected! Refreshing secret list")
			return secretsLoadedMsg(wrapped)
		}

		l().Info("checkSecretsCmd: No changes detected, scheduling next poll")
		return PollRetryMsg{}
	}
}

func (m *Model) inspectSecretCmd(name string) tea.Cmd {
	secretOps := m.deps.Secrets
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
		defer cancel()
		sec, err := secretOps.InspectSecret(ctx, name)
		jsonStr := ""
		if err != nil {
			jsonStr = fmt.Sprintf("Error inspecting secret %q: %v", name, err)
		} else if data, err := sec.JSON(); err != nil {
			jsonStr = fmt.Sprintf("Error marshalling secret %q: %v", name, err)
		} else {
			jsonStr = string(data)
		}

		return view.NavigateToMsg{
			ViewName: inspectview.ViewName,
			Payload: map[string]any{
				"title": fmt.Sprintf("Secret: %s", name),
				"json":  jsonStr,
				"meta": map[string]any{
					"ID":   sec.Secret.ID,
					"Name": sec.Secret.Spec.Name,
					"Note": "Secret data is not available (write-only)",
				},
			},
		}
	}
}

func (m *Model) deleteSecretCmd(name string) tea.Cmd {
	secretOps := m.deps.Secrets
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
		defer cancel()
		err := secretOps.DeleteSecret(ctx, name)
		if err != nil {
			return errorMsg(fmt.Errorf("failed to delete secret '%s': %w", name, err))
		}
		return secretDeletedMsg{Name: name}
	}
}

func loadFilesCmd(dirPath string) tea.Cmd {
	return func() tea.Msg {
		files := []string{}

		// Expand ~ to home directory
		if strings.HasPrefix(dirPath, "~") {
			if homeDir, err := os.UserHomeDir(); err == nil {
				dirPath = strings.Replace(dirPath, "~", homeDir, 1)
			}
		}

		// Add parent directory option if not root
		if dirPath != "/" {
			files = append(files, "..")
		}

		entries, err := os.ReadDir(dirPath)
		if err != nil {
			return fileBrowserMsg{
				Path:  dirPath,
				Files: files,
			}
		}

		// Separate directories and regular files
		var dirs []string
		var regFiles []string

		for _, entry := range entries {
			if entry.IsDir() {
				// Add directory with trailing slash
				dirs = append(dirs, filepath.Join(dirPath, entry.Name())+"/")
			} else {
				// Add all files
				regFiles = append(regFiles, filepath.Join(dirPath, entry.Name()))
			}
		}

		// Add directories first, then files
		files = append(files, dirs...)
		files = append(files, regFiles...)

		return fileBrowserMsg{
			Path:  dirPath,
			Files: files,
		}
	}
}

func (m *Model) createSecretFromFileCmd(name, filePath string, labels map[string]string, encode bool) tea.Cmd {
	secretOps := m.deps.Secrets
	return func() tea.Msg {
		l().Infof("Creating secret %s from file %s (encode=%v, labels=%v)", name, filePath, encode, labels)

		// Read file content
		data, err := os.ReadFile(filePath)
		if err != nil {
			l().Errorf("Failed to read file %s: %v", filePath, err)
			return errorMsg(fmt.Errorf("failed to read file: %w", err))
		}

		// Base64 encode if requested. The payload is never logged, at any level:
		// it is the secret, and a debug log is the file an operator attaches to
		// a bug report — one lumberjack keeps as five compressed rotations for
		// fourteen days, long after the terminal it was typed into is gone.
		// CreateSecret already logs its size, which is the part worth having.
		if encode {
			data = []byte(base64.StdEncoding.EncodeToString(data))
		}

		// Create the secret
		ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
		defer cancel()
		newSec, err := secretOps.CreateSecret(ctx, name, data, labels)
		if err != nil {
			l().Errorf("Failed to create secret %s: %v", name, err)
			return errorMsg(fmt.Errorf("failed to create secret: %w", err))
		}

		l().Infof("Successfully created secret %s from file", name)
		return secretCreatedMsg{
			Name:   name,
			Secret: docker.SecretWithDecodedData{Secret: newSec, Data: nil},
		}
	}
}

func (m *Model) createSecretFromContentCmd(name string, content []byte, labels map[string]string, encode bool) tea.Cmd {
	secretOps := m.deps.Secrets
	return func() tea.Msg {
		l().Infof("Creating secret %s from inline content (encode=%v, labels=%v)", name, encode, labels)

		// Never logged — see createSecretFromFileCmd.
		if encode {
			content = []byte(base64.StdEncoding.EncodeToString(content))
		}

		// Create the secret
		ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
		defer cancel()
		newSec, err := secretOps.CreateSecret(ctx, name, content, labels)
		if err != nil {
			l().Errorf("Failed to create secret %s: %v", name, err)
			return errorMsg(fmt.Errorf("failed to create secret: %w", err))
		}

		l().Infof("Successfully created secret %s", name)
		return secretCreatedMsg{
			Name:   name,
			Secret: docker.SecretWithDecodedData{Secret: newSec, Data: nil},
		}
	}
}

func (m *Model) getUsedByStacksCmd(secretName string) tea.Cmd {
	secretOps := m.deps.Secrets
	return func() tea.Msg {
		l().Infof("Getting stacks/services that use secret: %s", secretName)

		ctx, cancel := context.WithTimeout(context.Background(), userActionTimeout)
		defer cancel()
		// Get secret ID for robust matching
		sec, err := secretOps.InspectSecret(ctx, secretName)
		if err != nil {
			l().Errorf("Failed to inspect secret %s: %v", secretName, err)
			return errorMsg(err)
		}

		// Services referencing this secret by either name or ID. The index holds
		// both keys, so one listing replaces the two this used to merge by hand.
		svcsBySecret, err := secretOps.ServicesUsingSecrets(ctx)
		if err != nil {
			l().Errorf("Failed to index services by secret: %v", err)
			return errorMsg(err)
		}

		svcMap := make(map[string]swarm.Service)
		for _, svc := range svcsBySecret[secretName] {
			svcMap[svc.ID] = svc
		}
		for _, svc := range svcsBySecret[sec.Secret.ID] {
			svcMap[svc.ID] = svc
		}

		// Collect stack/service pairs
		var usedBy []usedByItem
		for _, svc := range svcMap {
			stackName := svc.Spec.Labels["com.docker.stack.namespace"]
			if stackName == "" {
				stackName = "(no stack)"
			}
			usedBy = append(usedBy, usedByItem{
				StackName:   stackName,
				ServiceName: svc.Spec.Name,
			})
		}

		// Sort by stack then service
		sort.Slice(usedBy, func(i, j int) bool {
			if usedBy[i].StackName == usedBy[j].StackName {
				return usedBy[i].ServiceName < usedBy[j].ServiceName
			}
			return usedBy[i].StackName < usedBy[j].StackName
		})

		l().Infof("Secret %s is used by %d service(s)", secretName, len(usedBy))

		return usedByMsg{SecretName: secretName, UsedBy: usedBy}
	}
}

// openEditorForContentCmd opens the user's editor to edit secret content
func openEditorForContentCmd(initialData string) tea.Cmd {
	l().Infoln("openEditorForContentCmd: started")

	tmp, err := os.CreateTemp("", "secret-*.txt")
	if err != nil {
		l().Infoln("CreateTemp error:", err)
		return func() tea.Msg { return errorMsg(fmt.Errorf("failed to create temp file: %w", err)) }
	}
	defer func(tmp *os.File) {
		_ = tmp.Close()
	}(tmp)

	if initialData != "" {
		if _, err := tmp.WriteString(initialData); err != nil {
			l().Infoln("Write temp error:", err)
			return func() tea.Msg { return errorMsg(fmt.Errorf("failed to write temp file: %w", err)) }
		}
	}

	l().Infoln("Created temp file:", tmp.Name())

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano"
	}

	l().Infoln("Invoking editor:", editor, tmp.Name())
	cmd := exec.Command(editor, tmp.Name())
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
			return errorMsg(fmt.Errorf("editor failed: %w", err))
		}

		l().Infoln("Editor closed, reading back from temp")
		newData, err := os.ReadFile(tmp.Name())
		if err != nil {
			l().Infoln("ReadFile error:", err)
			return errorMsg(fmt.Errorf("failed to read edited file: %w", err))
		}

		l().Infoln("Read new data, length:", len(newData))
		return editorContentMsg{Content: string(newData)}
	})
}
