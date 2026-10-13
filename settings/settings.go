// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

// Package settings persists small, single-purpose CLI preferences to the user
// config dir (~/.config/swarmcli). Today it records only the version at which
// the user dismissed the startup update notice.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Overridable for tests.
var (
	userHomeDirFn = os.UserHomeDir
	readFileFn    = os.ReadFile
	writeFileFn   = os.WriteFile
	mkdirAllFn    = os.MkdirAll
)

// relPath is the per-user preferences file, beside the license key under
// ~/.config/swarmcli.
const relPath = ".config/swarmcli/update-notice.json"

// Settings is the persisted CLI preference state.
type Settings struct {
	// DismissedUpdateVersion is the latest-release version at which the user
	// ticked "do not show again for this version" in the startup update
	// notice. A different latest version re-shows the notice.
	DismissedUpdateVersion string `json:"dismissedUpdateVersion,omitempty"`
}

func path() (string, error) {
	home, err := userHomeDirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, relPath), nil
}

// Load reads the settings file. A missing or unreadable/corrupt file is not an
// error — it yields the zero value.
func Load() Settings {
	var s Settings
	p, err := path()
	if err != nil {
		return s
	}
	data, err := readFileFn(p)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}
	}
	return s
}

// Save writes the settings file (0644), creating ~/.config/swarmcli if needed.
func (s Settings) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := mkdirAllFn(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeFileFn(p, data, 0o644)
}
