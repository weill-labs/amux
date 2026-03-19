package render

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/mux"
)

// fakePaneData implements PaneData with a fixed screen string.
type fakePaneData struct {
	id           uint32
	name         string
	screen       string
	minimized    bool
	cursorHidden bool
}

func (f *fakePaneData) RenderScreen(bool) string { return f.screen }
func (f *fakePaneData) CellAt(col, row int, active bool) ScreenCell {
	return ScreenCell{Char: " ", Width: 1}
}
func (f *fakePaneData) CursorPos() (int, int)  { return 0, 0 }
func (f *fakePaneData) CursorHidden() bool     { return f.cursorHidden }
func (f *fakePaneData) ID() uint32             { return f.id }
func (f *fakePaneData) Name() string           { return f.name }
func (f *fakePaneData) Host() string           { return "local" }
func (f *fakePaneData) Task() string           { return "" }
func (f *fakePaneData) Color() string          { return "f5e0dc" }
func (f *fakePaneData) Minimized() bool        { return f.minimized }
func (f *fakePaneData) Idle() bool             { return true }
func (f *fakePaneData) ConnStatus() string     { return "" }
func (f *fakePaneData) InCopyMode() bool       { return false }
func (f *fakePaneData) CopyModeSearch() string { return "" }
func (f *fakePaneData) HasCursorBlock() bool   { return false }

type cursorPaneData struct {
	id    uint32
	name  string
	color string
	emu   mux.TerminalEmulator
}

func (e *cursorPaneData) RenderScreen(active bool) string {
	if !active {
		return e.emu.RenderWithoutCursorBlock()
	}
	return e.emu.Render()
}

func (e *cursorPaneData) CellAt(col, row int, active bool) ScreenCell {
	cell := CellFromUV(e.emu.CellAt(col, row))
	if active {
		return cell
	}
	cursorX, cursorY := e.emu.CursorPosition()
	if col != cursorX || row != cursorY {
		return cell
	}
	if cell.Style.Attrs&uv.AttrReverse == 0 || cell.Char != " " {
		return cell
	}
	w, _ := e.emu.Size()
	if col > 0 {
		if left := e.emu.CellAt(col-1, row); left != nil && left.Style.Attrs&uv.AttrReverse != 0 {
			return cell
		}
	}
	if col < w-1 {
		if right := e.emu.CellAt(col+1, row); right != nil && right.Style.Attrs&uv.AttrReverse != 0 {
			return cell
		}
	}
	cell.Style.Attrs &^= uv.AttrReverse
	return cell
}

func (e *cursorPaneData) CursorPos() (int, int)  { return e.emu.CursorPosition() }
func (e *cursorPaneData) CursorHidden() bool     { return e.emu.CursorHidden() }
func (e *cursorPaneData) HasCursorBlock() bool   { return e.emu.HasCursorBlock() }
func (e *cursorPaneData) ID() uint32             { return e.id }
func (e *cursorPaneData) Name() string           { return e.name }
func (e *cursorPaneData) Host() string           { return "local" }
func (e *cursorPaneData) Task() string           { return "" }
func (e *cursorPaneData) Color() string          { return e.color }
func (e *cursorPaneData) Minimized() bool        { return false }
func (e *cursorPaneData) Idle() bool             { return true }
func (e *cursorPaneData) ConnStatus() string     { return "" }
func (e *cursorPaneData) InCopyMode() bool       { return false }
func (e *cursorPaneData) CopyModeSearch() string { return "" }

