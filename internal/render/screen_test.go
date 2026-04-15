package render

import (
	"fmt"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
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

// --- Oracle regression tests ---
// These verify that RenderDiff applied to a display emulator produces the
// same visual result as RenderFull passed through MaterializeGrid.

// emuPaneData wraps a real VT emulator for oracle tests.
type emuPaneData struct {
	emu          *vt.SafeEmulator
	id           uint32
	name         string
	color        string
	cursorHidden bool
	lead         bool
}

func (e *emuPaneData) RenderScreen(active bool) string { return e.emu.Render() }
func (e *emuPaneData) CellAt(col, row int, active bool) ScreenCell {
	return CellFromUV(e.emu.CellAt(col, row))
}
func (e *emuPaneData) CursorPos() (int, int)               { p := e.emu.CursorPosition(); return p.X, p.Y }
func (e *emuPaneData) CursorHidden() bool                  { return e.cursorHidden }
func (e *emuPaneData) HasCursorBlock() bool                { return false }
func (e *emuPaneData) ID() uint32                          { return e.id }
func (e *emuPaneData) Name() string                        { return e.name }
func (e *emuPaneData) TrackedPRs() []proto.TrackedPR       { return nil }
func (e *emuPaneData) TrackedIssues() []proto.TrackedIssue { return nil }
func (e *emuPaneData) Issue() string                       { return "" }
func (e *emuPaneData) Host() string                        { return "local" }
func (e *emuPaneData) Task() string                        { return "" }
func (e *emuPaneData) Color() string                       { return e.color }
func (e *emuPaneData) Idle() bool                          { return true }
func (e *emuPaneData) IsLead() bool                        { return e.lead }
func (e *emuPaneData) ConnStatus() string                  { return "" }
func (e *emuPaneData) InCopyMode() bool                    { return false }
func (e *emuPaneData) CopyModeSearch() string              { return "" }
func (e *emuPaneData) CopyModeOverlay() *proto.ViewportOverlay {
	return nil
}

// twoPaneLookup returns a lookup function for two side-by-side panes with
// standard test colors (rosewater for pane-1, mauve for pane-2).
func twoPaneLookup(pane1Emu, pane2Emu *vt.SafeEmulator) func(uint32) PaneData {
	return func(id uint32) PaneData {
		switch id {
		case 1:
			return &emuPaneData{emu: pane1Emu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
		case 2:
			return &emuPaneData{emu: pane2Emu, id: 2, name: "pane-2", color: "cba6f7", cursorHidden: true}
		}
		return nil
	}
}

// oobErrors returns one error string per out-of-bounds grid write recorded
// by the compositor's last render. Returns nil when there are no OOB writes.
func oobErrors(comp *Compositor) []string {
	g := comp.LastGrid()
	if g == nil {
		return nil
	}
	oob := g.OOBWrites()
	if len(oob) == 0 {
		return nil
	}
	errs := make([]string, len(oob))
	for i, w := range oob {
		errs[i] = fmt.Sprintf("ScreenGrid.Set: out-of-bounds (%d,%d) on %dx%d grid", w.X, w.Y, g.Width, g.Height)
	}
	return errs
}

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
	if errs := oobErrors(comp); len(errs) > 0 {
		return strings.Join(errs, "\n")
	}
	return ""
}

// frozenTime is the deterministic clock used in screen/compositor oracle tests.
var frozenTime = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

// newTestCompositor creates a compositor with frozen time and debug OOB recording.
func newTestCompositor(width, height int, sessionName string) *Compositor {
	c := NewCompositor(width, height, sessionName)
	c.TimeNow = func() time.Time { return frozenTime }
	c.debug = true
	return c
}

func TestScreenGrid_SetDebugRecordsOOB(t *testing.T) {
	t.Parallel()
	g := NewScreenGrid(10, 5)
	g.Debug = true

	// In-bounds write: no OOB recorded.
	g.Set(0, 0, ScreenCell{Char: "A", Width: 1})
	if got := g.Get(0, 0); got.Char != "A" {
		t.Fatalf("in-bounds write failed: got %q", got.Char)
	}
	if len(g.OOBWrites()) != 0 {
		t.Fatalf("expected 0 OOB writes after in-bounds Set, got %d", len(g.OOBWrites()))
	}

	// OOB writes are recorded, not panicked.
	g.Set(10, 0, ScreenCell{Char: "X", Width: 1})
	g.Set(0, 5, ScreenCell{Char: "X", Width: 1})
	g.Set(-1, 0, ScreenCell{Char: "X", Width: 1})
	g.Set(0, -1, ScreenCell{Char: "X", Width: 1})

	oob := g.OOBWrites()
	if len(oob) != 4 {
		t.Fatalf("expected 4 OOB writes, got %d", len(oob))
	}
	expected := []OOBWrite{{10, 0}, {0, 5}, {-1, 0}, {0, -1}}
	for i, want := range expected {
		if oob[i] != want {
			t.Errorf("OOBWrites[%d] = (%d,%d), want (%d,%d)", i, oob[i].X, oob[i].Y, want.X, want.Y)
		}
	}
}

func TestScreenGrid_SetDebugOff(t *testing.T) {
	t.Parallel()
	g := NewScreenGrid(10, 5)
	// Debug=false (default): OOB writes silently ignored, not recorded.
	g.Set(100, 100, ScreenCell{Char: "X", Width: 1})
	if len(g.OOBWrites()) != 0 {
		t.Errorf("Debug=false should not record OOB writes")
	}
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

	comp := newTestCompositor(width, totalH, "test")
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

	comp := newTestCompositor(width, totalH, "test")
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

	comp := newTestCompositor(width, totalH, "test")
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

	comp := newTestCompositor(width, totalH, "test")
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

	comp := newTestCompositor(width, totalH, "test")
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

func TestPrevGridText(t *testing.T) {
	t.Parallel()
	width, height := 40, 6
	totalH := height + GlobalBarHeight

	paneEmu := vt.NewSafeEmulator(40, height-mux.StatusLineRows)
	paneEmu.Write([]byte("hello world"))

	root := mux.NewLeafByID(1, 0, 0, width, height)
	lookup := func(id uint32) PaneData {
		if id == 1 {
			return &emuPaneData{emu: paneEmu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
		}
		return nil
	}

	comp := newTestCompositor(width, totalH, "test")

	// Before any render, PrevGridText returns empty.
	if got := comp.PrevGridText(); got != "" {
		t.Errorf("before render: PrevGridText = %q, want empty", got)
	}

	// After RenderDiff, PrevGridText should match MaterializeGrid of RenderFull.
	comp.RenderDiff(root, 1, lookup)
	got := comp.PrevGridText()
	if got == "" {
		t.Fatal("after RenderDiff: PrevGridText is empty")
	}

	full := comp.RenderFull(root, 1, lookup, true)
	expected := MaterializeGrid(full, width, totalH)
	if got != expected {
		t.Errorf("PrevGridText doesn't match MaterializeGrid:\ngot:\n%s\nwant:\n%s", got, expected)
	}
}

func TestPrevGridText_CursorAssembledGraphemeClusters(t *testing.T) {
	t.Parallel()

	width, height := 20, 4
	totalH := height + GlobalBarHeight

	paneEmu := vt.NewSafeEmulator(width, height-mux.StatusLineRows)
	if _, err := paneEmu.Write([]byte("🤷")); err != nil {
		t.Fatalf("write emoji base: %v", err)
	}
	if _, err := paneEmu.Write([]byte("‍♂️3")); err != nil {
		t.Fatalf("write emoji suffix: %v", err)
	}

	root := mux.NewLeafByID(1, 0, 0, width, height)
	lookup := func(id uint32) PaneData {
		if id == 1 {
			return &emuPaneData{emu: paneEmu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
		}
		return nil
	}

	comp := newTestCompositor(width, totalH, "test")
	comp.RenderDiff(root, 1, lookup)

	got := comp.PrevGridText()
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("PrevGridText = %q, want at least status and content rows", got)
	}
	if lines[1] != "🤷‍♂️3" {
		t.Fatalf("content row = %q, want %q", lines[1], "🤷‍♂️3")
	}
}

func TestCompactRowCell_DoesNotMergeUnrelatedCells(t *testing.T) {
	t.Parallel()

	paneEmu := vt.NewSafeEmulator(4, 1)
	if _, err := paneEmu.Write([]byte("AB")); err != nil {
		t.Fatalf("write plain text: %v", err)
	}
	pd := &emuPaneData{emu: paneEmu, cursorHidden: true}

	base := pd.CellAt(0, 0, true)
	got, gotWidth, nextSrc := compactRowCell(4, 0, true, pd, nil, 0, base)

	if got.Char != "A" || got.Width != 1 {
		t.Fatalf("compactRowCell() = %+v, want single-cell A", got)
	}
	if gotWidth != 1 {
		t.Fatalf("compactRowCell() width = %d, want 1", gotWidth)
	}
	if nextSrc != 1 {
		t.Fatalf("compactRowCell() nextSrc = %d, want 1", nextSrc)
	}
}

func TestGridToText_EmptyCharCell(t *testing.T) {
	t.Parallel()
	g := NewScreenGrid(5, 2)
	g.Set(1, 0, ScreenCell{Char: "", Width: 1})  // empty char → treated as space
	g.Set(2, 0, ScreenCell{Char: "A", Width: 1}) // normal cell

	got := gridToText(g)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if !strings.Contains(lines[0], " A") {
		t.Errorf("row 0 should show space+A for empty+normal cell, got %q", lines[0])
	}
}

// --- Color-aware oracle tests ---
// These verify that RenderDiff produces the same visual result as RenderFull
// including cell styles (foreground, background, attributes), not just text.

// cellContent returns a cell's visible character, defaulting to space for nil
// or empty cells.
func cellContent(c *uv.Cell) string {
	if c != nil && c.Content != "" {
		return c.Content
	}
	return " "
}

// cellStyle returns a cell's style, defaulting to zero-value for nil cells.
func cellStyle(c *uv.Cell) uv.Style {
	if c != nil {
		return c.Style
	}
	return uv.Style{}
}

// htopSampleANSI is htop-like ANSI content used across color oracle tests:
// colored CPU bars, bold/dim styles, sort indicator, and process rows.
var htopSampleANSI = strings.Join([]string{
	"\033[32m|||||\033[31m||\033[90;1m   \033[0m CPU1 [##...]",
	"\033[34m|||\033[33m|||\033[90;1m    \033[0m CPU2 [##...]",
	"  PID USER  \033[30;42m▽CPU%\033[0m MEM%  TIME+",
	"\033[1;37m 1234 root  \033[0m\033[32m 45.2\033[0m  2.1  0:12.34",
	"\033[37m 5678 user  \033[0m\033[33m 12.1\033[0m  1.5  0:05.67",
}, "\r\n")

// colorOracleCheck feeds RenderDiff output into diffDisplay (persistent, like a
// real terminal), creates a fresh display from RenderFull for ground truth, and
// compares cell-by-cell. Foreground-only differences on space characters are
// ignored since they're visually invisible (the RenderFull ANSI path inherits
// fg state onto trailing spaces, while BuildGrid sets only bg on fill cells).
func colorOracleCheck(comp *Compositor, diffDisplay *vt.SafeEmulator, root *mux.LayoutCell, activeID uint32, lookup func(uint32) PaneData, width, height int) []string {
	// Ground truth: RenderFull (clear screen) -> fresh display emulator.
	fullANSI := comp.RenderFull(root, activeID, lookup, true)
	fullDisplay := vt.NewSafeEmulator(width, height)
	fullDisplay.Write([]byte(fullANSI))

	// Diff path: RenderDiff -> persistent display emulator.
	diffANSI := comp.RenderDiff(root, activeID, lookup)
	diffDisplay.Write([]byte(diffANSI))

	var mismatches []string
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			fullCell := fullDisplay.CellAt(x, y)
			diffCell := diffDisplay.CellAt(x, y)
			fc := cellContent(fullCell)
			dc := cellContent(diffCell)

			if fc != dc {
				mismatches = append(mismatches, fmt.Sprintf(
					"cell(%d,%d): content full=%q diff=%q", x, y, fc, dc))
				continue
			}

			fs := cellStyle(fullCell)
			ds := cellStyle(diffCell)

			// Skip fg-only differences on space characters — the RenderFull
			// ANSI path inherits fg from previous styled characters onto
			// trailing fill spaces, while BuildGrid explicitly sets only bg
			// on fill cells. Both produce identical visual output.
			if fc == " " {
				fs.Fg = nil
				ds.Fg = nil
			}

			if !fs.Equal(&ds) {
				mismatches = append(mismatches, fmt.Sprintf(
					"cell(%d,%d) %q: style full=%s diff=%s", x, y, fc,
					fs.String(), ds.String()))
			}
		}
	}

	mismatches = append(mismatches, oobErrors(comp)...)
	return mismatches
}

func muxTextOracleCheck(comp *Compositor, diffDisplay mux.TerminalEmulator, root *mux.LayoutCell, activeID uint32, lookup func(uint32) PaneData, width, height int) string {
	fullANSI := comp.RenderFull(root, activeID, lookup, true)
	fullDisplay := mux.NewVTEmulatorWithDrain(width, height)
	defer fullDisplay.Close()
	if _, err := fullDisplay.Write([]byte(fullANSI)); err != nil {
		return fmt.Sprintf("writing full ANSI: %v", err)
	}

	diffANSI := comp.RenderDiff(root, activeID, lookup)
	if _, err := diffDisplay.Write([]byte(diffANSI)); err != nil {
		return fmt.Sprintf("writing diff ANSI: %v", err)
	}

	var expected, actual strings.Builder
	for y := 0; y < height; y++ {
		if y > 0 {
			expected.WriteByte('\n')
			actual.WriteByte('\n')
		}
		expected.WriteString(fullDisplay.ScreenLineText(y))
		actual.WriteString(diffDisplay.ScreenLineText(y))
	}

	if expected.String() != actual.String() {
		return "oracle mismatch:\n--- expected (RenderFull) ---\n" + expected.String() + "\n--- actual (RenderDiff) ---\n" + actual.String()
	}
	return ""
}

func TestRenderDiff_ColorOracle(t *testing.T) {
	t.Parallel()
	width, height := 60, 10
	totalH := height + GlobalBarHeight
	contentH := height - mux.StatusLineRows

	paneEmu := vt.NewSafeEmulator(width, contentH)
	paneEmu.Write([]byte(htopSampleANSI))

	root := mux.NewLeafByID(1, 0, 0, width, height)
	lookup := func(id uint32) PaneData {
		if id == 1 {
			return &emuPaneData{emu: paneEmu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
		}
		return nil
	}

	comp := newTestCompositor(width, totalH, "test")
	diffDisplay := vt.NewSafeEmulator(width, totalH)

	// First render: prevGrid is nil so DiffGrid returns all cells.
	if mismatches := colorOracleCheck(comp, diffDisplay, root, 1, lookup, width, totalH); len(mismatches) > 0 {
		t.Errorf("initial paint: %d color mismatches:\n%s",
			len(mismatches), strings.Join(mismatches, "\n"))
	}
}

func TestRenderDiff_ColorOracle_IncrementalUpdate(t *testing.T) {
	t.Parallel()
	width, height := 60, 10
	totalH := height + GlobalBarHeight
	contentH := height - mux.StatusLineRows

	paneEmu := vt.NewSafeEmulator(width, contentH)
	paneEmu.Write([]byte(htopSampleANSI))

	root := mux.NewLeafByID(1, 0, 0, width, height)
	lookup := func(id uint32) PaneData {
		if id == 1 {
			return &emuPaneData{emu: paneEmu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
		}
		return nil
	}

	comp := newTestCompositor(width, totalH, "test")
	diffDisplay := vt.NewSafeEmulator(width, totalH)

	// Initial render: populate prevGrid and prime the diff display.
	initDiff := comp.RenderDiff(root, 1, lookup)
	diffDisplay.Write([]byte(initDiff))

	// Simulate htop redraw: overwrite CPU bars with new values.
	paneEmu.Write([]byte(
		"\033[1;1H" + // home cursor to top-left of pane content
			"\033[32m|||||||\033[31m|\033[90;1m  \033[0m CPU1 [###..]" +
			"\r\n" +
			"\033[34m||\033[33m||||\033[90;1m    \033[0m CPU2 [##...]",
	))

	// Second render: only changed cells emitted via diff, applied to same display.
	if mismatches := colorOracleCheck(comp, diffDisplay, root, 1, lookup, width, totalH); len(mismatches) > 0 {
		t.Errorf("incremental update: %d color mismatches:\n%s",
			len(mismatches), strings.Join(mismatches, "\n"))
	}
}

func TestRenderDiff_ColorOracle_CursorAssembledGraphemeClusters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		initial string
		update  string
	}{
		{
			name:    "emoji modifier applied after wide emoji",
			initial: "👍",
			update:  "🏻2",
		},
		{
			name:    "zwj suffix written after emoji base",
			initial: "🤷",
			update:  "\u200d♂️3",
		},
		{
			name:    "regional indicator repair via backspace",
			initial: "🇸4",
			update:  "\b🇪4",
		},
		{
			name:    "same-cell emoji modifier overwrite",
			initial: "👍2",
			update:  "\r🏻2",
		},
		{
			name:    "same-cell zwj suffix overwrite",
			initial: "🤷3",
			update:  "\r\u200d♂️3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			width, height := 40, 8
			totalH := height + GlobalBarHeight
			contentH := height - mux.StatusLineRows

			paneEmu := vt.NewSafeEmulator(width, contentH)
			paneEmu.Write([]byte(tt.initial))

			root := mux.NewLeafByID(1, 0, 0, width, height)
			lookup := func(id uint32) PaneData {
				if id == 1 {
					return &emuPaneData{emu: paneEmu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
				}
				return nil
			}

			comp := newTestCompositor(width, totalH, "test")
			diffDisplay := mux.NewVTEmulatorWithDrain(width, totalH)
			defer diffDisplay.Close()

			initDiff := comp.RenderDiff(root, 1, lookup)
			if _, err := diffDisplay.Write([]byte(initDiff)); err != nil {
				t.Fatalf("writing initial diff: %v", err)
			}

			paneEmu.Write([]byte(tt.update))

			if err := muxTextOracleCheck(comp, diffDisplay, root, 1, lookup, width, totalH); err != "" {
				t.Fatalf("cursor-assembled grapheme mismatch:\n%s", err)
			}
		})
	}
}

func TestRenderDiff_ColorOracle_LeadPane(t *testing.T) {
	t.Parallel()

	width, height := 60, 10
	totalH := height + GlobalBarHeight
	contentH := height - mux.StatusLineRows

	paneEmu := vt.NewSafeEmulator(width, contentH)
	paneEmu.Write([]byte("lead pane content"))

	root := mux.NewLeafByID(1, 0, 0, width, height)
	lookup := func(id uint32) PaneData {
		if id == 1 {
			return &emuPaneData{
				emu:          paneEmu,
				id:           1,
				name:         "pane-1",
				color:        "f5e0dc",
				cursorHidden: true,
				lead:         true,
			}
		}
		return nil
	}

	comp := newTestCompositor(width, totalH, "test")
	diffDisplay := vt.NewSafeEmulator(width, totalH)

	if mismatches := colorOracleCheck(comp, diffDisplay, root, 1, lookup, width, totalH); len(mismatches) > 0 {
		t.Fatalf("lead pane oracle mismatch:\n%s", strings.Join(mismatches, "\n"))
	}
}

// --- Layer boundary validation ---
// validateLayerBoundaries checks that pane content, status lines, borders,
// and the global bar occupy non-overlapping regions of the grid.
// Returns descriptions of any overlapping cells.
func validateLayerBoundaries(root *mux.LayoutCell, width, height int) []string {
	const (
		layerNone    byte = iota
		layerStatus       // per-pane status line
		layerContent      // pane content area
		layerBorder       // border characters
		layerGlobal       // global status bar
	)
	layerName := map[byte]string{
		layerNone: "none", layerStatus: "status", layerContent: "content",
		layerBorder: "border", layerGlobal: "global",
	}

	grid := make([]byte, width*height) // all layerNone

	var overlaps []string
	claim := func(x, y int, layer byte, desc string) {
		if x < 0 || x >= width || y < 0 || y >= height {
			return
		}
		idx := y*width + x
		if grid[idx] != layerNone {
			overlaps = append(overlaps, fmt.Sprintf(
				"(%d,%d): %s overlaps with %s (%s)",
				x, y, layerName[layer], layerName[grid[idx]], desc))
		}
		grid[idx] = layer
	}

	// Pane status lines and content areas.
	root.Walk(func(cell *mux.LayoutCell) {
		pid := cell.CellPaneID()
		if pid == 0 {
			return
		}
		// Status line row.
		for col := 0; col < cell.W; col++ {
			claim(cell.X+col, cell.Y, layerStatus,
				fmt.Sprintf("pane-%d status", pid))
		}
		// Content area.
		contentH := mux.PaneContentHeight(cell.H)
		for row := 0; row < contentH; row++ {
			for col := 0; col < cell.W; col++ {
				claim(cell.X+col, cell.Y+mux.StatusLineRows+row, layerContent,
					fmt.Sprintf("pane-%d content", pid))
			}
		}
	})

	// Border positions.
	bm := buildBorderMap(root, width, height)
	for _, pos := range bm.positions {
		claim(pos.x, pos.y, layerBorder, "border")
	}

	// Global bar (last row).
	globalY := height - 1
	for x := 0; x < width; x++ {
		claim(x, globalY, layerGlobal, "global bar")
	}

	return overlaps
}

func TestValidateLayerBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		width, height int
		buildRoot     func(w, h int) *mux.LayoutCell
	}{
		{"TwoPanes", 41, 6, buildTwoPaneVertical},
		{"FourPanes", 81, 20, buildFourPane},
		{"NinePanes", 80, 24, func(w, h int) *mux.LayoutCell {
			return buildNinePaneGrid(26, 8, w, h)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			totalH := tt.height + GlobalBarHeight
			root := tt.buildRoot(tt.width, tt.height)
			if overlaps := validateLayerBoundaries(root, tt.width, totalH); len(overlaps) > 0 {
				t.Errorf("layer overlaps:\n%s", strings.Join(overlaps, "\n"))
			}
		})
	}
}

