package render

import (
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/weill-labs/amux/internal/mux"
)

func TestScreenGrid_SetGet(t *testing.T) {
	t.Parallel()
	g := NewScreenGrid(10, 5)

	// Default cell is space with width 1.
	cell := g.Get(0, 0)
	if cell.Char != " " || cell.Width != 1 {
		t.Errorf("default cell: Char=%q Width=%d, want ' ' 1", cell.Char, cell.Width)
	}

	// Set and get back.
	red := uv.Style{Fg: ansi.RGBColor{R: 255}}
	g.Set(3, 2, ScreenCell{Char: "A", Style: red, Width: 1})
	got := g.Get(3, 2)
	if got.Char != "A" || !got.Style.Equal(&red) || got.Width != 1 {
		t.Errorf("got Char=%q Width=%d, want Char=A Width=1", got.Char, got.Width)
	}

	// Out-of-bounds get returns default.
	if oob := g.Get(-1, 0); oob.Char != " " {
		t.Errorf("oob get: Char=%q, want ' '", oob.Char)
	}

	// Out-of-bounds set is silently ignored (no panic).
	g.Set(100, 100, ScreenCell{Char: "X", Width: 1})
}

func TestDiffGrid_NoChanges(t *testing.T) {
	t.Parallel()
	a := NewScreenGrid(5, 3)
	b := NewScreenGrid(5, 3)
	if changes := DiffGrid(a, b); len(changes) != 0 {
		t.Errorf("identical grids: got %d changes, want 0", len(changes))
	}
}

func TestDiffGrid_SingleCell(t *testing.T) {
	t.Parallel()
	a := NewScreenGrid(5, 3)
	b := NewScreenGrid(5, 3)
	b.Set(2, 1, ScreenCell{Char: "X", Width: 1})

	changes := DiffGrid(a, b)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	ch := changes[0]
	if ch.X != 2 || ch.Y != 1 || ch.Cell.Char != "X" {
		t.Errorf("change = (%d,%d) Char=%q, want (2,1) X", ch.X, ch.Y, ch.Cell.Char)
	}
}

func TestDiffGrid_FullRow(t *testing.T) {
	t.Parallel()
	a := NewScreenGrid(5, 3)
	b := NewScreenGrid(5, 3)
	for x := 0; x < 5; x++ {
		b.Set(x, 1, ScreenCell{Char: string(rune('A' + x)), Width: 1})
	}

	changes := DiffGrid(a, b)
	if len(changes) != 5 {
		t.Fatalf("got %d changes, want 5", len(changes))
	}
	for i, ch := range changes {
		if ch.Y != 1 || ch.X != i {
			t.Errorf("change[%d] = (%d,%d), want (%d,1)", i, ch.X, ch.Y, i)
		}
	}
}

func TestDiffGrid_NilPrev(t *testing.T) {
	t.Parallel()
	b := NewScreenGrid(3, 2)
	b.Set(1, 0, ScreenCell{Char: "X", Width: 1})

	changes := DiffGrid(nil, b)
	if len(changes) != 6 {
		t.Fatalf("nil prev: got %d changes, want 6", len(changes))
	}
}

func TestEmitDiff_CUP(t *testing.T) {
	t.Parallel()
	changes := []CellChange{
		{X: 3, Y: 1, Cell: ScreenCell{Char: "A", Width: 1}},
		{X: 7, Y: 3, Cell: ScreenCell{Char: "B", Width: 1}},
	}
	output := EmitDiff(changes)

	// Verify via VT emulator.
	emu := vt.NewSafeEmulator(10, 5)
	emu.Write([]byte(output))

	cellA := emu.CellAt(3, 1)
	if cellA == nil || cellA.Content != "A" {
		content := ""
		if cellA != nil {
			content = cellA.Content
		}
		t.Errorf("cell(3,1) = %q, want A", content)
	}
	cellB := emu.CellAt(7, 3)
	if cellB == nil || cellB.Content != "B" {
		content := ""
		if cellB != nil {
			content = cellB.Content
		}
		t.Errorf("cell(7,3) = %q, want B", content)
	}
}

