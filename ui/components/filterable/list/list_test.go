// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package filterlist

import (
	"github.com/Eldara-Tech/swarmcli/v2/ui"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// --- Column computation ---

func TestComputeColWidths_EqualDistribution(t *testing.T) {
	cols := []ColumnDef{
		{Label: "A"}, {Label: "B"}, {Label: "C"}, {Label: "D"},
	}
	widths := computeColWidths(cols, 100)
	if len(widths) != 4 {
		t.Fatalf("expected 4 widths, got %d", len(widths))
	}
	total := 0
	for _, w := range widths {
		total += w
	}
	if total != 100 {
		t.Errorf("total width %d, want 100", total)
	}
	// Each should be 25
	for i, w := range widths {
		if w != 25 {
			t.Errorf("widths[%d] = %d, want 25", i, w)
		}
	}
}

func TestComputeColWidths_Percentage(t *testing.T) {
	cols := []ColumnDef{
		{Label: "A", Pct: 25},
		{Label: "B", Pct: 10},
		{Label: "C", Pct: 10},
		{Label: "D", Pct: 55},
	}
	widths := computeColWidths(cols, 200)
	if widths[0] != 50 {
		t.Errorf("widths[0] = %d, want 50", widths[0])
	}
	if widths[1] != 20 {
		t.Errorf("widths[1] = %d, want 20", widths[1])
	}
	if widths[2] != 20 {
		t.Errorf("widths[2] = %d, want 20", widths[2])
	}
	if widths[3] != 110 {
		t.Errorf("widths[3] = %d, want 110", widths[3])
	}
}

func TestComputeColWidths_MinWidth(t *testing.T) {
	// 3 equal cols in 30 width = 10 each. MinWidth 15 on col 2 should
	// steal from earlier cols.
	cols := []ColumnDef{
		{Label: "A"},
		{Label: "B"},
		{Label: "C", MinWidth: 15},
	}
	widths := computeColWidths(cols, 30)
	if widths[2] != 15 {
		t.Errorf("widths[2] = %d, want 15", widths[2])
	}
	total := 0
	for _, w := range widths {
		total += w
	}
	// Total should still be 30 (stolen from earlier columns)
	if total != 30 {
		t.Errorf("total = %d, want 30", total)
	}
}

func TestComputeColWidths_ColWidthsFunc(t *testing.T) {
	custom := func(w int) []int { return []int{w / 2, w / 2} }
	fl := FilterableList[string]{
		Viewport: viewport.New(100, 10),
		Header: &HeaderConfig{
			Columns:       []ColumnDef{{Label: "A"}, {Label: "B"}},
			ColWidthsFunc: custom,
		},
	}
	cw := fl.ColWidths()
	if len(cw) != 2 || cw[0] != 50 || cw[1] != 50 {
		t.Errorf("ColWidths() = %v, want [50 50]", cw)
	}
}

func TestComputeColWidths_ZeroWidth(t *testing.T) {
	cols := []ColumnDef{{Label: "A"}, {Label: "B"}}
	widths := computeColWidths(cols, 0)
	total := 0
	for _, w := range widths {
		total += w
	}
	if total != 80 {
		t.Errorf("total = %d, want 80 (default)", total)
	}
}

func TestComputeColWidths_Nil(t *testing.T) {
	widths := computeColWidths(nil, 100)
	if widths != nil {
		t.Errorf("expected nil, got %v", widths)
	}
}

// --- Header rendering ---

func TestRenderHeader_NilConfig(t *testing.T) {
	fl := FilterableList[string]{}
	if got := fl.RenderHeader(); got != "" {
		t.Errorf("RenderHeader() = %q, want empty", got)
	}
}

func TestRenderHeader_BasicLabels(t *testing.T) {
	fl := FilterableList[string]{
		Viewport: viewport.New(80, 10),
		Header: &HeaderConfig{
			Columns: []ColumnDef{{Label: "NAME"}, {Label: "ID"}},
		},
	}
	got := fl.RenderHeader()
	if got == "" {
		t.Fatal("expected non-empty header")
	}
	// Should contain both labels
	if !strings.Contains(got, "NAME") || !strings.Contains(got, "ID") {
		t.Errorf("header missing labels: %q", got)
	}
}

func TestRenderHeader_SortArrow(t *testing.T) {
	fl := FilterableList[string]{
		Viewport: viewport.New(80, 10),
		Header: &HeaderConfig{
			Columns:       []ColumnDef{{Label: "NAME"}, {Label: "ID"}},
			SortIndicator: func() (int, bool) { return 0, true },
		},
	}
	got := fl.RenderHeader()
	if !strings.Contains(got, "▲") {
		t.Errorf("expected ascending arrow in header: %q", got)
	}
}

func TestRenderHeader_DynamicLabel(t *testing.T) {
	fl := FilterableList[string]{
		Viewport: viewport.New(80, 10),
		Header: &HeaderConfig{
			Columns: []ColumnDef{{Label: "NAME"}, {Label: "ERROR"}},
			DynamicLabel: func(idx int, base string) string {
				if idx == 1 {
					return "ERROR: 3"
				}
				return ""
			},
		},
	}
	got := fl.RenderHeader()
	if !strings.Contains(got, "ERROR: 3") {
		t.Errorf("expected dynamic label in header: %q", got)
	}
}

// --- Footer rendering ---

func TestRenderFooter_NilConfig(t *testing.T) {
	fl := FilterableList[string]{}
	if got := fl.RenderFooter(); got != "" {
		t.Errorf("RenderFooter() = %q, want empty", got)
	}
}

func TestRenderFooter_Standard(t *testing.T) {
	fl := FilterableList[string]{
		Filtered: []string{"a", "b", "c"},
		Cursor:   2,
		Footer:   &FooterConfig{ItemLabel: "Item"},
	}
	got := fl.RenderFooter()
	if !strings.Contains(got, "Item 3 of 3") {
		t.Errorf("footer = %q, want 'Item 3 of 3'", got)
	}
}

func TestRenderFooter_Searching(t *testing.T) {
	fl := FilterableList[string]{
		Filtered: []string{"a"},
		Cursor:   0,
		Mode:     ModeSearching,
		Query:    "test",
		Footer:   &FooterConfig{ItemLabel: "Item"},
	}
	got := fl.RenderFooter()
	if !strings.Contains(got, "Filter (type then Enter): test") {
		t.Errorf("footer = %q, want filter line", got)
	}
}

func TestRenderFooter_EmptyList(t *testing.T) {
	fl := FilterableList[string]{
		Items:    []string{},
		Filtered: []string{},
		Footer:   &FooterConfig{ItemLabel: "Secret"},
	}
	got := fl.RenderFooter()
	if !strings.Contains(got, "No Secrets found") {
		t.Errorf("footer = %q, want 'No Secrets found'", got)
	}
}

func TestRenderFooter_Override(t *testing.T) {
	called := false
	fl := FilterableList[string]{
		Filtered: []string{"a"},
		Footer: &FooterConfig{
			Override: func(cursor, count int, mode ModeType, query string) string {
				called = true
				return "custom footer"
			},
		},
	}
	got := fl.RenderFooter()
	if !called {
		t.Error("Override was not called")
	}
	if got != "custom footer" {
		t.Errorf("footer = %q, want 'custom footer'", got)
	}
}

// --- Nil-slice safety ---

func TestHandleKey_NilFiltered(t *testing.T) {
	fl := FilterableList[string]{
		Viewport: viewport.New(80, 10),
	}
	// Should not panic
	fl.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	fl.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
}

func TestApplyFilter_NilItems(t *testing.T) {
	fl := FilterableList[string]{
		Viewport: viewport.New(80, 10),
		Match:    func(s string, q string) bool { return strings.Contains(s, q) },
	}
	// Items is nil
	fl.ApplyFilter()
	if fl.Filtered != nil {
		t.Errorf("expected Filtered to be nil when Items is nil, got %v", fl.Filtered)
	}
}

func TestEnsureCursorVisible_Empty(t *testing.T) {
	fl := FilterableList[string]{
		Viewport: viewport.New(80, 10),
	}
	// Should not panic
	fl.ensureCursorVisible()
}

func TestVisibleContent_NilItems(t *testing.T) {
	fl := FilterableList[string]{
		Viewport: viewport.New(80, 10),
	}
	// Items is nil — should show viewport content (loading placeholder)
	got := fl.VisibleContent(5)
	// Should not panic; return value depends on viewport state
	_ = got
}

// --- ColWidths ---

func TestColWidths_NilHeader(t *testing.T) {
	fl := FilterableList[string]{}
	if got := fl.ColWidths(); got != nil {
		t.Errorf("ColWidths() = %v, want nil", got)
	}
}

func TestColWidths_MatchesHeader(t *testing.T) {
	fl := FilterableList[string]{
		Viewport: viewport.New(100, 10),
		Header: &HeaderConfig{
			Columns: []ColumnDef{
				{Label: "A", Pct: 50},
				{Label: "B", Pct: 50},
			},
		},
	}
	cw := fl.ColWidths()
	if len(cw) != 2 {
		t.Fatalf("expected 2 widths, got %d", len(cw))
	}
	if cw[0] != 50 || cw[1] != 50 {
		t.Errorf("ColWidths() = %v, want [50 50]", cw)
	}
}

// --- RenderFramedView ---

func TestRenderFramedView_Full(t *testing.T) {
	fl := FilterableList[string]{
		Viewport: viewport.New(60, 15),
		Items:    []string{"alpha", "beta", "gamma"},
		Filtered: []string{"alpha", "beta", "gamma"},
		Header: &HeaderConfig{
			Columns: []ColumnDef{{Label: "NAME"}},
		},
		Footer: &FooterConfig{ItemLabel: "Item"},
		RenderItem: func(item string, selected bool, _ int) string {
			return item
		},
		Match: func(s string, q string) bool { return true },
	}
	framed, frame := fl.RenderFramedView("Test Title")
	if framed == "" {
		t.Fatal("expected non-empty framed view")
	}
	if !strings.Contains(framed, "Test Title") {
		t.Error("framed view missing title")
	}
	if !strings.Contains(framed, "NAME") {
		t.Error("framed view missing header")
	}
	if !strings.Contains(framed, "Item 1 of 3") {
		t.Error("framed view missing footer")
	}
	if frame.FrameWidth <= 0 {
		t.Error("frame width should be positive")
	}
}

// --- SetOuterSize / effectiveWidth / effectiveHeight ---

func TestEffectiveWidth_Fallback(t *testing.T) {
	fl := FilterableList[string]{}
	if got := fl.effectiveWidth(); got != 80 {
		t.Errorf("effectiveWidth() = %d, want 80", got)
	}
	fl.SetOuterSize(120, 30)
	if got := fl.effectiveWidth(); got != 120 {
		t.Errorf("effectiveWidth() = %d, want 120", got)
	}
	fl.Viewport = viewport.New(100, 10)
	if got := fl.effectiveWidth(); got != 100 {
		t.Errorf("effectiveWidth() = %d, want 100", got)
	}
}

func TestEffectiveHeight_Fallback(t *testing.T) {
	fl := FilterableList[string]{}
	if got := fl.effectiveHeight(); got != 20 {
		t.Errorf("effectiveHeight() = %d, want 20", got)
	}
	fl.SetOuterSize(120, 30)
	if got := fl.effectiveHeight(); got != 30 {
		t.Errorf("effectiveHeight() = %d, want 30", got)
	}
	fl.Viewport = viewport.New(100, 25)
	if got := fl.effectiveHeight(); got != 25 {
		t.Errorf("effectiveHeight() = %d, want 25", got)
	}
}

// --- RenderFramedView with empty list ---

func TestRenderFramedView_Empty(t *testing.T) {
	fl := FilterableList[string]{
		Viewport: viewport.New(60, 15),
		Items:    []string{},
		Filtered: []string{},
		Header: &HeaderConfig{
			Columns: []ColumnDef{{Label: "NAME"}},
		},
		Footer: &FooterConfig{ItemLabel: "Widget"},
		RenderItem: func(item string, selected bool, _ int) string {
			return item
		},
	}
	framed, frame := fl.RenderFramedView("Empty List")
	if !strings.Contains(framed, "No Widgets found") {
		t.Errorf("expected 'No Widgets found' in footer, got:\n%s", framed)
	}
	_ = frame
}

// --- ComputeFrameDimensions integration ---

func TestComputeFrameDimensions_WithHeaderFooter(t *testing.T) {
	header := "HEADER LINE"
	footer := "FOOTER LINE"
	spec := ui.ComputeFrameDimensions(80, 20, 0, 0, header, footer)
	// frameWidth = 80 + 4 = 84
	if spec.FrameWidth != 84 {
		t.Errorf("FrameWidth = %d, want 84", spec.FrameWidth)
	}
	// desiredContentLines = 20 - 2 - 1 (header) - 1 (footer) = 16
	if spec.DesiredContentLines != 16 {
		t.Errorf("DesiredContentLines = %d, want 16", spec.DesiredContentLines)
	}
}

// --- SelectLine ---

// selectLineList has three items; "b" is drawn on two lines.
func selectLineList() *FilterableList[string] {
	items := []string{"a", "b", "c"}
	return &FilterableList[string]{
		Items:    items,
		Filtered: items,
		RenderItem: func(item string, _ bool, _ int) string {
			if item == "b" {
				return "b\n  b-detail"
			}
			return item
		},
	}
}

func TestSelectLine_MapsLinesToItems(t *testing.T) {
	for line, want := range []int{0, 1, 1, 2} {
		f := selectLineList()
		if !f.SelectLine(line) {
			t.Fatalf("line %d: expected a row", line)
		}
		if f.Cursor != want {
			t.Errorf("line %d: cursor = %d, want %d", line, f.Cursor, want)
		}
	}
}

func TestSelectLine_CountsFromTheScrolledWindow(t *testing.T) {
	f := selectLineList()
	f.Viewport.YOffset = 2 // the window starts on b's second line
	if !f.SelectLine(1) || f.Cursor != 2 {
		t.Errorf("line 1 with YOffset 2: cursor = %d, want 2", f.Cursor)
	}
}

func TestSelectLine_NoRowLeavesCursor(t *testing.T) {
	for _, line := range []int{-1, 4, 50} {
		f := selectLineList()
		f.Cursor = 1
		if f.SelectLine(line) {
			t.Errorf("line %d: expected no row", line)
		}
		if f.Cursor != 1 {
			t.Errorf("line %d: cursor moved to %d", line, f.Cursor)
		}
	}
}

func TestSelectLine_EmptyList(t *testing.T) {
	f := &FilterableList[string]{}
	if f.SelectLine(0) {
		t.Error("expected no row in an empty list")
	}
}

func TestSelectLine_WithoutRenderItem(t *testing.T) {
	items := []string{"a", "b"}
	f := &FilterableList[string]{Items: items, Filtered: items}
	if !f.SelectLine(1) || f.Cursor != 1 {
		t.Errorf("cursor = %d, want 1", f.Cursor)
	}
}
