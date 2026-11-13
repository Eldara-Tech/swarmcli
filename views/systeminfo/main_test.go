// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package systeminfoview

import (
	"os"
	"testing"
)

// TestMain points config.yaml at an empty directory, so the check-in tests do
// not read the developer's own telemetry preference.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "swarmcli-systeminfo-test")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