// buildNinePaneGrid creates a 3x3 grid of panes.
func buildNinePaneGrid(colW, rowH, totalW, totalH int) *mux.LayoutCell {
	var id uint32
	buildCol := func(x int) *mux.LayoutCell {
		var rows []*mux.LayoutCell
		for r := 0; r < 3; r++ {
			id++
			y := r * (rowH + 1)
			h := rowH
			if r == 2 {
				h = totalH - y // last row gets remaining height
			}
			rows = append(rows, mux.NewLeafByID(id, x, y, colW, h))
		}
		return mkSplit(mux.SplitHorizontal, x, 0, colW, totalH, rows...)
	}

	return mkSplit(mux.SplitVertical, 0, 0, totalW, totalH,
		buildCol(0),
		buildCol(colW+1),
		buildCol(2*(colW+1)),
	)
}

// --- Long-line oracle tests (issue #166) ---
// These verify that lines exceeding pane width are handled correctly
// by both RenderFull and RenderDiff, preventing the LAB-235 class of bugs.

func TestRenderDiff_LongLines(t *testing.T) {
	t.Parallel()
	width, height := 30, 6
	totalH := height + GlobalBarHeight
	contentH := height - mux.StatusLineRows

	paneEmu := vt.NewSafeEmulator(width, contentH)
	// Write a line that exceeds the pane width — triggers wrapping in the VT emulator.
	longLine := strings.Repeat("A", width+20)
	paneEmu.Write([]byte(longLine))

	root := mux.NewLeafByID(1, 0, 0, width, height)
	lookup := func(id uint32) PaneData {
		if id == 1 {
			return &emuPaneData{emu: paneEmu, id: 1, name: "pane-1", color: "f5e0dc", cursorHidden: true}
		}
		return nil
	}

	comp := newTestCompositor(width, totalH, "test")
	display := vt.NewSafeEmulator(width, totalH)

	if err := oracleCheck(comp, display, root, 1, lookup, width, totalH); err != "" {
		t.Error(err)
	}
}

