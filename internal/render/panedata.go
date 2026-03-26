package render

import "github.com/weill-labs/amux/internal/proto"

// PaneData provides the data the compositor needs for rendering a pane.
// Server-side *mux.Pane and client-side emulator+metadata adapters both
// satisfy this interface.
type PaneData interface {
	// RenderScreen returns the pane's screen content as an ANSI string.
	// When active is false, app-rendered cursor blocks (isolated reverse-video
	// spaces) are stripped so unfocused panes don't show stray cursors.
	RenderScreen(active bool) string
	// CellAt returns the cell at (col, row) for cell-grid compositing.
	// For copy mode panes, returns cells with selection/search/cursor overlays.
	// For inactive panes, cursor block reverse-video is cleared.
	CellAt(col, row int, active bool) ScreenCell
	CursorPos() (col, row int)
	CursorHidden() bool
	// HasCursorBlock reports whether the screen contains an app-rendered
	// block cursor (isolated reverse-video space). When true, the compositor
	// hides the terminal cursor to avoid showing two cursors.
	HasCursorBlock() bool
	ID() uint32
	Name() string
	TrackedPRs() []proto.TrackedPR
	TrackedIssues() []proto.TrackedIssue
	Host() string
	Task() string
	Color() string
	Idle() bool
	IsLead() bool
	// ConnStatus returns the connection state for remote panes:
	// "", "connected", "reconnecting", or "disconnected".
	ConnStatus() string
	InCopyMode() bool
	// CopyModeSearch returns the search prompt text (e.g., "/pattern")
	// when the user is actively typing a search in copy mode. Empty otherwise.
	CopyModeSearch() string
}
