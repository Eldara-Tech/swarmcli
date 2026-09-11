// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

// Package telemetry reports that swarmcli is being used, and nothing about what
// it is used on.
//
// What it sends is enumerated in `docs/license.md` under Privacy and is the
// whole of it: an install identifier, the version and edition, the operating
// system and architecture, how the binary was installed, and whether it is the
// TUI or the controller. Deliberately absent, and listed here so the next
// person adding a field has to argue past it: hostnames, cluster or node names,
// service, stack and image names, command arguments, error text, and the
// address the request came from. Those describe a customer's infrastructure,
// and the licence documentation promises "how many, never which ones".
//
// It is on by default and `SWARMCLI_TELEMETRY=off` switches it off. The
// update check is a separate thing and survives that switch — see Client.
package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Overridable for tests.
var (
	userHomeDirFn = os.UserHomeDir
	readFileFn    = os.ReadFile
	writeFileFn   = os.WriteFile
	mkdirAllFn    = os.MkdirAll
	newUUIDFn     = func() string { return uuid.NewString() }
)

// installRelPath is the install identity, in its own file beside the licence
// key and the update-notice preferences under ~/.config/swarmcli.
//
// **Its own file rather than a field on settings.Settings, for two reasons that
// are both about lifetime.**
//
// The first is a live hazard rather than a hypothetical. `app/update.go` saves
// the update-notice dismissal as `settings.Settings{DismissedUpdateVersion:
// …}.Save()` — a fresh struct, so the write replaces the whole file and
// discards every field it does not name. There is only one field today, so
// nothing is lost today. An install id added beside it would be erased the
// first time somebody ticked "do not show again", and the install would be
// counted twice. Fixing that call site is the other way round; this way the
// identity is not reachable from it at all.
//
// The second is corruption. `settings.Load` answers the zero value for an
// unparseable file, which is right for a preference and wrong for an identity:
// a preference that resets is a small annoyance, an identity that resets is a
// second install in the data that never existed. Separate files mean a damaged
// preference cannot take the identity with it.
const installRelPath = ".config/swarmcli/install.json"

type installFile struct {
	// InstallID identifies this installation and nothing about the person
	// running it. A v4 UUID, generated once on the first run that reports, and
	// never derived from anything on the machine — not the hostname, not a MAC
	// address, not a machine-id. A derived identifier would be stable across
	// reinstalls and correlatable with other software that derives it the same
	// way, which is the opposite of what this is for.
	InstallID string `json:"installId"`
}

func installPath() (string, error) {
	home, err := userHomeDirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, installRelPath), nil
}

// InstallID returns this installation's identifier, creating and persisting one
// on first use.
//
// Returns "" when no identity can be stored — no home directory, an unwritable
// config directory. That is deliberate and the caller treats it as "do not
// report": a per-process identifier would turn every launch into a new install
// and silently inflate the only number this exists to produce. Better to count
// nothing than to count wrongly.
//
// Reads first and writes only when there is nothing to read, so the id is
// stable across launches, which is the entire point of it.
func InstallID() string {
	p, err := installPath()
	if err != nil {
		return ""
	}

	if data, err := readFileFn(p); err == nil {
		var f installFile
		if err := json.Unmarshal(data, &f); err == nil && f.InstallID != "" {
			return f.InstallID
		}
		// An unreadable or empty file falls through to writing a fresh one. It
		// is the same outcome as a first run, which is the best available
		// answer: the previous identity is not recoverable from here.
	}

	id := newUUIDFn()
	if err := mkdirAllFn(filepath.Dir(p), 0o755); err != nil {
		return ""
	}
	data, err := json.Marshal(installFile{InstallID: id})
	if err != nil {
		return ""
	}
	if err := writeFileFn(p, data, 0o644); err != nil {
		// Not persisted, so not usable: returning the id anyway would report a
		// different one on every launch.
		return ""
	}

	return id
}

// IsFirstRun reports whether no install identity has been stored yet.
//
// Called before InstallID so the notice is shown on the run that creates the
// identity rather than the one after it. Any error answers false: a machine
// that cannot read its own config directory should not be told about telemetry
// it is also unable to store an identity for.
func IsFirstRun() bool {
	p, err := installPath()
	if err != nil {
		return false
	}
	data, err := readFileFn(p)
	if err != nil {
		return true
	}
	var f installFile
	return json.Unmarshal(data, &f) != nil || f.InstallID == ""
}