func TestRenderDiff_LongLines_TwoPanes(t *testing.T) {
	t.Parallel()
	pane1W := 25
	pane2W := 25
	width := pane1W + 1 + pane2W // 51
	height := 8
	totalH := height + GlobalBarHeight
	contentH := height - mux.StatusLineRows

	pane1Emu := vt.NewSafeEmulator(pane1W, contentH)
	pane2Emu := vt.NewSafeEmulator(pane2W, contentH)

	// Left pane: long line with ANSI colors (simulates a colored prompt with long branch name).
	pane1Emu.Write([]byte(
		"\033[32muser@host\033[0m:\033[34m~/very/deeply/nested/project/directory\033[0m (feature/extremely-long-branch-name-that-overflows)$ ",
	))
	// Right pane: long plain text line.
	pane2Emu.Write([]byte(strings.Repeat("B", pane2W+15)))

	root := mkSplit(mux.SplitVertical, 0, 0, width, height,
		mux.NewLeafByID(1, 0, 0, pane1W, height),
		mux.NewLeafByID(2, pane1W+1, 0, pane2W, height),
	)
	lookup := twoPaneLookup(pane1Emu, pane2Emu)

	comp := newTestCompositor(width, totalH, "test")
	display := vt.NewSafeEmulator(width, totalH)

	// Initial paint.
	if err := oracleCheck(comp, display, root, 1, lookup, width, totalH); err != "" {
		t.Error(err)
	}

	// Incremental: add more long content.
	pane1Emu.Write([]byte("\r\n" + strings.Repeat("X", pane1W+10)))
	if err := oracleCheck(comp, display, root, 1, lookup, width, totalH); err != "" {
		t.Error(err)
	}
}

