package render

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func TestChooserOverlayGuardsAndWindowing(t *testing.T) {
	t.Parallel()

	t.Run("guards draw nothing", func(t *testing.T) {
		t.Parallel()

		row := []ChooserOverlayRow{{Text: "a", Selectable: true}}
		tests := []struct {
			name    string
			width   int
			height  int
			overlay *ChooserOverlay
		}{
			{name: "nil overlay", width: 20, height: 10},
			{name: "tiny width", width: 8, height: 10, overlay: &ChooserOverlay{Title: "x", Rows: row}},
			{name: "tiny height", width: 20, height: 4, overlay: &ChooserOverlay{Title: "x", Rows: row}},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				grid := NewScreenGrid(tt.width, tt.height)
				buildChooserOverlayCells(grid, tt.overlay)
				if got := strings.TrimSpace(gridToText(grid)); got != "" {
					t.Fatalf("expected nothing drawn, got %q", got)
				}
			})
		}
	})

	t.Run("windows selected row into view", func(t *testing.T) {
		t.Parallel()

		start, end := chooserVisibleWindow(10, 8, 5)
		if start != 5 || end != 10 {
			t.Fatalf("chooserVisibleWindow(10,8,5) = (%d,%d), want (5,10)", start, end)
		}
		if 8 < start || 8 >= end {
			t.Fatalf("selected row 8 not within window [%d,%d)", start, end)
		}
	})

	t.Run("no windowing when rows fit", func(t *testing.T) {
		t.Parallel()

		start, end := chooserVisibleWindow(3, 1, 10)
		if start != 0 || end != 3 {
			t.Fatalf("chooserVisibleWindow(3,1,10) = (%d,%d), want (0,3)", start, end)
		}
	})
}

func TestChooserRowAtPoint(t *testing.T) {
	t.Parallel()

	overlay := &ChooserOverlay{
		Title: "choose-window",
		Rows: []ChooserOverlayRow{
			{Text: "1:amux (2 panes)", Selectable: true, Header: true},
			{Text: "  ● pane-1", Selectable: true},
			{Text: "  pane-2", Selectable: true},
		},
		Selected: 1,
	}
	const screenW, screenH = 60, 20

	_, bodyTop, ok := chooserChrome(screenH, overlay).computeLayout(screenW, screenH)
	if !ok {
		t.Fatal("chooser layout should fit")
	}

	// Click the second list row (bodyTop is the first row).
	if absRow, onRow, inside := ChooserRowAtPoint(screenW, screenH, overlay, screenW/2, bodyTop+1); !inside || !onRow || absRow != 1 {
		t.Fatalf("click row 1 = (abs=%d, onRow=%v, inside=%v), want (1,true,true)", absRow, onRow, inside)
	}

	// Click the top border: inside the box, but not on a row.
	if _, onRow, inside := ChooserRowAtPoint(screenW, screenH, overlay, screenW/2, 0); inside && onRow {
		t.Fatal("a point above the modal should not register as a row")
	}

	// Click far outside the modal.
	if _, _, inside := ChooserRowAtPoint(screenW, screenH, overlay, 0, 0); inside {
		t.Fatal("click at (0,0) should be outside the modal")
	}

	// nil overlay.
	if _, _, inside := ChooserRowAtPoint(screenW, screenH, nil, screenW/2, bodyTop); inside {
		t.Fatal("nil overlay should never be inside")
	}
}

