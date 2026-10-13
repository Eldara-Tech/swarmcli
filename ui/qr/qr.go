// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

// Package qr draws QR codes as text, for a terminal to show and a phone camera
// to read.
package qr

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	qrcode "github.com/skip2/go-qrcode"
)

// style paints the code light-on-dark explicitly, so it scans the same on
// light and dark terminal themes. ToSmallString(false) draws light modules
// (and the quiet zone) as the glyph, dark modules as the gap between.
var style = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("#000000"))

// Render draws content as a half-block QR code, two modules per text row, or
// returns "" when there is nothing to encode. The error correction is the
// lowest level: the code is read off a screen, where a smaller code matters
// more than surviving damage.
func Render(content string) string {
	if content == "" {
		return ""
	}
	q, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		return ""
	}
	return style.Render(strings.TrimSuffix(q.ToSmallString(false), "\n"))
}
