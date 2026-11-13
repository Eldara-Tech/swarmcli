// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/telemetry"
	"github.com/Eldara-Tech/swarmcli/v2/userconfig"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestMain points config.yaml at an empty directory, so no test reads the
// developer's preferences and a `:mouse` toggle writes nothing of theirs.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "swarmcli-app-test")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

var errUnparseable = fmt.Errorf("%w: line 1", userconfig.ErrUnparseable)

// initNotices runs Init's notice decisions on m, with the disclosure and the
// phone-app line due as given.
func initNotices(t *testing.T, m *Model, disclosure, connectDue bool) {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // no install identity: a first run
	t.Setenv(telemetry.Env, "none")
	if disclosure {
		t.Setenv(telemetry.Env, "")
	}
	orig := connectNoticeDueFn
	t.Cleanup(func() { connectNoticeDueFn = orig })
	connectNoticeDueFn = func() bool { return connectDue }
	m.Init()
}

func TestConfigNotice_SaysOnceThatTheSessionRunsOnDefaults(t *testing.T) {
	setStackBarSuffix(t, "")
	m := newTestAppModel(&stubView{name: view.NameStacks})
	m.configErr = errUnparseable
	initNotices(t, m, false, true)

	require.Contains(t, ansi.Strip(m.renderStackBar()), "config.yaml does not parse · running on defaults")
	require.False(t, m.connectNoticeActive, "the phone-app line waits for a session it can have alone")

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	require.NotContains(t, ansi.Strip(m.renderStackBar()), "config.yaml", "the next key clears it")
}

func TestConfigNotice_NothingToSayWhenTheFileWasUsed(t *testing.T) {
	m := newTestAppModel(&stubView{name: view.NameStacks})
	initNotices(t, m, false, true)
	require.False(t, m.configNoticeActive)
	require.True(t, m.connectNoticeActive)
}

// The first-run disclosure comes first, and the config.yaml line is shown
// after it rather than retired unseen by the same key.
func TestConfigNotice_WaitsOutTheDisclosure(t *testing.T) {
	setStackBarSuffix(t, "")
	m := newTestAppModel(&stubView{name: view.NameStacks})
	m.configErr = errUnparseable
	initNotices(t, m, true, true)

	require.Contains(t, ansi.Strip(m.renderStackBar()), telemetry.NoticeLine)
	require.NotContains(t, ansi.Strip(m.renderStackBar()), "config.yaml")

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	require.Contains(t, ansi.Strip(m.renderStackBar()), "config.yaml does not parse")

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	require.NotContains(t, ansi.Strip(m.renderStackBar()), "config.yaml")
}

func TestConfigNotice_Text(t *testing.T) {
	line, short := configNotice(errUnparseable)
	require.Equal(t, "config.yaml does not parse · running on defaults · details in the log", line)
	require.Equal(t, "config.yaml does not parse", short)

	line, short = configNotice(errors.New("permission denied"))
	require.Equal(t, "config.yaml could not be read · running on defaults · details in the log", line)
	require.Equal(t, "config.yaml unreadable", short)
}
