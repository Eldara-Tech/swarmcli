// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"testing"

	"github.com/Eldara-Tech/swarmcli/v2/settings"
	"github.com/Eldara-Tech/swarmcli/v2/views/confirmdialog"

	"github.com/stretchr/testify/require"
)

func TestUpdateNoticeMessage_CE(t *testing.T) {
	origV, origE := version, edition
	defer func() { version, edition = origV, origE }()
	version, edition = "v1.8.0", "ce"

	msg := updateNoticeMessage("v1.9.0")
	require.Contains(t, msg, "v1.9.0")
	require.Contains(t, msg, "v1.8.0")
	require.Contains(t, msg, installDocsURL)
	require.Contains(t, msg, installDocsURLBusiness) // CE → BE upsell present
	require.Contains(t, msg, "Business Edition")
}

func TestUpdateNoticeMessage_BE(t *testing.T) {
	origV, origE := version, edition
	defer func() { version, edition = origV, origE }()
	version, edition = "v1.8.0", "be"

	msg := updateNoticeMessage("v1.9.0")
	require.Contains(t, msg, installDocsURL)
	require.NotContains(t, msg, installDocsURLBusiness) // one install link for every edition
	require.NotContains(t, msg, "Business Edition")     // BE is not named to a BE user
}

// Unlicensed BE: the build flag is "be" but BusinessEditionActive is overridden
// to report false (no valid license), so the notice shows the CE copy + upsell,
// matching the Community Edition presentation the rest of the UI uses.
func TestUpdateNoticeMessage_UnlicensedBE_ShowsUpsell(t *testing.T) {
	origV, origE, origP := version, edition, BusinessEditionActive
	defer func() { version, edition, BusinessEditionActive = origV, origE, origP }()
	version, edition = "v1.8.0", "be"
	BusinessEditionActive = func() bool { return false }

	msg := updateNoticeMessage("v1.9.0")
	require.Contains(t, msg, installDocsURL)
	require.Contains(t, msg, "Try Business Edition")
}

// Licensed BE: predicate true → shared copy, no upsell, regardless of build flag.
func TestUpdateNoticeMessage_LicensedBE_NoUpsell(t *testing.T) {
	origV, origE, origP := version, edition, BusinessEditionActive
	defer func() { version, edition, BusinessEditionActive = origV, origE, origP }()
	version, edition = "v1.8.0", "ce" // build flag deliberately not "be"
	BusinessEditionActive = func() bool { return true }

	msg := updateNoticeMessage("v1.9.0")
	require.Contains(t, msg, installDocsURL)
	require.NotContains(t, msg, "Try Business Edition")
}

func TestShowUpdateNotice_SetsInfoDialogWithCheckbox(t *testing.T) {
	m := newTestAppModel(&stubView{name: "test"})
	m.updateDialog = confirmdialog.New(200, 50)

	m.showUpdateNotice("v1.9.0")

	require.True(t, m.updateDialogActive)
	require.True(t, m.updateDialog.Visible)
	require.True(t, m.updateDialog.InfoMode)
	require.Equal(t, updateNoticeCheckbox, m.updateDialog.CheckboxLabel)
	require.False(t, m.updateDialog.CheckboxChecked)
	require.Equal(t, "v1.9.0", m.pendingUpdateVersion)
}

func TestUpdateDialogDismiss_PersistsWhenChecked(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // redirect settings file location

	m := newTestAppModel(&stubView{name: "test"})
	m.updateDialog = confirmdialog.New(200, 50)
	m.showUpdateNotice("v1.9.0")

	m.Update(confirmdialog.ResultMsg{CheckboxChecked: true})

	require.False(t, m.updateDialogActive)
	require.Equal(t, "v1.9.0", settings.Load().DismissedUpdateVersion)
}

func TestUpdateDialogDismiss_NotPersistedWhenUnchecked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := newTestAppModel(&stubView{name: "test"})
	m.updateDialog = confirmdialog.New(200, 50)
	m.showUpdateNotice("v1.9.0")

	m.Update(confirmdialog.ResultMsg{CheckboxChecked: false})

	require.False(t, m.updateDialogActive)
	require.Equal(t, "", settings.Load().DismissedUpdateVersion)
}