func TestRenderDiff_StatusLineWideRuneMatchesRenderFullAcrossPanes(t *testing.T) {
	t.Parallel()

	pane1W := 32
	pane2W := 32
	width := pane1W + 1 + pane2W
	height := 6
	totalH := height + GlobalBarHeight

	root := mkSplit(mux.SplitVertical, 0, 0, width, height,
		mux.NewLeafByID(1, 0, 0, pane1W, height),
		mux.NewLeafByID(2, pane1W+1, 0, pane2W, height),
	)
	lookup := func(id uint32) PaneData {
		switch id {
		case 1:
			return &statusPaneData{
				id:           1,
				name:         "pane-1",
				connStatus:   "connected",
				task:         "sync-logs",
				color:        config.TextColorHex,
				screen:       "",
				cursorHidden: true,
			}
		case 2:
			return &statusPaneData{
				id:           2,
				name:         "pane-2",
				color:        config.TextColorHex,
				screen:       "",
				cursorHidden: true,
			}
		}
		return nil
	}

	comp := newTestCompositor(width, totalH, "test")
	display := vt.NewSafeEmulator(width, totalH)

	if err := oracleCheck(comp, display, root, 1, lookup, width, totalH); err != "" {
		t.Error(err)
	}
}

func TestRenderDiff_ColorOracle_LongLines(t *testing.T) {
	t.Parallel()
	pane1W := 20
	pane2W := 20
	width := pane1W + 1 + pane2W // 41
	height := 8
	totalH := height + GlobalBarHeight
	contentH := height - mux.StatusLineRows

	pane1Emu := vt.NewSafeEmulator(pane1W, contentH)
	pane2Emu := vt.NewSafeEmulator(pane2W, contentH)

	// Colored long lines — ANSI escapes + text exceeding pane width.
	pane1Emu.Write([]byte(
		"\033[32m" + strings.Repeat("|", pane1W+10) + "\033[0m",
	))
	pane2Emu.Write([]byte(
		"\033[31m" + strings.Repeat("#", pane2W+10) + "\033[0m",
	))

	root := mkSplit(mux.SplitVertical, 0, 0, width, height,
		mux.NewLeafByID(1, 0, 0, pane1W, height),
		mux.NewLeafByID(2, pane1W+1, 0, pane2W, height),
	)
	lookup := twoPaneLookup(pane1Emu, pane2Emu)

	comp := newTestCompositor(width, totalH, "test")
	diffDisplay := vt.NewSafeEmulator(width, totalH)

	if mismatches := colorOracleCheck(comp, diffDisplay, root, 1, lookup, width, totalH); len(mismatches) > 0 {
		t.Errorf("long-line color oracle: %d mismatches:\n%s",
			len(mismatches), strings.Join(mismatches, "\n"))
	}

	// Incremental update: overwrite with new long colored content.
	pane1Emu.Write([]byte(
		"\033[1;1H\033[33m" + strings.Repeat("=", pane1W+5) + "\033[0m",
	))
	if mismatches := colorOracleCheck(comp, diffDisplay, root, 1, lookup, width, totalH); len(mismatches) > 0 {
		t.Errorf("long-line color oracle incremental: %d mismatches:\n%s",
			len(mismatches), strings.Join(mismatches, "\n"))
	}
}

