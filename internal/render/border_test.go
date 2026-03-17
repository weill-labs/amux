package render

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

// mkLeaf creates a leaf LayoutCell with the given pane ID and geometry.
func mkLeaf(id uint32, x, y, w, h int) *mux.LayoutCell {
	return mux.NewLeaf(&mux.Pane{ID: id}, x, y, w, h)
}

// mkSplit creates an internal LayoutCell with the given children.
func mkSplit(dir mux.SplitDir, x, y, w, h int, children ...*mux.LayoutCell) *mux.LayoutCell {
	cell := &mux.LayoutCell{
		X: x, Y: y, W: w, H: h,
		Dir:      dir,
		Children: children,
	}
	for _, c := range children {
		c.Parent = cell
	}
	return cell
}

// buildTwoPaneVertical creates:
//
//	pane-1 (top)
//	────────────
//	pane-2 (bottom)
func buildTwoPaneVertical(w, h int) *mux.LayoutCell {
	topH := (h - 1) / 2
	botH := h - 1 - topH
	return mkSplit(mux.SplitHorizontal, 0, 0, w, h,
		mkLeaf(1, 0, 0, w, topH),
		mkLeaf(2, 0, topH+1, w, botH),
	)
}

// buildThreePaneT creates:
//
//	pane-1 (top, full width)
//	────────────────────────
//	pane-2 (bot-L) │ pane-3 (bot-R)
func buildThreePaneT(w, h int) *mux.LayoutCell {
	topH := (h - 1) / 2
	botH := h - 1 - topH
	botLeftW := (w - 1) / 2
	botRightW := w - 1 - botLeftW
	bot := mkSplit(mux.SplitVertical, 0, topH+1, w, botH,
		mkLeaf(2, 0, topH+1, botLeftW, botH),
		mkLeaf(3, botLeftW+1, topH+1, botRightW, botH),
	)
	return mkSplit(mux.SplitHorizontal, 0, 0, w, h,
		mkLeaf(1, 0, 0, w, topH),
		bot,
	)
}

// buildFourPane creates:
//
//	pane-1 │ pane-2
//	───────┼───────
//	pane-4 │ pane-3
func buildFourPane(w, h int) *mux.LayoutCell {
	leftW := (w - 1) / 2
	rightW := w - 1 - leftW
	topH := (h - 1) / 2
	botH := h - 1 - topH
	left := mkSplit(mux.SplitHorizontal, 0, 0, leftW, h,
		mkLeaf(1, 0, 0, leftW, topH),
		mkLeaf(4, 0, topH+1, leftW, botH),
	)
	right := mkSplit(mux.SplitHorizontal, leftW+1, 0, rightW, h,
		mkLeaf(2, leftW+1, 0, rightW, topH),
		mkLeaf(3, leftW+1, topH+1, rightW, botH),
	)
	return mkSplit(mux.SplitVertical, 0, 0, w, h, left, right)
}

// buildThreeColSplitMiddle creates:
//
//	pane-1 │ pane-2 │ pane-3
//	       ├────────┤
//	       │ pane-4 │
//	       ├────────┤
//	       │ pane-5 │
func buildThreeColSplitMiddle(w, h int) *mux.LayoutCell {
	colW := (w - 2) / 3
	midW := w - 2 - 2*colW
	midX := colW + 1
	rightX := midX + midW + 1

	// Middle column: 3 panes stacked vertically
	topH := (h - 2) / 3
	midH := topH
	botH := h - 2 - 2*topH
	mid := mkSplit(mux.SplitHorizontal, midX, 0, midW, h,
		mkLeaf(2, midX, 0, midW, topH),
		mkLeaf(4, midX, topH+1, midW, midH),
		mkLeaf(5, midX, topH+1+midH+1, midW, botH),
	)

	return mkSplit(mux.SplitVertical, 0, 0, w, h,
		mkLeaf(1, 0, 0, colW, h),
		mid,
		mkLeaf(3, rightX, 0, colW, h),
	)
}

// colorMapString builds a color map from a layout tree.
// At each border position, prints 'A' if the active pane is adjacent, '.' otherwise.
func colorMapString(root *mux.LayoutCell, w, h int, activePaneID uint32) string {
	bm := buildBorderMap(root, w, h)
	activeColor := "ACTIVE" // sentinel
	grid := make([][]byte, h)
	for y := range grid {
		grid[y] = make([]byte, w)
		for x := range grid[y] {
			grid[y][x] = ' '
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !bm.has(x, y) {
				continue
			}
			up := bm.has(x, y-1)
			down := bm.has(x, y+1)
			left := bm.has(x-1, y)
			right := bm.has(x+1, y)
			bc := bm.get(x, y)
			isJunction := (up || down) && (left || right)
			color := borderColor(bc.left, bc.right, x, y, isJunction, activePaneID, activeColor)
			if color == activeColor {
				grid[y][x] = 'A'
			} else {
				grid[y][x] = '.'
			}
		}
	}
	var buf strings.Builder
	for y, row := range grid {
		if y > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(strings.TrimRight(string(row), " "))
	}
	return buf.String()
}

