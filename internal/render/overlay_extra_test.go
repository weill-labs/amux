package render

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func TestChooserOverlayLayoutGuardsAndWindowing(t *testing.T) {
	t.Parallel()

	t.Run("guards", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			width   int
			height  int
			overlay *ChooserOverlay
		}{
			{name: "nil overlay", width: 20, height: 10},
			{name: "non-positive width", width: 0, height: 10, overlay: &ChooserOverlay{Title: "x"}},
			{name: "tiny width", width: 8, height: 10, overlay: &ChooserOverlay{Title: "x"}},
			{name: "tiny height", width: 20, height: 4, overlay: &ChooserOverlay{Title: "x"}},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				lines, styles, x, y := chooserOverlayLayout(tt.width, tt.height, tt.overlay)
				if lines != nil || styles != nil || x != 0 || y != 0 {
					t.Fatalf("chooserOverlayLayout(%d, %d, %+v) = (%v, %v, %d, %d), want nil layout", tt.width, tt.height, tt.overlay, lines, styles, x, y)
				}
			})
		}
	})

	t.Run("windows selected row into view", func(t *testing.T) {
		t.Parallel()

		rows := make([]ChooserOverlayRow, 10)
		for i := range rows {
			rows[i] = ChooserOverlayRow{Text: "row-" + string(rune('0'+i)), Selectable: true}
		}
		overlay := &ChooserOverlay{
			Title:    "choose-tree",
			Query:    "pane",
			Rows:     rows,
			Selected: 8,
		}

		lines, styles, x, y := chooserOverlayLayout(30, 12, overlay)
		if len(lines) != 8 {
			t.Fatalf("len(lines) = %d, want 8", len(lines))
		}
		if x <= 0 || y <= 0 {
			t.Fatalf("layout origin = (%d, %d), want centered positive coordinates", x, y)
		}
		if !strings.Contains(lines[2], "row-5") || !strings.Contains(lines[6], "row-9") {
			t.Fatalf("visible rows = %q .. %q, want rows 5 through 9", lines[2], lines[6])
		}
		if styles[5] != chooserRowSelected {
			t.Fatalf("selected row style = %q, want %q", styles[5], chooserRowSelected)
		}
	})
}

func TestChooserOverlayRenderAndCells(t *testing.T) {
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

	lines, _, x, y := chooserOverlayLayout(32, 12, overlay)
	if len(lines) == 0 {
		t.Fatal("chooserOverlayLayout returned no lines")
	}

	grid := NewScreenGrid(32, 12)
	buildChooserOverlayCells(grid, overlay)
	text := gridToText(grid)
	for _, line := range lines {
		if !strings.Contains(text, line) {
			t.Fatalf("grid text missing overlay line %q:\n%s", line, text)
		}
	}
	if got := grid.Get(x+1, y+2).Char; got != string(lines[2][1]) {
		t.Fatalf("grid cell at selected row = %q, want %q", got, string(lines[2][1]))
	}

	var buf strings.Builder
	renderChooserOverlay(&buf, 32, 12, overlay)
	rendered := buf.String()
	for row, line := range lines {
		if !strings.Contains(rendered, line) {
			t.Fatalf("rendered overlay missing line %q:\n%s", line, rendered)
		}
		if !strings.Contains(rendered, cursorPos(y+row+1, x+1)) {
			t.Fatalf("rendered overlay missing cursor position for row %d", row)
		}
	}
}

func TestRenderGlobalBarAndTruncateRunes(t *testing.T) {
	frozen := time.Date(2026, time.March, 22, 9, 41, 0, 0, time.UTC)

	if got := truncateRunes("abcdef", 4); got != "abc…" {
		t.Fatalf("truncateRunes = %q, want %q", got, "abc…")
	}

	var buf strings.Builder
	renderGlobalBar(&buf, "main", 3, 36, 8, []WindowInfo{
		{Index: 1, Name: "dev", IsActive: true},
		{Index: 2, Name: "logs", IsActive: false},
	}, "very long command feedback that must truncate", frozen)
	rendered := buf.String()
	for _, want := range []string{"1:dev", "2:logs", "very long …"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderGlobalBar missing %q:\n%s", want, rendered)
		}
	}

	buf.Reset()
	renderGlobalBar(&buf, "main", 7, 30, 8, nil, "", frozen)
	rendered = buf.String()
	for _, want := range []string{" main ", "7 panes", "09:41"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderGlobalBar missing %q:\n%s", want, rendered)
		}
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

	if _, ok := GlobalBarWindowAtColumn([]WindowInfo{{Index: 1, Name: "solo", IsActive: true}}, globalBarPrefixVisibleWidth); ok {
		t.Fatal("single-window global bar should not expose a clickable tab")
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
	pd := &cursorPaneData{id: 1, name: "pane-1", color: config.CatppuccinMocha[0], emu: emu}
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

func cursorPos(row, col int) string {
	return "\x1b[" + strconv.Itoa(row) + ";" + strconv.Itoa(col) + "H"
}