func TestEmitDiff_StyleTransition(t *testing.T) {
	t.Parallel()
	red := uv.Style{Fg: ansi.RGBColor{R: 255}}
	blue := uv.Style{Fg: ansi.RGBColor{B: 255}}

	changes := []CellChange{
		{X: 0, Y: 0, Cell: ScreenCell{Char: "R", Style: red, Width: 1}},
		{X: 1, Y: 0, Cell: ScreenCell{Char: "B", Style: blue, Width: 1}},
	}
	output := EmitDiff(changes)

	emu := vt.NewSafeEmulator(10, 5)
	emu.Write([]byte(output))

	cellR := emu.CellAt(0, 0)
	if cellR == nil || cellR.Content != "R" {
		t.Fatal("cell(0,0) missing")
	}
	if cellR.Style.Fg == nil {
		t.Fatal("cell R has nil Fg")
	}
	r, g, b, _ := cellR.Style.Fg.RGBA()
	if r>>8 != 255 || g>>8 != 0 || b>>8 != 0 {
		t.Errorf("cell R fg = (%d,%d,%d), want (255,0,0)", r>>8, g>>8, b>>8)
	}

	cellB := emu.CellAt(1, 0)
	if cellB == nil || cellB.Content != "B" {
		t.Fatal("cell(1,0) missing")
	}
	if cellB.Style.Fg == nil {
		t.Fatal("cell B has nil Fg")
	}
	r, g, b, _ = cellB.Style.Fg.RGBA()
	if r>>8 != 0 || g>>8 != 0 || b>>8 != 255 {
		t.Errorf("cell B fg = (%d,%d,%d), want (0,0,255)", r>>8, g>>8, b>>8)
	}
}

func TestEmitDiff_BatchConsecutive(t *testing.T) {
	t.Parallel()
	// Three consecutive cells on row 2.
	changes := []CellChange{
		{X: 3, Y: 2, Cell: ScreenCell{Char: "A", Width: 1}},
		{X: 4, Y: 2, Cell: ScreenCell{Char: "B", Width: 1}},
		{X: 5, Y: 2, Cell: ScreenCell{Char: "C", Width: 1}},
	}
	output := EmitDiff(changes)

	// Count CUP escapes (\033[row;colH). Should be exactly 1.
	cups := countCUPs(output)
	if cups != 1 {
		t.Errorf("got %d CUP escapes, want 1 (output: %q)", cups, output)
	}

	// Verify content via emulator.
	emu := vt.NewSafeEmulator(10, 5)
	emu.Write([]byte(output))
	for i, want := range []string{"A", "B", "C"} {
		cell := emu.CellAt(3+i, 2)
		if cell == nil || cell.Content != want {
			t.Errorf("cell(%d,2) = %v, want %q", 3+i, cell, want)
		}
	}
}

// countCUPs counts CUP (\033[row;colH) escape sequences in s.
func countCUPs(s string) int {
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\033' || i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == ';') {
			j++
		}
		if j < len(s) && s[j] == 'H' {
			count++
		}
		i = j
	}
	return count
}

func TestCellFromUV(t *testing.T) {
	t.Parallel()

	// Nil cell returns default.
	sc := CellFromUV(nil)
	if sc.Char != " " || sc.Width != 1 {
		t.Errorf("nil: Char=%q Width=%d, want ' ' 1", sc.Char, sc.Width)
	}

	// Cell with content.
	c := &uv.Cell{Content: "X", Width: 1, Style: uv.Style{Attrs: uv.AttrBold}}
	sc = CellFromUV(c)
	if sc.Char != "X" || sc.Width != 1 || sc.Style.Attrs != uv.AttrBold {
		t.Errorf("cell: %+v", sc)
	}

	// Empty content becomes space.
	c2 := &uv.Cell{Content: "", Width: 1}
	sc2 := CellFromUV(c2)
	if sc2.Char != " " {
		t.Errorf("empty content: Char=%q, want ' '", sc2.Char)
	}
}

func TestHexToColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hex     string
		r, g, b uint8
		wantNil bool
	}{
		{name: "rosewater", hex: "f5e0dc", r: 245, g: 224, b: 220},
		{name: "dim", hex: "6c7086", r: 108, g: 112, b: 134},
		{name: "black", hex: "000000", r: 0, g: 0, b: 0},
		{name: "short", hex: "abc", wantNil: true},
		{name: "empty", hex: "", wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := hexToColor(tt.hex)
			if tt.wantNil {
				if c != nil {
					t.Errorf("hexToColor(%q) = %v, want nil", tt.hex, c)
				}
				return
			}
			if c == nil {
				t.Fatalf("hexToColor(%q) = nil", tt.hex)
			}
			r, g, b, _ := c.RGBA()
			// RGBA returns pre-multiplied uint16; shift to get uint8.
			if uint8(r>>8) != tt.r || uint8(g>>8) != tt.g || uint8(b>>8) != tt.b {
				t.Errorf("hexToColor(%q) RGBA = (%d,%d,%d), want (%d,%d,%d)",
					tt.hex, r>>8, g>>8, b>>8, tt.r, tt.g, tt.b)
			}
		})
	}
}

func TestEmitDiff_NonConsecutiveRows(t *testing.T) {
	t.Parallel()
	// Changes on different rows need separate CUPs.
	changes := []CellChange{
		{X: 0, Y: 0, Cell: ScreenCell{Char: "A", Width: 1}},
		{X: 0, Y: 2, Cell: ScreenCell{Char: "B", Width: 1}},
		{X: 0, Y: 4, Cell: ScreenCell{Char: "C", Width: 1}},
	}
	output := EmitDiff(changes)

	cups := countCUPs(output)
	if cups != 3 {
		t.Errorf("got %d CUP escapes, want 3", cups)
	}

	// Verify positions.
	emu := vt.NewSafeEmulator(10, 5)
	emu.Write([]byte(output))

	for _, tc := range []struct {
		x, y int
		want string
	}{
		{0, 0, "A"}, {0, 2, "B"}, {0, 4, "C"},
	} {
		cell := emu.CellAt(tc.x, tc.y)
		if cell == nil || cell.Content != tc.want {
			t.Errorf("cell(%d,%d) = %v, want %q", tc.x, tc.y, cell, tc.want)
		}
	}
}

func TestEmitDiff_Empty(t *testing.T) {
	t.Parallel()
	if output := EmitDiff(nil); output != "" {
		t.Errorf("nil changes: got %q, want empty", output)
	}
	if output := EmitDiff([]CellChange{}); output != "" {
		t.Errorf("empty changes: got %q, want empty", output)
	}
}

// Verify that ScreenCell.Equal detects style differences.
func TestScreenCell_EqualDetectsStyleDiff(t *testing.T) {
	t.Parallel()
	a := ScreenCell{Char: "X", Width: 1, Style: uv.Style{Fg: ansi.RGBColor{R: 255}}}
	b := ScreenCell{Char: "X", Width: 1, Style: uv.Style{Fg: ansi.RGBColor{B: 255}}}
	same := ScreenCell{Char: "X", Width: 1, Style: uv.Style{Fg: ansi.RGBColor{R: 255}}}

	if a.Equal(b) {
		t.Error("cells with different Fg should not be equal")
	}
	if !a.Equal(same) {
		t.Error("cells with same style should be equal")
	}
}

// Verify that DiffGrid detects style-only changes.
// --- Oracle regression tests ---
// These verify that RenderDiff applied to a display emulator produces the
// same visual result as RenderFull passed through MaterializeGrid.

// emuPaneData wraps a real VT emulator for oracle tests.
type emuPaneData struct {
	emu          *vt.SafeEmulator
	id           uint32
	name         string
	color        string
	minimized    bool
	cursorHidden bool
}

