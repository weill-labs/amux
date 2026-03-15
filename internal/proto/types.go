// Package proto defines shared types used in the amux wire protocol.
// This package has no dependencies on other amux packages, preventing import cycles.
package proto

// LayoutSnapshot is a serializable representation of the full layout state.
type LayoutSnapshot struct {
	SessionName  string
	ActivePaneID uint32
	ZoomedPaneID uint32
	Root         CellSnapshot
	Panes        []PaneSnapshot
	Width        int
	Height       int
}

// CellSnapshot is a serializable layout tree node.
type CellSnapshot struct {
	X, Y, W, H int
	IsLeaf      bool
	Dir         int    // -1 for leaf, 0 for SplitHorizontal, 1 for SplitVertical
	PaneID      uint32 // only for leaves
	Children    []CellSnapshot
}

// PaneSnapshot holds metadata for one pane.
type PaneSnapshot struct {
	ID        uint32
	Name      string
	Host      string
	Task      string
	Color     string
	Minimized bool
}
