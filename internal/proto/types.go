// Package proto defines shared types used in the amux wire protocol.
// This package has no dependencies on other amux packages, preventing import cycles.
package proto

// LayoutSnapshot is a serializable representation of the full layout state.
// When Windows is non-empty, the multi-window fields take precedence over
// the legacy single-window fields (Root, Panes, ActivePaneID).
type LayoutSnapshot struct {
	SessionName  string         `json:"session_name"`
	ActivePaneID uint32         `json:"active_pane_id"`
	ZoomedPaneID uint32         `json:"zoomed_pane_id"`
	LeadPaneID   uint32         `json:"lead_pane_id,omitempty"`
	Notice       string         `json:"notice,omitempty"`
	Root         CellSnapshot   `json:"root"`
	Panes        []PaneSnapshot `json:"panes"`
	Width        int            `json:"width"`
	Height       int            `json:"height"`

	// Multi-window fields
	Windows          []WindowSnapshot `json:"windows,omitempty"`
	ActiveWindowID   uint32           `json:"active_window_id,omitempty"`
	PreviousWindowID uint32           `json:"previous_window_id,omitempty"`
}

// WindowSnapshot captures one window's state for the wire protocol.
type WindowSnapshot struct {
	ID           uint32         `json:"id"`
	Name         string         `json:"name"`
	Index        int            `json:"index"` // 1-based display order
	ActivePaneID uint32         `json:"active_pane_id"`
	ZoomedPaneID uint32         `json:"zoomed_pane_id"`
	LeadPaneID   uint32         `json:"lead_pane_id,omitempty"`
	Root         CellSnapshot   `json:"root"`
	Panes        []PaneSnapshot `json:"panes"`
}

// CellSnapshot is a serializable layout tree node.
type CellSnapshot struct {
	X        int            `json:"x"`
	Y        int            `json:"y"`
	W        int            `json:"w"`
	H        int            `json:"h"`
	IsLeaf   bool           `json:"is_leaf"`
	Dir      int            `json:"dir"`     // -1 for leaf, 0 for SplitVertical, 1 for SplitHorizontal
	PaneID   uint32         `json:"pane_id"` // only for leaves
	Children []CellSnapshot `json:"children,omitempty"`
}

// PaneSnapshot holds metadata for one pane.
type PaneSnapshot struct {
	ID            uint32            `json:"id"`
	Name          string            `json:"name"`
	Host          string            `json:"host"`
	Task          string            `json:"task"`
	Color         string            `json:"color"`
	ColumnIndex   int               `json:"column_index"`
	Idle          bool              `json:"idle"`
	ConnStatus    string            `json:"conn_status,omitempty"` // "", "connected", "reconnecting", "disconnected" (remote panes only)
	KV            map[string]string `json:"kv,omitempty"`
	GitBranch     string            `json:"git_branch,omitempty"`
	PR            string            `json:"pr,omitempty"`
	TrackedPRs    []TrackedPR       `json:"tracked_prs,omitempty"`
	TrackedIssues []TrackedIssue    `json:"tracked_issues,omitempty"`
	Lead          bool              `json:"lead,omitempty"`
}

// CaptureJSON is the full-screen JSON capture output.
type CaptureJSON struct {
	Session string        `json:"session"`
	Window  CaptureWindow `json:"window"`
	Width   int           `json:"width"`
	Height  int           `json:"height"`
	Notice  string        `json:"notice,omitempty"`
	Panes   []CapturePane `json:"panes"`
	UI      *CaptureUI    `json:"ui,omitempty"`
	Error   *CaptureError `json:"error,omitempty"`
}

// CaptureUI holds client-local UI state for JSON capture.
type CaptureUI struct {
	CopyMode     bool   `json:"copy_mode,omitempty"`
	DisplayPanes bool   `json:"display_panes,omitempty"`
	Chooser      string `json:"chooser,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	InputIdle    bool   `json:"input_idle"`
}

// CaptureWindow identifies the captured window.
type CaptureWindow struct {
	ID    uint32 `json:"id"`
	Name  string `json:"name"`
	Index int    `json:"index"`
}

// CaptureMeta holds user-managed pane metadata for JSON capture.
type CaptureMeta struct {
	KV            map[string]string `json:"kv,omitempty"`
	Task          string            `json:"task,omitempty"`
	GitBranch     string            `json:"git_branch,omitempty"`
	PR            string            `json:"pr,omitempty"`
	TrackedPRs    []TrackedPR       `json:"tracked_prs,omitempty"`
	TrackedIssues []TrackedIssue    `json:"tracked_issues,omitempty"`
}

// CaptureError describes an unavailable or invalid JSON capture result.
type CaptureError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CapturePane holds one pane's metadata, cursor, and content for JSON output.
type CapturePane struct {
	ID          uint32           `json:"id"`
	Name        string           `json:"name"`
	Active      bool             `json:"active"`
	Lead        bool             `json:"lead,omitempty"`
	Zoomed      bool             `json:"zoomed"`
	Host        string           `json:"host"`
	Task        string           `json:"task"`
	Color       string           `json:"color"`
	ColumnIndex int              `json:"column_index"`
	Meta        CaptureMeta      `json:"meta"`
	Position    *CapturePos      `json:"position,omitempty"`
	Cursor      CaptureCursor    `json:"cursor"`
	Terminal    *CaptureTerminal `json:"terminal,omitempty"`
	Content     []string         `json:"content"`
	History     []string         `json:"history,omitempty"`
	CopyMode    bool             `json:"copy_mode,omitempty"`

	// ConnStatus is the remote connection state: "", "connected", "reconnecting", "disconnected".
	// Empty for local panes.
	ConnStatus string `json:"conn_status,omitempty"`

	// CWD/branch metadata.
	Cwd       string `json:"cwd,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	PR        string `json:"pr,omitempty"`

	// Agent status fields (LAB-159).
	// Exited is true when no foreground command is running in the pane.
	Exited bool `json:"exited"`
	// ExitedSince is the RFC3339 timestamp of the last busy→exited transition.
	// Omitted when the pane is busy.
	ExitedSince string `json:"exited_since,omitempty"`
	// CurrentCommand is the foreground process name when busy, or the
	// shell name (e.g., "bash") when exited.
	CurrentCommand string `json:"current_command"`
	// ChildPIDs lists the direct child PIDs of the pane's shell process.
	// These are ephemeral OS-level PIDs — they change across captures.
	ChildPIDs []int `json:"child_pids"`
	// Idle is true when pane output has been quiet for the settle window.
	Idle bool `json:"idle"`
	// IdleSince is the RFC3339 timestamp when the pane most recently
	// became screen-quiet. Omitted while output is still active.
	IdleSince string `json:"idle_since,omitempty"`
	// LastOutput is the RFC3339 timestamp of the most recent pane output edge.
	// Omitted when no output has been observed for the pane.
	LastOutput string `json:"last_output,omitempty"`

	Error *CaptureError `json:"error,omitempty"`
}