// TestChooserChromeFitsTallList is the regression guard for the rowLimit
// off-by-overhead bug: a list far taller than the screen must still window
// down and render, never collapse to a blank box.
func TestChooserChromeFitsTallList(t *testing.T) {
	t.Parallel()

	rows := make([]ChooserOverlayRow, 50)
	for i := range rows {
		rows[i] = ChooserOverlayRow{Text: "row", Selectable: true}
	}
	overlay := &ChooserOverlay{
		Title:    "Choose",
		Rows:     rows,
		Selected: 40,
		Toggle:   &ChooserToggle{Options: []string{"Tree", "Window"}, Selected: 0},
	}
	const w, h = 80, 24

	g := NewScreenGrid(w, h)
	rect := chooserChrome(h, overlay).place(g, defaultDialogStyles())
	if rect.W <= 0 || rect.H <= 0 {
		t.Fatal("chooser must still render with a tall list (regression: blank box)")
	}
	if rect.H > h-chooserModalMinMargin*2 {
		t.Fatalf("box height %d exceeds the screen budget %d", rect.H, h-chooserModalMinMargin*2)
	}
	// The selected row must be within the windowed slice that was drawn.
	if got := ChooserRowLimit(h); got < 1 {
		t.Fatalf("ChooserRowLimit(%d) = %d, want >= 1", h, got)
	}
}

func TestChooserOverlayRendersChrome(t *testing.T) {
	t.Parallel()

	overlay := &ChooserOverlay{
		Title: "choose-window",
		Query: "pane",
		Rows: []ChooserOverlayRow{
			{Text: "0:main", Selectable: true},
			{Text: "section", Selectable: false},
			{Text: "1:logs", Selectable: true},
		},
		Selected: 2,
	}

	grid := NewScreenGrid(60, 16)
	buildChooserOverlayCells(grid, overlay)
	text := gridToText(grid)
	for _, want := range []string{"╭", "╮", "╰", "╯", "choose-window", "> pane", "0:main", "1:logs", "esc close"} {
		if !strings.Contains(text, want) {
			t.Errorf("chooser grid missing %q:\n%s", want, text)
		}
	}

	var buf strings.Builder
	renderChooserOverlay(&buf, 60, 16, overlay)
	rendered := buf.String()
	if !strings.Contains(rendered, "choose-window") || !strings.Contains(rendered, "1:logs") {
		t.Errorf("rendered chooser missing content:\n%s", rendered)
	}
}

func TestRenderGlobalBarAndTruncateRunes(t *testing.T) {
	t.Parallel()

	frozen := time.Date(2026, time.March, 22, 9, 41, 0, 0, time.UTC)

	if got := truncateRunes("abcdef", 4); got != "abc…" {
		t.Fatalf("truncateRunes = %q, want %q", got, "abc…")
	}

	var buf strings.Builder
	renderGlobalBar(&buf, "main", 3, 36, 8, []WindowInfo{
		{Index: 1, Name: "dev", IsActive: true},
		{Index: 2, Name: "logs", IsActive: false},
	}, "very long command feedback that must truncate", frozen)
	rendered := MaterializeGrid(buf.String(), 36, 1)
	for _, want := range []string{"1:dev", "2:logs", "very long …"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderGlobalBar missing %q:\n%s", want, rendered)
		}
	}

	buf.Reset()
	renderGlobalBar(&buf, "main", 7, 30, 8, nil, "", frozen)
	rendered = MaterializeGrid(buf.String(), 30, 1)
	for _, want := range []string{" main ", "7 panes", "09:41"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderGlobalBar missing %q:\n%s", want, rendered)
		}
	}
}

func TestRenderGlobalBarPlacesHelpToggleNextToClock(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	frozen := time.Date(2026, time.March, 22, 9, 41, 0, 0, time.UTC)

	renderGlobalBar(&buf, "main", 3, 44, 0, nil, "", frozen)
	rendered := MaterializeGrid(buf.String(), 44, 1)

	sessionIdx := strings.Index(rendered, "main")
	panesIdx := strings.Index(rendered, "3 panes")
	helpIdx := strings.Index(rendered, "? help")
	clockIdx := strings.Index(rendered, "09:41")
	if sessionIdx < 0 || panesIdx < 0 || helpIdx < 0 || clockIdx < 0 {
		t.Fatalf("global bar missing expected segments: %q", rendered)
	}
	if !(sessionIdx < panesIdx && panesIdx < helpIdx && helpIdx < clockIdx) {
		t.Fatalf("global bar should place ? help to the right of the pane count and left of the clock: %q", rendered)
	}
}