func TestRenderDiff_LongLines_NinePanes(t *testing.T) {
	t.Parallel()
	colW := 26
	rowH := 8
	width := 3*colW + 2  // 80
	height := 3*rowH + 2 // 26
	totalH := height + GlobalBarHeight
	contentH := rowH - mux.StatusLineRows

	// Build 9 pane emulators with long lines.
	emus := make([]*vt.SafeEmulator, 9)
	for i := range emus {
		emus[i] = vt.NewSafeEmulator(colW, contentH)
		// Each pane gets a line that overflows its width.
		line := fmt.Sprintf("pane-%d:%s", i+1, strings.Repeat(string(rune('a'+i)), colW+10))
		emus[i].Write([]byte(line))
	}

	root := buildNinePaneGrid(colW, rowH, width, height)

	colors := []string{"f5e0dc", "cba6f7", "f38ba8", "fab387", "f9e2af", "a6e3a1", "89dceb", "74c7ec", "b4befe"}
	lookup := func(id uint32) PaneData {
		idx := int(id) - 1
		if idx >= 0 && idx < len(emus) {
			return &emuPaneData{
				emu: emus[idx], id: id,
				name:         fmt.Sprintf("pane-%d", id),
				color:        colors[idx],
				cursorHidden: true,
			}
		}
		return nil
	}

	comp := newTestCompositor(width, totalH, "test")
	display := vt.NewSafeEmulator(width, totalH)

	// Text oracle.
	if err := oracleCheck(comp, display, root, 1, lookup, width, totalH); err != "" {
		t.Error(err)
	}

	// Layer boundary validation.
	if overlaps := validateLayerBoundaries(root, width, totalH); len(overlaps) > 0 {
		t.Errorf("9-pane layer overlaps:\n%s", strings.Join(overlaps, "\n"))
	}
}

