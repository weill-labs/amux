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
	ZoomedPaneID uint32
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
	Idle      bool
}

// CaptureJSON is the full-screen JSON capture output.
type CaptureJSON struct {
	Session string        `json:"session"`
	Window  CaptureWindow `json:"window"`
	Width   int           `json:"width"`
	Height  int           `json:"height"`
	Panes   []CapturePane `json:"panes"`
}

// CaptureWindow identifies the captured window.
type CaptureWindow struct {
	ID    uint32 `json:"id"`
	Name  string `json:"name"`
	Index int    `json:"index"`
}

// CapturePane holds one pane's metadata, cursor, and content for JSON output.
type CapturePane struct {
	ID        uint32        `json:"id"`
	Name      string        `json:"name"`
	Active    bool          `json:"active"`
	Minimized bool          `json:"minimized"`
	Zoomed    bool          `json:"zoomed"`
	Host      string        `json:"host"`
	Task      string        `json:"task"`
	Color     string        `json:"color"`
	Position  *CapturePos   `json:"position,omitempty"`
	Cursor    CaptureCursor `json:"cursor"`
	Content   []string      `json:"content"`

	// Agent status fields (LAB-159).
	// Idle is true when no foreground command is running in the pane.
	Idle bool `json:"idle"`
	// IdleSince is the RFC3339 timestamp of the last busy→idle transition.
	// Omitted when the pane is busy.
	IdleSince string `json:"idle_since,omitempty"`
	// CurrentCommand is the foreground process name when busy, or the
	// shell name (e.g., "bash") when idle.
	CurrentCommand string `json:"current_command"`
	// ChildPIDs lists the direct child PIDs of the pane's shell process.
	// These are ephemeral OS-level PIDs — they change across captures.
	ChildPIDs []int `json:"child_pids"`
}

// CapturePos holds a pane's position and size within the layout.
type CapturePos struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// CaptureCursor holds cursor state for JSON output.
type CaptureCursor struct {
	Col    int  `json:"col"`
	Row    int  `json:"row"`
	Hidden bool `json:"hidden"`
}
