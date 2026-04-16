package render

import (
	"strings"

	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

// borderCell tracks a border position and the two layout cells on either side.
type borderCell struct {
	left, right *mux.LayoutCell // for vertical borders (or top/bottom for horizontal)
}

// borderPos records a border cell position for sparse iteration.
type borderPos struct {
	x, y int
}

// borderMap is a 2D grid marking which terminal cells are borders.
// Each entry stores the adjacent cells for color determination.
type borderMap struct {
	width, height int
	cells         []borderCell // width * height, row-major
	isBorder      []bool       // same layout
	positions     []borderPos  // sparse list of border positions
}

func newBorderMap(w, h int) *borderMap {
	return &borderMap{
		width:    w,
		height:   h,
		cells:    make([]borderCell, w*h),
		isBorder: make([]bool, w*h),
	}
}

func (bm *borderMap) set(x, y int, left, right *mux.LayoutCell) {
	if x >= 0 && x < bm.width && y >= 0 && y < bm.height {
		idx := y*bm.width + x
		if !bm.isBorder[idx] {
			bm.positions = append(bm.positions, borderPos{x, y})
		}
		bm.isBorder[idx] = true
		bm.cells[idx] = borderCell{left: left, right: right}
	}
}

func (bm *borderMap) has(x, y int) bool {
	if x < 0 || x >= bm.width || y < 0 || y >= bm.height {
		return false
	}
	return bm.isBorder[y*bm.width+x]
}

func (bm *borderMap) get(x, y int) borderCell {
	return bm.cells[y*bm.width+x]
}

// junctionChar picks the box-drawing character based on which neighbors are borders.
func junctionChar(up, down, left, right bool) string {
	switch {
	case up && down && left && right:
		return "┼"
	case up && down && right:
		return "├"
	case up && down && left:
		return "┤"
	case down && left && right:
		return "┬"
	case up && left && right:
		return "┴"
	case down && right:
		return "┌"
	case down && left:
		return "┐"
	case up && right:
		return "└"
	case up && left:
		return "┘"
	case up && down:
		return "│"
	case left && right:
		return "─"
	case up:
		return "│"
	case down:
		return "│"
	case left:
		return "─"
	case right:
		return "─"
	default:
		return "+"
	}
}

// buildBorderMap walks the layout tree and marks all border cells.
func buildBorderMap(root *mux.LayoutCell, w, h int) *borderMap {
	bm := newBorderMap(w, h)
	markBorders(bm, root)
	return bm
}

// markBorders recursively marks border cells in the map.
func markBorders(bm *borderMap, cell *mux.LayoutCell) {
	if cell.IsLeaf() {
		return
	}

	children := cell.Children
	for i := 0; i < len(children)-1; i++ {
		left := children[i]
		right := children[i+1]

		if cell.Dir == mux.SplitVertical {
			// Vertical border at x = left.X + left.W
			x := left.X + left.W
			for y := cell.Y; y < cell.Y+cell.H; y++ {
				bm.set(x, y, left, right)
			}
		} else {
			// Horizontal border at y = left.Y + left.H
			y := left.Y + left.H
			for x := cell.X; x < cell.X+cell.W; x++ {
				bm.set(x, y, left, right)
			}
		}
	}

	for _, child := range children {
		markBorders(bm, child)
	}
}

// renderBorders draws all border cells with junction characters and per-cell coloring.
// Iterates the sparse position list instead of scanning the full w*h grid.
func renderBordersWithProfile(buf *strings.Builder, bm *borderMap, root *mux.LayoutCell, activePaneID uint32, activeColor string, profile termenv.Profile) {
	lastColor := ""
	dimColor := fgHexSequence(config.DimColorHex, profile)
	for _, pos := range bm.positions {
		x, y := pos.x, pos.y

		// Determine junction character from neighbors
		up := bm.has(x, y-1)
		down := bm.has(x, y+1)
		left := bm.has(x-1, y)
		right := bm.has(x+1, y)
		ch := junctionChar(up, down, left, right)

		// Determine color from adjacent panes
		bc := bm.get(x, y)
		isJunction := (up || down) && (left || right)
		color := borderColor(bc.left, bc.right, x, y, isJunction, activePaneID, activeColor)
		if color == DimFg {
			color = dimColor
		}

		if color != lastColor {
			if lastColor != "" {
				buf.WriteString(Reset)
			}
			if color != "" {
				buf.WriteString(color)
			}
			lastColor = color
		}

		writeCursorTo(buf, y+1, x+1)
		buf.WriteString(ch)
	}
	if lastColor != "" {
		buf.WriteString(Reset)
	}
}

// Probe offsets for borderColor — hoisted to package level to avoid
// per-call slice allocation. Junctions probe all 4 diagonals; straight
// segments probe the 2 positions perpendicular to the border direction.
var (
	junctionOffsets   = [4][2]int{{-1, -1}, {1, -1}, {-1, 1}, {1, 1}}
	verticalOffsets   = [2][2]int{{-1, 0}, {1, 0}}
	horizontalOffsets = [2][2]int{{0, -1}, {0, 1}}
)

// borderAdjacentToActive reports whether the active pane is adjacent to the
// border at (x, y). For junctions (where perpendicular borders meet), it
// probes the 4 diagonal positions which are always inside pane cells.
// For straight segments, it probes the two positions perpendicular to the
// border direction (x+-1 for vertical borders, y+-1 for horizontal).
func borderAdjacentToActive(a, b *mux.LayoutCell, x, y int, junction bool, activePaneID uint32) bool {
	if activePaneID == 0 {
		return false
	}

	var offsets [][2]int
	if junction {
		offsets = junctionOffsets[:]
	} else if x == a.X+a.W {
		offsets = verticalOffsets[:]
	} else {
		offsets = horizontalOffsets[:]
	}

	for _, off := range offsets {
		nx, ny := x+off[0], y+off[1]
		leaf := findLeafByAxis(a, nx, ny)
		if leaf == nil {
			leaf = findLeafByAxis(b, nx, ny)
		}
		if leaf != nil && leaf.CellPaneID() == activePaneID {
			return true
		}
	}
	return false
}

// borderColor determines the ANSI color for a border cell: activeColor when
// the active pane is adjacent, DimFg otherwise.
func borderColor(a, b *mux.LayoutCell, x, y int, junction bool, activePaneID uint32, activeColor string) string {
	if borderAdjacentToActive(a, b, x, y, junction, activePaneID) {
		return activeColor
	}
	return DimFg
}

// findLeafByAxis finds the leaf pane adjacent to a border position.
// Border cells sit between panes, so exact position may not be inside
// any cell. We search by the perpendicular axis and use inclusive
// upper bounds (<=) to catch boundary positions at cell edges.
//
// Consequence: when (x, y) falls on an internal border within a subtree,
// the FIRST child whose inclusive range covers it wins. This means callers
// must choose probe offsets carefully — borderColor uses perpendicular
// offsets for straight segments and diagonal offsets for junctions to
// avoid landing on internal borders (see border.go).
func findLeafByAxis(cell *mux.LayoutCell, x, y int) *mux.LayoutCell {
	if cell.IsLeaf() {
		// Use inclusive upper bound — border junctions sit at cell edges
		if y >= cell.Y && y <= cell.Y+cell.H && x >= cell.X && x <= cell.X+cell.W {
			return cell
		}
		return nil
	}
	for _, child := range cell.Children {
		if cell.Dir == mux.SplitHorizontal {
			if y >= child.Y && y <= child.Y+child.H {
				if found := findLeafByAxis(child, x, y); found != nil {
					return found
				}
			}
		} else {
			if x >= child.X && x <= child.X+child.W {
				if found := findLeafByAxis(child, x, y); found != nil {
					return found
				}
			}
		}
	}
	return nil
}
