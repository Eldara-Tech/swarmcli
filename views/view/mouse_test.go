// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package view

import (
	"io"
	"os/exec"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// sequenceSteps runs a tea.Sequence command and returns the commands in it.
// The sequence type is unexported, so it is read as the slice it is.
func sequenceSteps(t *testing.T, cmd tea.Cmd) []tea.Cmd {
	t.Helper()
	v := reflect.ValueOf(cmd())
	require.Equal(t, reflect.Slice, v.Kind(), "expected a sequence, got %T", cmd())
	steps := make([]tea.Cmd, v.Len())
	for i := range steps {
		steps[i] = v.Index(i).Interface().(tea.Cmd)
	}
	return steps
}

// The handover comes first and the restore second: Bubble Tea runs a sequence
// in order, so the mouse is switched back on only once the process has exited.
func TestExecProcessRestoresTheMouseAfterwards(t *testing.T) {
	steps := sequenceSteps(t, ExecProcess(exec.Command("true"), nil))

	require.Len(t, steps, 2)
	require.Equal(t, RestoreMouseMsg{}, steps[1]())
}

type noopExec struct{}

func (noopExec) Run() error          { return nil }
func (noopExec) SetStdin(io.Reader)  {}
func (noopExec) SetStdout(io.Writer) {}
func (noopExec) SetStderr(io.Writer) {}

func TestExecRestoresTheMouseAfterwards(t *testing.T) {
	steps := sequenceSteps(t, Exec(noopExec{}, nil))

	require.Len(t, steps, 2)
	require.Equal(t, RestoreMouseMsg{}, steps[1]())
}