// PaneAgentStatus holds process-level status for a pane, gathered by the
// server and forwarded to the client for JSON capture.
type PaneAgentStatus struct {
	Exited         bool
	ExitedSince    string // RFC3339 or ""
	CurrentCommand string
	ChildPIDs      []int
	Idle           bool
	IdleSince      string // RFC3339 or ""
	LastOutput     string // RFC3339 or ""
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
	Col      int    `json:"col"`
	Row      int    `json:"row"`
	Hidden   bool   `json:"hidden"`
	Style    string `json:"style"`
	Blinking bool   `json:"blinking"`
}

// CaptureTerminal holds pane terminal metadata that is not represented in
// plain text content.
type CaptureTerminal struct {
	AltScreen       bool                  `json:"alt_screen"`
	ForegroundColor string                `json:"foreground_color"`
	BackgroundColor string                `json:"background_color"`
	CursorColor     string                `json:"cursor_color"`
	Hyperlink       *CaptureHyperlink     `json:"hyperlink,omitempty"`
	Mouse           *CaptureMouseProtocol `json:"mouse"`
	Palette         []string              `json:"palette"`
}

// CaptureHyperlink is the currently active OSC 8 hyperlink state.
type CaptureHyperlink struct {
	URL    string `json:"url"`
	Params string `json:"params,omitempty"`
}

// CaptureMouseProtocol describes the pane's current mouse tracking mode.
type CaptureMouseProtocol struct {
	Tracking string `json:"tracking"`
	SGR      bool   `json:"sgr"`
}

// ApplyAgentStatus populates agent status fields on a CapturePane from
// the server-gathered status map. ChildPIDs is normalized to an empty
// slice (never nil) for consistent JSON output.
func (cp *CapturePane) ApplyAgentStatus(status map[uint32]PaneAgentStatus) {
	st, ok := status[cp.ID]
	if !ok {
		return
	}
	cp.Exited = st.Exited
	cp.ExitedSince = st.ExitedSince
	cp.Idle = st.Idle
	cp.IdleSince = st.IdleSince
	cp.CurrentCommand = st.CurrentCommand
	cp.LastOutput = st.LastOutput
	if st.ChildPIDs != nil {
		cp.ChildPIDs = st.ChildPIDs
	} else {
		cp.ChildPIDs = []int{}
	}
}

// FindCellInSnapshot finds a leaf cell by pane ID in a CellSnapshot tree.
// Returns nil if no matching leaf is found.
func FindCellInSnapshot(cs CellSnapshot, paneID uint32) *CellSnapshot {
	if cs.IsLeaf && cs.PaneID == paneID {
		return &cs
	}
	for i := range cs.Children {
		if found := FindCellInSnapshot(cs.Children[i], paneID); found != nil {
			return found
		}
	}
	return nil
}

// FindPaneDimensions returns the width and content height for a pane,
// searching activeRoot first, then all windows in the snapshot.
// contentHeight is a caller-provided function that converts a cell height
// to the pane's content height (subtracting status bar, etc.).
// Falls back to the full snapshot dimensions if the pane is not found.
func FindPaneDimensions(snap *LayoutSnapshot, activeRoot CellSnapshot, paneID uint32, contentHeight func(int) int) (int, int) {
	if cell := FindCellInSnapshot(activeRoot, paneID); cell != nil {
		return cell.W, contentHeight(cell.H)
	}
	for _, ws := range snap.Windows {
		if cell := FindCellInSnapshot(ws.Root, paneID); cell != nil {
			return cell.W, contentHeight(cell.H)
		}
	}
	return snap.Width, contentHeight(snap.Height)
}
