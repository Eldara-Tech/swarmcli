// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package command

import (
	"os"
	"testing"
)

// TestMain points config.yaml at an empty directory, so the telemetry command's
// tests do not read the developer's own preference.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "swarmcli-command-test")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