func TestRenderDiff_ColorOracle_TwoPanes(t *testing.T) {
	t.Parallel()
	width, height := 81, 10
	totalH := height + GlobalBarHeight
	contentH := height - mux.StatusLineRows

	pane1W := 40
	pane2W := 40
	pane1Emu := vt.NewSafeEmulator(pane1W, contentH)
	pane2Emu := vt.NewSafeEmulator(pane2W, contentH)

	// Left pane: htop-like colored bars
	pane1Emu.Write([]byte(
		"\033[32m|||||\033[31m||\033[90;1m   \033[0m CPU1\r\n" +
			"\033[34m|||\033[33m|||\033[90;1m    \033[0m CPU2",
	))

	// Right pane: plain text
	pane2Emu.Write([]byte("$ top\r\nPID  CPU%  MEM%"))

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

	comp := newTestCompositor(width, totalH, "test")
	diffDisplay := vt.NewSafeEmulator(width, totalH)

	// Initial paint with both panes.
	if mismatches := colorOracleCheck(comp, diffDisplay, root, 1, lookup, width, totalH); len(mismatches) > 0 {
		t.Errorf("two-pane initial: %d color mismatches:\n%s",
			len(mismatches), strings.Join(mismatches, "\n"))
	}

	// Update left pane, keep right pane unchanged.
	pane1Emu.Write([]byte(
		"\033[1;1H" +
			"\033[32m|||||||\033[31m|\033[90;1m  \033[0m CPU1",
	))

	if mismatches := colorOracleCheck(comp, diffDisplay, root, 1, lookup, width, totalH); len(mismatches) > 0 {
		t.Errorf("two-pane incremental: %d color mismatches:\n%s",
			len(mismatches), strings.Join(mismatches, "\n"))
	}
}