func (e *emuPaneData) RenderScreen(active bool) string { return e.emu.Render() }
func (e *emuPaneData) CellAt(col, row int, active bool) ScreenCell {
	return CellFromUV(e.emu.CellAt(col, row))
}
func (e *emuPaneData) CursorPos() (int, int)  { p := e.emu.CursorPosition(); return p.X, p.Y }
func (e *emuPaneData) CursorHidden() bool     { return e.cursorHidden }
func (e *emuPaneData) HasCursorBlock() bool   { return false }
func (e *emuPaneData) ID() uint32             { return e.id }
func (e *emuPaneData) Name() string           { return e.name }
func (e *emuPaneData) Host() string           { return "local" }
func (e *emuPaneData) Task() string           { return "" }
func (e *emuPaneData) Color() string          { return e.color }
func (e *emuPaneData) Minimized() bool        { return e.minimized }
func (e *emuPaneData) Idle() bool             { return true }
func (e *emuPaneData) ConnStatus() string     { return "" }
func (e *emuPaneData) InCopyMode() bool       { return false }
func (e *emuPaneData) CopyModeSearch() string { return "" }

// oracleCheck compares RenderDiff (applied to display emu) against RenderFull
// (materialized). Returns an error message if they don't match, empty string if OK.
func oracleCheck(comp *Compositor, display *vt.SafeEmulator, root *mux.LayoutCell, activeID uint32, lookup func(uint32) PaneData, width, height int) string {
	// Ground truth: RenderFull → MaterializeGrid
	full := comp.RenderFull(root, activeID, lookup, true)
	expected := MaterializeGrid(full, width, height)

	// Diff path: RenderDiff → feed to display emulator → read back
	diff := comp.RenderDiff(root, activeID, lookup)
	display.Write([]byte(diff))

	// Read display grid
	var actual strings.Builder
	for y := 0; y < height; y++ {
		if y > 0 {
			actual.WriteByte('\n')
		}
		var row strings.Builder
		for x := 0; x < width; x++ {
			cell := display.CellAt(x, y)
			if cell == nil || cell.Content == "" {
				row.WriteByte(' ')
			} else {
				row.WriteString(cell.Content)
			}
		}
		actual.WriteString(strings.TrimRight(row.String(), " "))
	}

	if expected != actual.String() {
		return "oracle mismatch:\n--- expected (RenderFull) ---\n" + expected + "\n--- actual (RenderDiff) ---\n" + actual.String()
	}
	return ""
}

func init() {
	// Freeze time for deterministic oracle tests (global bar shows HH:MM).
	frozen := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	timeNow = func() time.Time { return frozen }
}

