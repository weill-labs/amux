package render

import (
	"strings"

	"github.com/weill-labs/amux/internal/mux"
)

// borderCell tracks a border position and the two layout cells on either side.
type borderCell struct {
	left, right *mux.LayoutCell // for vertical borders (or top/bottom for horizontal)
}

// borderMap is a 2D grid marking which terminal cells are borders.
// Each entry stores the adjacent cells for color determination.
type borderMap struct {
	width, height int
	cells         []borderCell // width * height, row-major
	isBorder      []bool       // same layout
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

		if cell.Dir == mux.SplitHorizontal {
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
func renderBorders(buf *strings.Builder, bm *borderMap, root *mux.LayoutCell, activePaneID uint32, activeColor string) {
	lastColor := ""
	for y := 0; y < bm.height; y++ {
		for x := 0; x < bm.width; x++ {
			if !bm.has(x, y) {
				continue
			}

			// Determine junction character from neighbors
			up := bm.has(x, y-1)
			down := bm.has(x, y+1)
			left := bm.has(x-1, y)
			right := bm.has(x+1, y)
			ch := junctionChar(up, down, left, right)

			// Determine color from adjacent panes.
			// Junctions (3+ neighbors) use subtree search since the position
			// falls at a corner outside all adjacent cells' bounds.
			bc := bm.get(x, y)
			neighbors := 0
			if up {
				neighbors++
			}
			if down {
				neighbors++
			}
			if left {
				neighbors++
			}
			if right {
				neighbors++
			}
			var color string
			if neighbors >= 3 {
				color = borderColorAtJunction(bc.left, bc.right, x, y, activePaneID, activeColor)
			} else {
				color = borderColorAt(bc.left, bc.right, x, y, activePaneID, activeColor)
			}

			if color != lastColor {
				if lastColor != "" {
					buf.WriteString(Reset)
				}
				buf.WriteString(color)
				lastColor = color
			}

			buf.WriteString(CursorTo(y+1, x+1))
			buf.WriteString(ch)
		}
	}
	if lastColor != "" {
		buf.WriteString(Reset)
	}
}

// borderColorAt determines the color for a border cell based on which leaf
// pane is adjacent.
func borderColorAt(a, b *mux.LayoutCell, x, y int, activePaneID uint32, activeColor string) string {
	if activePaneID == 0 {
		return DimFg
	}

	// Search slightly inside each subtree. The border position (x,y)
	// sits between the two cells, so x is at a.X+a.W (outside a) and
	// at b.X-1 (outside b). Search at (x-1) for left/top and (x+1)
	// for right/bottom to find the adjacent leaf.
	leafA := findLeafByAxis(a, x-1, y-1)
	if leafA == nil {
		leafA = findLeafByAxis(a, x, y)
	}
	leafB := findLeafByAxis(b, x+1, y+1)
	if leafB == nil {
		leafB = findLeafByAxis(b, x, y)
	}

	if (leafA != nil && leafA.CellPaneID() == activePaneID) ||
		(leafB != nil && leafB.CellPaneID() == activePaneID) {
		return activeColor
	}

	return DimFg
}

// borderColorAtJunction checks only the directly adjacent leaf panes at a
// junction position rather than searching entire subtrees. This prevents
// coloring junctions that are not adjacent to the active pane.
func borderColorAtJunction(a, b *mux.LayoutCell, x, y int, activePaneID uint32, activeColor string) string {
	if activePaneID == 0 {
		return DimFg
	}
	// Check leaves adjacent to the junction in all 8 directions (cardinal +
	// diagonal). Cardinal positions can land on border cells where inclusive
	// bounds in findLeafByAxis claim the wrong pane. Diagonal positions are
	// always inside cells, so they reliably find the correct corner pane.
	for _, off := range [][2]int{
		{-1, 0}, {1, 0}, {0, -1}, {0, 1},
		{-1, -1}, {1, -1}, {-1, 1}, {1, 1},
	} {
		nx, ny := x+off[0], y+off[1]
		leaf := findLeafByAxis(a, nx, ny)
		if leaf == nil {
			leaf = findLeafByAxis(b, nx, ny)
		}
		if leaf != nil && leaf.CellPaneID() == activePaneID {
			return activeColor
		}
	}
	return DimFg
}

// findLeafByAxis finds the leaf pane adjacent to a border position.
// Border cells sit between panes, so exact position may not be inside
// any cell. We search by the perpendicular axis and use inclusive
// bounds to catch boundary positions (junctions).
func findLeafByAxis(cell *mux.LayoutCell, x, y int) *mux.LayoutCell {
	if cell.IsLeaf() {
		// Use inclusive upper bound — border junctions sit at cell edges
		if y >= cell.Y && y <= cell.Y+cell.H && x >= cell.X && x <= cell.X+cell.W {
			return cell
		}
		return nil
	}
	for _, child := range cell.Children {
		if cell.Dir == mux.SplitVertical {
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
