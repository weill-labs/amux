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

type statusPaneData struct {
	fakePaneData
	host       string
	task       string
	color      string
	connStatus string
	idle       bool
	inCopyMode bool
	search     string
}

func (p *statusPaneData) Host() string           { return p.host }
func (p *statusPaneData) Task() string           { return p.task }
func (p *statusPaneData) Color() string          { return p.color }
func (p *statusPaneData) Idle() bool             { return p.idle }
func (p *statusPaneData) ConnStatus() string     { return p.connStatus }
func (p *statusPaneData) InCopyMode() bool       { return p.inCopyMode }
func (p *statusPaneData) CopyModeSearch() string { return p.search }

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

func TestPadOrTrim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		s     string
		width int
		want  string
	}{
		{name: "non-positive width", s: "hello", width: 0, want: ""},
		{name: "pads shorter strings", s: "hi", width: 4, want: "hi  "},
		{name: "returns exact width", s: "word", width: 4, want: "word"},
		{name: "trims longer strings", s: "abcdef", width: 3, want: "abc"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := padOrTrim(tt.s, tt.width); got != tt.want {
				t.Fatalf("padOrTrim(%q, %d) = %q, want %q", tt.s, tt.width, got, tt.want)
			}
		})
	}
}

func TestPaneOverlayPlacementAndRendering(t *testing.T) {
	t.Parallel()

	t.Run("placement", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			cell  *mux.LayoutCell
			label string
			want  string
		}{
			{name: "nil cell", label: "a", want: ""},
			{name: "empty label", cell: mkLeaf(1, 0, 0, 8, 4), want: ""},
			{name: "narrow cell uses first rune", cell: mkLeaf(1, 4, 3, 2, 4), label: "zoom", want: "z"},
			{name: "wide cell uses badge", cell: mkLeaf(1, 0, 0, 8, 4), label: "b", want: "[b]"},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, _, _ := paneOverlayPlacement(tt.cell, tt.label)
				if got != tt.want {
					t.Fatalf("paneOverlayPlacement(%v, %q) badge = %q, want %q", tt.cell, tt.label, got, tt.want)
				}
			})
		}
	})

	t.Run("builds grid cells and ansi output", func(t *testing.T) {
		t.Parallel()

		root := buildFourPane(19, 9)
		labels := []PaneOverlayLabel{
			{PaneID: 3, Label: "x"},
			{PaneID: 99, Label: "ignored"},
			{PaneID: 4, Label: ""},
		}
		lookup := func(paneID uint32) PaneData {
			if paneID != 3 {
				return nil
			}
			return &statusPaneData{
				fakePaneData: fakePaneData{id: 3, name: "pane-3"},
				color:        config.TextColorHex,
			}
		}

		grid := NewScreenGrid(19, 9)
		buildPaneOverlayCells(grid, root, lookup, labels)
		badge, x, y := paneOverlayPlacement(root.FindByPaneID(3), "x")
		if got := grid.Get(x, y).Char + grid.Get(x+1, y).Char + grid.Get(x+2, y).Char; got != badge {
			t.Fatalf("badge text = %q, want %q", got, badge)
		}

		var buf strings.Builder
		renderPaneOverlay(&buf, root, lookup, labels)
		rendered := buf.String()
		if !strings.Contains(rendered, cursorPos(y+1, x+1)) {
			t.Fatalf("rendered pane overlay missing cursor position: %q", rendered)
		}
		if !strings.Contains(rendered, badge) {
			t.Fatalf("rendered pane overlay missing badge %q: %q", badge, rendered)
		}
	})
}

func TestRenderPaneStatusVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		active   bool
		pd       *statusPaneData
		contains []string
	}{
		{
			name:   "active copy mode remote connected",
			active: true,
			pd: &statusPaneData{
				fakePaneData: fakePaneData{id: 1, name: "pane-1"},
				host:         "gpu",
				task:         "tail -f",
				color:        config.CatppuccinMocha[0],
				connStatus:   "connected",
				inCopyMode:   true,
				search:       "/panic",
			},
			contains: []string{"●", "[pane-1]", "[copy] /panic", "@gpu", "⚡", "tail -f"},
		},
		{
			name:   "inactive idle disconnected",
			active: false,
			pd: &statusPaneData{
				fakePaneData: fakePaneData{id: 2, name: "pane-2"},
				host:         "edge",
				color:        config.CatppuccinMocha[1],
				connStatus:   "disconnected",
				idle:         true,
			},
			contains: []string{"◇", "[pane-2]", "@edge", "✕"},
		},
		{
			name:   "inactive busy reconnecting",
			active: false,
			pd: &statusPaneData{
				fakePaneData: fakePaneData{id: 3, name: "pane-3"},
				host:         mux.DefaultHost,
				task:         "htop",
				color:        config.CatppuccinMocha[2],
				connStatus:   "reconnecting",
			},
			contains: []string{"○", "[pane-3]", "⟳", "htop"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder
			renderPaneStatus(&buf, mkLeaf(tt.pd.id, 0, 0, 48, 4), tt.active, tt.pd)
			rendered := buf.String()
			for _, want := range tt.contains {
				if !strings.Contains(rendered, want) {
					t.Fatalf("renderPaneStatus missing %q:\n%s", want, rendered)
				}
			}
		})
	}
}

func TestRenderGlobalBarAndTruncateRunes(t *testing.T) {
	frozen := time.Date(2026, time.March, 22, 9, 41, 0, 0, time.UTC)
	prevTimeNow := timeNow
	timeNow = func() time.Time { return frozen }
	defer func() { timeNow = prevTimeNow }()

	if got := truncateRunes("abcdef", 4); got != "abc…" {
		t.Fatalf("truncateRunes = %q, want %q", got, "abc…")
	}

	var buf strings.Builder
	renderGlobalBar(&buf, "main", 3, 36, 8, []WindowInfo{
		{Index: 1, Name: "dev", IsActive: true},
		{Index: 2, Name: "logs", IsActive: false},
	}, "very long command feedback that must truncate")
	rendered := buf.String()
	for _, want := range []string{"[1:dev]", "2:logs", "very lon…"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderGlobalBar missing %q:\n%s", want, rendered)
		}
	}

	buf.Reset()
	renderGlobalBar(&buf, "main", 7, 30, 8, nil, "")
	rendered = buf.String()
	for _, want := range []string{" main ", "7 panes", "09:41"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderGlobalBar missing %q:\n%s", want, rendered)
		}
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
