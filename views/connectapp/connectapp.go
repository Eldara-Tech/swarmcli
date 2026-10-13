// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

// Package connectapp is the popup behind the "c" key and :connect: a QR code
// that gets the SwarmCLI phone app and, where an extension supplies one, a
// Connect tab that puts the current swarm on the phone.
package connectapp

import (
	"strings"

	"github.com/Eldara-Tech/swarmcli/v2/ui/dialog"
	"github.com/Eldara-Tech/swarmcli/v2/ui/qr"
	"github.com/Eldara-Tech/swarmcli/v2/views/view"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
)

// AppURL is the web page about the app. DownloadURL sends a phone to its own
// app store and anything else to AppURL; ref=tui tells the site where the
// visit came from. Both are swarmcli.io addresses rather than store links, so
// a store listing can move without a release of this binary.
const (
	AppURL      = "https://swarmcli.io/app"
	DownloadURL = "https://swarmcli.io/get?ref=tui"
)

// Pane is the popup's Connect tab. The popup owns the frame, the download code
// and the keys that move between them; a pane owns what connecting a swarm
// takes, which this repository does not implement.
type Pane interface {
	Init() tea.Cmd
	// Update receives every key the popup does not use itself while the
	// Connect tab is showing, and every non-key message while the popup is up.
	Update(msg tea.Msg) tea.Cmd
	// View renders the tab in at most width columns and height rows.
	View(width, height int) string
	// Unavailable says why this swarm cannot be connected yet and what would
	// fix it, or "" when it can. While it is non-empty the popup shows the
	// download code with this text beside it instead of the tab.
	Unavailable() string
}

// NewPane, when set by an extension in init(), builds the Connect tab each
// time the popup opens. Without it the popup offers the download alone, with
// UnavailableText saying why.
var NewPane func() Pane

// UnavailableText is what the popup says about connecting when no pane is
// registered. Override in init() to customise.
var UnavailableText = view.BEUnavailableErr("Connecting a phone").Error()

type tab int

const (
	tabConnect tab = iota
	tabDownload
)

// Model is the popup. It is closed until Open.
type Model struct {
	visible       bool
	width, height int // the terminal's
	pane          Pane
	tab           tab
	downloadQR    string
}

// New returns a closed popup.
func New() *Model {
	return &Model{downloadQR: qr.Render(DownloadURL)}
}

// Visible reports whether the popup is open.
func (m *Model) Visible() bool { return m != nil && m.visible }

// Open shows the popup sized to a width x height terminal, on the Connect tab
// when there is one.
func (m *Model) Open(width, height int) tea.Cmd {
	m.visible = true
	m.width, m.height = width, height
	m.tab = tabConnect
	m.pane = nil
	if NewPane != nil {
		m.pane = NewPane()
	}
	if m.pane == nil {
		return nil
	}
	return m.pane.Init()
}

// Close hides the popup and drops the pane, and with it anything it showed.
func (m *Model) Close() {
	m.visible = false
	m.pane = nil
}

func (m *Model) connectable() bool {
	return m.pane != nil && m.pane.Unavailable() == ""
}

// Update handles msg while the popup is open: esc closes it, tab switches
// between Connect and Get the app, and the rest goes to the pane.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	if !m.Visible() {
		return nil
	}
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			m.Close()
			return nil
		case "tab":
			if m.connectable() {
				m.tab = 1 - m.tab
			}
			return nil
		}
		if !m.connectable() || m.tab != tabConnect {
			return nil
		}
	}
	if m.pane == nil {
		return nil
	}
	return m.pane.Update(msg)
}

// The popup's frame costs this much around its content: the border on each
// side plus the dialog inset, and the border rows plus the title row.
const (
	frameColumns = 2 + dialog.BoxInsetColumns
	frameRows    = 3
)

const (
	gap        = "  "
	textColumn = 34 // the prose beside the download code
)

var (
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(dialog.BrightFg)
	textStyle    = lipgloss.NewStyle().Foreground(dialog.MutedFg)
	greyStyle    = lipgloss.NewStyle().Foreground(dialog.HelpFg)
	tabStyle     = lipgloss.NewStyle().Foreground(dialog.MutedFg).Padding(0, 1)
)

// View renders the popup, or "" while it is closed.
func (m *Model) View() string {
	if !m.Visible() {
		return ""
	}
	var body []string
	if m.connectable() {
		body = append(body, m.tabs())
		if m.tab == tabConnect {
			// Rows left for the pane: the frame, the tab row and the footer.
			body = append(body, m.pane.View(m.width-frameColumns, m.height-frameRows-2))
		} else {
			body = append(body, m.download(""))
		}
	} else {
		reason := UnavailableText
		if m.pane != nil {
			reason = m.pane.Unavailable()
		}
		body = append(body, m.download(reason))
	}
	body = append(body, m.footer())
	content := lipgloss.JoinVertical(lipgloss.Left, body...)

	// A clipped code does not scan, so a popup that cannot fit whole falls
	// back to the addresses alone.
	if lipgloss.Width(content)+frameColumns > m.width || lipgloss.Height(content)+frameRows > m.height {
		content = m.linksOnly()
	}
	width := lipgloss.Width(content)
	title := dialog.TitleStyle.Width(width).Render(" Connect the SwarmCLI app ")
	return dialog.BorderStyle.Width(width + dialog.BoxInsetColumns).
		Render(lipgloss.JoinVertical(lipgloss.Left, title, lipgloss.NewStyle().Padding(0, 1).Render(content)))
}

func (m *Model) tabs() string {
	names := []string{"Connect this swarm", "Get the app"}
	parts := make([]string, len(names))
	for i, name := range names {
		if tab(i) == m.tab {
			parts[i] = dialog.SelectedStyle.Render(name)
		} else {
			parts[i] = tabStyle.Render(name)
		}
	}
	return strings.Join(parts, " ") + gap + greyStyle.Render("tab switches")
}

// download is the code that gets the app, with what it is beside it, and the
// reason connecting is unavailable under that when there is one.
func (m *Model) download(unavailable string) string {
	lines := []string{
		headingStyle.Render("SwarmCLI for iPhone and Android"),
		textStyle.Render(wordwrap.String("Swarm health, logs and restarts from your phone. Free.", textColumn)),
		"",
		textStyle.Render(wordwrap.String("Scan with your phone for the app.", textColumn)),
		"",
		greyStyle.Render(wordwrap.String("No swarm at hand? The app has a built-in demo swarm.", textColumn)),
	}
	if unavailable != "" {
		lines = append(lines,
			"",
			greyStyle.Render(strings.Repeat("─", textColumn)),
			greyStyle.Render("Connect this swarm"),
			greyStyle.Render(wordwrap.String(unavailable, textColumn)),
		)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, m.downloadQR, gap, strings.Join(lines, "\n"))
}

func (m *Model) footer() string {
	hint := dialog.KeyStyle.Render("<esc>") + greyStyle.Render(" close")
	if m.connectable() {
		hint = dialog.KeyStyle.Render("<tab>") + greyStyle.Render(" switch  ") + hint
	}
	return textStyle.Render("More information: "+AppURL) + gap + hint
}

// linksOnly is the popup for a terminal too small to show a code whole.
func (m *Model) linksOnly() string {
	return strings.Join([]string{
		textStyle.Render("Get the SwarmCLI app: " + DownloadURL),
		textStyle.Render("More information: " + AppURL),
		"",
		greyStyle.Render("Enlarge the terminal to show a QR code."),
		dialog.KeyStyle.Render("<esc>") + greyStyle.Render(" close"),
	}, "\n")
}
