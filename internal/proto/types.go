// Package proto defines shared types used in the amux wire protocol.
// This package has no dependencies on other amux packages, preventing import cycles.
package proto

// LayoutSnapshot is a serializable representation of the full layout state.
// When Windows is non-empty, the multi-window fields take precedence over
// the legacy single-window fields (Root, Panes, ActivePaneID).
type LayoutSnapshot struct {
	SessionName  string
	ActivePaneID uint32
	ZoomedPaneID uint32
	Root         CellSnapshot
	Panes        []PaneSnapshot
	Width        int
	Height       int

	// Multi-window fields
	Windows        []WindowSnapshot
	ActiveWindowID uint32
}

// WindowSnapshot captures one window's state for the wire protocol.
type WindowSnapshot struct {
	ID           uint32
	Name         string
	Index        int // 1-based display order
	ActivePaneID uint32
	Root         CellSnapshot
	Panes        []PaneSnapshot
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
