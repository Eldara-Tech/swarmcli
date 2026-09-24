// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package helpbar

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type HelpEntry struct {
	Key      string
	Desc     string
	Disabled bool
}

type Model struct {
	globalHelp []HelpEntry
	viewHelp   []HelpEntry
	width      int
	height     int
}

// EditionLabel is rendered below the logo. Override in init() to customise.
var EditionLabel = "Community Edition"

func SetEditionLabel(label string) {
	EditionLabel = label
}

// KeyBack is the canonical key for "go back / close this view".
const KeyBack = "esc"

// KeyQuit is the canonical key for "quit the application".
const KeyQuit = "ctrl+q"

func New(width, height int) *Model {
	return &Model{
		globalHelp: []HelpEntry{{Key: KeyQuit, Desc: "quit"}, {Key: "?", Desc: "help"}},
		width:      width,
		height:     height,
	}
}

func (m *Model) WithGlobalHelp(entries []HelpEntry) *Model {
	m.globalHelp = entries
	return m
}

func (m *Model) WithViewHelp(entries []HelpEntry) *Model {
	m.viewHelp = entries
	return m
}

func (m *Model) SetWidth(width int) *Model {
	m.width = width
	return m
}

func (m *Model) SetHeight(height int) *Model {
	m.height = height
	return m
}

// Horizontal separators used when composing the header.
const (
	logoSpacer = "  "  // between the help block and the logo
	colGap     = "   " // between help columns
)

// View renders the help bar. hasError turns the logo red and, failing wins,
// hasWarning otherwise turns it amber.
func (m *Model) View(systemInfo string, hasError, hasWarning bool) string {
	allHelp := append(m.globalHelp, m.viewHelp...)
	if len(allHelp) == 0 {
		return systemInfo
	}

	infoWidth := lipgloss.Width(systemInfo)

	// Render every candidate help column at its natural width and build the
	// logo once, so the packing below can budget against real widths.
	cols, colWidths := renderColumns(allHelp)
	logo := buildLogo(hasError, hasWarning)
	logoWidth := lipgloss.Width(logo)

	// Decide whether the logo fits, and how much room is left for help. The
	// logo is right-aligned, so when it is shown it reserves spacer+logo and
	// the help block fills the remaining gap.
	showLogo := m.width-infoWidth >= len(logoSpacer)+logoWidth
	helpFill := m.width - infoWidth
	if showLogo {
		helpFill -= len(logoSpacer) + logoWidth
	}

	// Keep leading columns (left to right) while they fit within helpFill;
	// drop the rest. This degrades gracefully as the terminal narrows.
	kept, used := 0, 0
	for i, w := range colWidths {
		next := used + w
		if i > 0 {
			next += len(colGap)
		}
		if next > helpFill {
			break
		}
		used, kept = next, i+1
	}

	parts := []string{systemInfo}
	switch {
	case kept > 0:
		items := make([]string, 0, kept*2-1)
		for i := 0; i < kept; i++ {
			if i > 0 {
				items = append(items, colGap)
			}
			items = append(items, cols[i])
		}
		helpBlock := lipgloss.JoinHorizontal(lipgloss.Top, items...)
		if helpFill > 0 {
			helpBlock = lipgloss.NewStyle().Width(helpFill).Align(lipgloss.Left).Render(helpBlock)
		}
		parts = append(parts, helpBlock)
	case showLogo && helpFill > 0:
		// No help columns fit; reserve the gap so the logo stays right-aligned.
		parts = append(parts, lipgloss.NewStyle().Width(helpFill).Render(""))
	}
	if showLogo {
		parts = append(parts, logoSpacer, logo)
	}

	out := lipgloss.JoinHorizontal(lipgloss.Top, parts...)

	// Hard backstop: the app layout assumes the header is exactly m.height
	// lines tall and no wider than m.width. Clamp so it can never overflow its
	// box (and scramble the screen) even if the packing above is ever wrong.
	return lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(out)
}

// renderColumns lays out the help entries into columns (filled top-to-bottom,
// 5 rows per column) and returns each column's rendered block plus its width.
func renderColumns(allHelp []HelpEntry) (blocks []string, widths []int) {
	const rowsPerColumn = 5
	numCols := (len(allHelp) + rowsPerColumn - 1) / rowsPerColumn

	columns := make([][]HelpEntry, numCols)
	for i, entry := range allHelp {
		col := i / rowsPerColumn
		columns[col] = append(columns[col], entry)
	}

	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	disabledKeyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	disabledDescStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	for _, col := range columns {
		// Align descriptions on the widest key in this column.
		maxKeyLen := 0
		for _, entry := range col {
			if l := lipgloss.Width("<" + entry.Key + ">"); l > maxKeyLen {
				maxKeyLen = l
			}
		}

		var lines []string
		for _, entry := range col {
			keyText := "<" + entry.Key + ">"
			padding := maxKeyLen - lipgloss.Width(keyText)
			var line string
			if entry.Disabled {
				line = disabledKeyStyle.Render(keyText) + strings.Repeat(" ", padding+2) + disabledDescStyle.Render(entry.Desc)
			} else {
				line = keyStyle.Render(keyText) + strings.Repeat(" ", padding+2) + entry.Desc
			}
			lines = append(lines, line)
		}

		block := strings.Join(lines, "\n")
		blocks = append(blocks, block)
		widths = append(widths, lipgloss.Width(block))
	}
	return blocks, widths
}

// buildLogo renders the SWC logo with the edition label right-aligned on its
// last line. The label is truncated if it would overflow the logo's width.
func buildLogo(hasError, hasWarning bool) string {
	logoTop := `  ___________      ___________
 /   _____/  \    /  \_   ___ \
 \_____  \\   \/\/   /    \  \/
 /        \\        /\     \____
/_______  / \__/\__/  \________/`

	logoBottom := `        \/       \/          \/`

	logoColor := lipgloss.Color("214") // yellow by default
	switch {
	case hasError:
		logoColor = lipgloss.Color("9") // red when errors exist
	case hasWarning:
		logoColor = lipgloss.Color("3") // amber while converging, as its rows are
	}
	logoStyle := lipgloss.NewStyle().Foreground(logoColor).Bold(true)
	editionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)

	label := EditionLabel
	if lipgloss.Width(label) >= len(logoBottom) {
		// Keep the label inside the logo so the slice below cannot panic and
		// the logo width stays fixed.
		label = lipgloss.NewStyle().MaxWidth(len(logoBottom) - 1).Render(label)
	}
	pad := len(logoBottom) - lipgloss.Width(label)
	lastLine := logoStyle.Render(logoBottom[:pad]) + editionStyle.Render(label)
	return logoStyle.Render(logoTop) + "\n" + lastLine
}
