package render

// PaneData provides the data the compositor needs for rendering a pane.
// Server-side *mux.Pane and client-side emulator+metadata adapters both
// satisfy this interface.
type PaneData interface {
	// RenderScreen returns the pane's screen content as an ANSI string.
	// When active is false, app-rendered cursor blocks (isolated reverse-video
	// spaces) are stripped so unfocused panes don't show stray cursors.
	RenderScreen(active bool) string
	CursorPos() (col, row int)
	CursorHidden() bool
	// HasCursorBlock reports whether the screen contains an app-rendered
	// block cursor (isolated reverse-video space). When true, the compositor
	// hides the terminal cursor to avoid showing two cursors.
	HasCursorBlock() bool
	ID() uint32
	Name() string
	Host() string
	Task() string
	Color() string
	Minimized() bool
	Idle() bool
	InCopyMode() bool
	// CopyModeSearch returns the search prompt text (e.g., "/pattern")
	// when the user is actively typing a search in copy mode. Empty otherwise.
	CopyModeSearch() string
}
