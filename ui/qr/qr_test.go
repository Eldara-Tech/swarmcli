// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package qr

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	qrcode "github.com/skip2/go-qrcode"
	"github.com/stretchr/testify/require"
)

// The rendering is only useful if a phone reads back what went in, so the test
// rebuilds the module matrix from the half blocks and compares it with the
// encoder's own.
func TestRender_EncodesTheContent(t *testing.T) {
	const content = "https://swarmcli.io/get?ref=tui"
	q, err := qrcode.New(content, qrcode.Low)
	require.NoError(t, err)
	want := q.Bitmap() // true = dark module, 4-module quiet zone included

	rows := strings.Split(ansi.Strip(Render(content)), "\n")
	require.Len(t, rows, (len(want)+1)/2)
	got := make([][]bool, 0, len(want))
	for _, row := range rows {
		top, bottom := []bool{}, []bool{}
		for _, r := range row {
			switch r {
			case '█':
				top, bottom = append(top, false), append(bottom, false)
			case ' ':
				top, bottom = append(top, true), append(bottom, true)
			case '▀':
				top, bottom = append(top, false), append(bottom, true)
			case '▄':
				top, bottom = append(top, true), append(bottom, false)
			default:
				t.Fatalf("unexpected rune %q in QR", r)
			}
		}
		got = append(got, top, bottom)
	}
	require.Equal(t, want, got[:len(want)])
}

func TestRender_NothingToEncode(t *testing.T) {
	require.Empty(t, Render(""))
}