func TestRenderDiff_InitialPaint(t *testing.T) {
	t.Parallel()
	width, height := 40, 6
	totalH := height + GlobalBarHeight

	pane1Emu := vt.NewSafeEmulator(40, height-mux.StatusLineRows)
	pane1Emu.Write([]byte("hello world"))

	root := mux.NewLeafByID(1, 0, 0, width, height)
	lookup := func(id uint32) PaneData {
		if id == 1 {
			return &emuPaneData{emu: pane1Emu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
		}
		return nil
	}

	comp := NewCompositor(width, totalH, "test")
	display := vt.NewSafeEmulator(width, totalH)

	if err := oracleCheck(comp, display, root, 1, lookup, width, totalH); err != "" {
		t.Error(err)
	}
}

func TestRenderDiff_PaneOutput(t *testing.T) {
	t.Parallel()
	width, height := 40, 6
	totalH := height + GlobalBarHeight

	pane1Emu := vt.NewSafeEmulator(40, height-mux.StatusLineRows)

	root := mux.NewLeafByID(1, 0, 0, width, height)
	lookup := func(id uint32) PaneData {
		if id == 1 {
			return &emuPaneData{emu: pane1Emu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
		}
		return nil
	}

	comp := NewCompositor(width, totalH, "test")
	display := vt.NewSafeEmulator(width, totalH)

	// Initial paint.
	diff := comp.RenderDiff(root, 1, lookup)
	display.Write([]byte(diff))

	// Feed output to pane emulator.
	pane1Emu.Write([]byte("hello world"))

	// Second render should diff correctly.
	if err := oracleCheck(comp, display, root, 1, lookup, width, totalH); err != "" {
		t.Error(err)
	}
}

func TestRenderDiff_FocusChange(t *testing.T) {
	t.Parallel()
	width, height := 41, 6
	totalH := height + GlobalBarHeight

	// Two panes side by side.
	pane1W := 20
	pane2W := 20
	contentH := height - mux.StatusLineRows
	pane1Emu := vt.NewSafeEmulator(pane1W, contentH)
	pane2Emu := vt.NewSafeEmulator(pane2W, contentH)
	pane1Emu.Write([]byte("left pane"))
	pane2Emu.Write([]byte("right pane"))

	left := mux.NewLeafByID(1, 0, 0, pane1W, height)
	right := mux.NewLeafByID(2, pane1W+1, 0, pane2W, height)
	root := &mux.LayoutCell{
		X: 0, Y: 0, W: width, H: height,
		Dir:      mux.SplitVertical,
		Children: []*mux.LayoutCell{left, right},
	}
	left.Parent = root
	right.Parent = root

	lookup := func(id uint32) PaneData {
		switch id {
		case 1:
			return &emuPaneData{emu: pane1Emu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
		case 2:
			return &emuPaneData{emu: pane2Emu, id: 2, name: "pane-2", color: "cba6f7", cursorHidden: true}
		}
		return nil
	}

	comp := NewCompositor(width, totalH, "test")
	display := vt.NewSafeEmulator(width, totalH)

	// Initial render with pane-1 active.
	diff := comp.RenderDiff(root, 1, lookup)
	display.Write([]byte(diff))

	// Switch focus to pane-2.
	if err := oracleCheck(comp, display, root, 2, lookup, width, totalH); err != "" {
		t.Error(err)
	}
}

func TestRenderDiff_Backspace(t *testing.T) {
	t.Parallel()
	width, height := 40, 6
	totalH := height + GlobalBarHeight

	pane1Emu := vt.NewSafeEmulator(40, height-mux.StatusLineRows)
	pane1Emu.Write([]byte("hello"))

	root := mux.NewLeafByID(1, 0, 0, width, height)
	lookup := func(id uint32) PaneData {
		if id == 1 {
			return &emuPaneData{emu: pane1Emu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
		}
		return nil
	}

	comp := NewCompositor(width, totalH, "test")
	display := vt.NewSafeEmulator(width, totalH)

	// Initial paint.
	diff := comp.RenderDiff(root, 1, lookup)
	display.Write([]byte(diff))

	// Backspace: erase the 'o'.
	pane1Emu.Write([]byte("\b \b"))

	if err := oracleCheck(comp, display, root, 1, lookup, width, totalH); err != "" {
		t.Error(err)
	}
}

func TestRenderDiff_Resize(t *testing.T) {
	t.Parallel()
	width, height := 40, 6
	totalH := height + GlobalBarHeight

	pane1Emu := vt.NewSafeEmulator(40, height-mux.StatusLineRows)
	pane1Emu.Write([]byte("content"))

	root := mux.NewLeafByID(1, 0, 0, width, height)
	lookup := func(id uint32) PaneData {
		if id == 1 {
			return &emuPaneData{emu: pane1Emu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
		}
		return nil
	}

	comp := NewCompositor(width, totalH, "test")
	display := vt.NewSafeEmulator(width, totalH)

	// Initial paint.
	diff := comp.RenderDiff(root, 1, lookup)
	display.Write([]byte(diff))

	// Resize — should clear prevGrid and force full repaint.
	newW, newH := 50, 8
	newTotalH := newH + GlobalBarHeight
	comp.Resize(newW, newTotalH)
	pane1Emu.Resize(50, newH-mux.StatusLineRows)
	root2 := mux.NewLeafByID(1, 0, 0, newW, newH)
	display2 := vt.NewSafeEmulator(newW, newTotalH)

	if err := oracleCheck(comp, display2, root2, 1, lookup, newW, newTotalH); err != "" {
		t.Error(err)
	}
}

func TestDiffGrid_StyleChange(t *testing.T) {
	t.Parallel()
	a := NewScreenGrid(3, 1)
	b := NewScreenGrid(3, 1)

	red := uv.Style{Fg: ansi.RGBColor{R: 255}}
	b.Set(1, 0, ScreenCell{Char: " ", Style: red, Width: 1})

	changes := DiffGrid(a, b)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1 (style-only change)", len(changes))
	}
	if !strings.Contains(changes[0].Cell.Style.String(), "38;2;255;0;0") {
		t.Errorf("style should be red fg: %q", changes[0].Cell.Style.String())
	}
}