func TestGlobalBarWindowAtColumn(t *testing.T) {
	t.Parallel()

	windows := []WindowInfo{
		{Index: 1, Name: "main", IsActive: false},
		{Index: 2, Name: "bugs", IsActive: true},
		{Index: 3, Name: "docs", IsActive: false},
	}
	tabs := buildGlobalBarWindowTabs(windows)
	if len(tabs) != 3 {
		t.Fatalf("len(tabs) = %d, want 3", len(tabs))
	}

	tests := []struct {
		name string
		x    int
		want int
		ok   bool
	}{
		{name: "first tab", x: tabs[0].start, want: 1, ok: true},
		{name: "active tab start", x: tabs[1].start, want: 2, ok: true},
		{name: "third tab", x: tabs[2].end - 1, want: 3, ok: true},
		{name: "space after first tab", x: tabs[0].end, ok: false},
		{name: "separator after tabs", x: tabs[2].end + 1, ok: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := GlobalBarWindowAtColumn(windows, tt.x)
			if ok != tt.ok {
				t.Fatalf("GlobalBarWindowAtColumn(..., %d) ok = %v, want %v", tt.x, ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if got.Index != tt.want {
				t.Fatalf("GlobalBarWindowAtColumn(..., %d) = %d, want %d", tt.x, got.Index, tt.want)
			}
		})
	}

	if _, ok := GlobalBarWindowAtColumn([]WindowInfo{{Index: 1, Name: "solo", IsActive: true}}, globalBarTitlePrefixVisibleWidth); ok {
		t.Fatal("single-window global bar should not expose a clickable tab")
	}

	hiddenHelpTabs := buildGlobalBarWindowTabs(windows)
	if got, ok := GlobalBarWindowAtColumn(windows, hiddenHelpTabs[1].start); !ok || got.Index != 2 {
		t.Fatalf("GlobalBarWindowAtColumn(..., %d) = (%d, %v), want (2, true)", hiddenHelpTabs[1].start, got.Index, ok)
	}
	frozen := time.Date(2026, time.March, 22, 9, 41, 0, 0, time.UTC)
	if GlobalBarHelpToggleAtColumn(globalBarTitlePrefixVisibleWidth, 44, 3, true, frozen) {
		t.Fatal("GlobalBarHelpToggleAtColumn should ignore the old left-side help columns")
	}
	var buf strings.Builder
	renderGlobalBar(&buf, "main", 3, 44, 0, nil, "", frozen)
	rendered := MaterializeGrid(buf.String(), 44, 1)
	helpStart := strings.Index(rendered, "? help")
	if helpStart < 0 {
		t.Fatalf("rendered global bar missing ? help: %q", rendered)
	}
	if !GlobalBarHelpToggleAtColumn(helpStart, 44, 3, true, frozen) {
		t.Fatal("GlobalBarHelpToggleAtColumn should match the rendered right-side help segment")
	}
	if GlobalBarHelpToggleAtColumn(helpStart, 44, 3, false, frozen) {
		t.Fatal("GlobalBarHelpToggleAtColumn should ignore clicks when help is hidden")
	}
}

func TestGlobalBarWindowDropTargetAtColumn(t *testing.T) {
	t.Parallel()

	windows := []WindowInfo{
		{Index: 1, Name: "main", IsActive: true},
		{Index: 2, Name: "bugs", IsActive: false},
		{Index: 3, Name: "docs", IsActive: false},
	}
	tabs := buildGlobalBarWindowTabs(windows)
	if len(tabs) != 3 {
		t.Fatalf("len(tabs) = %d, want 3", len(tabs))
	}

	tests := []struct {
		name      string
		source    int
		x         int
		wantDest  int
		wantCol   int
		wantMatch bool
	}{
		{
			name:      "left half keeps source before hovered tab",
			source:    1,
			x:         tabs[1].start,
			wantDest:  1,
			wantCol:   tabs[1].start - 1,
			wantMatch: true,
		},
		{
			name:      "right half moves source after hovered tab",
			source:    1,
			x:         tabs[1].end - 1,
			wantDest:  2,
			wantCol:   tabs[1].end,
			wantMatch: true,
		},
		{
			name:      "left half of first tab moves later source to front",
			source:    3,
			x:         tabs[0].start,
			wantDest:  1,
			wantCol:   tabs[0].start,
			wantMatch: true,
		},
		{
			name:      "source tab does not become a drop target",
			source:    2,
			x:         tabs[1].start + 1,
			wantMatch: false,
		},
		{
			name:      "space between tabs is ignored",
			source:    1,
			x:         tabs[0].end,
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := GlobalBarWindowDropTargetAtColumn(windows, tt.source, tt.x)
			if ok != tt.wantMatch {
				t.Fatalf("GlobalBarWindowDropTargetAtColumn(..., %d, %d) ok = %v, want %v", tt.source, tt.x, ok, tt.wantMatch)
			}
			if !tt.wantMatch {
				return
			}
			if got.DestinationIndex != tt.wantDest {
				t.Fatalf("destination index = %d, want %d", got.DestinationIndex, tt.wantDest)
			}
			if got.IndicatorColumn != tt.wantCol {
				t.Fatalf("indicator column = %d, want %d", got.IndicatorColumn, tt.wantCol)
			}
		})
	}

	if _, ok := GlobalBarWindowDropTargetAtColumn([]WindowInfo{{Index: 1, Name: "solo", IsActive: true}}, 1, globalBarTitlePrefixVisibleWidth); ok {
		t.Fatal("single-window global bar should not expose a reorder drop target")
	}
}