func TestMinimizedPaneHidesCursor(t *testing.T) {
	t.Parallel()

	// Two panes stacked vertically: pane-1 (top, minimized), pane-2 (bottom)
	width, height := 40, 10
	top := mux.NewLeaf(&mux.Pane{ID: 1, Meta: mux.PaneMeta{
		Name: "pane-1", Minimized: true,
	}}, 0, 0, width, mux.StatusLineRows)
	bottom := mux.NewLeaf(&mux.Pane{ID: 2, Meta: mux.PaneMeta{
		Name: "pane-2",
	}}, 0, mux.StatusLineRows, width, height-mux.StatusLineRows)
	root := &mux.LayoutCell{
		X: 0, Y: 0, W: width, H: height,
		Dir:      mux.SplitHorizontal,
		Children: []*mux.LayoutCell{top, bottom},
	}
	top.Parent = root
	bottom.Parent = root

	comp := NewCompositor(width, height+GlobalBarHeight, "test")

	lookup := func(id uint32) PaneData {
		switch id {
		case 1:
			return &fakePaneData{id: 1, name: "pane-1", screen: "", minimized: true}
		case 2:
			return &fakePaneData{id: 2, name: "pane-2", screen: "hello"}
		}
		return nil
	}

	// Active pane is the minimized pane-1
	output := comp.RenderFull(root, 1, lookup)

	// Should NOT contain ShowCursor since the active pane is minimized
	if strings.Contains(output, ShowCursor) {
		t.Error("cursor should be hidden when active pane is minimized")
	}
}

func TestRenderCursorEdgeCases(t *testing.T) {
	t.Parallel()

	width, height := 40, 5
	root := mux.NewLeaf(&mux.Pane{ID: 1, Meta: mux.PaneMeta{Name: "pane-1"}}, 0, 0, width, height)

	tests := []struct {
		name        string
		activeID    uint32
		lookup      func(uint32) PaneData
		wantVisible bool
	}{
		{
			name:     "no active pane",
			activeID: 0,
			lookup: func(id uint32) PaneData {
				return &fakePaneData{id: 1, name: "pane-1", screen: "hello"}
			},
			wantVisible: true,
		},
		{
			name:     "active pane not in layout",
			activeID: 99,
			lookup: func(id uint32) PaneData {
				if id == 1 {
					return &fakePaneData{id: 1, name: "pane-1", screen: "hello"}
				}
				return nil
			},
			wantVisible: true,
		},
		{
			name:     "lookup returns nil for active pane",
			activeID: 1,
			lookup: func(id uint32) PaneData {
				return nil
			},
			wantVisible: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			comp := NewCompositor(width, height+GlobalBarHeight, "test")
			output := comp.RenderFull(root, tt.activeID, tt.lookup)
			hasCursor := strings.Contains(output, ShowCursor)
			if hasCursor != tt.wantVisible {
				t.Errorf("cursor visible = %v, want %v", hasCursor, tt.wantVisible)
			}
		})
	}
}

func TestRenderCursorIgnoresOffCursorReverseVideoSpace(t *testing.T) {
	t.Parallel()

	width, height := 40, 5
	root := mux.NewLeaf(&mux.Pane{ID: 1, Meta: mux.PaneMeta{Name: "pane-1"}}, 0, 0, width, height)
	emu := mux.NewVTEmulator(width, height)
	if _, err := emu.Write([]byte("hello \033[7m \033[m")); err != nil {
		t.Fatalf("Write stale block: %v", err)
	}
	if _, err := emu.Write([]byte("\033[1;1H")); err != nil {
		t.Fatalf("Write cursor move: %v", err)
	}

	comp := NewCompositor(width, height+GlobalBarHeight, "test")
	output := comp.RenderFull(root, 1, func(id uint32) PaneData {
		if id != 1 {
			return nil
		}
		return &cursorPaneData{id: 1, name: "pane-1", color: "f5e0dc", emu: emu}
	})

	if !strings.Contains(output, ShowCursor) {
		t.Fatal("cursor should remain visible when reverse-video space is away from the cursor")
	}
}

func TestHexToANSI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hex  string
		want string
	}{
		{name: "short hex returns DimFg", hex: "abc", want: DimFg},
		{name: "empty hex returns DimFg", hex: "", want: DimFg},
		{name: "valid hex", hex: "ff8800", want: "\033[38;2;255;136;0m"},
		{name: "catppuccin rosewater", hex: "f5e0dc", want: "\033[38;2;245;224;220m"},
		{name: "black", hex: "000000", want: "\033[38;2;0;0;0m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hexToANSI(tt.hex)
			if got != tt.want {
				t.Errorf("hexToANSI(%q) = %q, want %q", tt.hex, got, tt.want)
			}
			// Verify caching: second call returns same result
			if got2 := hexToANSI(tt.hex); got2 != got {
				t.Errorf("cached result differs: %q vs %q", got, got2)
			}
		})
	}
}

func TestGlobalBarMultipleWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		windows []WindowInfo
		want    []string // substrings expected in output
	}{
		{
			name: "two windows",
			windows: []WindowInfo{
				{Index: 1, Name: "main", IsActive: true, Panes: 2},
				{Index: 2, Name: "logs", IsActive: false, Panes: 1},
			},
			want: []string{"[1:main]", "2:logs"},
		},
		{
			name: "three windows middle active",
			windows: []WindowInfo{
				{Index: 1, Name: "code", IsActive: false, Panes: 1},
				{Index: 2, Name: "test", IsActive: true, Panes: 3},
				{Index: 3, Name: "logs", IsActive: false, Panes: 1},
			},
			want: []string{"1:code", "[2:test]", "3:logs"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf strings.Builder
			renderGlobalBar(&buf, "test-session", 3, 120, 10, tt.windows, "")
			renderGlobalBar(&buf, "test-session", 3, 120, 10, tt.windows, "")
			output := buf.String()
			for _, s := range tt.want {
				if !strings.Contains(output, s) {
					t.Errorf("output missing %q", s)
				}
			}
		})
	}
}

func TestGlobalBarFillsFullWidth(t *testing.T) {
	t.Parallel()

	for _, width := range []int{80, 120, 200} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			t.Parallel()
			var buf strings.Builder
			renderGlobalBar(&buf, "default", 2, width, 0, nil, "")
			renderGlobalBar(&buf, "default", 2, width, 0, nil, "")
			grid := MaterializeGrid(buf.String(), width, 1)

			// width-1 accounts for MaterializeGrid trimming the trailing space.
			row := []rune(grid)
			if len(row) < width-1 {
				t.Errorf("global bar is %d cols, want >= %d\n  row: %q",
					len(row), width-1, string(row))
			}
		})
	}
}

func TestBlitPaneClipsToWidth(t *testing.T) {
	t.Parallel()

	// Two panes side by side: pane-1 (left, 10 cols) | pane-2 (right, 9 cols)
	// Total width = 10 + 1 (border) + 9 = 20, height = 3
	width, height := 20, 3
	left := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 10, height)
	right := mux.NewLeaf(&mux.Pane{ID: 2}, 11, 0, 9, height)
	root := &mux.LayoutCell{
		X: 0, Y: 0, W: width, H: height,
		Dir:      mux.SplitVertical,
		Children: []*mux.LayoutCell{left, right},
	}
	left.Parent = root
	right.Parent = root

	// Pane-1's emulator renders a line WIDER than its cell (15 chars in a 10-col cell).
	// This simulates a desync where the emulator width > cell width.
	longLine := "ABCDEFGHIJKLMNO" // 15 chars, wider than cell width of 10

	comp := NewCompositor(width, height+GlobalBarHeight, "test")

	lookup := func(id uint32) PaneData {
		switch id {
		case 1:
			return &fakePaneData{id: 1, name: "pane-1", screen: longLine, cursorHidden: true}
		case 2:
			// Empty content — overflow from pane-1 would be visible here
			return &fakePaneData{id: 2, name: "pane-2", screen: "", cursorHidden: true}
		}
		return nil
	}

	output := comp.RenderFull(root, 1, lookup)
	grid := MaterializeGrid(output, width, height+GlobalBarHeight)
	lines := strings.Split(grid, "\n")

	// Row 0 is the status line; row 1 is the content row.
	contentRow := []rune(lines[1])

	// Left pane (cols 0-9) should show the first 10 characters, clipped.
	leftContent := string(contentRow[:10])
	if leftContent != "ABCDEFGHIJ" {
		t.Errorf("left pane content = %q, want %q", leftContent, "ABCDEFGHIJ")
	}

	// Right pane region (col 11+) should be empty — no bleed from pane-1.
	for col := 11; col < len(contentRow); col++ {
		if contentRow[col] != ' ' {
			t.Errorf("pane-1 content bled into pane-2 at col %d: %q\n  full row: %q",
				col, string(contentRow[col]), string(contentRow))
			break
		}
	}
}
