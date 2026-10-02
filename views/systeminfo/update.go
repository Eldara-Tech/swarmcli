// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package systeminfoview

import (
	"fmt"
	"strings"

	"github.com/briandowns/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case Msg:
		m.SetContent(msg)
		// Deliberately does NOT start a resource-usage collection. This message
		// is the result of LoadStatus, which the app re-runs on its own 5s tick;
		// chaining the collection off it ran a second, faster loop alongside the
		// one SlowStatusMsg re-arms, and neither could see the other, so rounds
		// piled up on the daemon. The counts here are cheap and stay on the app
		// tick; the fan-out has exactly one driver.
		return nil

	case LatestVersionMsg:
		m.latest = msg.LatestVersion
		m.content = m.buildContent()
		return nil

	case NoVersionUpdateMsg:
		return nil

	case SlowStatusMsg:
		if msg.generation != m.generation {
			// Measured the context the session has since left. Drop it and
			// start a round against the current one now rather than after a
			// tick, so the spinner is not held for an extra interval. This is
			// still the one chain: the stale round has finished.
			return m.LoadSlowStatus()
		}
		m.updateCPUMem(msg)
		// Schedule next tick 8 seconds after collection completes
		return m.tickCmd()

	case TickMsg:
		// Only show spinner on first load, keep previous values during refresh
		if m.firstLoad {
			m.loadingCPU = true
			m.loadingMem = true
			m.content = m.buildContent()
		}
		// Trigger slow status reload (will schedule next tick after completion)
		return m.LoadSlowStatus()

	case SpinnerTickMsg:
		// Fast animation tick - always keep running
		m.spinner++
		needsUpdate := false

		if m.loadingCPU || m.loadingMem {
			needsUpdate = true
		}

		// Handle pulsing for trend arrows - decrement every 3 ticks for slower pulse
		if m.cpuBlinkCount > 0 || m.memBlinkCount > 0 {
			// Decrement counters every 3rd tick (240ms intervals)
			if m.spinner%3 == 0 {
				if m.cpuBlinkCount > 0 {
					m.cpuBlinkCount--
				}
				if m.memBlinkCount > 0 {
					m.memBlinkCount--
				}
			}
			needsUpdate = true
		}

		if needsUpdate {
			m.content = m.buildContent()
		}

		return m.spinnerTickCmd()
	}

	var cmd tea.Cmd
	return cmd
}

func (m *Model) buildContent() string {
	// Use briandowns/spinner character set 14 (dots)
	spinnerFrames := spinner.CharSets[14]

	cpu := m.cpuUsage
	if m.loadingCPU {
		cpu = spinnerFrames[m.spinner%len(spinnerFrames)]
	} else if m.cpuBlinkCount > 0 && m.cpuBlinkCount%2 == 1 {
		// Hide arrow during odd blink counts (creates pulse effect)
		var cpuVal float64
		if _, err := fmt.Sscanf(m.cpuUsage, "%f%%", &cpuVal); err == nil {
			cpu = fmt.Sprintf("%.1f%%", cpuVal)
		}
	}

	mem := m.memUsage
	if m.loadingMem {
		mem = spinnerFrames[m.spinner%len(spinnerFrames)]
	} else if m.memBlinkCount > 0 && m.memBlinkCount%2 == 1 {
		// Hide arrow during odd blink counts (creates pulse effect)
		var memVal float64
		if _, err := fmt.Sscanf(m.memUsage, "%f%%", &memVal); err == nil {
			mem = fmt.Sprintf("%.1f%%", memVal)
		}
	}

	return content(
		m.context, m.versionDisplay(), cpu, mem, m.containerCount, m.serviceCount,
	)
}

func (m *Model) versionDisplay() string {
	if strings.TrimSpace(m.latest) == "" {
		return m.version
	}

	latest := strings.TrimSpace(m.latest)
	if !strings.HasPrefix(strings.ToLower(latest), "v") {
		latest = "v" + latest
	}

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")).
		Bold(true).
		Render("⚡" + latest)

	return m.version + " " + hint
}