func TestCompositorHelpersAndClipLine(t *testing.T) {
	t.Parallel()

	c := NewCompositor(12, 6, "alpha")
	if got := c.LayoutHeight(); got != 5 {
		t.Fatalf("LayoutHeight = %d, want 5", got)
	}

	windows := []WindowInfo{{Index: 1, Name: "main", IsActive: true, Panes: 1}}
	c.SetWindows(windows)
	c.SetSessionName("beta")
	if c.sessionName != "beta" {
		t.Fatalf("sessionName = %q, want beta", c.sessionName)
	}
	if !reflect.DeepEqual(c.windows, windows) {
		t.Fatalf("windows = %+v, want %+v", c.windows, windows)
	}

	root := mkLeaf(1, 0, 0, 12, 5)
	emu := mux.NewVTEmulatorWithScrollback(12, 4, mux.DefaultScrollbackLines)
	if _, err := emu.Write([]byte("hello")); err != nil {
		t.Fatalf("emu.Write: %v", err)
	}
	pd := &cursorPaneData{id: 1, name: "pane-1", color: config.AccentColor(0), emu: emu}
	rendered := c.RenderDiff(root, 1, func(uint32) PaneData { return pd })
	if rendered == "" {
		t.Fatal("RenderDiff returned empty output")
	}
	if got := c.PrevGridText(); !strings.Contains(got, "hello") {
		t.Fatalf("PrevGridText = %q, want pane content", got)
	}
	c.ClearPrevGrid()
	if got := c.PrevGridText(); got != "" {
		t.Fatalf("PrevGridText after ClearPrevGrid = %q, want empty", got)
	}

	tests := []struct {
		name     string
		line     string
		maxWidth int
		want     string
	}{
		{name: "plain text", line: "hello", maxWidth: 3, want: "hel"},
		{name: "preserves ansi prefix", line: "\x1b[31mhello", maxWidth: 3, want: "\x1b[31mhel"},
		{name: "skips control chars", line: "a\tbcd", maxWidth: 2, want: "a\tb"},
		{name: "wide runes use display columns", line: "中中中", maxWidth: 5, want: "中中"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := clipLine(tt.line, tt.maxWidth); got != tt.want {
				t.Fatalf("clipLine(%q, %d) = %q, want %q", tt.line, tt.maxWidth, got, tt.want)
			}
		})
	}
}
