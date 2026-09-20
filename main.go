// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package main

import (
	"github.com/Eldara-Tech/swarmcli/v2/app"
	"github.com/Eldara-Tech/swarmcli/v2/cli"
	swarmlog "github.com/Eldara-Tech/swarmcli/v2/utils/log"
	"os"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"
)

// Version information, set by GoReleaser at build time
var (
	version = "dev"
	edition = "ce"
	commit  = "none"
	date    = "unknown"
)

func init() {
	app.SetVersion(version)
	app.SetEdition(edition)
	// Distinct from app.SetEdition, which drives a label that follows live
	// licence state; this one names the build and never changes. See
	// cli.SetEdition and docs/editions.md.
	cli.SetEdition(edition)
	app.Init()
	// Log version info for debugging
	swarmlog.L().Infof("swarmcli version=%s edition=%s commit=%s date=%s", version, edition, commit, date)
}

func main() {
	// When invoked with arguments, run the non-interactive CLI (e.g.
	// `swarmcli charts install ...`) and exit. A bare `swarmcli` launches the TUI.
	if len(os.Args) > 1 {
		os.Exit(cli.Dispatch(os.Args[1:], version))
	}

	p := tea.NewProgram(app.InitialModel(), app.ProgramOptions()...)

	if _, err := p.Run(); err != nil {
		// Log full stack trace to aid debugging panic-causes inside the TUI
		swarmlog.L().Errorf("program exited with error: %v", err)
		swarmlog.L().Errorf("stack trace:\n%s", string(debug.Stack()))
		swarmlog.L().Fatal(err)
	}
}