func content(context, version, cpu, mem string, containers, services int) string {
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")).
		Bold(true).
		Width(12)

	// Use lipgloss Width to handle styled text properly
	return fmt.Sprintf(
		"%s %s\n%s %s\n%s %s\n%s %s\n%s %d\n%s %d",
		labelStyle.Render("Context:"), context,
		labelStyle.Render("Version:"), version,
		labelStyle.Render("CPU:"), cpu,
		labelStyle.Render("MEM:"), mem,
		labelStyle.Render("Containers:"), containers,
		labelStyle.Render("Services:"), services,
	)
}

func (m *Model) SetContent(msg Msg) {
	m.context = msg.context

	// Update capacity if provided
	if msg.cpuCapacity != "" {
		m.cpuCapacity = msg.cpuCapacity
	}
	if msg.memCapacity != "" {
		m.memCapacity = msg.memCapacity
	}

	m.containerCount = msg.containers
	m.serviceCount = msg.services

	m.content = m.buildContent()
}

// ResetResourceUsage puts CPU and MEM back into their first-load state. The
// app calls it when the session moves to another context: the values and their
// trend arrows describe the swarm it left, and the next round measures a
// different one. A round already in flight is discarded when it lands.
func (m *Model) ResetResourceUsage() {
	m.generation++
	m.loadingCPU = true
	m.loadingMem = true
	m.firstLoad = true
	m.cpuUsage, m.memUsage = "", ""
	m.prevCPU, m.prevMem = 0, 0
	m.prevCPUTrend, m.prevMemTrend = "", ""
	m.cpuBlinkCount, m.memBlinkCount = 0, 0
	m.content = m.buildContent()
}

func (m *Model) updateCPUMem(msg SlowStatusMsg) {
	// Parse current CPU/MEM values and add trend arrows
	var currentCPU, currentMem float64
	_, _ = fmt.Sscanf(msg.cpu, "%f%%", &currentCPU)
	_, _ = fmt.Sscanf(msg.mem, "%f%%", &currentMem)

	// Clear loading flags and firstLoad
	m.loadingCPU = false
	m.loadingMem = false
	m.firstLoad = false

	// Add trend arrows if we have previous values
	if m.prevCPU > 0 {
		var currentTrend string
		if currentCPU > m.prevCPU {
			currentTrend = "up"
			m.cpuUsage = msg.cpu + " " + lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("↑")
			// Pulse on every change
			if m.prevCPUTrend != currentTrend {
				m.cpuBlinkCount = 6
			}
		} else if currentCPU < m.prevCPU {
			currentTrend = "down"
			m.cpuUsage = msg.cpu + " " + lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("↓")
			// Pulse on every change
			if m.prevCPUTrend != currentTrend {
				m.cpuBlinkCount = 6
			}
		} else {
			currentTrend = ""
			m.cpuUsage = msg.cpu
		}
		m.prevCPUTrend = currentTrend
	} else {
		m.cpuUsage = msg.cpu
	}

	if m.prevMem > 0 {
		var currentTrend string
		if currentMem > m.prevMem {
			currentTrend = "up"
			m.memUsage = msg.mem + " " + lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("↑")
			// Pulse on every change
			if m.prevMemTrend != currentTrend {
				m.memBlinkCount = 6
			}
		} else if currentMem < m.prevMem {
			currentTrend = "down"
			m.memUsage = msg.mem + " " + lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("↓")
			// Pulse on every change
			if m.prevMemTrend != currentTrend {
				m.memBlinkCount = 6
			}
		} else {
			currentTrend = ""
			m.memUsage = msg.mem
		}
		m.prevMemTrend = currentTrend
	} else {
		m.memUsage = msg.mem
	}

	// Update previous values
	m.prevCPU = currentCPU
	m.prevMem = currentMem

	m.content = m.buildContent()
}