func TestBorderColor_TwoPaneVertical(t *testing.T) {
	t.Parallel()
	root := buildTwoPaneVertical(20, 11)
	// pane-2 active (bottom): horizontal border at y=5 should be colored
	cm := colorMapString(root, 20, 11, 2)
	lines := splitLines(cm)
	borderY := (11 - 1) / 2 // 5
	for x := 0; x < 20; x++ {
		if x >= len(lines[borderY]) || lines[borderY][x] != 'A' {
			t.Errorf("horizontal border at (%d, %d) should be 'A' (pane-2 active)\nColor map:\n%s", x, borderY, cm)
			break
		}
	}
}

func TestBorderColor_ThreePaneDiagonalJunction(t *testing.T) {
	t.Parallel()
	// pane-1 (top) / pane-2 (bot-L) | pane-3 (bot-R, active)
	root := buildThreePaneT(80, 23)
	cm := colorMapString(root, 80, 23, 3)
	topH := (23 - 1) / 2 // 11
	botLeftW := (80 - 1) / 2 // 39
	junctionX := botLeftW
	junctionY := topH

	// Junction at (39, 11) should be colored (pane-3 is diagonal)
	lines := splitLines(cm)
	if len(lines) <= junctionY || len(lines[junctionY]) <= junctionX {
		t.Fatalf("color map too small for junction check")
	}
	if lines[junctionY][junctionX] != 'A' {
		t.Errorf("junction at (%d, %d) should be 'A' when pane-3 (diagonal) is active, got %q\nColor map:\n%s",
			junctionX, junctionY, lines[junctionY][junctionX], cm)
	}

	// Horizontal border right of junction (above pane-3) should be colored
	for x := junctionX + 1; x < 80; x++ {
		if lines[junctionY][x] != 'A' {
			t.Errorf("horizontal border at (%d, %d) should be 'A' (above pane-3), got %q", x, junctionY, lines[junctionY][x])
			break
		}
	}

	// Horizontal border left of junction (above pane-2) should be dim
	for x := 0; x < junctionX; x++ {
		if lines[junctionY][x] != '.' {
			t.Errorf("horizontal border at (%d, %d) should be '.' (above pane-2), got %q", x, junctionY, lines[junctionY][x])
			break
		}
	}
}

func TestBorderColor_FourPaneDiagonal(t *testing.T) {
	t.Parallel()
	root := buildFourPane(80, 23)
	// pane-3 active (bottom-right)
	cm := colorMapString(root, 80, 23, 3)
	lines := splitLines(cm)
	leftW := (80 - 1) / 2 // 39
	topH := (23 - 1) / 2  // 11
	jX, jY := leftW, topH // junction at (39, 11)

	// Junction should be colored (pane-3 at diagonal)
	if lines[jY][jX] != 'A' {
		t.Errorf("junction at (%d, %d) should be 'A' when pane-3 (diagonal) is active, got %q\nColor map:\n%s",
			jX, jY, lines[jY][jX], cm)
	}

	// Vertical border below junction (between pane-4 and pane-3) should be colored
	for y := jY + 1; y < 23; y++ {
		if lines[y][jX] != 'A' {
			t.Errorf("vertical border at (%d, %d) should be 'A', got %q", jX, y, lines[y][jX])
			break
		}
	}

	// Vertical border above junction (between pane-1 and pane-2) should be dim
	for y := 0; y < jY; y++ {
		if lines[y][jX] != '.' {
			t.Errorf("vertical border at (%d, %d) should be '.', got %q", jX, y, lines[y][jX])
			break
		}
	}
}

func TestBorderColor_ThreeColSplitMiddle(t *testing.T) {
	t.Parallel()
	root := buildThreeColSplitMiddle(80, 23)
	// pane-4 active (middle of middle column)
	cm := colorMapString(root, 80, 23, 4)
	lines := splitLines(cm)

	colW := (80 - 2) / 3       // 26
	midW := 80 - 2 - 2*colW    // 26
	leftBorderX := colW        // 26
	rightBorderX := colW + 1 + midW // 53
	topH := (23 - 2) / 3       // 7
	midH := topH                // 7
	// pane-4 spans rows topH+1 to topH+midH (exclusive of borders)
	pane4StartY := topH + 1 // 8
	pane4EndY := pane4StartY + midH - 1 // 14

	// Both vertical borders should be colored in pane-4's row range
	for y := pane4StartY; y <= pane4EndY; y++ {
		if lines[y][leftBorderX] != 'A' {
			t.Errorf("left border at (%d, %d) should be 'A' (pane-4 active), got %q\nColor map:\n%s",
				leftBorderX, y, lines[y][leftBorderX], cm)
			break
		}
		if lines[y][rightBorderX] != 'A' {
			t.Errorf("right border at (%d, %d) should be 'A' (pane-4 active), got %q\nColor map:\n%s",
				rightBorderX, y, lines[y][rightBorderX], cm)
			break
		}
	}

	// Vertical borders outside pane-4's range should be dim
	for y := 0; y < topH; y++ {
		if lines[y][leftBorderX] != '.' {
			t.Errorf("left border at (%d, %d) should be '.' (above pane-4), got %q", leftBorderX, y, lines[y][leftBorderX])
			break
		}
	}
	for y := pane4EndY + 2; y < 23; y++ { // +2 to skip the horizontal border
		if lines[y][leftBorderX] != '.' {
			t.Errorf("left border at (%d, %d) should be '.' (below pane-4), got %q", leftBorderX, y, lines[y][leftBorderX])
			break
		}
	}
}

func splitLines(s string) []string {
	return strings.Split(s, "\n")
}