// --- Fuzz compositor (#169) ---

// Fuzz-specific minimum pane dimensions. Larger than PaneMinSize (2) to avoid
// overly cramped layouts where the status line leaves no useful content area.
// Width=12 fits "● [pane-NN]", height=4 fits status line + 2 content rows + 1 border.
const (
	fuzzMinW = 12
	fuzzMinH = 4
)

// fuzzLayout generates a random layout tree from fuzz data. It recursively
// splits available space, consuming 1 byte per decision: 0 = stop (leaf),
// odd = split horizontal, even = split vertical. Respects fuzzMinW/fuzzMinH
// constraints. Returns the root cell and a list of pane IDs assigned to leaves.
func fuzzLayout(data []byte, width, height int) (*mux.LayoutCell, []uint32) {
	var nextID uint32
	var paneIDs []uint32
	pos := 0

	consume := func() byte {
		if pos >= len(data) {
			return 0
		}
		b := data[pos]
		pos++
		return b
	}

	makeLeaf := func(x, y, w, h int) *mux.LayoutCell {
		nextID++
		paneIDs = append(paneIDs, nextID)
		return mux.NewLeafByID(nextID, x, y, w, h)
	}

	var build func(x, y, w, h int) *mux.LayoutCell
	build = func(x, y, w, h int) *mux.LayoutCell {
		b := consume()
		canSplitW := w >= 2*fuzzMinW+1
		canSplitH := h >= 2*fuzzMinH+1

		if b == 0 || (!canSplitW && !canSplitH) {
			return makeLeaf(x, y, w, h)
		}

		// Decide split direction: prefer byte parity, fall back to whatever fits.
		splitH := b%2 == 1
		if splitH && !canSplitH {
			splitH = false
		} else if !splitH && !canSplitW {
			splitH = true
		}

		if splitH {
			rangeH := h - 1 - 2*fuzzMinH + 1
			topH := fuzzMinH + int(consume())%rangeH
			botH := h - 1 - topH
			top := build(x, y, w, topH)
			bot := build(x, y+topH+1, w, botH)
			cell := &mux.LayoutCell{
				X: x, Y: y, W: w, H: h,
				Dir:      mux.SplitHorizontal,
				Children: []*mux.LayoutCell{top, bot},
			}
			top.Parent = cell
			bot.Parent = cell
			return cell
		}

		rangeW := w - 1 - 2*fuzzMinW + 1
		leftW := fuzzMinW + int(consume())%rangeW
		rightW := w - 1 - leftW
		left := build(x, y, leftW, h)
		right := build(x+leftW+1, y, rightW, h)
		cell := &mux.LayoutCell{
			X: x, Y: y, W: w, H: h,
			Dir:      mux.SplitVertical,
			Children: []*mux.LayoutCell{left, right},
		}
		left.Parent = cell
		right.Parent = cell
		return cell
	}

	root := build(0, 0, width, height)
	return root, paneIDs
}

