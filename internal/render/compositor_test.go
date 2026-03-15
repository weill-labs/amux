package render

import (
	"strings"
	"testing"

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
func (f *fakePaneData) CursorPos() (int, int)   { return 0, 0 }
func (f *fakePaneData) CursorHidden() bool      { return f.cursorHidden }
func (f *fakePaneData) ID() uint32              { return f.id }
func (f *fakePaneData) Name() string            { return f.name }
func (f *fakePaneData) Host() string            { return "local" }
func (f *fakePaneData) Task() string            { return "" }
func (f *fakePaneData) Color() string           { return "f5e0dc" }
func (f *fakePaneData) Minimized() bool         { return f.minimized }
func (f *fakePaneData) InCopyMode() bool        { return false }
func (f *fakePaneData) HasCursorBlock() bool    { return false }

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
		Dir:      mux.SplitVertical,
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
	output := string(comp.RenderFull(root, 1, lookup))

	// Should NOT contain ShowCursor since the active pane is minimized
	if strings.Contains(output, ShowCursor) {
		t.Error("cursor should be hidden when active pane is minimized")
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
		Dir:      mux.SplitHorizontal,
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
	grid := MaterializeGrid(string(output), width, height+GlobalBarHeight)
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
