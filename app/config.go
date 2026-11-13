// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package app

import (
	"errors"

	"github.com/Eldara-Tech/swarmcli/v2/userconfig"
)

// startupConfigFn is config.yaml as this process read it. Overridable for
// tests.
var startupConfigFn = userconfig.Startup

// configNotice is the stack-bar line for a config.yaml the session could not
// use. The error itself is in the log.
func configNotice(err error) (line, short string) {
	if errors.Is(err, userconfig.ErrUnparseable) {
		return "config.yaml does not parse · running on defaults · details in the log", "config.yaml does not parse"
	}
	return "config.yaml could not be read · running on defaults · details in the log", "config.yaml unreadable"
}