// fuzzPaneContent generates lines of varying length from fuzz data.
// Some lines intentionally exceed pane width — the LAB-235 trigger pattern.
// Only printable ASCII; wide chars and ANSI escapes are a follow-up.
func fuzzPaneContent(data []byte, width, height int) string {
	if len(data) == 0 {
		return ""
	}
	var lines []string
	pos := 0
	for i := 0; i < height && pos < len(data); i++ {
		// Line length: consume one byte to decide length relative to width.
		lenByte := data[pos]
		pos++
		// Allow lines from 0 to width+width/2 (some overflow).
		lineLen := int(lenByte) % (width + width/2 + 1)
		var line strings.Builder
		for j := 0; j < lineLen && pos < len(data); j++ {
			// Map byte to printable ASCII range (0x20–0x7E).
			ch := 0x20 + data[pos]%0x5F
			line.WriteByte(ch)
			pos++
		}
		lines = append(lines, line.String())
	}
	return strings.Join(lines, "\r\n")
}

func FuzzCompositor(f *testing.F) {
	// Seed corpus: empty (1 pane), 2-byte (2 panes), 8-byte (several splits).
	f.Add([]byte{})
	f.Add([]byte{1, 0})
	f.Add([]byte{2, 0, 1, 0, 2, 0, 1, 0})

	colors := config.AccentColors()

	f.Fuzz(func(t *testing.T, data []byte) {
		width, height := 80, 24
		totalH := height + GlobalBarHeight

		// Split data: first 16 bytes for layout, rest for content.
		layoutData := data
		var contentData []byte
		if len(data) > 16 {
			layoutData = data[:16]
			contentData = data[16:]
		}

		root, paneIDs := fuzzLayout(layoutData, width, height)
		if len(paneIDs) == 0 {
			return
		}

		// Create emulators and feed content.
		type paneInfo struct {
			emu   *vt.SafeEmulator
			id    uint32
			name  string
			color string
		}
		panes := make([]paneInfo, len(paneIDs))
		for i, id := range paneIDs {
			// Find this pane's cell to get dimensions.
			cell := root.FindByPaneID(id)
			if cell == nil {
				continue
			}
			contentH := mux.PaneContentHeight(cell.H)
			if contentH < 1 {
				contentH = 1
			}
			emu := vt.NewSafeEmulator(cell.W, contentH)

			// Generate content from fuzz data.
			if len(contentData) > 0 {
				perPane := len(contentData) / len(paneIDs)
				if perPane < 1 {
					perPane = 1
				}
				start := i * perPane
				end := start + perPane
				if end > len(contentData) {
					end = len(contentData)
				}
				if start < len(contentData) {
					content := fuzzPaneContent(contentData[start:end], cell.W, contentH)
					emu.Write([]byte(content))
				}
			}

			colorIdx := int(id-1) % len(colors)
			panes[i] = paneInfo{
				emu:   emu,
				id:    id,
				name:  fmt.Sprintf("pane-%d", id),
				color: colors[colorIdx],
			}
		}

		lookup := func(id uint32) PaneData {
			for _, p := range panes {
				if p.id == id && p.emu != nil {
					return &emuPaneData{
						emu: p.emu, id: p.id,
						name: p.name, color: p.color,
						cursorHidden: true,
					}
				}
			}
			return nil
		}

		comp := newTestCompositor(width, totalH, "fuzz")
		display := vt.NewSafeEmulator(width, totalH)

		// Oracle check: RenderFull vs RenderDiff text comparison + OOB detection.
		if err := oracleCheck(comp, display, root, paneIDs[0], lookup, width, totalH); err != "" {
			t.Error(err)
		}

		// Layer boundary validation: geometric overlap detection.
		if overlaps := validateLayerBoundaries(root, width, totalH); len(overlaps) > 0 {
			t.Errorf("layer overlaps:\n%s", strings.Join(overlaps, "\n"))
		}
	})
}
