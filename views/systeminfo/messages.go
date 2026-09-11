// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package systeminfoview

import "time"

// SystemInfoMsg is implemented by all messages owned by the systeminfo component.
// The app-level router uses this to forward messages without listing each type.
type SystemInfoMsg interface {
	systemInfoMsg()
}

func (Msg) systemInfoMsg()                {}
func (SlowStatusMsg) systemInfoMsg()      {}
func (TickMsg) systemInfoMsg()            {}
func (SpinnerTickMsg) systemInfoMsg()     {}
func (LatestVersionMsg) systemInfoMsg()   {}
func (NoVersionUpdateMsg) systemInfoMsg() {}

type Msg struct {
	context     string
	cpu         string
	mem         string
	cpuCapacity string
	memCapacity string
	containers  int
	services    int
}

type SlowStatusMsg struct {
	cpu string
	mem string
}

type TickMsg time.Time

type SpinnerTickMsg time.Time

// LatestVersionMsg reports that the version API returned a newer release than
// the running build. The app layer reads LatestVersion to raise the startup
// update notice, so the field is exported.
type LatestVersionMsg struct {
	LatestVersion string
}

type NoVersionUpdateMsg struct{}

// HeartbeatMsg is the daily timer firing: a session that is still open should
// re-report. Carries nothing — the model has everything the report needs.
type HeartbeatMsg struct{}

// HeartbeatSentMsg closes one heartbeat round. The app re-arms the timer on it
// rather than on HeartbeatMsg, so a slow or failed report cannot leave two
// rounds in flight — the same one-chain-in-one-chain-out rule the resource tick
// follows.
type HeartbeatSentMsg struct{}
